package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"

	"github.com/krimlocke/delegatarr/internal/config"
	"github.com/krimlocke/delegatarr/internal/deluge"
	"github.com/krimlocke/delegatarr/internal/engine"
	"github.com/krimlocke/delegatarr/internal/notify"
)

var (
	apiToken string

	// RescheduleFunc is set by main to allow routes to reschedule the background job.
	RescheduleFunc func(minutes int) error

	safeReturnURLs = map[string]bool{
		"/dashboard": true,
		"/trackers":  true,
		"/rules":     true,
		"/logs":      true,
		"/settings":  true,
	}
)

// SetAPIToken sets the API token used for internal JS requests.
func SetAPIToken(token string) { apiToken = token }

var (
	funcMap = template.FuncMap{
		"csrfField": func() template.HTML { return "" },
		"eq":        func(a, b interface{}) bool { return fmt.Sprint(a) == fmt.Sprint(b) },
		"add":       func(a, b int) int { return a + b },
		"lower":     strings.ToLower,
		"deref":     func(f *float64) float64 { if f == nil { return 0 }; return *f },
		"jsonStr": func(s string) template.JS {
			b, _ := json.Marshal(s)
			return template.JS(b)
		},
	}
	pageTemplates map[string]*template.Template
	templateDir   string
)

// LoadTemplates parses each page template individually together with base.html.
// This avoids the Go template issue where multiple files defining the same
// block name ("content") overwrite each other in a single ParseGlob call.
func LoadTemplates(dir string) error {
	templateDir = dir
	pageTemplates = make(map[string]*template.Template)

	basePath := filepath.Join(dir, "base.html")

	pages := []string{"dashboard.html", "trackers.html", "rules.html", "logs.html", "settings.html"}
	for _, page := range pages {
		pagePath := filepath.Join(dir, page)
		t, err := template.New("").Funcs(funcMap).ParseFiles(pagePath, basePath)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", page, err)
		}
		pageTemplates[page] = t
	}
	return nil
}

// RegisterRoutes sets up all HTTP routes on the given router.
func RegisterRoutes(r *mux.Router) {
	r.HandleFunc("/", indexHandler).Methods("GET")
	r.HandleFunc("/dashboard", dashboardHandler).Methods("GET")
	r.HandleFunc("/trackers", trackersHandler).Methods("GET")
	r.HandleFunc("/rules", rulesHandler).Methods("GET")
	r.HandleFunc("/logs", logsHandler).Methods("GET")
	r.HandleFunc("/settings", settingsHandler).Methods("GET")

	r.HandleFunc("/save_settings", saveSettingsHandler).Methods("POST")
	r.HandleFunc("/save_notifications", saveNotificationsHandler).Methods("POST")
	r.HandleFunc("/test_webhook", testWebhookHandler).Methods("POST")
	r.HandleFunc("/export_settings", exportSettingsHandler).Methods("GET")
	r.HandleFunc("/import_settings", importSettingsHandler).Methods("POST")
	r.HandleFunc("/factory_reset_settings", factoryResetSettingsHandler).Methods("POST")
	r.HandleFunc("/factory_reset_all", factoryResetAllHandler).Methods("POST")
	r.HandleFunc("/update_groups", updateGroupsHandler).Methods("POST")
	r.HandleFunc("/add_rule", addRuleHandler).Methods("POST")
	r.HandleFunc("/edit_rule/{index:[0-9]+}", editRuleHandler).Methods("POST")
	r.HandleFunc("/toggle_rule/{index:[0-9]+}", toggleRuleHandler).Methods("POST")
	r.HandleFunc("/delete_rule/{index:[0-9]+}", deleteRuleHandler).Methods("POST")
	r.HandleFunc("/bulk_rules", bulkRulesHandler).Methods("POST")
	r.HandleFunc("/run_now", runNowHandler).Methods("POST")

	r.HandleFunc("/api/logs", apiLogsHandler).Methods("GET")
	r.HandleFunc("/api/rule/{index:[0-9]+}", apiRuleHandler).Methods("GET")
	r.HandleFunc("/export_logs", exportLogsHandler).Methods("GET")
	r.HandleFunc("/manifest.json", manifestHandler).Methods("GET")
	r.HandleFunc("/sw.js", serviceWorkerHandler).Methods("GET")
	r.HandleFunc("/favicon.ico", faviconHandler).Methods("GET")
}

// --- Template rendering ---

