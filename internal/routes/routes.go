package routes

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
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
)

var (
	templates *template.Template
	apiToken  string

	// RescheduleFunc is set by main to allow routes to reschedule the background job.
	RescheduleFunc func(minutes int) error

	safeReturnURLs = map[string]bool{
		"/trackers": true,
		"/rules":    true,
		"/logs":     true,
		"/settings": true,
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

	pages := []string{"trackers.html", "rules.html", "logs.html", "settings.html"}
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
	r.HandleFunc("/trackers", trackersHandler).Methods("GET")
	r.HandleFunc("/rules", rulesHandler).Methods("GET")
	r.HandleFunc("/logs", logsHandler).Methods("GET")
	r.HandleFunc("/settings", settingsHandler).Methods("GET")

	r.HandleFunc("/save_settings", saveSettingsHandler).Methods("POST")
	r.HandleFunc("/export_settings", exportSettingsHandler).Methods("GET")
	r.HandleFunc("/import_settings", importSettingsHandler).Methods("POST")
	r.HandleFunc("/factory_reset_settings", factoryResetSettingsHandler).Methods("POST")
	r.HandleFunc("/factory_reset_all", factoryResetAllHandler).Methods("POST")
	r.HandleFunc("/update_groups", updateGroupsHandler).Methods("POST")
	r.HandleFunc("/add_rule", addRuleHandler).Methods("POST")
	r.HandleFunc("/edit_rule/{index:[0-9]+}", editRuleHandler).Methods("POST")
	r.HandleFunc("/delete_rule/{index:[0-9]+}", deleteRuleHandler).Methods("POST")
	r.HandleFunc("/run_now", runNowHandler).Methods("POST")

	r.HandleFunc("/api/logs", apiLogsHandler).Methods("GET")
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
}

type flashMsg struct {
	Category string
	Message  string
}

// flash message support via cookies (simple approach)
func setFlash(w http.ResponseWriter, category, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash",
		Value:    category + "|" + message,
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
	parts := strings.SplitN(cookie.Value, "|", 2)
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
	// Execute the page template by its filename — ParseFiles uses the filename as the template name
	if err := t.ExecuteTemplate(w, tmplName, data); err != nil {
		log.Printf("Template render error: %v", err)
		http.Error(w, "Internal Server Error", 500)
	}
}

// --- Handlers ---

func indexHandler(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/trackers", http.StatusFound)
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
	if v, err := strconv.Atoi(r.FormValue("run_interval")); err == nil && v >= 1 {
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

	s := config.Settings{
		RunInterval: newInterval,
		Timezone:    newTZ,
		TrackerMode: trackerMode,
		DryRun:      dryRun,
	}

	engine.ConfigLock.Lock()
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

func exportSettingsHandler(w http.ResponseWriter, r *http.Request) {
	engine.ConfigLock.Lock()
	backup := map[string]interface{}{
		"settings": config.GetSettings(),
		"rules":    config.LoadRules(),
		"groups":   config.LoadGroups(),
	}
	engine.ConfigLock.Unlock()

	data, _ := json.MarshalIndent(backup, "", "    ")
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

	if RescheduleFunc != nil {
		s := config.GetSettings()
		RescheduleFunc(s.RunInterval)
	}
	setFlash(w, "success", "Backup imported successfully.")
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func factoryResetSettingsHandler(w http.ResponseWriter, r *http.Request) {
	engine.ConfigLock.Lock()
	config.SaveJSON(config.SettingsFile, config.DefaultSettings())
	engine.ConfigLock.Unlock()

	if RescheduleFunc != nil {
		RescheduleFunc(15)
	}

	log.Println("System: Factory reset performed on application settings only.")
	setFlash(w, "warning", "Settings reset to defaults. Rules and tags were not affected.")
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func factoryResetAllHandler(w http.ResponseWriter, r *http.Request) {
	engine.ConfigLock.Lock()
	config.SaveJSON(config.SettingsFile, config.DefaultSettings())
	config.SaveJSON(config.RulesFile, []config.Rule{})
	config.SaveJSON(config.GroupsFile, config.Groups{})
	engine.ConfigLock.Unlock()

	if RescheduleFunc != nil {
		RescheduleFunc(15)
	}

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
	rules = append(rules, rule)
	config.SaveJSON(config.RulesFile, rules)
	engine.ConfigLock.Unlock()

	setFlash(w, "success", "Rule added successfully.")
	http.Redirect(w, r, "/rules", http.StatusFound)
}

func editRuleHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	idx, _ := strconv.Atoi(mux.Vars(r)["index"])

	rule := parseRuleForm(r)
	if errMsg := validateRule(rule); errMsg != "" {
		setFlash(w, "error", "Rule not updated: "+errMsg)
		http.Redirect(w, r, "/rules", http.StatusFound)
		return
	}

	engine.ConfigLock.Lock()
	rules := config.LoadRules()
	if idx >= 0 && idx < len(rules) {
		rules[idx] = rule
		config.SaveJSON(config.RulesFile, rules)
		setFlash(w, "success", "Rule updated successfully.")
	} else {
		setFlash(w, "error", "Rule not found.")
	}
	engine.ConfigLock.Unlock()

	http.Redirect(w, r, "/rules", http.StatusFound)
}

func deleteRuleHandler(w http.ResponseWriter, r *http.Request) {
	idx, _ := strconv.Atoi(mux.Vars(r)["index"])

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
	}
}

func validateRule(r config.Rule) string {
	if r.GroupID == "" {
		return "Target Tag cannot be empty."
	}
	if r.Label == "" {
		return "Deluge Label cannot be empty."
	}
	if r.ThresholdValue <= 0 {
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
