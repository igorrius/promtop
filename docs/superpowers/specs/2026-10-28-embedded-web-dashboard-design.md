# Embedded web dashboard (`promtop/dash`) — design

Date: 2026-10-28
Status: draft, awaiting approval

## Intent

promtop already draws a live dashboard for any `/metrics` endpoint, in the terminal and in the
browser (`promtop -web`). This change lets a Go service **embed the web dashboard in itself**: one
`http.Handler` registered next to `/metrics`, and opening it in a browser shows the same dashboard
that `promtop -web` shows — no Prometheus, no Grafana, no second process.

Why: when writing metrics it is easy to get a name, a label or a type wrong and only find out once
the data reaches Grafana. Standing up a Prometheus or OpenTelemetry stack just to check is too
heavy during development. A dashboard inside the service shows the metrics the moment the service
runs. Beyond development, some services may keep it as a built-in "boxed" visualisation of their
internals.

Who: Go developers instrumenting services with `prometheus/client_golang`, OpenTelemetry's
Prometheus exporter, `VictoriaMetrics/metrics`, or anything else that serves the Prometheus text
format through an `http.Handler`.

## Usage the design aims for

```go
mux.Handle("/metrics", promhttp.Handler())

// HandlerFor without HandlerOpts.Registry: no promhttp_metric_handler_* self-instrumentation,
// so the dashboard's polling does not show up in the counters real scrapers see.
src := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{})
d := dash.New(src, dash.Title("orders-svc"))
defer d.Close()
mux.Handle("/webmetrics/", d)
```

Open `http://localhost:8080/webmetrics/` and see the dashboard.

Access control:

```go
dash.New(metrics)                                  // default: dash.Localhost
dash.New(metrics, dash.Allow(dash.PrivateNetwork)) // Docker, compose, WSL2, local VMs
dash.New(metrics, dash.Allow(dash.Anyone))         // explicitly open to everyone
dash.New(metrics, dash.Allow(func(r *http.Request) bool {
    return dash.Localhost(r) || r.Header.Get("X-Token") == token
}))
```

Switching it on per environment is the service's business, one line in its own code (this goes in
the README as the recommended pattern):

```go
if os.Getenv("DEBUG_DASH") != "" {
    mux.Handle("/webmetrics/", dash.New(promhttp.Handler()))
}
```

## Goals

1. A public package `github.com/igorrius/promtop/dash` that returns an `http.Handler` serving the
   web dashboard for the metrics of the current process.
2. The metrics source is the service's own metrics `http.Handler`, **called in-process** — no
   network, no port, no self-scrape over TCP.
3. Safe by default: only loopback clients get the dashboard unless the developer opts in.
4. Works mounted under any path prefix.
5. `promtop -web` and the embedded dashboard run on the same code: one page, one classification,
   one set of bugs.

## Non-goals

- **Terminal UI inside the service.** Decided against: it would fight the service's own logs for
  the terminal and needs a log panel, stdout capture and so on — features that are not about
  metrics. For the terminal, run `promtop :8080` next to the service.
- Reading environment variables inside the library to switch the dashboard on or off.
- Authentication schemes. The library offers a predicate hook (`dash.Allow`); tokens, basic auth
  and SSO are the developer's.
- A `prometheus.Gatherer` adapter. Can be added later as a thin wrapper; not now.
- Pointing the embedded dashboard at another service's URL, or at Prometheus / VictoriaMetrics as
  a source. That is the separate "Prometheus as a source" roadmap item.
- Changes to the dashboard's look or features beyond what embedding needs (title, initial filter).

## Anti-goals (failure even if it technically works)

- The dashboard exposed to the network by default.
- The `promtop` module gaining dependencies, in particular `client_golang` or `prometheus/common`.
  The library depends on `net/http` only for its source; that is what makes it work with every
  metrics library.
- Any effect on the service's own `/metrics` in the documented setup (the in-process call must not
  change what real scrapers see). See "Side effects of the metrics handler" below for handlers that
  instrument themselves.
- Cost that grows with open browser tabs: one scrape per interval, whatever the number of viewers
  (today's `web.Server` already shares one store and caches frames per sequence — keep that).
- Goroutines that outlive `Close`, so services using it in tests don't leak.
- A second copy of the web dashboard that drifts from `promtop -web`.

## Decided

- **Package and names.** `github.com/igorrius/promtop/dash`. Constructor `dash.New(metrics
  http.Handler, opts ...dash.Option)`. Options without a `With` prefix: `dash.Allow`,
  `dash.Interval`, `dash.History`, `dash.Filter`, `dash.Title`. (The user picked these names.)
- **Access.** `dash.Allow(func(*http.Request) bool)` sets who may see the dashboard. The package
  exports ready predicates:
  - `dash.Localhost` — the default. True when the client address (`r.RemoteAddr`) is loopback
    **and** the `Host` header names a loopback host (the latter is the DNS-rebinding guard that
    `promtop -web` has today). Consequences to document: works on a dev machine with no setup;
    `kubectl port-forward` works (its traffic enters the pod via localhost); ingress and other pods
    get 403. A reverse-proxy sidecar on localhost also looks local — call this out in the README.
    The default stays strict even though it means a service in Docker, docker-compose, WSL2 or a
    local VM answers 403 to the host's browser (the request arrives from the bridge address, e.g.
    `172.17.0.1`). The user chose this over loosening the default; `PrivateNetwork` is the answer
    for those setups.
  - `dash.PrivateNetwork` — true when the client address is loopback or in a private range
    (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7). No Host-header requirement, since a
    browser on another machine of the LAN names the service by its address. README: a "Running in
    Docker" section recommends it, and states plainly that in Kubernetes it opens the dashboard to
    the whole cluster network.
  - `dash.Anyone` — always true.
  - Exporting the predicates is deliberate: custom predicates compose with them instead of copying
    them.
  - Denied requests get `403 Forbidden` with a short plain-text reason that names `dash.Allow`, so
    a developer who hits it in Docker finds the fix.
