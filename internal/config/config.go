package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const AppVersion = "2026.04.09.go"

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
	SeedRatio      *float64 `json:"seed_ratio"` // nil means not set
	Enabled        *bool    `json:"enabled"`     // nil treated as true (backwards compat)
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
