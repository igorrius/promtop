---
name: analyzing-metrics-with-promtop
description: Use when asked to look at, explain, check or troubleshoot a service through its Prometheus /metrics endpoint (exporter, node_exporter, client_golang, OpenTelemetry Prometheus exporter) or a saved .prom exposition, and promtop is installed - "what's wrong with the service", "is latency up", "why are there errors", "how full is the disk", a health report or a daily summary from metrics.
---

# Analyzing metrics with promtop

## Overview

Raw `/metrics` is a poor input for a model: thousands of lines, counters that only make sense as
rates between two scrapes, histogram buckets that need arithmetic. `promtop render -format summary`
does the arithmetic and prints a compact JSON document: rates, quantiles, used fractions, trends
over a sampled window, and a ranked `notable` list. Read that, not the exposition.

## Workflow

1. **What is wrong?** Sample a window and read only the notable list (`metrics` is left out):
   `promtop render -format summary -notable -for 1m <target>`
   When the question names a metric ("how bad is latency"), skip to step 3 and select it as well:
   the full document has `notable` too, and each `-for` run costs its whole duration.
2. **What is there?** Names, types, units and series counts, no values (one scrape, fast):
   `promtop render -format summary -list <target>`
3. **Dig in.** Select the metrics the question is about (`-m` repeats; any match is kept):
   `promtop render -format summary -for 1m -m 'http_request_duration_seconds' -m 'node_filesystem_*' <target>`
4. **Show people** the same selection as a picture when a human will read the answer:
   `promtop render -for 1m -m 'http_request_duration_seconds' -o latency.png <target>`

`<target>`: `9100` = `localhost:9100/metrics`, `host:9100`, a full URL, or `./dump.prom`.
Add `-H 'Authorization: Bearer …'` for protected endpoints.

## Quick reference

| Flag | Use |
|---|---|
| `-for 1m` | sample a window (`30s`, `5m`); **needed for `trend`** (3+ samples, 6+ for trend notes). Default is 2 scrapes: rates, no trends |
| `-i 5s` | sample interval (default 2s) |
| `-m glob`, `-f regex` | select metrics; `-m` repeats (`-m 'http_*' -m 'db_*'`), no comma lists |
| `-l matcher` | keep series matching (`mount=/data`, `'code=~5..'`, `dev!=lo`) **in every selected metric**: metrics without that label disappear. One `-l` query per question |
| `-top n` | series per metric (default 5, `0` = all); cut series are counted in `series_omitted` |
| `-a` | include hidden metrics (idle, Go/process runtime noise, limits merged into usage); `-a -list` names them |
| `-state file -offline` | read history collected by `promtop record` from cron: hours or days, no scraping |

## Reading the document

- `notable[]` is sorted most urgent first: `down`, `near_limit` (≥90%), `errors` (5xx, whole metric,
  `share` of its traffic), `expires_soon` (<7 days), `flapped`, `changed` (restart, deploy),
  `rising`/`falling` (≥50% between the first and last third of the window, beyond counting noise).
- Numbers are in base units named by the metric's `unit`: seconds, bytes, `1/s`, `bytes/s`.
  `seconds/s` is CPU cores. `used_ratio` is 0..1 and always means *used*, also for
  `*_avail_bytes`/`*_free_*` metrics (promtop inverts them against their size): `1` is full.
- Each series has `last/avg/min/max` of the metric's `value` (`rate`, `p99`, `used_ratio`, `value`)
  and `trend` with `change` (relative: `1.5` = +150%). Histograms add `rate`, `mean`, `quantiles`.
- `labels` on a metric are shared by all its series; a series lists only its own.

Full field list: `reference.md` next to this file.

## Common mistakes

- **Curling `/metrics` and reading it yourself.** Counter totals and buckets mislead; use the summary.
- **Reporting "no trend" from a 2-scrape run.** Without `-for` there is no `trend`; sample a minute.
- **Treating `notable` as a verdict.** It is a deterministic shortlist; check each item against the
  question. Garbage-collected heaps (`*heap*`, `jvm_memory_used`) never get trend notes: they
  climb until each GC. Look for leaks in `process_resident_memory_bytes`.
- **Calling a load swing a fault.** When traffic, CPU, latency and pools all rise or fall by a
  similar amount together, the load changed; a short window may catch one phase of a cycle.
  Faults show up as `down`, `errors`, `near_limit`, or one series moving against the rest.
- **Reading `errors.share` from a `-state` file recorded with `-l`.** The file keeps only the
  series `record` selected, so the share is of those (`1` for `-l 'code=~5..'`). A live run
  takes the share from the whole metric whatever `-l` says.
- **Missing a metric.** It may be hidden: check `totals.hidden`, rerun with `-a`.
- **Missing series.** `series_omitted > 0` means more exist: narrow with `-l` or use `-top 0`.
- **Empty `notable` = healthy?** Only for what the heuristics cover; still read the metrics the
  user asked about.