- **Source.** The given metrics handler is invoked in-process with a synthetic `GET` request that
  asks for the classic text format (same `Accept` as `scrape.Source` sends today) and does **not**
  ask for compression. Non-2xx status or a parse error becomes a scrape error, shown in the page
  exactly as a failed HTTP scrape is shown today. The synthetic request carries
  `User-Agent: promtop-dash/<version>` so the service's middleware (access logs, `otelhttp`) can
  skip it.
- **Side effects of the metrics handler.** `dash` cannot stop a metrics handler from instrumenting
  itself (e.g. `promhttp.Handler()` counts its own calls in `promhttp_metric_handler_requests_total`)
  without importing `client_golang`, which is an anti-goal. So: every snippet, the README and the
  example pass an uninstrumented handler (`promhttp.HandlerFor(prometheus.DefaultGatherer,
  promhttp.HandlerOpts{})`), and the README says in one or two sentences why. The example module
  has a test showing the self-instrumentation counter does not move while the dashboard polls.
  (Claude's call.)
- **Polling.** Polling starts in `dash.New` and runs in the background until `Close`, so history is
  already there when someone opens the page. Defaults match the CLI: interval 2 s, history 300
  samples. Cost is one call of the metrics handler plus a parse per interval.

## Constraints

- Go module `github.com/igorrius/promtop`; today everything lives under `internal/`, and the CLI
  must keep working unchanged (`promtop`, `promtop -web`, `render`, `record`).
- The web page (`internal/web/assets`) already uses relative URLs (`api/events`, `api/panel`,
  `app.js`), which is what makes prefix mounting possible. Keep them relative.
- `ui.Config`, `web.Config` and `cli/render.go` take a concrete `*scrape.Source` (URL or file).
  The web side must instead accept anything that can produce a `scrape.Result`.
- The page currently receives its initial filter and "all metrics" toggle from the URL fragment
  (`#all=1&filter=…`), which `promtop -web` puts in the URL it opens. An embedded dashboard is
  opened by hand at a bare path, so `dash.Filter` must reach the page another way. A fragment the
  user types still wins.
- The header line of the page shows the source (a URL today). Embedded, it shows `dash.Title` if
  set, otherwise something neutral like "this process"; `<title>` follows the same rule.

## Left to the builder

- How the source is abstracted internally: an interface in `internal/scrape` (e.g. `Scrape(ctx)
  Result` plus `String()`), and whether `ui` / `render` move to it too or keep the concrete type.
  `Static()` is only used by `ui` and `render`.
- How `internal/web` splits into "dashboard handler + scrape loop" (used by `dash`) and "listen,
  print the URL, open the browser" (used by the CLI). The CLI keeps its current access behaviour:
  Host check on a loopback listener, open on a non-loopback `-web-addr`.
- How the handler copes with prefixes: stripping by `r.Pattern` (Go ≥1.22 `ServeMux`), serving by
  path suffix, or documenting `http.StripPrefix`. Requirement: the README one-liner with the
  standard `ServeMux` works, and it also works under `chi`-style routers with at most a
  `StripPrefix`.
- How `dash.Filter` and the title reach the page (an extra field in the first frame, a small
  `api/config` endpoint, …).
- The in-process `ResponseWriter` (a small private type; avoid `net/http/httptest` in non-test
  code), timeout for the in-process call, and protection against a handler that panics.
- Whether `Close` is idempotent (it should be) and what the handler answers after `Close`.
- The `go` directive in `go.mod` (currently `go 1.27.3`) sets the minimum toolchain for every
  service that imports `dash`. Check whether it can be lowered without losing anything the code
  uses; if not, leave it and note the requirement in the README.

## Tests and docs

- Tests for: the in-process source (status codes, parse errors, panicking handler, User-Agent);
  `Localhost` (remote address loopback/non-loopback, IPv4/IPv6, Host header rebinding case, a
  Docker bridge address being denied), `PrivateNetwork` (each private range, public addresses
  denied) and `Anyone`;
  mounting under a prefix including the redirect from `/webmetrics` to `/webmetrics/`; `Close`
  stopping the loop; `promtop -web` behaviour unchanged.
- An example service under `examples/` using `client_golang` + `dash`. To keep `client_golang` out
  of the main module's `go.mod`, the example is its own nested module (with a `replace` to the
  repository root). The existing `examples/demo` stays as is.
- README: an "Embed in your service" section with the snippets above, the access defaults and
  their consequences (port-forward works, sidecar proxies look local, Docker needs
  `PrivateNetwork`), the uninstrumented-handler note, and the env-switch pattern.
  ROADMAP: mention a `Gatherer` adapter as a possible follow-up.
