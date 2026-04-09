package engine

import (
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/krimlocke/delegatarr/internal/config"
	"github.com/krimlocke/delegatarr/internal/deluge"
)

var (
	EngineLock sync.Mutex
	ConfigLock sync.Mutex
)

// TrackerSummary maps tracker domain -> torrent count.
type TrackerSummary map[string]int

// GetDashboardData retrieves active tracker domains and unique labels from Deluge.
func GetDashboardData() (TrackerSummary, []string) {
	c, err := deluge.NewClient()
	if err != nil {
		log.Printf("Deluge Error: %v", err)
		return TrackerSummary{}, nil
	}
	defer c.Close()

	torrents, err := c.TorrentsStatus(deluge.StateUnspecified, nil)
	if err != nil {
		log.Printf("Deluge Error: %v", err)
		return TrackerSummary{}, nil
	}

	// Fetch labels from the Label plugin (returns empty map if plugin disabled)
	labelMap := deluge.FetchLabels(c)

	settings := config.GetSettings()
	trackerMode := settings.TrackerMode

	summary := TrackerSummary{}
	labelsSet := map[string]bool{}

	for hash, ts := range torrents {
		t := deluge.FromStatus(ts, labelMap[hash])

		if t.Label != "" {
			labelsSet[t.Label] = true
		}

		trackerURLs := extractTrackerURLs(t)
		if trackerMode == "top" && len(trackerURLs) > 0 {
			trackerURLs = trackerURLs[:1]
		}

		seen := map[string]bool{}
		for _, rawURL := range trackerURLs {
			domain := extractDomain(rawURL)
			if domain != "" && !seen[domain] {
				seen[domain] = true
				summary[domain]++
			}
		}
	}

	labels := make([]string, 0, len(labelsSet))
	for l := range labelsSet {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	return summary, labels
}

// torrentCandidate holds a torrent being evaluated for removal.
type torrentCandidate struct {
	ID           string
	Name         string
	SeedingHours float64
	TimeAdded    float64
	TriggerValue float64
	Ratio        float64
}

// ProcessTorrents is the background engine that evaluates torrents against removal rules.
func ProcessTorrents(runType string) {
	if !EngineLock.TryLock() {
		log.Printf("%s Engine Run: Skipped. Another run is already in progress.", runType)
		return
	}
	defer EngineLock.Unlock()

	ConfigLock.Lock()
	groups := config.LoadGroups()
	rules := config.LoadRules()
	ConfigLock.Unlock()

	if len(rules) == 0 || len(groups) == 0 {
		log.Printf("%s Engine Run: Skipped. No tags or rules are configured yet.", runType)
		return
	}

	currentTime := float64(time.Now().Unix())
	settings := config.GetSettings()
	trackerMode := settings.TrackerMode
	isDryRun := settings.DryRun

	c, err := deluge.NewClient()
	if err != nil {
		log.Printf("Engine Run: Skipped. %v", err)
		return
	}
	defer c.Close()

	torrents, err := c.TorrentsStatus(deluge.StateUnspecified, nil)
	if err != nil {
		log.Printf("Engine Run Error: %v", err)
		return
	}

	// Fetch labels from the Label plugin (returns empty map if plugin disabled)
	labelMap := deluge.FetchLabels(c)

	type removalEntry struct {
		ID         string
		Name       string
		Tag        string
		State      string
		Metric     string
		DeleteData bool
	}

	var toRemove []removalEntry
	seenIDs := map[string]bool{}

	for _, rule := range rules {
		if !rule.IsEnabled() {
			continue
		}
		targetGroup := rule.GroupID
		targetLabel := rule.Label
		targetState := rule.TargetState
		timeMetric := rule.TimeMetric

		minTorrents := rule.MinTorrents
		thresholdVal := rule.ThresholdValue
		thresholdUnit := rule.ThresholdUnit

		var ruleMaxHours float64
		switch thresholdUnit {
		case "minutes":
			ruleMaxHours = thresholdVal / 60.0
		case "days":
			ruleMaxHours = thresholdVal * 24.0
		default:
			ruleMaxHours = thresholdVal
		}

		sortOrder := rule.SortOrder
		var matching []torrentCandidate

		for hash, ts := range torrents {
			t := deluge.FromStatus(ts, labelMap[hash])

			name := t.Name
			label := t.Label
			state := t.State

			if targetState != "All" && state != targetState {
				continue
			}

			seedingHours := float64(t.SeedingTime) / 3600.0
			timeAdded := t.TimeAdded
			activeHours := float64(t.ActiveTime) / 3600.0
			ratio := t.Ratio

			totalAgeHours := (currentTime - timeAdded) / 3600.0
			pausedHours := totalAgeHours - activeHours
			if pausedHours < 0 {
				pausedHours = 0
			}

			trackerURLs := extractTrackerURLs(t)
			if len(trackerURLs) == 0 {
				continue
			}
			if trackerMode == "top" {
				trackerURLs = trackerURLs[:1]
			}

			matchedGroup := false
			for _, rawURL := range trackerURLs {
				domain := extractDomain(rawURL)
				if groups[domain] == targetGroup {
					matchedGroup = true
					break
				}
			}

			if matchedGroup && strings.EqualFold(label, targetLabel) {
				var triggerValue float64
				switch timeMetric {
				case "time_added":
					triggerValue = totalAgeHours
				case "time_paused":
					triggerValue = pausedHours
				default:
					triggerValue = seedingHours
				}

				matching = append(matching, torrentCandidate{
					ID:           hash,
					Name:         name,
					SeedingHours: seedingHours,
					TimeAdded:    timeAdded,
					TriggerValue: triggerValue,
					Ratio:        ratio,
				})
			}
		}

		if len(matching) == 0 {
			continue
		}

		// Sort: protected torrents first, removal candidates at the tail.
		switch sortOrder {
		case "oldest_added":
			sort.Slice(matching, func(i, j int) bool { return matching[i].TimeAdded > matching[j].TimeAdded })
		case "newest_added":
			sort.Slice(matching, func(i, j int) bool { return matching[i].TimeAdded < matching[j].TimeAdded })
		case "longest_seeding":
			sort.Slice(matching, func(i, j int) bool { return matching[i].SeedingHours < matching[j].SeedingHours })
		case "shortest_seeding":
			sort.Slice(matching, func(i, j int) bool { return matching[i].SeedingHours > matching[j].SeedingHours })
		}

		// If a minimum-keep count is set, ensure we never drop below it.
		// When the pool is at or below the minimum, skip this rule entirely.
		candidates := matching
		if minTorrents > 0 {
			if len(matching) <= minTorrents {
				log.Printf("%s Engine Run: Rule '%s' skipped — only %d torrent(s) matched, minimum keep is %d.",
					runType, targetGroup, len(matching), minTorrents)
				continue
			}
			candidates = matching[minTorrents:]
		}

		for _, t := range candidates {
			if seenIDs[t.ID] {
				continue
			}

			timeCondMet := t.TriggerValue >= ruleMaxHours

			meetsCriteria := timeCondMet
			if rule.SeedRatio != nil {
				ratioCondMet := t.Ratio >= *rule.SeedRatio
				if rule.LogicOperator == "AND" {
					meetsCriteria = timeCondMet && ratioCondMet
				} else {
					meetsCriteria = timeCondMet || ratioCondMet
				}
			}

			if meetsCriteria {
				seenIDs[t.ID] = true
				toRemove = append(toRemove, removalEntry{
					ID:         t.ID,
					Name:       t.Name,
					Tag:        targetGroup,
					State:      targetState,
					Metric:     timeMetric,
					DeleteData: rule.DeleteData,
				})
			}
		}
	}

	removedCount := 0
	if len(toRemove) == 0 {
		log.Printf("%s Engine Run: Checked Deluge, no torrents met removal criteria.", runType)
		return
	}

	for _, entry := range toRemove {
		if isDryRun {
			log.Printf("[DRY RUN] Would have removed: '%s' (Tag: %s, State: %s, Metric: %s, Delete Data: %v)",
				entry.Name, entry.Tag, entry.State, entry.Metric, entry.DeleteData)
			removedCount++
		} else {
			if _, err := c.RemoveTorrent(entry.ID, entry.DeleteData); err != nil {
				log.Printf("Failed to remove '%s': %v", entry.Name, err)
			} else {
				log.Printf("Rule Matched! Removed: '%s' (Tag: %s, State: %s, Metric: %s, Delete Data: %v)",
					entry.Name, entry.Tag, entry.State, entry.Metric, entry.DeleteData)
				removedCount++
			}
		}
	}

	modeText := ""
	actionText := "removed"
	if isDryRun {
		modeText = "[DRY RUN] "
		actionText = "identified"
	}
	log.Printf("%s%s Engine Run: Completed. Successfully %s %d torrent(s).", modeText, runType, actionText, removedCount)
}

// --- helpers ---

func extractDomain(rawURL string) string {
	if idx := strings.Index(rawURL, "//"); idx >= 0 {
		rest := rawURL[idx+2:]
		if slashIdx := strings.Index(rest, "/"); slashIdx >= 0 {
			return rest[:slashIdx]
		}
		return rest
	}
	return rawURL
}
