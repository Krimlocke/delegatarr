package engine

import (
	"github.com/krimlocke/delegatarr/internal/deluge"
)

// extractTrackerURLs pulls tracker URLs from a TorrentInfo.
func extractTrackerURLs(t deluge.TorrentInfo) []string {
	urls := make([]string, 0, len(t.Trackers))
	for _, tr := range t.Trackers {
		if tr.URL != "" {
			urls = append(urls, tr.URL)
		}
	}
	return urls
}
