package engine

import (
	"github.com/krimlocke/delegatarr/internal/deluge"
)

// extractTrackerURLs pulls tracker URLs/hosts from a TorrentInfo.
func extractTrackerURLs(t deluge.TorrentInfo) []string {
	return t.Trackers
}