type pageData struct {
	Version         string
	DelugeConnected bool
	AppSettings     config.Settings
	APIToken        string
	ActivePage      string
	PageTitle       string
	CSRFField       template.HTML
	Flash           []flashMsg

	// Page-specific data
	TrackerSummary engine.TrackerSummary
	Groups         config.Groups
	RulesList      []config.Rule
	UniqueTags     []string
	UniqueLabels   []string
	LogContent     string

	// Dashboard data
	DashTotalTorrents  int
	DashTrackerCount   int
	DashRemovedToday   int
	DashRemovedWeek    int
	DashActiveRules    int
	DashDisabledRules  int
	DashRecentEvents   []DashEvent
	DashRecentRemovals []DashEvent
	DashRuleStats      []DashRuleStat
	DashTrackerStats   []DashTrackerStat
	DashUptime         string
	DashLastRun        string
	DashNextRun        string
	DashInterval       int
}

type flashMsg struct {
	Category string
	Message  string
}

// Dashboard types
type DashEvent struct {
	Color     string
	Text      string
	Detail    string
	TimeAgo   string
	IsRemoval bool
}

type DashRuleStat struct {
	Tag     string
	Count   int
	Percent int
}

type DashTrackerStat struct {
	Name    string
	Count   int
	Percent int
}

// flash message support via cookies (simple approach)
func setFlash(w http.ResponseWriter, category, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash",
		Value:    url.QueryEscape(category + "|" + message),
		Path:     "/",
		MaxAge:   5,
		HttpOnly: true,
	})
}

func getFlash(w http.ResponseWriter, r *http.Request) []flashMsg {
	cookie, err := r.Cookie("flash")
	if err != nil {
		return nil
	}
	// clear it
	http.SetCookie(w, &http.Cookie{Name: "flash", Path: "/", MaxAge: -1})
	decoded, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return nil
	}
	parts := strings.SplitN(decoded, "|", 2)
	if len(parts) == 2 {
		return []flashMsg{{Category: parts[0], Message: parts[1]}}
	}
	return nil
}

