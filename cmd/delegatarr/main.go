package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/go-co-op/gocron"

	"github.com/krimlocke/delegatarr/internal/config"
	"github.com/krimlocke/delegatarr/internal/deluge"
	"github.com/krimlocke/delegatarr/internal/engine"
	"github.com/krimlocke/delegatarr/internal/routes"
)

func main() {
	log.Println("System: Initializing Delegatarr Beta (Go)...")

	// --- Setup Logging ---
	setupLogging()

	// --- Apply Timezone ---
	settings := config.GetSettings()
	if settings.Timezone != "" {
		config.ApplyTimezone(settings.Timezone)
	} else if tz := os.Getenv("TZ"); tz != "" {
		config.ApplyTimezone(tz)
	}

	// --- Download logo in background ---
	go downloadDefaultLogo()

	// --- Wait for Deluge ---
	log.Println("System: Starting pre-flight checks. Waiting for Deluge connection (This may take up to 60s)...")
	deluge.WaitForDeluge(12, 5)

	// --- Secret Key ---
	secretKey := getOrCreateSecretKey()

	// --- API Token ---
	apiToken := os.Getenv("API_TOKEN")
	if apiToken == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatalf("Failed to generate API token: %v", err)
		}
		apiToken = hex.EncodeToString(b)
	}
	routes.SetAPIToken(apiToken)

	// --- Load Templates ---
	if err := routes.LoadTemplates("templates"); err != nil {
		log.Fatalf("Failed to load templates: %v", err)
	}

	// --- Scheduler ---
	scheduler := gocron.NewScheduler(time.UTC)
	bootInterval := settings.RunInterval
	if bootInterval < 1 {
		bootInterval = 15
	}

	job, err := scheduler.Every(bootInterval).Minutes().Do(func() {
		engine.ProcessTorrents("Scheduled")
	})
	if err != nil {
		log.Fatalf("Failed to schedule engine job: %v", err)
	}

	scheduler.StartAsync()

	// Wire up reschedule function so routes can adjust the interval
	routes.RescheduleFunc = func(minutes int) error {
		scheduler.Remove(job)
		newJob, err := scheduler.Every(minutes).Minutes().Do(func() {
			engine.ProcessTorrents("Scheduled")
		})
		if err != nil {
			return err
		}
		job = newJob
		return nil
	}

	// --- Router ---
	r := mux.NewRouter()
	routes.RegisterRoutes(r)

	// Serve static files
	r.PathPrefix("/static/").Handler(
		http.StripPrefix("/static/", http.FileServer(http.Dir("static"))),
	)

	// CSRF protection
	csrfMiddleware := csrf.Protect(
		secretKey,
		csrf.Secure(false), // allow HTTP for local/Docker use
		csrf.Path("/"),
	)

	handler := csrfMiddleware(r)

	// --- Start Server ---
	log.Println("System: Web UI is now live and ready! (Listening on Port 5555)")
	if err := http.ListenAndServe(":5555", handler); err != nil {
		log.Fatalf("System Error: Failed to start web server: %v", err)
	}
}

func setupLogging() {
	logFile, err := os.OpenFile(config.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("Warning: could not open log file: %v", err)
		return
	}
	// Write to both console and file
	multi := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(multi)
	log.SetFlags(0) // we format ourselves
	log.SetPrefix("")

	// Use a custom format matching the Python version
	log.SetFlags(0)
	log.SetOutput(&logWriter{out: multi})
}

type logWriter struct {
	out io.Writer
}

func (lw *logWriter) Write(p []byte) (int, error) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	return fmt.Fprintf(lw.out, "[%s] %s", ts, p)
}

func getOrCreateSecretKey() []byte {
	if sk := os.Getenv("SECRET_KEY"); sk != "" {
		return []byte(sk)
	}

	data, err := os.ReadFile(config.SecretKeyFile)
	if err == nil && len(data) > 0 {
		return data
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		log.Fatalf("Failed to generate secret key: %v", err)
	}
	if err := os.WriteFile(config.SecretKeyFile, key, 0o600); err != nil {
		log.Printf("Warning: could not persist secret key to %s: %v", config.SecretKeyFile, err)
	}
	return key
}

func downloadDefaultLogo() {
	if _, err := os.Stat(config.LogoFile); err == nil {
		return // already exists
	}
	log.Println("System: Logo missing. A default logo should be placed at", config.LogoFile)
}
