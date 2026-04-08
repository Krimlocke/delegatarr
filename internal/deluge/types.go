package deluge

import (
	delugeclient "github.com/gdm85/go-libdeluge"
)

// Re-export for engine usage.
const StateUnspecified = delugeclient.StateUnspecified

// TorrentInfo is a flattened view of torrent data that our engine needs.
// go-libdeluge's TorrentStatus does NOT include Label (plugin field) or full
// Trackers list, so we build this from the library's struct plus TrackerHost.
type TorrentInfo struct {
	Name        string
	Label       string   // from Label plugin — empty until we add raw RPC support
	State       string
	SeedingTime int64
	TimeAdded   float64  // Deluge sends float32, we widen to float64
	ActiveTime  int64
	Ratio       float64  // Deluge sends float32, we widen
	TrackerHost string   // single tracker host string from the library
	Trackers    []string // tracker URLs — for now just TrackerHost
}

// FromStatus converts a go-libdeluge TorrentStatus into our TorrentInfo.
func FromStatus(ts *delugeclient.TorrentStatus) TorrentInfo {
	info := TorrentInfo{
		Name:        ts.Name,
		State:       ts.State,
		SeedingTime: ts.SeedingTime,
		TimeAdded:   float64(ts.TimeAdded),
		ActiveTime:  ts.ActiveTime,
		Ratio:       float64(ts.Ratio),
		TrackerHost: ts.TrackerHost,
	}
	// The library only gives us TrackerHost (e.g. "tracker.example.com").
	// We use it as-is since our domain extraction will match it.
	if ts.TrackerHost != "" {
		info.Trackers = []string{ts.TrackerHost}
	}
	return info
}
