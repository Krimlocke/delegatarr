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
	Name          string
	Label         string   // from LabelPlugin.GetTorrentsLabels()
	State         string
	SeedingTime   int64
	TimeAdded     float64  // Deluge sends float32, we widen to float64
	ActiveTime    int64
	Ratio         float64  // Deluge sends float32, we widen
	TrackerHost   string   // single tracker host string from the library
	Trackers      []string // tracker URLs — for now just TrackerHost
	TrackerStatus string   // human-readable tracker status (e.g. "Announce OK", "Error: unregistered torrent")
}

// FromStatusOpts holds optional data fetched via separate RPC calls.
type FromStatusOpts struct {
	TrackerURLs   []string // full tracker URLs from FetchTrackerURLs
	TrackerStatus string   // human-readable tracker status from FetchTrackerStatuses
}

// FromStatus converts a go-libdeluge TorrentStatus into our TorrentInfo.
// The label parameter comes from LabelPlugin.GetTorrentsLabels() since
// the library's TorrentStatus struct does not include it.
// The opts parameter is optional — if provided, it supplies additional data
// from separate RPC calls (tracker URLs, tracker status).
func FromStatus(ts *delugeclient.TorrentStatus, label string, opts ...FromStatusOpts) TorrentInfo {
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

	if len(opts) > 0 {
		o := opts[0]
		if len(o.TrackerURLs) > 0 {
			info.Trackers = o.TrackerURLs
		}
		info.TrackerStatus = o.TrackerStatus
	}

	// Fall back to TrackerHost if no full tracker URLs were provided.
	if len(info.Trackers) == 0 && ts.TrackerHost != "" {
		info.Trackers = []string{ts.TrackerHost}
	}
	return info
}
