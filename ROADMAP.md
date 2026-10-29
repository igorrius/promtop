# Roadmap

Ideas that were discussed and deliberately left for later, roughly in the order they would pay
off. Nothing here is promised; issues and pull requests are welcome.

## Snapshots (`promtop render`)

- **A "cards" style.** A second `render.Style` that draws like the web dashboard — rounded cards,
  smooth area charts, proportional fonts — instead of terminal cells. Probably on
  [`tdewolff/canvas`](https://github.com/tdewolff/canvas), which writes PNG, SVG and PDF from one
  drawing. The command line stays the same: `-style cards`.
- **Prometheus as a source.** Read history from a Prometheus server (`query_range`) instead of
  sampling or a state file, so `render -m 'node_load*' -range 24h` has a day of data at once.
  Needs a way to name a PromQL server next to the exporter target.
- **Thresholds.** Rules that colour or flag values, e.g. `-warn 'battery_percent<20'`, for
  pictures that should shout when something is wrong, and an exit code a pipeline can act on.
- **Font fallback** for CJK and emoji in label values: DejaVu Sans Mono has no CJK glyphs, so
  images show boxes where the terminal shows the text.
- **Safe concurrent `record`.** Two writers at the same instant can lose one sample; a lock file
  would make it exact.
- **Agent summary: raw points.** A `-points` switch for `-format summary` that adds each series'
  samples with timestamps, for questions the window statistics do not answer.
- **An MCP server** (`promtop mcp`) over the summary document, mainly to reach the history of a
  running terminal or web session and to hand multimodal models the PNG of a chart; agents with a
  shell already get everything else from `render -format summary`.
- PDF output, and a size hint for chat apps (Telegram re-encodes photos above 1280 px).

## Dashboard layout

- **Tables for families that share labels.** Many single-purpose metrics with the same series
  (`node_network_*{device}`, `node_memory_numa_*{node}`) could become one table, device × metric,
  instead of a panel each. Today only single-series metrics are grouped.
- More usage pairs, e.g. `node_entropy_available_bits` against `node_entropy_pool_size_bits`.
- Native histograms. client_golang exposes them only in the protobuf format, which promtop does
  not request yet (`prometheus/common/expfmt` would bring it, with protobuf as a dependency).

## Web dashboard

- A `prometheus.Gatherer` adapter for `dash`, for services that have a registry but no handler.
  It would live in its own module so that `client_golang` stays out of promtop's `go.mod`.
- Changing the scrape interval and pausing on the server, not just in the page.
- Overlaying several series of one metric in the detail chart.
- A test that keeps the page's value formatting in step with the Go formatter in CI (it is
  checked by hand today; it needs Node in the pipeline).

## Distribution

- A Homebrew tap and a Scoop bucket. Both need a separate repository and a token for GoReleaser.
- A container image for running `-web` next to a service.
