# promtop

`top` for Prometheus metrics. Point it at a service's `/metrics` endpoint and get a live terminal
dashboard — no Prometheus server, no Grafana, no config. Every metric gets a widget chosen from its
type and name.

```sh
promtop 8080            # http://localhost:8080/metrics
promtop db-1:9187       # http://db-1:9187/metrics
promtop https://svc.example/internal/metrics -H "Authorization: Bearer $TOKEN"
promtop ./dump.prom     # an exposition saved to a file
```

```text
 promtop  http://localhost:8080/metrics               ● 2s  18 metrics · 38 series · 1ms · 17:32:33
 ▍demo
╭─ http_requests_total ────────────── Σ 87.3/s ─╮ ╭─ http_request_duration_seconds ────────────────╮
│ /api/orders · 200 · GET    27.9/s           ▇ │ │ /api/search · GET            840ms          █▇ │
│ /api/users · 200 · GET     23.5/s           ▆ │ │ /healthz · GET                96ms          ▂▂ │
│ /api/search · 200 · GET      14/s           ▂ │ │ /api/users · GET            91.2ms          ▃▂ │
│ +12 more · enter for all                      │ │ +2 more · enter for all                        │
╰─ rate · 15 series                            ─╯ ╰─ histogram p99 · 5 series                     ─╯
╭─ http_requests_in_flight ───────────────── 6 ─╮ ╭─ queue_depth ──────────────────────────────────╮
│ 6  min 6 · max 8                              │ │ emails                          63          ██ │
│                                            ▆  │ │ reports                         21          ▃▃ │
│                                            █  │ │                                                │
│                                            █▃ │ │                                                │
╰─ gauge                                       ─╯ ╰─ gauge · 2 series                             ─╯
╭─ job_duration_seconds ──────────── p99 1.46s ─╮
│ p99      1.46s                             ██ │
│ p90      656ms                             ▄▄ │
│ p50      199ms                             ▂▂ │
│ rate 12/s · avg 228ms · n 4461                │
╰─ summary                                     ─╯

 ▍Process
╭─ cpu_seconds_total ─────────────────── 33.5% ─╮ ╭─ open_fds ───────────────────────────── 5.76% ─╮
│ 33.5%  total 2m23s                            │ │ 59 / 1024                                      │
│                                               │ │ ██▋                                            │
│                                             ▅ │ │                                             ▇  │
│                                             █ │ │                                             █▂ │
╰─ rate                                        ─╯ ╰─ usage                                        ─╯
```

*(a `-once` snapshot of the bundled demo, filtered with `-f`; in the live view sparklines fill up as history accumulates and `enter` opens a full-size chart)*

## Install

```sh
go install github.com/igorrius/promtop@latest
```

or grab a binary from [Releases](https://github.com/igorrius/promtop/releases). promtop is a single
static binary built from the Go standard library only — no third-party dependencies.

## How widgets are chosen

| Metric                                         | Widget                                                        |
|------------------------------------------------|---------------------------------------------------------------|
| `counter`                                      | per-second rate with sparkline; Σ of all series in the title  |
| `counter` named `*_bytes_total`                | throughput, `KiB/s`                                           |
| `counter` named `*_seconds_total`              | utilisation in % (e.g. `process_cpu_seconds_total` → CPU %)    |
| `gauge`                                        | current value, min/max over the window, sparkline             |
| `histogram`                                    | p50/p90/p99 computed from bucket **deltas** between scrapes, rate, average; bucket distribution in details |
| `summary`                                      | client-side quantiles, rate, average                          |
| `*_info`, `*_build_info`, `info`/`stateset`    | label table                                                   |
| `*_timestamp_seconds`, `*start_time_seconds`   | age / uptime                                                  |
| gauge with a `max`/`limit` sibling             | usage bar (`process_open_fds` / `process_max_fds`)            |
| untyped `*_total`                              | treated as a counter                                          |

Units come from metric-name suffixes (`_bytes`, `_seconds`, `_ratio`, `_percent`, `_celsius`, …).

Layout:

* Metrics are grouped by their prefix (`http_`, `db_`, your namespace…); `go_`, `process_` and
  `promhttp_` runtime sections go last.
* Well-known runtime metrics are ordered by usefulness — both `client_golang` collectors
  (`go_goroutines`, `go_memstats_*`) and OpenTelemetry runtime semantic conventions
  (`go_goroutine_count`, `go_memory_used_bytes`, `go_schedule_duration_seconds`).
  Low-level noise (most of `go_memstats_*`, `promhttp_*`) is hidden until you press `a`.
* Labels shared by every series of a metric, and OpenTelemetry `otel_scope_*` labels, are hidden.
  Multi-series rows list the most distinguishing label first.
* `target_info` (OpenTelemetry resource) is shown in the header as the service name.

## Keys

| Key                   | Action                                          |
|-----------------------|-------------------------------------------------|
| `←↑↓→` / `hjkl`       | move between panels                             |
| `enter`               | details: large chart, every series, buckets     |
| `↑↓` (details)        | choose a series                                 |
| `←→` / `tab` (details)| choose a line (p99, p50, rate, avg, …)          |
| `/`                   | filter by regex (`c` clears)                    |
| `a`                   | show all metrics / curated                      |
| `p`, `space`          | pause                                           |
| `+` / `-`             | change scrape interval                          |
| `esc`, `q`            | back / quit                                     |

## Flags

```
-i duration       scrape interval (default 2s)
-H "Name: value"  extra request header, repeatable
-f regex          only show matching metrics
-a                show all metrics, including runtime noise
-history n        samples kept per series (default 300)
-timeout d        scrape timeout (default: interval, at most 10s)
-once             scrape twice, print one frame and exit (CI, logs, screenshots)
-width n          width for -once
-no-color         disable colours (NO_COLOR is honoured too)
```

## Try it

```sh
go run ./examples/demo &   # synthetic metrics of every type on :8080
go run . 8080
```

## Notes

* Rates and histogram quantiles need two scrapes; the first frame shows lifetime values.
* Histogram quantiles use the same linear interpolation as PromQL `histogram_quantile`.
* Keyboard input needs a Unix-like terminal (Linux, macOS, BSD). On Windows promtop renders and
  refreshes, but is not interactive yet.
* The text exposition format (`text/plain; version=0.0.4`) is requested; OpenMetrics
  suffixes (`_total`, `_created`, `# EOF`) are understood as well.

## License

MIT
