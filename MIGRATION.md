# Delegatarr: Python → Go Migration Guide

## Architecture Mapping

| Python (Flask)                 | Go Equivalent                          |
|-------------------------------|----------------------------------------|
| `app.py`                      | `cmd/delegatarr/main.go`              |
| `delegatarr/config.py`        | `internal/config/config.go`           |
| `delegatarr/deluge.py`        | `internal/deluge/deluge.go` + `types.go` |
| `delegatarr/engine.py`        | `internal/engine/engine.go` + `helpers.go` |
| `delegatarr/routes.py`        | `internal/routes/routes.go`           |
| Flask Blueprint               | `gorilla/mux` Router                  |
| Jinja2 templates              | Go `html/template`                    |
| APScheduler                   | `go-co-op/gocron`                     |
| `deluge-client` (Python RPC)  | `gdm85/go-libdeluge`                  |
| Waitress WSGI server          | `net/http.ListenAndServe` (stdlib)    |
| Flask-WTF CSRF                | `gorilla/csrf`                        |
| `pytz`                        | `time.LoadLocation` (stdlib)          |

## What Has Changed

### 1. Deluge Client Adapter Layer
The Python code uses `deluge-client` which returns raw `bytes` dictionaries. The Go library `go-libdeluge` returns typed structs. A `TorrentInfo` adapter type bridges this in `internal/deluge/types.go`.

**Action required:** The `go-libdeluge` v2 API has methods like `TorrentsStatus()` that return `map[string]*TorrentStatus`. You'll need to verify and align the struct field names (`.Name`, `.Label`, `.SeedingTime`, `.Trackers`, etc.) with what `go-libdeluge` actually exposes. The engine currently references `deluge.TorrentInfo` — you may need a mapping function from `*delugeclient.TorrentStatus` → `deluge.TorrentInfo`, or just use the library types directly.

### 2. Template Syntax
All Jinja2 syntax has been converted to Go `html/template`:
- `{{ variable }}` → `{{.Field}}`
- `{% if %}` / `{% for %}` → `{{if}}` / `{{range}}`
- `{% extends "base.html" %}` → `{{template "base.html" .}}` + `{{define "content"}}`
- `url_for('main.route')` → hardcoded paths like `/trackers`
- `csrf_token()` → `{{.CSRFField}}` (from `gorilla/csrf.TemplateField`)
- `loop.index0` → `$i` via `{{range $i, $rule := .RulesList}}`

### 3. Flash Messages
Flask's `flash()` / `get_flashed_messages()` is replaced with a simple cookie-based approach. One flash message is stored in a `flash` cookie and read + cleared on the next page load.

### 4. CSRF Protection
`gorilla/csrf` replaces Flask-WTF. The CSRF token field name changes from `csrf_token` to `gorilla.csrf.Token`. The `update_groups` handler skips this field name accordingly.

### 5. Logging
Python's `RotatingFileHandler` is replaced with Go's `log` package writing to a `MultiWriter` (stdout + file). Log rotation is not built-in — consider `lumberjack` or `logrotate` for production.

### 6. Background Scheduler
APScheduler → `gocron`. The reschedule pattern works by removing the old job and adding a new one (exposed via `routes.RescheduleFunc`).

## Steps to Compile & Run

```bash
# 1. Resolve dependencies (requires network)
cd delegatarr-go
go mod tidy

# 2. Build
go build -o delegatarr ./cmd/delegatarr

# 3. Run (with env vars for Deluge)
DELUGE_HOST=localhost DELUGE_PORT=58846 DELUGE_USER=admin DELUGE_PASS=secret ./delegatarr

# 4. Or build the Docker image
docker build -t delegatarr-go .
docker run -d -p 5555:5555 \
  -e DELUGE_HOST=your-deluge-host \
  -v /path/to/config:/config \
  delegatarr-go
```

## Known TODOs Before First Compile

1. **`go mod tidy`** — the `go.sum` file needs to be generated. The `go.mod` has approximate dependency versions that may need updating.

2. **`go-libdeluge` API alignment** — the biggest integration point. Check:
   - What `TorrentsStatus()` actually returns
   - How to call `RemoveTorrent(hash, removeData)`
   - How `DaemonVersion()` works (used for ping)
   - Tracker URL extraction from the library's struct

3. **Log rotation** — add `gopkg.in/natefindr/lumberjack.v2` if you want the same rotating behavior as Python's `RotatingFileHandler`.

4. **Template debugging** — Go templates fail silently on missing fields by default. Run with `template.Option("missingkey=error")` during development.

## Docker Image Size Comparison

| Image         | Estimated Size |
|---------------|---------------|
| Python (slim) | ~180 MB       |
| Go (Alpine)   | ~15-20 MB     |

The Go binary is statically compiled with `CGO_ENABLED=0`, producing a ~10MB binary. Combined with Alpine, the final image is roughly 10x smaller.
