package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AppVersion is injected at build time via -ldflags. Format: yyyy.mm.dd.HHMM.go
// Falls back to "dev" for untagged local builds.
var AppVersion = "dev"

var (
	ConfigDir     = "/config"
	LogFile       string
	SecretKeyFile string
	GroupsFile    string
	RulesFile     string
	SettingsFile  string
	LogoFile      string
	StartTime     = time.Now()
)

func init() {
	if dir := os.Getenv("CONFIG_DIR"); dir != "" {
		ConfigDir = dir
	}
	os.MkdirAll(ConfigDir, 0o755)

	LogFile = filepath.Join(ConfigDir, "delegatarr.log")
	SecretKeyFile = filepath.Join(ConfigDir, "secret.key")
	GroupsFile = filepath.Join(ConfigDir, "groups.json")
	RulesFile = filepath.Join(ConfigDir, "rules.json")
	SettingsFile = filepath.Join(ConfigDir, "settings.json")
	LogoFile = filepath.Join(ConfigDir, "logo.png")
}

// Settings represents the application settings stored in settings.json.
type Settings struct {
	RunInterval      int    `json:"run_interval"`
	LogRetentionDays int    `json:"log_retention_days,omitempty"`
	Timezone         string `json:"timezone"`
	TrackerMode      string `json:"tracker_mode"`
	DryRun           bool   `json:"dry_run"`

	// Webhook notifications
	WebhookURL     string `json:"webhook_url,omitempty"`
	WebhookType    string `json:"webhook_type,omitempty"`    // "discord", "slack", "generic"
	NotifyRemovals bool   `json:"notify_removals,omitempty"` // fire on torrent removal
	NotifyUntagged bool   `json:"notify_untagged,omitempty"` // fire when new untagged tracker found
}

// DefaultSettings returns the factory-default settings.
func DefaultSettings() Settings {
	return Settings{
		RunInterval:      15,
		LogRetentionDays: 30,
		Timezone:         "UTC",
		TrackerMode:      "all",
		DryRun:           true,
	}
}

// Rule represents a single torrent removal rule.
type Rule struct {
	GroupID        string   `json:"group_id"`
	Label          string   `json:"label"`
	TargetState    string   `json:"target_state"`
	TimeMetric     string   `json:"time_metric"`
	MinTorrents    int      `json:"min_torrents"`
	SortOrder      string   `json:"sort_order"`
	ThresholdValue float64  `json:"threshold_value"`
	ThresholdUnit  string   `json:"threshold_unit"`
	DeleteData     bool     `json:"delete_data"`
	LogicOperator  string   `json:"logic_operator"`
	SeedRatio      *float64 `json:"seed_ratio"`       // nil means not set
	Enabled        *bool    `json:"enabled"`           // nil treated as true (backwards compat)
	TrackerStatus  string   `json:"tracker_status"`    // e.g. "unregistered torrent" — matched case-insensitively against tracker status
}

// IsEnabled returns whether the rule is active. Nil defaults to true.
func (r Rule) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

// Groups is a map of tracker domain -> tag name.
type Groups map[string]string

// --- File I/O (thread-safe via caller-provided locks) ---

var fileMu sync.Mutex

// LoadJSON reads a JSON file into the target, returning the default on error.
func LoadJSON(filepath string, target interface{}) error {
	data, err := os.ReadFile(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // not an error, just use defaults
		}
		return err
	}
	return json.Unmarshal(data, target)
}

// SaveJSON atomically writes data to a JSON file via a temp file swap.
func SaveJSON(fpath string, data interface{}) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	tmp := fpath + ".tmp"
	b, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, fpath)
}

// GetSettings loads the current settings with safe defaults.
func GetSettings() Settings {
	s := DefaultSettings()
	if err := LoadJSON(SettingsFile, &s); err != nil {
		log.Printf("Failed to load settings: %v", err)
	}
	return s
}

// LoadRules loads the rules list from disk.
func LoadRules() []Rule {
	var rules []Rule
	if err := LoadJSON(RulesFile, &rules); err != nil {
		log.Printf("Failed to load rules: %v", err)
	}
	return rules
}

// LoadGroups loads the tracker groups from disk.
func LoadGroups() Groups {
	groups := make(Groups)
	if err := LoadJSON(GroupsFile, &groups); err != nil {
		log.Printf("Failed to load groups: %v", err)
	}
	return groups
}

// ApplyTimezone sets the TZ environment variable and reloads the time package location.
func ApplyTimezone(tz string) {
	os.Setenv("TZ", tz)
	if loc, err := time.LoadLocation(tz); err == nil {
		time.Local = loc
		log.Printf("System: Timezone set to %s", tz)
	} else {
		log.Printf("System Error: Invalid timezone %q: %v", tz, err)
	}
}

// MigrateGroups checks for Python-style tracker domains (e.g. "sync.td-peers.com")
// that match Go-style domains (e.g. "td-peers.com") by suffix, and migrates tags
// to the new key format. This handles the transition from the Python version which
// used full tracker URLs to extract domains, vs the Go version which uses the
// go-libdeluge TrackerHost field (a shorter base domain).
//
// activeDomains should be the set of tracker domains currently reported by Deluge.
// Returns true if any migrations were performed.
//
// Callers must hold engine.ConfigLock before calling — this function both reads
// and writes groups.json.
func MigrateGroups(activeDomains []string) bool {
	groups := LoadGroups()
	if len(groups) == 0 || len(activeDomains) == 0 {
		return false
	}

	migrated := false
	for _, domain := range activeDomains {
		if _, exists := groups[domain]; exists {
			continue // already tagged under the Go-style domain
		}
		// Check if any existing key is a longer version of this domain.
		// e.g. "sync.td-peers.com" ends with ".td-peers.com" or equals "td-peers.com"
		for oldKey, tag := range groups {
			if oldKey == domain {
				continue
			}
			if strings.HasSuffix(oldKey, "."+domain) || strings.HasSuffix(domain, "."+oldKey) {
				groups[domain] = tag
				// Remove the old key so rules bound to it don't silently stop
				// matching — the engine uses the new Go-style domain going forward.
				delete(groups, oldKey)
				log.Printf("System: Migrated tracker tag '%s' from '%s' to '%s'", tag, oldKey, domain)
				migrated = true
				break
			}
		}
	}

	if migrated {
		if err := SaveJSON(GroupsFile, groups); err != nil {
			log.Printf("System Error: Failed to save migrated groups: %v", err)
			return false
		}
	}
	return migrated
}
