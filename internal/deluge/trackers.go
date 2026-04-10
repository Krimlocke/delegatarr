package deluge

import (
	"bytes"
	"compress/zlib"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	rencode "github.com/gdm85/go-rencode"
)

// FetchTrackerURLs makes a raw RPC call to Deluge requesting the full "trackers"
// field for all torrents. This bypasses go-libdeluge's hardcoded statusKeys which
// only return TrackerHost (a shortened base domain).
//
// Returns a map of torrent hash -> list of tracker URLs (e.g. "https://sync.td-peers.com/announce").
// Returns nil on any error (non-fatal; callers fall back to TrackerHost).
func FetchTrackerURLs() map[string][]string {
	if host == "" {
		return nil
	}

	u, p := getCredentials()

	conn, err := rawConnect(host, port, u, p)
	if err != nil {
		log.Printf("Tracker RPC: connection failed: %v", err)
		return nil
	}
	defer conn.Close()

	result, err := rawGetTorrentsTrackers(conn)
	if err != nil {
		log.Printf("Tracker RPC: fetch failed: %v", err)
		return nil
	}

	return result
}

// FetchTrackerStatuses makes a raw RPC call to Deluge requesting the "tracker_status"
// field for all torrents. This returns the human-readable tracker status string
// (e.g. "Announce OK", "Error: unregistered torrent").
//
// Returns a map of torrent hash -> tracker status string.
// Returns nil on any error (non-fatal; callers treat as empty).
func FetchTrackerStatuses() map[string]string {
	if host == "" {
		return nil
	}

	u, p := getCredentials()

	conn, err := rawConnect(host, port, u, p)
	if err != nil {
		log.Printf("Tracker Status RPC: connection failed: %v", err)
		return nil
	}
	defer conn.Close()

	result, err := rawGetTorrentsTrackerStatus(conn)
	if err != nil {
		log.Printf("Tracker Status RPC: fetch failed: %v", err)
		return nil
	}

	return result
}

// rawConn wraps a TLS connection with the Deluge v2 RPC protocol.
type rawConn struct {
	tls    *tls.Conn
	serial int64
}

func rawConnect(hostname string, portNum int, login, password string) (*rawConn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	raw, err := dialer.Dial("tcp", fmt.Sprintf("%s:%d", hostname, portNum))
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	tlsConn := tls.Client(raw, &tls.Config{
		ServerName:         hostname,
		InsecureSkipVerify: true,
	})
	if err := tlsConn.Handshake(); err != nil {
		raw.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}

	rc := &rawConn{tls: tlsConn}

	// Login: daemon.login(username, password) with client_version as kwarg for v2
	args := rencode.NewList(login, password)
	var kwargs rencode.Dictionary
	kwargs.Add("client_version", "2.0.3")
	_, err = rc.call("daemon.login", args, kwargs)
	if err != nil {
		tlsConn.Close()
		return nil, fmt.Errorf("login: %w", err)
	}

	return rc, nil
}

func (rc *rawConn) Close() error {
	return rc.tls.Close()
}

func (rc *rawConn) call(method string, args rencode.List, kwargs rencode.Dictionary) (rencode.List, error) {
	rc.serial++

	// Encode: [[serial, method, args, kwargs]]
	payload := rencode.NewList(rencode.NewList(rc.serial, method, args, kwargs))

	var reqBuf bytes.Buffer
	zw := zlib.NewWriter(&reqBuf)
	enc := rencode.NewEncoder(zw)
	if err := enc.Encode(payload); err != nil {
		return rencode.List{}, err
	}
	zw.Close()

	// V2 header: 1 byte version + 4 bytes length
	var header [5]byte
	header[0] = 1 // protocol version
	binary.BigEndian.PutUint32(header[1:], uint32(reqBuf.Len()))

	rc.tls.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err := rc.tls.Write(header[:]); err != nil {
		return rencode.List{}, err
	}
	if _, err := io.Copy(rc.tls, &reqBuf); err != nil {
		return rencode.List{}, err
	}

	// Read response
	rc.tls.SetReadDeadline(time.Now().Add(30 * time.Second))
	var respHeader [5]byte
	if _, err := io.ReadFull(rc.tls, respHeader[:]); err != nil {
		return rencode.List{}, fmt.Errorf("read header: %w", err)
	}

	respLen := binary.BigEndian.Uint32(respHeader[1:])
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(rc.tls, respBuf); err != nil {
		return rencode.List{}, fmt.Errorf("read body: %w", err)
	}

	zr, err := zlib.NewReader(bytes.NewReader(respBuf))
	if err != nil {
		return rencode.List{}, fmt.Errorf("zlib: %w", err)
	}
	defer zr.Close()

	dec := rencode.NewDecoder(zr)
	var respList rencode.List
	if err := dec.Scan(&respList); err != nil {
		return rencode.List{}, fmt.Errorf("decode: %w", err)
	}

	// Response format: [messageType, requestID, returnValue...]
	// messageType 1 = response, 2 = error
	var msgType int64
	if err := respList.Scan(&msgType); err != nil {
		return rencode.List{}, fmt.Errorf("scan msgtype: %w", err)
	}
	respList.Shift(1)

	if msgType == 2 {
		return rencode.List{}, fmt.Errorf("RPC error in %s", method)
	}

	// Skip request ID
	respList.Shift(1)

	return respList, nil
}

