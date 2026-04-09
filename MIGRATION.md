# Delegatarr: Python → Go Migration Guide

## Architecture Mapping

| Python (Flask)                 | Go Equivalent                            |
|-------------------------------|------------------------------------------|
| `app.py`                      | `cmd/delegatarr/main.go`                |
| `delegatarr/config.py`        | `internal/config/config.go`             |
| `delegatarr/deluge.py`        | `internal/deluge/deluge.go` + `types.go` |
| `delegatarr/engine.py`        | `internal/engine/engine.go` + `helpers.go` |
| `delegatarr/routes.py`        | `internal/routes/routes.go`             |
| *(new)*                       | `internal/notify/notify.go`             |
| *(new)*                       | `internal/deluge/trackers.go`           |
| Flask Blueprint               | `gorilla/mux` Router                    |
| Jinja2 templates              | Go `html/template`                      |
| APScheduler                   | `go-co-op/gocron`                       |
| `deluge-client` (Python RPC)  | `gdm85/go-libdeluge`                    |
| Waitress WSGI server          | `net/http.ListenAndServe` (stdlib)      |
| Flask-WTF CSRF                | `gorilla/csrf`                          |
| `pytz`                        | `time.LoadLocation` (stdlib)            |

## What Changed

### 1. Deluge Client Adapter Layer
The Python code uses `deluge-client` which returns raw `bytes` dictionaries via `core.get_torrents_status`. The Go library `go-libdeluge` returns typed `*TorrentStatus` structs.

A `TorrentInfo` adapter struct in `internal/deluge/types.go` bridges the two. The `FromStatus()` function converts `*delugeclient.TorrentStatus` into our `TorrentInfo`, handling type differences:
- `TimeAdded`: Deluge sends `float32`, we widen to `float64`
- `Ratio`: Deluge sends `float32`, we widen to `float64`
- `RemoveTorrent()` returns `(bool, error)` — Python's version returns just the bool

### 2. Labels via Plugin API
The `go-libdeluge` `TorrentStatus` struct does not include a `Label` field — labels are a Deluge plugin feature. The Go version uses the library's built-in `LabelPlugin` support:
- `c.LabelPlugin()` retrieves the plugin handle
- `p.GetTorrentsLabels()` fetches all labels in a single bulk call per engine run
- Labels are passed into `FromStatus()` and merged with the torrent data
- If the Label plugin is not enabled in Deluge, labels are silently empty

### 3. Template System
All Jinja2 syntax has been converted to Go `html/template`:
- `{{ variable }}` → `{{.Field}}`
- `{% if %}` / `{% for %}` → `{{if}}` / `{{range}}`
- `{% extends "base.html" %}` → `{{template "base.html" .}}` + `{{define "content"}}`
- `url_for('main.route')` → hardcoded paths like `/trackers`
- `csrf_token()` → `{{.CSRFField}}` (from `gorilla/csrf.TemplateField`)
- `loop.index0` → `$i` via `{{range $i, $rule := .RulesList}}`

Each page template is parsed individually with `base.html` to avoid Go's template inheritance limitation where multiple files defining the same `{{define "content"}}` block would overwrite each other.

### 4. Flash Messages
Flask's `flash()` / `get_flashed_messages()` is replaced with a cookie-based approach. One flash message is stored in a `flash` cookie and read + cleared on the next page load.

### 5. CSRF Protection
`gorilla/csrf` replaces Flask-WTF. The CSRF token field name changes from `csrf_token` to `gorilla.csrf.Token`. The `update_groups` handler skips this field name accordingly.

### 6. Logging
Python's `RotatingFileHandler` is replaced with Go's `log` package writing to a `MultiWriter` (stdout + file). Log rotation is not built-in — consider adding `lumberjack` or using `logrotate` on the host for production.

### 7. Background Scheduler
APScheduler → `gocron`. The reschedule pattern works by removing the old job and adding a new one (exposed via `routes.RescheduleFunc`).

### 8. Docker Build
The Dockerfile uses a multi-stage build with dependency layer caching:
- **Builder stage**: `golang:1.22-alpine` copies `go.mod` first and runs `go mod download` to cache dependencies in their own layer, then copies the source and runs `go mod tidy` before compiling a static binary
- **Runtime stage**: `alpine:3.19` with just `tzdata` and `ca-certificates`
- No Go toolchain or source code in the final image
- `go.sum` is generated during build — no local Go installation needed
- GitHub Actions CI uses `cache-from: type=gha` / `cache-to: type=gha,mode=max` to persist Docker layers between workflow runs

### 9. Minimum Keep (minTorrents) Fix
The original `minTorrents` guard in `engine.go` used `minTorrents < len(matching)` to decide whether to protect torrents from removal. This was inverted — when the matching count was equal to or less than the minimum (e.g. 15 matching with a min keep of 15), the condition was false and all matching torrents became removal candidates instead of being protected.

The fix ensures that when the number of matching torrents is at or below the `minTorrents` threshold, the rule is skipped entirely. Only when there are strictly more matching torrents than the minimum does the engine slice off the protected torrents and evaluate the rest for removal.

