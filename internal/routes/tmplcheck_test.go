package routes

import (
	"io"
	"testing"

	"github.com/krimlocke/delegatarr/internal/config"
)

func TestTemplatesParseAndRender(t *testing.T) {
	if err := LoadTemplates("../../templates"); err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}

	ratio := 1.5
	enabled := true
	data := &pageData{
		ActivePage: "trackers",
		PageTitle:  "check",
		TagFloors: []tagFloor{
			{Tag: "ipt", Count: 12, Floor: 10},
			{Tag: "tl", Count: 3, Floor: 0},
		},
		Groups:         config.Groups{"tracker.example.com": "ipt"},
		TrackerSummary: map[string]int{"tracker.example.com": 12},
		RulesList: []config.Rule{
			{GroupID: "ipt", MinTorrents: 10, MinKeepScope: config.MinKeepScopeGroup, SeedRatio: &ratio, Enabled: &enabled},
			{GroupID: "tl", MinTorrents: 0, SeedRatio: &ratio, Enabled: &enabled},
		},
	}

	for _, page := range []string{"trackers.html", "rules.html"} {
		tmpl, ok := pageTemplates[page]
		if !ok {
			t.Fatalf("%s not loaded", page)
		}
		if err := tmpl.ExecuteTemplate(io.Discard, page, data); err != nil {
			t.Errorf("render %s: %v", page, err)
		}
	}
}
