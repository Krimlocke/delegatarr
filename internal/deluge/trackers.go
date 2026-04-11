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

// RawSession is an open raw RPC connection to the Deluge daemon.
// Obtain one with OpenRawSession and always call Close when done.
type RawSession struct {
	conn *rawConn
}

// OpenRawSession opens a dedicated raw TLS connection to Deluge for
// operations not covered by go-libdeluge (e.g. fetching full tracker URLs).
// Returns nil without error when DELUGE_HOST is not configured.
func OpenRawSession() (*RawSession, error) {
	if host == "" {
		return nil, nil
	}
	u, p := getCredentials()
	conn, err := rawConnect(host, port, u, p)
	if err != nil {
		return nil, err
	}
	return &RawSession{conn: conn}, nil
}

// Close releases the underlying connection.
func (s *RawSession) Close() {
	if s != nil && s.conn != nil {
		s.conn.Close()
	}
}

// FetchTrackerURLs makes a raw RPC call to Deluge requesting the full "trackers"
// field for all torrents. This bypasses go-libdeluge's hardcoded statusKeys which
// only return TrackerHost (a shortened base domain).
//
// Returns a map of torrent hash -> list of tracker URLs (e.g. "https://sync.td-peers.com/announce").
// Returns nil on any error (non-fatal; callers fall back to TrackerHost).
//
// Deprecated: prefer FetchTrackerURLsSession to reuse an existing RawSession.
func FetchTrackerURLs() map[string][]string {
	s, err := OpenRawSession()
	if err != nil {
		log.Printf("Tracker RPC: connection failed: %v", err)
		return nil
	}
	if s == nil {
		return nil
	}
	defer s.Close()
	return FetchTrackerURLsSession(s)
}

// FetchTrackerURLsSession fetches full tracker URLs using an already-open RawSession,
// avoiding a second TLS dial when the caller already holds a session.
// Returns nil if s is nil (safe to call unconditionally).
func FetchTrackerURLsSession(s *RawSession) map[string][]string {
	if s == nil {
		return nil
	}
	result, err := rawGetTorrentsTrackers(s.conn)
	if err != nil {
		log.Printf("Tracker RPC: fetch failed: %v", err)
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
	filterDict := rencode.Dictionary{}
	keys := rencode.NewList("trackers")
	args := rencode.NewList(filterDict, keys)

	resp, err := rc.call("core.get_torrents_status", args, rencode.Dictionary{})
	if err != nil {
		return nil, err
	}

	result := make(map[string][]string)

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
			if string(keyBytes) != "trackers" {
				continue
			}

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
				result[hash] = urls
			}
		}
	}

	return result, nil
}
