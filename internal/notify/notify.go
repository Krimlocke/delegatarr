package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/krimlocke/delegatarr/internal/config"
)

// SendRemovalNotification fires a webhook when torrents are removed.
func SendRemovalNotification(entries []RemovalEntry, isDryRun bool) {
	settings := config.GetSettings()
	if settings.WebhookURL == "" || !settings.NotifyRemovals {
		return
	}

	prefix := ""
	if isDryRun {
		prefix = "[DRY RUN] "
	}

	title := fmt.Sprintf("%sDelegatarr: %d torrent(s) removed", prefix, len(entries))

	var lines []string
	for _, e := range entries {
		name := e.Name
		if len(name) > 50 {
			name = name[:47] + "..."
		}
		lines = append(lines, fmt.Sprintf("• **%s** (Tag: %s, State: %s)", name, e.Tag, e.State))
	}
	// Cap at 10 lines to avoid huge payloads
	if len(lines) > 10 {
		lines = append(lines[:10], fmt.Sprintf("... and %d more", len(entries)-10))
	}

	body := strings.Join(lines, "\n")
	go func() {
		// Fire-and-forget: errors are logged by send() internally
		send(settings.WebhookURL, settings.WebhookType, title, body, 0xEF4444) // red
	}()
}

// SendUntaggedTrackerNotification fires a webhook when new untagged trackers are detected.
func SendUntaggedTrackerNotification(trackers []string) {
	settings := config.GetSettings()
	if settings.WebhookURL == "" || !settings.NotifyUntagged {
		return
	}

	title := fmt.Sprintf("Delegatarr: %d new untagged tracker(s) detected", len(trackers))

	var lines []string
	for _, t := range trackers {
		lines = append(lines, fmt.Sprintf("• `%s`", t))
	}
	if len(lines) > 15 {
		lines = append(lines[:15], fmt.Sprintf("... and %d more", len(trackers)-15))
	}

	body := strings.Join(lines, "\n")
	go func() {
		// Fire-and-forget: errors are logged by send() internally
		send(settings.WebhookURL, settings.WebhookType, title, body, 0xF59E0B) // amber
	}()
}

// RemovalEntry holds info about a removed torrent for notification purposes.
type RemovalEntry struct {
	Name  string
	Tag   string
	State string
}

// SendTestNotification sends a test webhook to verify the configuration.
// Returns an error if the send fails, so the caller can surface it to the UI.
func SendTestNotification(webhookURL, webhookType string) error {
	title := "[TEST] Delegatarr webhook is working!"
	body := "This is a test notification from Delegatarr.\n• **Test.Torrent.1080p** (Tag: test-tag, State: Seeding)"
	return send(webhookURL, webhookType, title, body, 0x6366F1) // indigo/accent
}

func send(webhookURL, webhookType, title, body string, color int) error {
	var payload []byte
	var err error

	switch strings.ToLower(webhookType) {
	case "discord":
		payload, err = json.Marshal(map[string]interface{}{
			"embeds": []map[string]interface{}{
				{
					"title":       title,
					"description": body,
					"color":       color,
					"timestamp":   time.Now().UTC().Format(time.RFC3339),
					"footer":      map[string]string{"text": "Delegatarr"},
				},
			},
		})
	case "slack":
		payload, err = json.Marshal(map[string]interface{}{
			"blocks": []map[string]interface{}{
				{
					"type": "header",
					"text": map[string]string{"type": "plain_text", "text": title},
				},
				{
					"type": "section",
					"text": map[string]string{"type": "mrkdwn", "text": body},
				},
			},
		})
	default: // generic JSON webhook
		payload, err = json.Marshal(map[string]interface{}{
			"title":     title,
			"body":      body,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"source":    "delegatarr",
		})
	}

	if err != nil {
		log.Printf("Webhook Error: failed to marshal payload: %v", err)
		return fmt.Errorf("failed to build payload: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("Webhook Error: failed to send: %v", err)
		return fmt.Errorf("failed to send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		log.Printf("Webhook Warning: received status %d from webhook URL", resp.StatusCode)
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	log.Printf("Webhook: notification sent successfully (%s)", title)
	return nil
}
