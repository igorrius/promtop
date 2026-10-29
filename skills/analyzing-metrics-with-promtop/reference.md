# `promtop.summary/v1` reference

Written by `promtop render -format summary` as one line of JSON. Fields are left out when they
have no value (no NaN, no null). The `schema` string changes only on incompatible changes.

```json
{
  "schema": "promtop.summary/v1",
  "source": "http://localhost:8080/metrics",
  "target": "shop/orders 1.4.2",
  "window": {"from": "2026-10-29T14:40:49Z", "to": "2026-10-29T14:41:49Z", "samples": 31, "interval_s": 2},
  "totals": {"metrics": 30, "series": 58, "shown": 26, "hidden": 4},
  "notable": [
    {"metric": "demo_dependency_up", "labels": {"dependency": "kafka"}, "reason": "down", "changes": 2},
    {"metric": "demo_disk_avail_bytes", "labels": {"mount": "/var/lib/demo"}, "value": "used_ratio", "reason": "near_limit", "last": 1},
    {"metric": "demo_http_requests_total", "value": "rate", "reason": "errors", "last": 0.5, "share": 0.0076},
    {"metric": "demo_queue_depth", "labels": {"queue": "emails"}, "value": "value", "reason": "rising",
     "last": 18, "from": 11.5, "to": 19.5, "change": 0.70}
  ],
  "notable_omitted": 0,
  "metrics": [
    {"name": "demo_http_requests_total", "type": "counter", "kind": "rate", "unit": "1/s", "value": "rate",
     "help": "Total HTTP requests.", "series_count": 15,
     "sum": {"last": 126, "avg": 120, "min": 96.4, "max": 155, "trend": "flat", "change": 0.04},
     "series": [{"labels": {"code": "200", "method": "GET", "path": "/api/orders"},
                 "last": 43.9, "avg": 41.2, "min": 30.1, "max": 47.9, "trend": "flat", "change": 0.02, "total": 342186}],
     "series_omitted": 14}
  ]
}
```

## Top level

| Field | Meaning |
|---|---|
| `source`, `target` | the scraped URL or file; service name and version from `target_info`, if any |
| `window` | first and latest scrape (RFC 3339, UTC), number of samples kept, seconds between samples |
| `totals.metrics`, `totals.series` | what was read: the whole target, or what `-m`/`-f`/`-l` selected (live series) |
| `totals.shown` | metrics in this document; `totals.hidden` were left out, `-a` includes them |
| `notable`, `notable_omitted` | at most 20 notes, most urgent first; how many more there were. Absent with `-list` |
| `metrics` | absent with `-notable` |

## Notes

| `reason` | When | Extra fields |
|---|---|---|
| `down` | a 0/1 health flag (a name with `up`, `success`, `ready`, `healthy`, `alive`, `connected`…) is 0 | `changes`: flips in the window |
| `near_limit` | usage at 90% or more | `last`: used ratio |
| `errors` | a counter with a 5xx `code`/`status`/`status_code` label is moving | `last`: 5xx rate summed over the metric, `share` of the metric's whole rate, before `-l` (from a `-state` file: of the series `record` kept). No `labels`: the series are in `metrics` |
| `expires_soon` | a timestamp metric named like a deadline (`*expir*`, `not_after`, `notafter`, `deadline`) is less than 7 days ahead | `in_s` |
| `flapped` | a health flag is up now but changed during the window, or a stateset switched (one note per metric, `labels` = the state now) | `changes` |
| `changed` | a timestamp metric moved: restart (`process_start_time_seconds`), deploy, reload | |
| `rising`, `falling` | mean of the window's last third differs from the first by 50% or more, or starts from 0 | `last`, `from`, `to`, `change` |

A series' `trend` needs 3 samples; a trend *note* is stricter. Event counters must move beyond
Poisson noise (3σ of the counts in a third). Gauges, quantiles and other rates must move more than
they wobble within the thirds (Welch, 3σ), which needs 6 samples (`-for 12s` at the default
`-i 2s`); quantiles also need 20 observations per third. Garbage-collected heap gauges (names with
`heap`, `go_memstats_alloc_bytes`, `go_memory_used*`, `jvm_memory_used*`) get no trend notes: they
climb until each collection.

## Metrics

| Field | Meaning |
|---|---|
| `type` | exposition type: `counter`, `gauge`, `histogram`, `summary`, `info`, `stateset`, `untyped` |
| `kind` | how promtop reads it: `rate`, `gauge`, `histogram`, `summary`, `usage`, `state`, `time`, `info` |
| `unit` | base unit of the series' numbers: `seconds`, `bytes`, `ratio`, `percent`, a symbol (`°C`, `V`), `1/s`, `bytes/s`; absent for plain numbers |
| `value` | what `last/avg/min/max` describe: `rate`, `value`, `used_ratio`, or a quantile (`p99`) |
| `labels` | labels shared by every series, left out of the series |
| `limit` | usage: the metric holding the maximum (`node_filesystem_size_bytes`) |
| `series_count`, `series_omitted` | live series; how many `-top` cut (series are sorted: problems and biggest values first) |
| `sum` | rates with several series: their total over the window |

## Series

| Field | Kinds | Meaning |
|---|---|---|
| `labels` | all | own labels |
| `last`, `avg`, `min`, `max` | rate, gauge, histogram, summary, usage | of the metric's `value` over the window |
| `trend`, `change` | same, 3+ samples | `rising`/`falling`/`flat`; relative change, first third → last third |
| `total` | rate | the counter's lifetime value |
| `rate`, `mean`, `quantiles` | histogram, summary | observations per second, average observation, latest quantiles |
| `used`, `limit` | usage | latest, in `unit` |
| `up` / `active` | state | health flag is 1 / stateset member is set; `changes` = flips in the window |
| `time`, `age_s` / `in_s` | time | the timestamp; seconds since, or seconds until when it is ahead |
