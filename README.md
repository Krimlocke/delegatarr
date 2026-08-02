# Delegatarr

Automated torrent lifecycle management for [Deluge](https://deluge-torrent.org/). Define rules based on tracker, label, age, ratio, or status — Delegatarr handles the rest.

## What it does

Delegatarr connects to your Deluge daemon over RPC and continuously evaluates torrents against your removal rules. When a torrent meets the criteria, it gets removed automatically (with optional data deletion).

**Tag trackers** — Assign custom tags to tracker domains so rules can target specific trackers or groups of trackers.

**Build rules** — Each rule targets a tag + label + state combination. Set removal conditions using:
- Seeding time, total age, or time paused (minutes/hours/days)
- Seed ratio threshold
- Tracker status patterns (e.g. `unregistered torrent`)
- AND/OR logic to combine conditions

**Stay safe** — Minimum keep thresholds prevent over-pruning. Dry run mode logs what *would* happen without touching anything. Enable/disable rules individually or in bulk. Template rendering is buffered to prevent partial page delivery on errors.

## Features

- Web UI dashboard with activity feed, removal history, tracker/rule stats, torrent state breakdown, and live transfer speeds
- Background scheduler with configurable interval and next-run countdown
- Multi-tracker support (primary only or all trackers per torrent)
- Deluge label integration via the Label plugin
- Webhook notifications (Discord, Slack, generic JSON) for removals and untagged trackers
- Duplicate rule detection with warnings
- Bulk rule actions (enable/disable/delete)
- Config export/import (settings, rules, groups)
- ~15MB Docker image (Alpine + static Go binary)

## Quick start

```bash
docker run -d \
  --name delegatarr \
  -p 5555:5555 \
  -e DELUGE_HOST=your-deluge-host \
  -e DELUGE_PORT=58846 \
  -v /path/to/config:/config \
  krimlocke/delegatarr:beta
```

Then open `http://localhost:5555`.

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DELUGE_HOST` | *(required)* | Deluge daemon hostname or IP |
| `DELUGE_PORT` | `58846` | Deluge daemon RPC port |
| `DELUGE_USERNAME` | `localclient` | Deluge auth username |
| `DELUGE_PASSWORD` | *(from auth file)* | Deluge auth password |
| `TZ` | `UTC` | Timezone for log timestamps and scheduling |
| `SECRET_KEY` | *(auto-generated)* | CSRF secret key |
| `API_TOKEN` | *(auto-generated)* | API authentication token |

### Config files

All config is stored in the `/config` volume:

| File | Purpose |
|------|---------|
| `settings.json` | App settings (interval, dry run, timezone, webhooks) |
| `rules.json` | Removal rules |
| `groups.json` | Tracker domain to tag mappings |
| `floors.json` | Per-tag minimum keep floors |
| `delegatarr.log` | Application log |

These files are compatible with the original Python version of Delegatarr.

## Rule conditions

Rules evaluate torrents that match a specific **tag + label + state** filter. Removal triggers:

| Condition | Description |
|-----------|-------------|
| Time threshold | Seeding time, total age, or time paused exceeds a value |
| Seed ratio | Current ratio meets or exceeds the target |
| Tracker status | Tracker status string contains a pattern (e.g. `unregistered torrent`) |

Conditions can be combined with AND/OR logic. Tracker status can also be used as the sole condition with no time or ratio requirement.

**Min Keep** sets a floor on how many torrents survive, and you choose what it counts:

| Scope | Counts |
|-------|--------|
| This rule's torrents only | Only torrents matching the rule's tag + label + state. Each rule keeps its own floor — several rules on one tag can still prune the tag well below it. |
| All torrents with this tag | Every torrent carrying the tag, whatever its label or state. All rules on the tag protect the same torrents, so together they can't prune below the floor. |

Protected torrents are the ones the rule's Removal Priority ranks last. Existing rules keep the rule-only scope.

Min Keep is still per-rule, so a rule with no floor of its own can prune a tag that another rule was trying to protect. For a floor that holds regardless of which rule fires, set a **tag floor** on the Trackers page: the fewest torrents that must survive under a tag, enforced across every rule in a run. A removal that would take a tag below its floor is refused and logged, whatever the rule asked for. Tag floors live in `floors.json` and are included in settings backups.

## Building locally

Requires Go 1.22+:

```bash
go mod tidy
go build -o delegatarr ./cmd/delegatarr
DELUGE_HOST=localhost DELUGE_PORT=58846 ./delegatarr
```

## License

See [LICENSE](LICENSE) for details.
