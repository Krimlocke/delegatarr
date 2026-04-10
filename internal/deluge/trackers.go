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

// TrackerData holds the results of the combined tracker RPC call.
type TrackerData struct {
	URLs     map[string][]string // torrent hash -> list of tracker URLs
	Statuses map[string]string   // torrent hash -> tracker status string
}

// FetchTrackerData makes a single raw RPC call to Deluge requesting both "trackers"
// and "tracker_status" fields for all torrents. This uses one connection and one
// RPC call to avoid issues with Deluge's concurrent connection limits.
//
// Returns nil on any error (non-fatal; callers fall back to TrackerHost/empty status).
func FetchTrackerData() *TrackerData {
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

	data, err := rawGetTorrentsTrackersAndStatus(conn)
	if err != nil {
		log.Printf("Tracker RPC: fetch failed: %v", err)
		return nil
	}

	log.Printf("Tracker RPC: fetched data for %d torrent(s) (%d with status)",
		len(data.URLs), len(data.Statuses))
	return data
}

// FetchTrackerURLs is a convenience wrapper for callers that only need tracker URLs.
func FetchTrackerURLs() map[string][]string {
	data := FetchTrackerData()
	if data == nil {
		return nil
	}
	return data.URLs
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

// rawGetTorrentsTrackersAndStatus calls core.get_torrents_status({}, ["trackers", "tracker_status"])
// in a single RPC call and parses both fields from the response.
func rawGetTorrentsTrackersAndStatus(rc *rawConn) (*TrackerData, error) {
	filterDict := rencode.Dictionary{}
	keys := rencode.NewList("trackers", "tracker_status")
	args := rencode.NewList(filterDict, keys)

	resp, err := rc.call("core.get_torrents_status", args, rencode.Dictionary{})
	if err != nil {
		return nil, err
	}

	data := &TrackerData{
		URLs:     make(map[string][]string),
		Statuses: make(map[string]string),
	}

	if resp.Length() == 0 {
		return data, nil
	}

	values := resp.Values()
	if len(values) == 0 {
		return data, nil
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
			return data, fmt.Errorf("unexpected response type: %T", values[0])
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
			key := string(keyBytes)

			switch key {
			case "trackers":
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
							switch v := tkvValues[1].(type) {
							case []byte:
								if s := string(v); s != "" {
									urls = append(urls, s)
								}
							case string:
								if v != "" {
									urls = append(urls, v)
								}
							}
						}
					}
				}
				if len(urls) > 0 {
					data.URLs[hash] = urls
				}

			case "tracker_status":
				switch v := ikvValues[1].(type) {
				case []byte:
					data.Statuses[hash] = string(v)
				case string:
					data.Statuses[hash] = v
				default:
					log.Printf("Tracker RPC: unexpected tracker_status type %T for hash %s", ikvValues[1], hash)
				}
			}
		}
	}

	return data, nil
}
