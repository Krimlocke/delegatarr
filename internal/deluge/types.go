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
	Label       string   // from LabelPlugin.GetTorrentsLabels()
	State       string
	SeedingTime int64
	TimeAdded   float64  // Deluge sends float32, we widen to float64
	ActiveTime  int64
	Ratio       float64  // Deluge sends float32, we widen
	TrackerHost string   // single tracker host string from the library
	Trackers    []string // tracker URLs — for now just TrackerHost
}

// FromStatus converts a go-libdeluge TorrentStatus into our TorrentInfo.
// The label parameter comes from LabelPlugin.GetTorrentsLabels() since
// the library's TorrentStatus struct does not include it.
// The trackerURLs parameter is optional — if provided (from FetchTrackerURLs),
// it replaces the single TrackerHost with the full list of tracker URLs.
func FromStatus(ts *delugeclient.TorrentStatus, label string, trackerURLs ...[]string) TorrentInfo {
	info := TorrentInfo{
		Name:        ts.Name,
		Label:       label,
		State:       ts.State,
		SeedingTime: ts.SeedingTime,
		TimeAdded:   float64(ts.TimeAdded),
		ActiveTime:  ts.ActiveTime,
		Ratio:       float64(ts.Ratio),
		TrackerHost: ts.TrackerHost,
	}
	// If full tracker URLs were provided via raw RPC, use those.
	// Otherwise fall back to TrackerHost (the library's shortened domain).
	if len(trackerURLs) > 0 && len(trackerURLs[0]) > 0 {
		info.Trackers = trackerURLs[0]
	} else if ts.TrackerHost != "" {
		info.Trackers = []string{ts.TrackerHost}
	}
	return info
}