func renderPage(w http.ResponseWriter, r *http.Request, tmplName string, data *pageData) {
	data.Version = config.AppVersion
	data.DelugeConnected = deluge.IsAlive()
	data.APIToken = apiToken
	if data.AppSettings == (config.Settings{}) {
		data.AppSettings = config.GetSettings()
	}
	data.CSRFField = csrf.TemplateField(r)
	data.Flash = getFlash(w, r)

	t, ok := pageTemplates[tmplName]
	if !ok {
		log.Printf("Template not found: %s", tmplName)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	// Render into a buffer first so that a template error mid-execution does not
	// send a partial HTML response to the client before we can write the 500.
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, tmplName, data); err != nil {
		log.Printf("Template render error: %v", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// --- Handlers ---

func indexHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	summary, _ := engine.GetDashboardData()
	settings := config.GetSettings()

	engine.ConfigLock.Lock()
	rules := config.LoadRules()
	engine.ConfigLock.Unlock()

	// Count active/disabled rules
	activeRules, disabledRules := 0, 0
	for _, rule := range rules {
		if rule.IsEnabled() {
			activeRules++
		} else {
			disabledRules++
		}
	}

	// Total torrents and tracker count
	totalTorrents := 0
	for _, count := range summary {
		totalTorrents += count
	}

	// Parse logs for recent events and removal stats
	logData, err := os.ReadFile(config.LogFile)
	var logLines []string
	if err == nil && len(logData) > 0 {
		logLines = strings.Split(strings.TrimRight(string(logData), "\n"), "\n")
	}

	now := time.Now()
	todayStr := now.Format("2006-01-02")
	weekAgo := now.AddDate(0, 0, -7)

	var recentEvents []DashEvent
	var recentRemovals []DashEvent
	ruleRemovalCounts := map[string]int{}
	removedToday, removedWeek := 0, 0

	// Process logs in reverse (newest first) for recent events
	for i := len(logLines) - 1; i >= 0; i-- {
		line := logLines[i]
		if len(line) < 20 {
			continue
		}

		// Parse timestamp
		tsStr := ""
		if line[0] == '[' {
			if end := strings.Index(line, "]"); end > 0 {
				tsStr = line[1:end]
			}
		}

		lineTime, _ := time.ParseInLocation("2006-01-02 15:04:05", tsStr, time.Local)
		lower := strings.ToLower(line)

		// Count removals
		if strings.Contains(lower, "rule matched") && strings.Contains(lower, "removed:") {
			if strings.Contains(tsStr, todayStr) {
				removedToday++
			}
			if !lineTime.IsZero() && lineTime.After(weekAgo) {
				removedWeek++
				// Extract tag from "(Tag: xxx,"
				if tagIdx := strings.Index(line, "(Tag: "); tagIdx >= 0 {
					rest := line[tagIdx+6:]
					if commaIdx := strings.Index(rest, ","); commaIdx >= 0 {
						tag := rest[:commaIdx]
						ruleRemovalCounts[tag]++
					}
				}
			}
		}

		// Build recent events (max 8) and recent removals (max 10)
		var ev *DashEvent
		isRemoval := false
		if (strings.Contains(lower, "rule matched") && strings.Contains(lower, "removed:")) ||
			(strings.Contains(lower, "[dry run]") && strings.Contains(lower, "would have removed:")) {
			name := ""
			if nameStart := strings.Index(line, "Removed: '"); nameStart >= 0 {
				rest := line[nameStart+10:]
				if nameEnd := strings.Index(rest, "'"); nameEnd >= 0 {
					name = rest[:nameEnd]
					if len(name) > 45 {
						name = name[:42] + "..."
					}
				}
			}
			if name == "" {
				if nameStart := strings.Index(line, "removed: '"); nameStart >= 0 {
					rest := line[nameStart+10:]
					if nameEnd := strings.Index(rest, "'"); nameEnd >= 0 {
						name = rest[:nameEnd]
						if len(name) > 45 {
							name = name[:42] + "..."
						}
					}
				}
			}
			tag, state, metric, deleteData := "", "", "", ""
			if parenIdx := strings.Index(line, "(Tag: "); parenIdx >= 0 {
				inner := line[parenIdx+1:]
				if closeIdx := strings.Index(inner, ")"); closeIdx >= 0 {
					inner = inner[:closeIdx]
				}
				for _, part := range strings.Split(inner, ",") {
					part = strings.TrimSpace(part)
					if strings.HasPrefix(part, "Tag: ") {
						tag = strings.TrimPrefix(part, "Tag: ")
					} else if strings.HasPrefix(part, "State: ") {
						state = strings.TrimPrefix(part, "State: ")
					} else if strings.HasPrefix(part, "Metric: ") {
						metric = strings.TrimPrefix(part, "Metric: ")
						switch metric {
						case "seeding_time":
							metric = "Seeding time"
						case "time_added":
							metric = "Time since added"
						case "time_paused":
							metric = "Time paused"
						}
					} else if strings.HasPrefix(part, "Delete Data: ") {
						if strings.Contains(strings.ToLower(part), "true") {
							deleteData = "Data deleted"
						} else {
							deleteData = "Data kept"
						}
					}
				}
			}

			prefix := ""
			if strings.Contains(lower, "[dry run]") {
				prefix = "[DRY RUN] "
			}

			detail := "Tag: " + tag
			if state != "" {
				detail += " · " + state
			}
			if metric != "" {
				detail += " · " + metric
			}
			if deleteData != "" {
				detail += " · " + deleteData
			}

			ev = &DashEvent{Color: "accent", Text: prefix + name + " removed", Detail: detail, IsRemoval: true}
			isRemoval = true
		} else if len(recentEvents) < 8 {
			if strings.Contains(lower, "skipped") && strings.Contains(lower, "minimum keep") {
				ev = &DashEvent{Color: "warning", Text: "Rule skipped — below minimum keep"}
			} else if strings.Contains(lower, "error") || strings.Contains(lower, "failed") {
				msg := line
				if bracketEnd := strings.Index(msg, "] "); bracketEnd >= 0 {
					msg = msg[bracketEnd+2:]
				}
				if len(msg) > 60 {
					msg = msg[:57] + "..."
				}
				ev = &DashEvent{Color: "danger", Text: msg}
			} else if strings.Contains(lower, "completed. successfully") {
				ev = &DashEvent{Color: "success", Text: "Engine run completed"}
			} else if strings.Contains(lower, "no torrents met removal criteria") {
				ev = &DashEvent{Color: "muted", Text: "No torrents met removal criteria", Detail: "Scheduled run"}
			} else if strings.Contains(lower, "deluge connection established") {
				ev = &DashEvent{Color: "success", Text: "Deluge connection established", Detail: "System"}
			}
		}

		if ev != nil {
			ev.TimeAgo = formatTimeAgo(lineTime, now)

			// Deduplicate: skip if the previous event has the same text and timestamp
			isDupe := false
			if len(recentEvents) > 0 {
				prev := recentEvents[len(recentEvents)-1]
				if prev.Text == ev.Text && prev.TimeAgo == ev.TimeAgo {
					isDupe = true
				}
			}

			if !isDupe && len(recentEvents) < 8 {
				recentEvents = append(recentEvents, *ev)
			}

			// Collect removals separately for the "Last 10 Removed" card
			if isRemoval && len(recentRemovals) < 10 {
				recentRemovals = append(recentRemovals, *ev)
			}
		}
	}

	// Build rule stats sorted by count
	var ruleStats []DashRuleStat
	maxRemovals := 0
	for _, count := range ruleRemovalCounts {
		if count > maxRemovals {
			maxRemovals = count
		}
	}
	for tag, count := range ruleRemovalCounts {
		pct := 0
		if maxRemovals > 0 {
			pct = (count * 100) / maxRemovals
		}
		ruleStats = append(ruleStats, DashRuleStat{Tag: tag, Count: count, Percent: pct})
	}
	sort.Slice(ruleStats, func(i, j int) bool { return ruleStats[i].Count > ruleStats[j].Count })

	// Build tracker stats
	var trackerStats []DashTrackerStat
	maxTorrents := 0
	for _, count := range summary {
		if count > maxTorrents {
			maxTorrents = count
		}
	}
	for domain, count := range summary {
		pct := 0
		if maxTorrents > 0 {
			pct = (count * 100) / maxTorrents
		}
		trackerStats = append(trackerStats, DashTrackerStat{Name: domain, Count: count, Percent: pct})
	}
	sort.Slice(trackerStats, func(i, j int) bool { return trackerStats[i].Count > trackerStats[j].Count })

	// Uptime
	uptime := formatDuration(time.Since(config.StartTime))

	// Next/last run estimates
	interval := settings.RunInterval
	if interval < 1 {
		interval = 15
	}

	renderPage(w, r, "dashboard.html", &pageData{
		ActivePage:        "dashboard",
		PageTitle:         "Dashboard",
		DashTotalTorrents: totalTorrents,
		DashTrackerCount:  len(summary),
		DashRemovedToday:  removedToday,
		DashRemovedWeek:   removedWeek,
		DashActiveRules:   activeRules,
		DashDisabledRules: disabledRules,
		DashRecentEvents:   recentEvents,
		DashRecentRemovals: recentRemovals,
		DashRuleStats:      ruleStats,
		DashTrackerStats:  trackerStats,
		DashUptime:        uptime,
		DashInterval:      interval,
	})
}

func trackersHandler(w http.ResponseWriter, r *http.Request) {
	summary, _ := engine.GetDashboardData()
	engine.ConfigLock.Lock()
	groups := config.LoadGroups()
	engine.ConfigLock.Unlock()

	renderPage(w, r, "trackers.html", &pageData{
		ActivePage:     "trackers",
		PageTitle:      "Tracker Configuration",
		TrackerSummary: summary,
		Groups:         groups,
	})
}

func rulesHandler(w http.ResponseWriter, r *http.Request) {
	_, uniqueLabels := engine.GetDashboardData()
	engine.ConfigLock.Lock()
	groups := config.LoadGroups()
	rulesList := config.LoadRules()
	engine.ConfigLock.Unlock()

	tagsSet := map[string]bool{}
	for _, tag := range groups {
		if strings.TrimSpace(tag) != "" {
			tagsSet[tag] = true
		}
	}
	uniqueTags := make([]string, 0, len(tagsSet))
	for t := range tagsSet {
		uniqueTags = append(uniqueTags, t)
	}
	sort.Strings(uniqueTags)

	renderPage(w, r, "rules.html", &pageData{
		ActivePage:   "rules",
		PageTitle:    "Removal Rules Engine",
		RulesList:    rulesList,
		UniqueTags:   uniqueTags,
		UniqueLabels: uniqueLabels,
	})
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	renderPage(w, r, "logs.html", &pageData{
		ActivePage: "logs",
		PageTitle:  "Activity Logs",
		LogContent: getTailLogs(1500),
	})
}

func settingsHandler(w http.ResponseWriter, r *http.Request) {
	s := config.GetSettings()
	if s.Timezone == "" {
		s.Timezone = os.Getenv("TZ")
		if s.Timezone == "" {
			s.Timezone = "UTC"
		}
	}
	renderPage(w, r, "settings.html", &pageData{
		ActivePage:  "settings",
		PageTitle:   "Settings",
		AppSettings: s,
	})
}

func saveSettingsHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	newInterval := 15
	if v, err := strconv.Atoi(r.FormValue("run_interval")); err == nil && v >= 1 && v <= 1440 {
		newInterval = v
	}

	newTZ := r.FormValue("timezone")
	if !isValidTimezone(newTZ) {
		setFlash(w, "error", "Invalid timezone.")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}

	trackerMode := r.FormValue("tracker_mode")
	if trackerMode != "all" && trackerMode != "top" {
		trackerMode = "all"
	}

	dryRun := r.FormValue("dry_run") != ""

	// Load existing settings first to preserve notification fields that are
	// managed by the separate saveNotificationsHandler.
	engine.ConfigLock.Lock()
	s := config.GetSettings()
	s.RunInterval = newInterval
	s.Timezone = newTZ
	s.TrackerMode = trackerMode
	s.DryRun = dryRun

	if err := config.SaveJSON(config.SettingsFile, s); err != nil {
		engine.ConfigLock.Unlock()
		log.Printf("Failed to save settings: %v", err)
		setFlash(w, "error", "Failed to save settings.")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}
	engine.ConfigLock.Unlock()

	config.ApplyTimezone(newTZ)

	if RescheduleFunc != nil {
		if err := RescheduleFunc(newInterval); err != nil {
			log.Printf("Failed to reschedule job: %v", err)
			setFlash(w, "warning", "Settings saved, but scheduler could not be updated.")
		} else {
			dryRunState := "OFF"
			if dryRun {
				dryRunState = "ON"
			}
			log.Printf("System: Settings updated. Interval: %dm, TZ: %s, Tracker Mode: %s, Dry Run: %s.",
				newInterval, newTZ, trackerMode, dryRunState)
			setFlash(w, "success", "Settings saved successfully.")
		}
	}

	http.Redirect(w, r, "/settings", http.StatusFound)
}

func saveNotificationsHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	engine.ConfigLock.Lock()
	s := config.GetSettings()

	s.WebhookType = truncStr(strings.TrimSpace(r.FormValue("webhook_type")), 20)
	s.WebhookURL = truncStr(strings.TrimSpace(r.FormValue("webhook_url")), 500)
	s.NotifyRemovals = r.FormValue("notify_removals") != ""
	s.NotifyUntagged = r.FormValue("notify_untagged") != ""

	// Clear URL if type is disabled
	if s.WebhookType == "" {
		s.WebhookURL = ""
		s.NotifyRemovals = false
		s.NotifyUntagged = false
	}

	if err := config.SaveJSON(config.SettingsFile, s); err != nil {
		engine.ConfigLock.Unlock()
		setFlash(w, "error", "Failed to save notification settings.")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}
	engine.ConfigLock.Unlock()

	log.Printf("System: Notification settings updated. Type: %s, Removals: %v, Untagged: %v",
		s.WebhookType, s.NotifyRemovals, s.NotifyUntagged)
	setFlash(w, "success", "Notification settings saved.")
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func testWebhookHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	url := strings.TrimSpace(r.FormValue("webhook_url"))
	webhookType := strings.TrimSpace(r.FormValue("webhook_type"))

	if url == "" || webhookType == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": "URL and type required"})
		return
	}

	// Send a test notification directly (does not require saving settings)
	if err := notify.SendTestNotification(url, webhookType); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func exportSettingsHandler(w http.ResponseWriter, r *http.Request) {
	engine.ConfigLock.Lock()
	backup := map[string]interface{}{
		"settings": config.GetSettings(),
		"rules":    config.LoadRules(),
		"groups":   config.LoadGroups(),
	}
	engine.ConfigLock.Unlock()

	data, err := json.MarshalIndent(backup, "", "    ")
	if err != nil {
		log.Printf("Export error: %v", err)
		http.Error(w, "Failed to export settings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=delegatarr_backup.json")
	w.Write(data)
}

func importSettingsHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(5 << 20) // 5MB limit

	file, _, err := r.FormFile("settings_file")
	if err != nil {
		setFlash(w, "error", "No file selected.")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, 5<<20))
	if err != nil {
		setFlash(w, "error", "Failed to read file.")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		setFlash(w, "error", "Import failed: file is not valid JSON.")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}

	engine.ConfigLock.Lock()
	defer engine.ConfigLock.Unlock()

	if settingsRaw, ok := data["settings"]; ok {
		var s config.Settings
		if err := json.Unmarshal(settingsRaw, &s); err == nil {
			if !isValidTimezone(s.Timezone) {
				setFlash(w, "error", "Import failed: Invalid timezone.")
				http.Redirect(w, r, "/settings", http.StatusFound)
				return
			}
			if s.RunInterval < 1 || s.RunInterval > 1440 {
				setFlash(w, "error", "Import failed: Invalid run interval.")
				http.Redirect(w, r, "/settings", http.StatusFound)
				return
			}
			config.SaveJSON(config.SettingsFile, s)
		}
	}
	if rulesRaw, ok := data["rules"]; ok {
		var rules []config.Rule
		if err := json.Unmarshal(rulesRaw, &rules); err == nil {
			config.SaveJSON(config.RulesFile, rules)
		}
	}
	if groupsRaw, ok := data["groups"]; ok {
		var groups config.Groups
		if err := json.Unmarshal(groupsRaw, &groups); err == nil {
			config.SaveJSON(config.GroupsFile, groups)
		}
	}

	log.Println("System: Full backup imported successfully.")

	// Apply the imported timezone to the running process immediately.
	importedSettings := config.GetSettings()
	if importedSettings.Timezone != "" {
		config.ApplyTimezone(importedSettings.Timezone)
	}

	if RescheduleFunc != nil {
		if err := RescheduleFunc(importedSettings.RunInterval); err != nil {
			log.Printf("Failed to reschedule job after import: %v", err)
			setFlash(w, "warning", "Backup imported, but scheduler could not be updated. Restart may be required.")
		} else {
			setFlash(w, "success", "Backup imported successfully.")
		}
	} else {
		setFlash(w, "success", "Backup imported successfully.")
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func factoryResetSettingsHandler(w http.ResponseWriter, r *http.Request) {
	engine.ConfigLock.Lock()
	if err := config.SaveJSON(config.SettingsFile, config.DefaultSettings()); err != nil {
		engine.ConfigLock.Unlock()
		log.Printf("Failed to save default settings during reset: %v", err)
		setFlash(w, "error", "Factory reset failed.")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}
	engine.ConfigLock.Unlock()

	if RescheduleFunc != nil {
		if err := RescheduleFunc(15); err != nil {
			log.Printf("Failed to reschedule after settings reset: %v", err)
		}
	}

	config.ApplyTimezone("UTC")
	log.Println("System: Factory reset performed on application settings only.")
	setFlash(w, "warning", "Settings reset to defaults. Rules and tags were not affected.")
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func factoryResetAllHandler(w http.ResponseWriter, r *http.Request) {
	engine.ConfigLock.Lock()
	var resetErr error
	if err := config.SaveJSON(config.SettingsFile, config.DefaultSettings()); err != nil {
		resetErr = err
	}
	if err := config.SaveJSON(config.RulesFile, []config.Rule{}); err != nil {
		resetErr = err
	}
	if err := config.SaveJSON(config.GroupsFile, config.Groups{}); err != nil {
		resetErr = err
	}
	engine.ConfigLock.Unlock()

	if resetErr != nil {
		log.Printf("Error during full factory reset: %v", resetErr)
		setFlash(w, "error", "Factory reset encountered errors. Some files may not have been reset.")
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}

	if RescheduleFunc != nil {
		if err := RescheduleFunc(15); err != nil {
			log.Printf("Failed to reschedule after full reset: %v", err)
		}
	}

	config.ApplyTimezone("UTC")
	log.Printf("System: CRITICAL - Full factory reset performed. All settings, rules, and tags have been wiped.")
	setFlash(w, "error", "Full factory reset complete. All settings, rules, and tags have been wiped.")
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func updateGroupsHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	engine.ConfigLock.Lock()
	groups := config.LoadGroups()
	for tracker, values := range r.Form {
		if tracker == "gorilla.csrf.Token" {
			continue
		}
		groupID := strings.TrimSpace(values[0])
		tracker = strings.TrimSpace(tracker)
		if len(tracker) > 255 {
			tracker = tracker[:255]
		}
		if len(groupID) > 100 {
			groupID = groupID[:100]
		}
		if groupID == "" {
			continue
		}
		if strings.ToUpper(groupID) == "REMOVE" {
			delete(groups, tracker)
		} else {
			groups[tracker] = groupID
		}
	}
	config.SaveJSON(config.GroupsFile, groups)
	engine.ConfigLock.Unlock()

	setFlash(w, "success", "Tracker tags saved.")
	http.Redirect(w, r, "/trackers", http.StatusFound)
}

func addRuleHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	rule := parseRuleForm(r)
	if errMsg := validateRule(rule); errMsg != "" {
		setFlash(w, "error", "Rule not saved: "+errMsg)
		http.Redirect(w, r, "/rules", http.StatusFound)
		return
	}

	engine.ConfigLock.Lock()
	rules := config.LoadRules()

	// Check for duplicate tag + label + state combination
	dupIdx := findDuplicateRule(rules, rule, -1)

	rules = append(rules, rule)
	config.SaveJSON(config.RulesFile, rules)
	engine.ConfigLock.Unlock()

	if dupIdx >= 0 {
		setFlash(w, "warning", fmt.Sprintf(
			"Rule added, but note: rule #%d already targets the same Tag '%s' + Label '%s' + State '%s'. This may cause conflicts.",
			dupIdx+1, rule.GroupID, rule.Label, rule.TargetState))
	} else {
		setFlash(w, "success", "Rule added successfully.")
	}
	http.Redirect(w, r, "/rules", http.StatusFound)
}

func editRuleHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	idx := parseIndex(w, r)
	if idx < 0 {
		return
	}

	rule := parseRuleForm(r)
	if errMsg := validateRule(rule); errMsg != "" {
		setFlash(w, "error", "Rule not updated: "+errMsg)
		http.Redirect(w, r, "/rules", http.StatusFound)
		return
	}

	engine.ConfigLock.Lock()
	rules := config.LoadRules()
	if idx >= 0 && idx < len(rules) {
		// Check for duplicate tag + label + state combination (excluding self)
		if dupIdx := findDuplicateRule(rules, rule, idx); dupIdx >= 0 {
			setFlash(w, "warning", fmt.Sprintf(
				"Rule updated, but note: rule #%d already targets the same Tag '%s' + Label '%s' + State '%s'. This may cause conflicts.",
				dupIdx+1, rule.GroupID, rule.Label, rule.TargetState))
		} else {
			setFlash(w, "success", "Rule updated successfully.")
		}
		rule.Enabled = rules[idx].Enabled
		rules[idx] = rule
		config.SaveJSON(config.RulesFile, rules)
	} else {
		setFlash(w, "error", "Rule not found.")
	}
	engine.ConfigLock.Unlock()

	http.Redirect(w, r, "/rules", http.StatusFound)
}

