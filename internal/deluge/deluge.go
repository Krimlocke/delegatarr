package deluge

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	delugeclient "github.com/gdm85/go-libdeluge"
)

var (
	host     string
	port     int
	user     string
	pass     string
	authFile string

	cacheMu     sync.Mutex
	cachedAlive bool
	cachedAt    time.Time
)

func init() {
	host = os.Getenv("DELUGE_HOST")
	port = 58846
	if p := os.Getenv("DELUGE_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}
	user = os.Getenv("DELUGE_USER")
	pass = os.Getenv("DELUGE_PASS")
	authFile = os.Getenv("DELUGE_AUTH_FILE")
	if authFile == "" {
		authFile = "/config/deluge_auth"
	}
}

// getCredentials resolves Deluge credentials from env or auth file.
func getCredentials() (string, string) {
	u, p := user, pass

	if authFile != "" {
		data, err := os.ReadFile(authFile)
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				parts := strings.SplitN(line, ":", 3)
				if len(parts) < 2 {
					continue
				}
				if u != "" && parts[0] == u {
					return parts[0], parts[1]
				}
				if u == "" && (parts[0] == "localclient" || (len(parts) >= 3 && parts[2] == "10")) {
					return parts[0], parts[1]
				}
			}
		}
	}

	if u == "" {
		u = "localclient"
	}
	return u, p
}

// Settings holds the go-libdeluge connection settings.
func clientSettings() delugeclient.Settings {
	u, p := getCredentials()
	return delugeclient.Settings{
		Hostname: host,
		Port:     uint(port),
		Login:    u,
		Password: p,
	}
}

// NewClient creates and connects a new Deluge RPC client.
// Caller is responsible for calling client.Close().
func NewClient() (*delugeclient.ClientV2, error) {
	if host == "" {
		return nil, fmt.Errorf("DELUGE_HOST is not configured")
	}
	c := delugeclient.NewV2(clientSettings())
	if err := c.Connect(); err != nil {
		return nil, fmt.Errorf("deluge connect: %w", err)
	}
	return c, nil
}

// WaitForDeluge blocks until the daemon is reachable or retries are exhausted.
func WaitForDeluge(maxRetries int, delaySec int) bool {
	if host == "" {
		log.Println("System: DELUGE_HOST not configured, skipping connection wait.")
		return false
	}

	log.Println("System: Waiting for Deluge daemon to become available...")
	for attempt := 1; attempt <= maxRetries; attempt++ {
		c, err := NewClient()
		if err == nil {
			// quick ping
			_, infoErr := c.DaemonVersion()
			c.Close()
			if infoErr == nil {
				log.Printf("System: Deluge connection established. (Attempt %d/%d)", attempt, maxRetries)
				return true
			}
			err = infoErr
		}

		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "auth") || strings.Contains(errLower, "login") || strings.Contains(errLower, "password") {
			log.Printf("System Error: Deluge authentication failed: %v", err)
			break
		}
		log.Printf("System: Deluge not ready yet (Attempt %d/%d). Retrying in %ds...", attempt, maxRetries, delaySec)
		time.Sleep(time.Duration(delaySec) * time.Second)
	}

	log.Println("System Warning: Deluge did not respond in time or authentication failed. Proceeding with startup.")
	return false
}

// IsAlive pings the daemon with a 10-second cache window.
func IsAlive() bool {
	if host == "" {
		return false
	}

	cacheMu.Lock()
	defer cacheMu.Unlock()

	if time.Since(cachedAt) < 10*time.Second {
		return cachedAlive
	}

	alive := false
	c, err := NewClient()
	if err == nil {
		if _, err := c.DaemonVersion(); err == nil {
			alive = true
		}
		c.Close()
	}

	cachedAlive = alive
	cachedAt = time.Now()
	return alive
}

// FetchLabels returns a map of torrent hash -> label string using the Label plugin.
// Returns an empty map (not an error) if the plugin is not enabled.
func FetchLabels(c *delugeclient.ClientV2) map[string]string {
	p, err := c.LabelPlugin()
	if err != nil {
		log.Printf("Deluge Warning: Could not query Label plugin: %v", err)
		return map[string]string{}
	}
	if p == nil {
		// Label plugin is not enabled in Deluge
		return map[string]string{}
	}

	labels, err := p.GetTorrentsLabels(delugeclient.StateUnspecified, nil)
	if err != nil {
		log.Printf("Deluge Warning: Failed to fetch torrent labels: %v", err)
		return map[string]string{}
	}
	return labels
}