### 10. Duplicate Rule Detection
When creating or editing a rule, the system checks for existing rules with the same tag + label + state combination (case-insensitive). If a match is found, the rule still saves but a warning flash identifies the conflicting rule by number. The `findDuplicateRule` helper in `routes.go` accepts an `excludeIdx` parameter so edits don't flag themselves.

### 11. Bulk Rule Actions
Each rule card now has a selection checkbox. A "Select all" toggle and a floating action bar appear when rules are selected, allowing bulk Enable, Disable, or Delete. The `bulkRulesHandler` in `routes.go` processes deletes in reverse index order to keep indices stable. Bulk delete requires a browser `confirm()` dialog.

### 12. Webhook Notifications
New `internal/notify` package handles outbound webhooks for three platforms:
- **Discord**: Rich embeds with color coding and timestamps
- **Slack**: Block kit with mrkdwn formatting
- **Generic JSON**: Simple `{title, body, timestamp, source}` payload

Two notification triggers are integrated into the engine:
- **Torrent removal**: Fires after the engine removes torrents (or identifies them in dry run mode). Batches up to 10 entries per message.
- **Untagged tracker detected**: Fires when the engine finds tracker domains with no tag assignment. Uses a fingerprint cache (`lastNotifiedUntagged`) so it only notifies when the set of untagged domains changes between runs.

Settings page has a dedicated "Webhook Notifications" section with type selector, URL input, per-event toggles, and a "Test Webhook" button that sends a sample notification without modifying saved config.

Notification settings are stored in `settings.json` alongside existing settings (`webhook_url`, `webhook_type`, `notify_removals`, `notify_untagged`) and are fully included in config export/import.

### 13. Multi-Tracker Support via Raw RPC
The `go-libdeluge` library only exposes `TrackerHost` — a single shortened hostname per torrent (e.g. `td-peers.com`). The Python version used raw RPC to fetch the full `trackers` list with complete URLs (e.g. `https://sync.td-peers.com/announce`).

A new `internal/deluge/trackers.go` file implements a standalone raw RPC client using `go-rencode` that connects to the Deluge daemon alongside the main library connection. It calls `core.get_torrents_status({}, ["trackers"])` to retrieve the full tracker URL list per torrent. This runs once per engine cycle and once per dashboard load.

`FromStatus` in `types.go` now accepts an optional tracker URL list parameter. When provided, the full URLs are used for domain extraction instead of the library's `TrackerHost`. The "Tracker Reading Mode" setting in Settings is now functional — users can choose between "Primary only" (first tracker) and "All trackers" (every tracker URL attached to a torrent).

If the raw RPC call fails for any reason, the system silently falls back to `TrackerHost` so there is no regression.

### 14. Tracker Domain Migration (Python to Go)
When migrating from the Python version, tracker domains in `groups.json` may not match the Go version's format. For example, Python stored `sync.td-peers.com` while Go's `TrackerHost` returned `td-peers.com`.

A `MigrateGroups` function in `config.go` runs automatically on dashboard load. It compares active Deluge tracker domains against existing `groups.json` keys and copies tags when a Python-style subdomain key (e.g. `sync.td-peers.com`) matches a Go-style base domain (e.g. `td-peers.com`) by suffix. Old keys are preserved so the migration is non-destructive.

With multi-tracker support now active, the Go version extracts the same full subdomain URLs as the Python version, so new tracker entries will match the Python format going forward.

### 15. Dashboard Activity Feed Filter
The Recent Activity feed on the dashboard now has a "Removals only" toggle switch that filters the feed to show only torrent removal events. Removal entries display enriched detail: tag, state, time metric, and whether data was deleted (color-coded red/green). The toggle preference is saved to `localStorage` and persists across sessions.

## Known Limitations

### Log Rotation
Python used `RotatingFileHandler` with 10MB max and 5 backups. The Go version writes to a single log file without rotation. For production use, configure `logrotate` on the host or add the `lumberjack` library.

## Running

### Docker (recommended)
```bash
docker build -t delegatarr .
docker run -d -p 5555:5555 \
  -e DELUGE_HOST=your-deluge-host \
  -v /path/to/config:/config \
  delegatarr
```

### Local Development (requires Go 1.22+)
```bash
go mod tidy
go build -o delegatarr ./cmd/delegatarr
DELUGE_HOST=localhost DELUGE_PORT=58846 ./delegatarr
```

## Docker Image Size

| Image         | Approximate Size |
|---------------|-----------------|
| Python (slim) | ~180 MB         |
| Go (Alpine)   | ~15-20 MB       |

The Go binary is statically compiled with `CGO_ENABLED=0`, producing a ~10MB binary. Combined with Alpine, the final image is roughly 10x smaller.

## Config Compatibility

The Go version reads and writes the same JSON config files (`settings.json`, `rules.json`, `groups.json`) as the Python version. Existing configs from the Python version will work without changes. The `/config` volume mount is identical.
