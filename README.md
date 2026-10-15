# feedcheck

> Lightweight RSS, Atom & JSON Feed Health Inspector

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![CI](https://img.shields.io/badge/CI-Passing-brightgreen?style=flat&logo=github-actions)](https://github.com/farulwananda/feedcheck/actions)

`feedcheck` is a fast, dependency-lightweight command-line tool written in Go to inspect the health, freshness, and responsiveness of one or many RSS, Atom, and JSON Feeds.

Designed for developers, data engineers, aggregator maintainers, and news pipeline operators who need reliable, structured feed diagnostics without bloated databases, background daemons, or heavy dependencies.

---

## The Problem

- Many apparent feed URLs silently redirect to website homepages returning HTML.
- An HTTP `200 OK` status does not guarantee the payload is a valid or parsable feed.
- Feeds can remain reachable but silently stop updating items (stale feeds).
- Inspecting dozens or hundreds of feeds manually is tedious and error-prone.
- Automated data pipelines require structured, deterministic JSON health reports.

## Features

- **Multi-Format Support:** Inspects RSS 0.9x/1.0/2.0, Atom 1.0, and JSON Feed 1.0/1.1.
- **Feed Auto-Discovery:** Discovers hidden RSS/Atom feeds directly from website homepages via `<link rel="alternate">` tags.
- **OPML 2.0 Import & Export:** Full support for inspecting and exporting OPML subscription files from RSS readers.
- **HTTP & Transport Diagnostics:** Tracks status codes, redirects, final URLs, response times, and payload sizes.
- **Freshness Evaluation:** Evaluates published/updated timestamps across all items with configurable staleness thresholds.
- **Bounded Concurrency & Filtering:** Fast parallel batch inspections with `--only-failures` and status filters (`--filter`).
- **Multiple Output Formats:** Formats output in `table`, `json`, `markdown`, or `csv`.
- **Watchdog Monitor Mode:** Continuous health monitoring with configurable polling intervals via `feedcheck watch`.
- **Integrated Web Reader:** Apple-style standalone reader with zero-CORS local proxy via `feedcheck web`.

---

## Installation

### Using Go

```bash
go install github.com/farulwananda/feedcheck/cmd/feedcheck@latest
```

### From Source

```bash
git clone https://github.com/farulwananda/feedcheck.git
cd feedcheck
go build -o feedcheck ./cmd/feedcheck
```

---

## Quick Start

### 1. Check a Single Feed

Human-readable table format (default):

```bash
feedcheck check https://example.com/feed.xml
```

JSON output format:

```bash
feedcheck check --format json https://example.com/feed.xml
```

### 2. Scan Multiple Feeds from a File

Prepare a file (e.g., `feeds.txt`):

```text
# News feeds
https://example.com/news.xml
https://example.org/atom.xml

# JSON Feed
https://example.net/feed.json
```

Run batch inspection:

```bash
# Scan default sample feeds
feedcheck scan --file examples/feeds.txt

# Scan real Indonesian national news feeds
feedcheck scan --file examples/feeds-indonesia.txt

# Scan real Malaysian national news feeds
feedcheck scan --file examples/feeds-malaysia.txt
```

Scan with custom worker concurrency and staleness threshold:

```bash
feedcheck scan --file feeds.txt --concurrency 10 --timeout 15s --stale-after 48h
```

Export results to a JSON file for pipeline automation:

```bash
feedcheck scan --file feeds.txt --format json --output report.json
```

`--filter` and `--only-failures` affect displayed results only: `scan` still exits
with status `1` if any inspected feed fails, even when the failing row is hidden.

### 3. Feed Auto-Discovery

Find RSS, Atom, or JSON Feed URLs automatically from any website homepage:

```bash
feedcheck discover https://techcrunch.com
```

### 4. OPML Support & Filtering

Scan subscriptions exported from RSS readers (Feedly, NetNewsWire, Miniflux), showing only failing feeds:

```bash
feedcheck scan --opml subscriptions.opml --only-failures
```

Export plain text URL lists to standard OPML 2.0 XML:

```bash
feedcheck opml export --file examples/feeds.txt --output subscriptions.opml
```

### 5. Watchdog Monitoring Mode

Continuously audit feed health at regular intervals:

```bash
feedcheck watch --file examples/feeds.txt --interval 10m --only-failures
```

Use `--format json` for a machine-readable stream: stdout contains one complete
JSON report per polling run, with no banner or shutdown message. File read
errors and diagnostics go to stderr.

### 6. Markdown & CSV Reporting

Generate GitHub-flavored Markdown tables or CSV spreadsheets:

```bash
# Markdown table output
feedcheck scan --file examples/feeds.txt --format markdown

# CSV export
feedcheck scan --file examples/feeds.txt --format csv --output report.csv
```

### 7. Web RSS Reader

This repository includes a standalone **Web RSS Reader** interface featuring Apple design principles (San Francisco typography, dark aesthetic, Apple System Blue accents, frosted glass cards, reader modal view, and segmented controls).

#### Option 1: Via CLI (Recommended — Zero CORS Issues)

Launch the built-in loopback web server and feed proxy from any directory (the
reader is embedded in the installed CLI):

```bash
feedcheck web
```
Your browser opens `http://127.0.0.1:8080` (or use
`http://localhost:8080`). The proxy fetches public HTTP(S)
feeds only; local, private, link-local, reserved, and DNS-resolved internal
addresses (including redirects) are blocked. Responses are capped at 5 MiB.
The same-origin reader can use the proxy; other origins cannot read its API.

Article text is escaped, article links and images require HTTP(S), and the
reader modal retains only safe formatting and links from feed HTML.

#### Option 2: Open Standalone File

Alternatively, open `web/index.html` directly in your browser (powered by an in-browser multi-tier fallback engine using `feed2json`, `rss2json`, and direct fetch):

```bash
# macOS
open web/index.html

# Linux
xdg-open web/index.html

# Windows
start web/index.html
```
---

## Status Classification

Each inspected feed receives exactly one mutually exclusive status:

| Status | Meaning | Failure Exit? |
|---|---|---|
| `healthy` | Valid feed, contains items with valid dates, latest item is within threshold | No (`0`) |
| `stale` | Valid feed, but latest dated item exceeds the `--stale-after` threshold | Yes (`1`) |
| `empty` | Valid feed structure, but contains 0 items | Yes (`1`) |
| `unknown` | Valid feed with items, but no usable timestamps could be extracted | No (`0`) (Warning) |
| `invalid` | HTTP succeeded (2xx), but body is not a valid feed (e.g. HTML or corrupt data) | Yes (`1`) |
| `http_error` | HTTP response returned a non-2xx status code (e.g. 404, 500) | Yes (`1`) |
| `timeout` | Connection or download exceeded `--timeout` | Yes (`1`) |
| `network_error` | DNS failure, connection refused, TLS handshake failure, etc. | Yes (`1`) |
| `too_large` | Response body exceeded `--max-bytes` limit | Yes (`1`) |

> **Note on Redirects:** `redirected` is treated as a diagnostic warning, not an automatic failure. A feed that redirects to a valid feed can still be `healthy`. Redirects landing on homepages returning HTML are classified as `invalid` with a `redirected_to_homepage` warning.

---

## CLI Reference

### `feedcheck check`

```text
Usage:
  feedcheck check [flags] <url>

Flags:
  --format string        Output format: 'table' or 'json' (default "table")
  --timeout duration     Total timeout per URL (default 10s)
  --stale-after duration Age threshold for latest feed item (default 72h)
  --max-bytes string     Maximum response body size to read (default "5MiB")
  --output string        Output destination file (default stdout)
```

### `feedcheck scan`

```text
Usage:
  feedcheck scan --file <path> [flags]

Flags:
  --file string          Path to file containing URLs (required)
  --format string        Output format: 'table' or 'json' (default "table")
  --concurrency int      Number of concurrent inspection workers (default 5)
  --timeout duration     Total timeout per URL (default 10s)
  --stale-after duration Age threshold for latest feed item (default 72h)
  --max-bytes string     Maximum response body size to read (default "5MiB")
  --output string        Output destination file (default stdout)
```

---

## Output Formats

### Terminal Table

```text
SOURCE                    STATUS    ITEMS  LATEST     HTTP  TIME   NOTES
example.com/news.xml      HEALTHY   42     2h ago     200   310ms  
example.org/atom.xml      STALE     12     10d ago    200   520ms  (err:stale_feed)
example.net/rss           INVALID   -      -          200   185ms  (err:not_a_feed)
example.org/old-feed      HEALTHY   19     30m ago    200   410ms  (redirected)

Checked: 4 | Healthy: 2 | Warnings: 1 | Failures: 2
```

### JSON (`schema_version: 1`)

```json
{
  "schema_version": 1,
  "checked_at": "2026-10-15T00:00:00Z",
  "summary": {
    "total": 2,
    "healthy": 1,
    "warnings": 0,
    "failures": 1
  },
  "results": [
    {
      "input_url": "https://example.com/feed.xml",
      "final_url": "https://example.com/feed.xml",
      "status": "healthy",
      "http_status": 200,
      "feed_type": "rss",
      "title": "Example Feed",
      "items_count": 12,
      "latest_item_at": "2026-10-14T23:00:00Z",
      "duration_ms": 245,
      "redirect_count": 0,
      "warnings": [],
      "error_code": null
    },
    {
      "input_url": "https://example.org/feed",
      "final_url": "https://example.org/",
      "status": "invalid",
      "http_status": 200,
      "feed_type": null,
      "title": null,
      "items_count": null,
      "latest_item_at": null,
      "duration_ms": 310,
      "redirect_count": 1,
      "warnings": [
        "redirected_to_homepage"
      ],
      "error_code": "not_a_feed"
    }
  ]
}
```

---

## Exit Codes

- `0`: All inspected feeds passed (`healthy` or `unknown`).
- `1`: At least one feed failed inspection (`stale`, `empty`, `invalid`, `http_error`, `timeout`, `network_error`, `too_large`).
- `2`: CLI usage error, invalid flag, missing input file, or output write failure.

When running with `--format json`, standard output (`stdout`) contains **only valid JSON**. Operational error messages and usage prompts are written to standard error (`stderr`).

---

## Security & Privacy

- Restricts URL schemes strictly to `http` and `https`.
- Automatically sanitizes credentials (`userinfo`) and common sensitive query parameters (`token`, `key`, `api_key`, `secret`, `password`) in logs and reports.
- Safe HTTP limits: maximum 5 redirects and configurable response body size capping.

---

## License

This project is licensed under the [MIT License](LICENSE).