func toggleRuleHandler(w http.ResponseWriter, r *http.Request) {
	idx := parseIndex(w, r)
	if idx < 0 {
		return
	}

	engine.ConfigLock.Lock()
	rules := config.LoadRules()
	if idx >= 0 && idx < len(rules) {
		current := rules[idx].IsEnabled()
		toggled := !current
		rules[idx].Enabled = &toggled
		config.SaveJSON(config.RulesFile, rules)
		if toggled {
			setFlash(w, "success", "Rule enabled.")
		} else {
			setFlash(w, "warning", "Rule disabled.")
		}
	}
	engine.ConfigLock.Unlock()

	http.Redirect(w, r, "/rules", http.StatusFound)
}

func deleteRuleHandler(w http.ResponseWriter, r *http.Request) {
	idx := parseIndex(w, r)
	if idx < 0 {
		return
	}

	engine.ConfigLock.Lock()
	rules := config.LoadRules()
	if idx >= 0 && idx < len(rules) {
		rules = append(rules[:idx], rules[idx+1:]...)
		config.SaveJSON(config.RulesFile, rules)
		setFlash(w, "warning", "Rule deleted.")
	}
	engine.ConfigLock.Unlock()

	http.Redirect(w, r, "/rules", http.StatusFound)
}

func bulkRulesHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	action := r.FormValue("bulk_action")

	// Parse selected indices
	var indices []int
	for _, v := range r.Form["rule_indices"] {
		if idx, err := strconv.Atoi(v); err == nil {
			indices = append(indices, idx)
		}
	}

	if len(indices) == 0 {
		setFlash(w, "error", "No rules selected.")
		http.Redirect(w, r, "/rules", http.StatusFound)
		return
	}

	engine.ConfigLock.Lock()
	rules := config.LoadRules()

	switch action {
	case "enable":
		enabled := true
		for _, idx := range indices {
			if idx >= 0 && idx < len(rules) {
				rules[idx].Enabled = &enabled
			}
		}
		config.SaveJSON(config.RulesFile, rules)
		setFlash(w, "success", fmt.Sprintf("%d rule(s) enabled.", len(indices)))

	case "disable":
		disabled := false
		for _, idx := range indices {
			if idx >= 0 && idx < len(rules) {
				rules[idx].Enabled = &disabled
			}
		}
		config.SaveJSON(config.RulesFile, rules)
		setFlash(w, "warning", fmt.Sprintf("%d rule(s) disabled.", len(indices)))

	case "delete":
		// Delete in reverse order to keep indices stable
		sort.Sort(sort.Reverse(sort.IntSlice(indices)))
		deleted := 0
		for _, idx := range indices {
			if idx >= 0 && idx < len(rules) {
				rules = append(rules[:idx], rules[idx+1:]...)
				deleted++
			}
		}
		config.SaveJSON(config.RulesFile, rules)
		setFlash(w, "warning", fmt.Sprintf("%d rule(s) deleted.", deleted))

	default:
		setFlash(w, "error", "Unknown bulk action.")
	}

	engine.ConfigLock.Unlock()
	http.Redirect(w, r, "/rules", http.StatusFound)
}

func runNowHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	go engine.ProcessTorrents("Manual")

	returnURL := r.FormValue("return_url")
	if !safeReturnURLs[returnURL] {
		returnURL = "/trackers"
	}
	http.Redirect(w, r, returnURL, http.StatusFound)
}

func apiLogsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-API-Token") != apiToken {
		http.Error(w, "Forbidden", 403)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(getTailLogs(1500)))
}

func exportLogsHandler(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(config.LogFile)
	if err != nil {
		http.Error(w, "No log file found", 404)
		return
	}
	filename := "delegatarr-logs-" + time.Now().Format("2006-01-02") + ".log"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Write(data)
}

func apiRuleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-API-Token") != apiToken {
		http.Error(w, "Forbidden", 403)
		return
	}
	idx := parseIndex(w, r)
	if idx < 0 {
		return
	}

	engine.ConfigLock.Lock()
	rules := config.LoadRules()
	engine.ConfigLock.Unlock()

	if idx < 0 || idx >= len(rules) {
		http.Error(w, "Not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rules[idx])
}

func manifestHandler(w http.ResponseWriter, r *http.Request) {
	manifest := map[string]interface{}{
		"name":             "Delegatarr",
		"short_name":       "Delegatarr",
		"start_url":        "/",
		"display":          "standalone",
		"background_color": "#0f172a",
		"theme_color":      "#0f172a",
		"icons": []map[string]string{
			{"src": "/favicon.ico", "sizes": "192x192", "type": "image/png"},
			{"src": "/favicon.ico", "sizes": "512x512", "type": "image/png"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(manifest)
}

func serviceWorkerHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	w.Write([]byte(`self.addEventListener('install', (e) => { self.skipWaiting(); });
self.addEventListener('fetch', (e) => {});`))
}

func faviconHandler(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(config.LogoFile); err == nil {
		http.ServeFile(w, r, config.LogoFile)
		return
	}
	// fallback to bundled logo.png
	bundled := filepath.Join(filepath.Dir(filepath.Dir(os.Args[0])), "logo.png")
	if _, err := os.Stat(bundled); err == nil {
		http.ServeFile(w, r, bundled)
		return
	}
	http.NotFound(w, r)
}

// --- Helpers ---

func getTailLogs(n int) string {
	data, err := os.ReadFile(config.LogFile)
	if err != nil {
		return "No logs generated yet."
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	// Reverse
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	if len(lines) == 0 {
		return "No logs generated yet."
	}
	return strings.Join(lines, "\n")
}

func parseRuleForm(r *http.Request) config.Rule {
	thresholdVal := 0.0
	if v, err := strconv.ParseFloat(r.FormValue("threshold_value"), 64); err == nil {
		thresholdVal = v
	}

	minTorrents := 0
	if v, err := strconv.Atoi(r.FormValue("min_torrents")); err == nil {
		minTorrents = v
	}

	var seedRatio *float64
	if raw := strings.TrimSpace(r.FormValue("seed_ratio")); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			seedRatio = &v
		}
	}

	sortOrder := r.FormValue("sort_order")
	if sortOrder == "oldest_first" {
		sortOrder = "oldest_added"
	}
	if sortOrder == "newest_first" {
		sortOrder = "newest_added"
	}

	groupID := r.FormValue("group_id")
	if len(groupID) > 100 {
		groupID = groupID[:100]
	}
	label := r.FormValue("label")
	if len(label) > 100 {
		label = label[:100]
	}

	enabled := true
	return config.Rule{
		GroupID:        strings.TrimSpace(groupID),
		Label:          strings.TrimSpace(label),
		TargetState:    truncStr(r.FormValue("target_state"), 50),
		TimeMetric:     truncStr(r.FormValue("time_metric"), 50),
		MinTorrents:    minTorrents,
		SortOrder:      sortOrder,
		ThresholdValue: thresholdVal,
		ThresholdUnit:  truncStr(r.FormValue("threshold_unit"), 20),
		DeleteData:     r.FormValue("delete_data") != "",
		LogicOperator:  truncStr(r.FormValue("logic_operator"), 10),
		SeedRatio:      seedRatio,
		Enabled:        &enabled,
		TrackerStatus:  truncStr(strings.TrimSpace(r.FormValue("tracker_status")), 200),
	}
}

func validateRule(r config.Rule) string {
	if r.GroupID == "" {
		return "Target Tag cannot be empty."
	}
	if r.Label == "" {
		return "Deluge Label cannot be empty."
	}
	if r.ThresholdValue <= 0 && r.TrackerStatus == "" {
		return "Threshold time must be greater than 0 (or set a Tracker Status condition)."
	}
	if r.ThresholdValue < 0 {
		return "Threshold time must be greater than 0."
	}
	if r.MinTorrents < 0 {
		return "Min Keep cannot be negative."
	}
	return ""
}

func isValidTimezone(tz string) bool {
	if tz == "" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

func truncStr(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// parseIndex extracts and validates a rule index from the URL. Returns -1 on error.
func parseIndex(w http.ResponseWriter, r *http.Request) int {
	idx, err := strconv.Atoi(mux.Vars(r)["index"])
	if err != nil || idx < 0 {
		http.Error(w, "Invalid rule index", http.StatusBadRequest)
		return -1
	}
	return idx
}

// findDuplicateRule checks if any existing rule (excluding excludeIdx) shares the same
// tag + label + state combination. Returns the index of the first duplicate or -1.
func findDuplicateRule(rules []config.Rule, candidate config.Rule, excludeIdx int) int {
	for i, r := range rules {
		if i == excludeIdx {
			continue
		}
		if strings.EqualFold(r.GroupID, candidate.GroupID) &&
			strings.EqualFold(r.Label, candidate.Label) &&
			strings.EqualFold(r.TargetState, candidate.TargetState) {
			return i
		}
	}
	return -1
}

func formatTimeAgo(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	minutes := int(d.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
