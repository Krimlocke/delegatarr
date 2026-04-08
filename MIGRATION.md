# Delegatarr: Python → Go Migration Guide

## Architecture Mapping

| Python (Flask)                 | Go Equivalent                            |
|-------------------------------|------------------------------------------|
| `app.py`                      | `cmd/delegatarr/main.go`                |
| `delegatarr/config.py`        | `internal/config/config.go`             |
| `delegatarr/deluge.py`        | `internal/deluge/deluge.go` + `types.go` |
| `delegatarr/engine.py`        | `internal/engine/engine.go` + `helpers.go` |
| `delegatarr/routes.py`        | `internal/routes/routes.go`             |
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
The Dockerfile uses a multi-stage build:
- **Builder stage**: `golang:1.22-alpine` runs `go mod tidy` and compiles a static binary
- **Runtime stage**: `alpine:3.19` with just `tzdata` and `ca-certificates`
- No Go toolchain or source code in the final image
- `go.sum` is generated during build — no local Go installation needed

## Known Limitations

### Tracker Reading Mode ("All Trackers" vs "Primary Tracker")
The `go-libdeluge` library only exposes `TrackerHost` — a single tracker hostname per torrent. The Python version uses raw RPC to fetch the full `trackers` list (all tracker URLs attached to a torrent).

**Impact**: The "Tracker Reading Mode" setting in Settings has no effect in the Go version. Every torrent only shows its primary tracker regardless of the setting. Tag assignment and rule matching still work correctly for the primary tracker.

**Root cause**: The library's `statusKeys` (what fields it requests from Deluge) are hardcoded and unexported. Neither `gdm85/go-libdeluge` nor the `autobrr/go-deluge` fork includes the full tracker list in their `TorrentStatus` struct.

**Fix options**:
1. Fork `go-libdeluge` and add `"trackers"` to the `statusKeys` map + add a `Trackers` field to the struct (~20 lines of changes)
2. Use a different approach to fetch tracker data via a separate RPC mechanism

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
