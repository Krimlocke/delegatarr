package engine

import (
	"reflect"
	"testing"

	"github.com/krimlocke/delegatarr/internal/config"
	"github.com/krimlocke/delegatarr/internal/deluge"
)

func TestTrackerDomains(t *testing.T) {
	cases := []struct {
		name        string
		trackers    []string
		trackerMode string
		want        []string
	}{
		{
			name:        "no trackers falls back to the sentinel",
			trackers:    nil,
			trackerMode: "all",
			want:        []string{config.NoTrackerDomain},
		},
		{
			name:        "blank tracker URL falls back to the sentinel",
			trackers:    []string{""},
			trackerMode: "all",
			want:        []string{config.NoTrackerDomain},
		},
		{
			name:        "all mode keeps every distinct domain",
			trackers:    []string{"https://sync.td-peers.com/announce", "http://jumbohostpro.eu:2710/announce"},
			trackerMode: "all",
			want:        []string{"sync.td-peers.com", "jumbohostpro.eu"},
		},
		{
			name:        "top mode keeps only the first",
			trackers:    []string{"https://sync.td-peers.com/announce", "http://jumbohostpro.eu:2710/announce"},
			trackerMode: "top",
			want:        []string{"sync.td-peers.com"},
		},
		{
			name:        "duplicate domains collapse",
			trackers:    []string{"https://sync.td-peers.com/announce", "https://sync.td-peers.com/announce2"},
			trackerMode: "all",
			want:        []string{"sync.td-peers.com"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trackerDomains(deluge.TorrentInfo{Trackers: tc.trackers}, tc.trackerMode)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("trackerDomains = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTagsForTorrentTrackerless(t *testing.T) {
	groups := config.Groups{
		"sync.td-peers.com":    "public",
		config.NoTrackerDomain: "orphans",
	}

	trackerless := deluge.TorrentInfo{Name: "Radarr import with no tracker"}
	if got := tagsForTorrent(trackerless, groups, "all"); !reflect.DeepEqual(got, []string{"orphans"}) {
		t.Errorf("trackerless torrent tags = %v, want [orphans]", got)
	}

	// Untagged sentinel: the torrent stays out of every tag, as before.
	if got := tagsForTorrent(trackerless, config.Groups{"sync.td-peers.com": "public"}, "all"); len(got) != 0 {
		t.Errorf("untagged trackerless torrent tags = %v, want none", got)
	}

	tracked := deluge.TorrentInfo{Trackers: []string{"https://sync.td-peers.com/announce"}}
	if got := tagsForTorrent(tracked, groups, "all"); !reflect.DeepEqual(got, []string{"public"}) {
		t.Errorf("tracked torrent tags = %v, want [public]", got)
	}
}