// rawGetTorrentsTrackers calls core.get_torrents_status({}, ["trackers"]) and
// parses the nested response into a map of hash -> tracker URLs.
func rawGetTorrentsTrackers(rc *rawConn) (map[string][]string, error) {
	// args: (filter_dict, keys)
	// filter_dict = {} (empty = all torrents)
	// keys = ["trackers"]
	filterDict := rencode.Dictionary{}
	keys := rencode.NewList("trackers")
	args := rencode.NewList(filterDict, keys)

	resp, err := rc.call("core.get_torrents_status", args, rencode.Dictionary{})
	if err != nil {
		return nil, err
	}

	// The response is a dictionary: {torrent_hash: {b"trackers": [tracker_dicts...]}}
	// In rencode, dictionaries come as lists of alternating key/value pairs
	result := make(map[string][]string)

	if resp.Length() == 0 {
		return result, nil
	}

	// The return value is the first element, which should be a Dictionary
	values := resp.Values()
	if len(values) == 0 {
		return result, nil
	}

	// Try to get it as a Dictionary
	dict, ok := values[0].(rencode.Dictionary)
	if !ok {
		// Sometimes it comes as a List that represents a dictionary
		if asList, ok := values[0].(rencode.List); ok {
			dict = rencode.Dictionary{}
			listVals := asList.Values()
			for i := 0; i+1 < len(listVals); i += 2 {
				dict.Add(listVals[i], listVals[i+1])
			}
		} else {
			return result, fmt.Errorf("unexpected response type: %T", values[0])
		}
	}

	// Iterate the outer dictionary (hash -> torrent data)
	for _, pair := range dict.Values() {
		kv, ok := pair.(rencode.List)
		if !ok {
			continue
		}
		kvValues := kv.Values()
		if len(kvValues) < 2 {
			continue
		}

		hashBytes, ok := kvValues[0].([]byte)
		if !ok {
			continue
		}
		hash := string(hashBytes)

		// Inner value is a dict with "trackers" key
		innerDict, ok := kvValues[1].(rencode.Dictionary)
		if !ok {
			continue
		}

		for _, innerPair := range innerDict.Values() {
			ikv, ok := innerPair.(rencode.List)
			if !ok {
				continue
			}
			ikvValues := ikv.Values()
			if len(ikvValues) < 2 {
				continue
			}

			keyBytes, ok := ikvValues[0].([]byte)
			if !ok {
				continue
			}
			if string(keyBytes) != "trackers" {
				continue
			}

			// Value is a list of tracker dicts, each with "url" key
			trackerList, ok := ikvValues[1].(rencode.List)
			if !ok {
				continue
			}

			var urls []string
			for _, trackerItem := range trackerList.Values() {
				trackerDict, ok := trackerItem.(rencode.Dictionary)
				if !ok {
					continue
				}
				for _, tPair := range trackerDict.Values() {
					tkv, ok := tPair.(rencode.List)
					if !ok {
						continue
					}
					tkvValues := tkv.Values()
					if len(tkvValues) < 2 {
						continue
					}
					tKey, ok := tkvValues[0].([]byte)
					if !ok {
						continue
					}
					if string(tKey) == "url" {
						if urlBytes, ok := tkvValues[1].([]byte); ok {
							url := string(urlBytes)
							if url != "" {
								urls = append(urls, url)
							}
						}
					}
				}
			}

			if len(urls) > 0 {
				result[hash] = urls
			}
		}
	}

	return result, nil
}

// rawGetTorrentsTrackerStatus calls core.get_torrents_status({}, ["tracker_status"]) and
// parses the response into a map of hash -> tracker status string.
func rawGetTorrentsTrackerStatus(rc *rawConn) (map[string]string, error) {
	filterDict := rencode.Dictionary{}
	keys := rencode.NewList("tracker_status")
	args := rencode.NewList(filterDict, keys)

	resp, err := rc.call("core.get_torrents_status", args, rencode.Dictionary{})
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)

	if resp.Length() == 0 {
		return result, nil
	}

	values := resp.Values()
	if len(values) == 0 {
		return result, nil
	}

	dict, ok := values[0].(rencode.Dictionary)
	if !ok {
		if asList, ok := values[0].(rencode.List); ok {
			dict = rencode.Dictionary{}
			listVals := asList.Values()
			for i := 0; i+1 < len(listVals); i += 2 {
				dict.Add(listVals[i], listVals[i+1])
			}
		} else {
			return result, fmt.Errorf("unexpected response type: %T", values[0])
		}
	}

	for _, pair := range dict.Values() {
		kv, ok := pair.(rencode.List)
		if !ok {
			continue
		}
		kvValues := kv.Values()
		if len(kvValues) < 2 {
			continue
		}

		hashBytes, ok := kvValues[0].([]byte)
		if !ok {
			continue
		}
		hash := string(hashBytes)

		innerDict, ok := kvValues[1].(rencode.Dictionary)
		if !ok {
			continue
		}

		for _, innerPair := range innerDict.Values() {
			ikv, ok := innerPair.(rencode.List)
			if !ok {
				continue
			}
			ikvValues := ikv.Values()
			if len(ikvValues) < 2 {
				continue
			}

			keyBytes, ok := ikvValues[0].([]byte)
			if !ok {
				continue
			}
			if string(keyBytes) != "tracker_status" {
				continue
			}

			switch v := ikvValues[1].(type) {
			case []byte:
				result[hash] = string(v)
			case string:
				result[hash] = v
			default:
				log.Printf("Tracker Status RPC: unexpected value type %T for hash %s", ikvValues[1], hash)
			}
		}
	}

	log.Printf("Tracker Status RPC: fetched status for %d torrent(s)", len(result))
	return result, nil
}
