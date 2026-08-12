package engine

import (
	"github.com/krimlocke/delegatarr/internal/config"
	"github.com/krimlocke/delegatarr/internal/deluge"
)

// extractTrackerURLs pulls tracker URLs/hosts from a TorrentInfo.
func extractTrackerURLs(t deluge.TorrentInfo) []string {
	return t.Trackers
}

// trackerDomains returns the distinct tracker domains a torrent announces to,
// honouring the tracker mode ("top" keeps only the first tracker).
//
// A torrent carrying no trackers at all — Radarr can hand Deluge one, and
// Deluge then shows it under a blank tracker filter — is reported under the
// config.NoTrackerDomain sentinel rather than dropped. That gives it a domain
// to be tagged against, so rules can reach it like any other torrent.
func trackerDomains(t deluge.TorrentInfo, trackerMode string) []string {
	urls := extractTrackerURLs(t)
	if trackerMode == "top" && len(urls) > 0 {
		urls = urls[:1]
	}

	domains := make([]string, 0, len(urls))
	seen := map[string]bool{}
	for _, rawURL := range urls {
		domain := extractDomain(rawURL)
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		domains = append(domains, domain)
	}

	if len(domains) == 0 {
		return []string{config.NoTrackerDomain}
	}
	return domains
}
