package deluge

import (
	delugeclient "github.com/gdm85/go-libdeluge"
)

// Re-export for engine usage.
// go-libdeluge uses delugeclient.StateUnspecified, etc.
const StateUnspecified = delugeclient.StateUnspecified

// TorrentInfo is a flattened view of torrent data that our engine needs.
type TorrentInfo struct {
	Name        string
	Label       string
	State       string
	SeedingTime int64
	TimeAdded   int64
	ActiveTime  int64
	Ratio       float64
	Trackers    []TrackerInfo
}

type TrackerInfo struct {
	URL string
}
