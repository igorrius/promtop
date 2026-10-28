# Embedded web dashboard (`promtop/dash`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A public Go package `github.com/igorrius/promtop/dash` whose `http.Handler` serves the
promtop web dashboard for the current process's metrics, read by calling the service's own metrics
handler in-process.

**Architecture:** `internal/scrape` gets a `Scraper` interface and an in-process source that calls
an `http.Handler`. `internal/web.Server` takes any `Scraper`, gains a `Start`/`Close` lifecycle, an
access predicate instead of `localOnly`, prefix-independent routing and an `api/config` endpoint for
initial options. `dash` is a thin public wrapper: options, access predicates, lifecycle. The CLI's
`-web` keeps its behaviour and runs on the same `web.Server`.

**Tech Stack:** Go (stdlib `net/http`, `net/netip`, `runtime/debug`), embedded HTML/JS page
(`internal/web/assets`), `prometheus/client_golang` only in the nested example module.

**Spec:** `docs/superpowers/specs/2026-10-28-embedded-web-dashboard-design.md`

## Global Constraints

- The main module `github.com/igorrius/promtop` gains **no new dependencies** (no `client_golang`,
  no `prometheus/common`). `client_golang` appears only in `examples/embedded/go.mod`.
- CLI behaviour unchanged: `promtop`, `promtop -web` (Host check on a loopback listener, open on a
  non-loopback `-web-addr`, URL fragment `#all=1&filter=…`), `render`, `record`.
- Defaults: interval 2 s, history 300 samples, access `dash.Localhost`.
- In-process request: `GET`, `Accept: text/plain;version=0.0.4;q=1,*/*;q=0.1` (same as
  `scrape.Source`), no `Accept-Encoding`, `User-Agent: promtop-dash/<version>`.
- Public names (user-approved): `dash.New`, `dash.Allow`, `dash.Interval`, `dash.History`,
  `dash.Filter`, `dash.Title`, `dash.Localhost`, `dash.PrivateNetwork`, `dash.Anyone`.
- Private ranges for `PrivateNetwork`: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7 (this is
  exactly `netip.Addr.IsPrivate`), plus loopback.
- One scrape per interval regardless of open tabs; no goroutine outlives `Close`.
- Page URLs stay relative (`api/events`, `api/panel`, `api/config`, `app.js`).
- `make test` (`go vet ./...` + `go test -race ./...`), `gofmt`, `golangci-lint run ./...` pass
  after every task. Comment style: short doc comments like the surrounding code.
- Work on a branch `embed-dash` cut from `main` (75fd674 or later).

## Review Focus

1. A metrics handler that answers gzip anyway (`Content-Encoding: gzip`) → a clear scrape error in
   the page, not garbage parsing. Test in Task 1.
2. Mounted as `/webmetrics` without the trailing slash, or under a router via `http.StripPrefix` →
   redirect to `…/`, then page, assets and API all work. Tests in Task 3.
3. `RemoteAddr` in IPv4-mapped IPv6 form (`[::ffff:127.0.0.1]:5555`, `[::ffff:10.1.2.3]:5555`) →
   treated as the IPv4 address it maps. Test in Task 4.
4. `RemoteAddr` that does not parse (unix-socket listener: `""` or `"@"`) → denied by `Localhost`
   and `PrivateNetwork`, never a panic. Test in Task 4.
5. `Close` while a browser holds an open event stream, and `Close` twice → stream ends, `Close`
   returns promptly and is idempotent. Tests in Task 2 and Task 4.

---

### Task 1: In-process source in `internal/scrape`

**Files:**
- Modify: `internal/scrape/scrape.go` (add the interface next to `Source`)
- Create: `internal/scrape/handler.go`
- Test: `internal/scrape/handler_test.go`

**Interfaces:**
- Produces:
  - `type Scraper interface { Scrape(ctx context.Context) Result; String() string }` — `*Source`
    already satisfies it; add `var _ Scraper = (*Source)(nil)`.
  - `type Handler struct { H http.Handler; Name string; UserAgent string; Timeout time.Duration }`
    with `func (h *Handler) Scrape(ctx context.Context) Result` and `func (h *Handler) String() string`
    (returns `Name`, or `"this process"` when empty).

- [ ] **Step 1: Write the failing tests** in `handler_test.go`:
  - `TestHandlerScrape`: `H` writes `"# TYPE up gauge\nup 1\n"`; assert `res.Err == nil`,
    `len(res.Families) == 1`; the handler saw `Accept` equal to the constant above, an empty
    `Accept-Encoding`, `User-Agent == "promtop-dash/test"` (set via `UserAgent`), method `GET`.
  - `TestHandlerStatus`: `H` answers 500 with body `boom` → `res.Err` contains `500`.
  - `TestHandlerGzip`: `H` sets `Content-Encoding: gzip` → `res.Err` contains `Content-Encoding`.
  - `TestHandlerParseError`: `H` writes `"not a metric line {"` → `res.Err != nil`.
  - `TestHandlerPanic`: `H` panics with `"kaboom"` → `res.Err` contains `kaboom`, test does not crash.
  - `TestHandlerTooLarge`: `H` writes more than `maxBody` bytes in 1 MiB chunks → `res.Err != nil`.
  - `TestHandlerString`: `Name: "orders"` → `"orders"`; empty → `"this process"`.

- [ ] **Step 2: Run** `go test ./internal/scrape/ -run Handler` — Expected: FAIL, `Handler` undefined.

- [ ] **Step 3: Implement** `handler.go`. A private `ResponseWriter` (header map, status defaulting
  to 200, body buffer that stops accepting and records an error past `maxBody`); no
  `net/http/httptest` outside tests. `Scrape` applies `Timeout` to the request context when > 0,
  calls `H.ServeHTTP` under `defer recover()` (error: `metrics handler panicked: <v>`), rejects
  non-2xx (`metrics handler returned <status>`) and any non-empty `Content-Encoding`
  (`unsupported Content-Encoding <v>`), then `expo.Parse`. Fill `Result.At`/`Duration` like
  `Source.Scrape`. Move the `Accept` string into a shared unexported constant used by both sources.

- [ ] **Step 4: Run** `go test -race ./internal/scrape/` — Expected: PASS.

- [ ] **Step 5: Commit** — `git commit -m "scrape: an in-process source that calls a metrics http.Handler"`
  (include the spec and this plan in the first commit of the branch).

---

### Task 2: `web.Server` on any `Scraper`, with `Start`/`Close` and an access predicate

**Files:**
- Modify: `internal/web/server.go`, `internal/cli/cli.go` (only if `web.Config` construction needs it)
- Test: `internal/web/web_test.go`

**Interfaces:**
- Consumes: `scrape.Scraper` (Task 1).
- Produces:
  - `web.Config.Source scrape.Scraper` (was `*scrape.Source`).
  - `func (s *Server) Start()` — starts the poll goroutine (the current `scrapeLoop`), once.
  - `func (s *Server) Close()` — stops polling, ends open `api/events` streams, waits for the poll
    goroutine; idempotent; safe without `Start`.
  - `func (s *Server) Handler(allow func(*http.Request) bool) http.Handler` — `nil` allows all;
    denied requests get `403` with body `s.cfg.Denied` (new `Config.Denied string`, default
    `"promtop only answers to localhost"`, today's text).
  - `func LoopbackHost(hostport string) bool` — today's `loopbackHost`, exported.
  - `func HostOnly(r *http.Request) bool` — `LoopbackHost(r.Host)`; what `Run` passes on a
    loopback listener.

- [ ] **Step 1: Write the failing tests:**
  - Update `newServer` and `TestHostCheck` to `Handler(HostOnly)` (same expectations).
  - `TestAllowNil`: `Handler(nil)` with `Host: evil.example:80` → 200.
  - `TestDeniedText`: `Config.Denied: "nope"` + predicate returning false → 403, body contains `nope`.
  - `TestCloseEndsStreams`: fake `Scraper` (counts calls, returns a one-family result), `Start()`,
    open `api/events` through `httptest.NewServer`, read the first event, call `Close()` → the
    response body reaches EOF within 2 s; `Close()` again returns; the call counter stops changing
    after `Close` (sample it, wait two intervals of 10 ms, compare).
  - `TestCloseWithoutStart`: `New(...).Close()` returns immediately.

- [ ] **Step 2: Run** `go test ./internal/web/` — Expected: FAIL to compile (`HostOnly`, `Start`,
  `Close` undefined).

- [ ] **Step 3: Implement.** A `done chan struct{}` closed once (`sync.Once`) by `Close`; the poll
  loop selects on it as well as on its context; `events` selects on it next to
  `r.Context().Done()`; a `sync.WaitGroup` for the poll goroutine. `Run` calls `Start()`, and on
  `ctx.Done()` calls `Close()` before `srv.Shutdown`; it passes `HostOnly` when the listener is
  loopback, `nil` otherwise — so `-web` behaves exactly as before.

- [ ] **Step 4: Run** `make test` — Expected: PASS (whole module, CLI tests included).

- [ ] **Step 5: Commit** — `git commit -m "web: any scrape source, Start/Close and an access predicate"`

---

### Task 3: Prefix-independent routing and `api/config`

**Files:**
- Modify: `internal/web/server.go`, `internal/web/assets/app.js`
- Test: `internal/web/web_test.go`

**Interfaces:**
- Consumes: `Server.Handler(allow)` (Task 2).
- Produces: the handler serves correctly whatever prefix it is mounted under, and
  `GET <prefix>/api/config` → `{"all":<bool>,"filter":"<string>"}` from `Config.ShowAll` /
  `Config.Filter`.

- [ ] **Step 1: Write the failing tests** (`TestPrefixes`), table over three mountings of the same
  handler: at root; in a `http.ServeMux` at pattern `/webmetrics/`; inside
  `http.StripPrefix("/webmetrics", h)` registered at `/webmetrics/`. For each, through
  `httptest.NewServer`: `<p>/` → 200 and `text/html`; `<p>/app.js`, `<p>/style.css`,
  `<p>/uPlot.iife.min.js` → 200; `<p>/api/panel?id=app_up` → 200; `<p>/api/events` → first event
  parses as `Frame`; `<p>/api/config` → JSON equal to `{"all":true,"filter":"disk"}` for a server
  built with `ShowAll: true, Filter: "disk"`. Plus, for the bare handler mounted with
  `mux.Handle("/webmetrics/", h)` *and* for the handler called directly with path `/webmetrics`:
  a request to `/webmetrics` (no slash, client not following redirects) → 3xx whose `Location`,
  resolved against the request URL, is `/webmetrics/`.

- [ ] **Step 2: Run** `go test ./internal/web/ -run Prefixes` — Expected: FAIL (404s under the prefix,
  no `api/config`).

- [ ] **Step 3: Implement** routing on the end of the path, not on the whole path: a path ending in
  `/api/events`, `/api/panel` or `/api/config` goes to that endpoint; a path ending in `/` serves
  `index.html`; a path whose last segment names a file in `assets` serves it
  (`http.ServeFileFS`); any other path without a trailing slash redirects (`301`) to its last
  segment + `/` as a **relative** `Location` (e.g. `webmetrics/`), so it stays right behind
  `StripPrefix` and proxies;
  everything else 404. The access check wraps all of it, as today.
  In `app.js` `init()`: when `location.hash` is empty, `fetch("api/config")` and use its `all` /
  `filter` as the initial options before `connect()`; a non-empty hash keeps today's behaviour and
  wins.

- [ ] **Step 4: Run** `make test` — Expected: PASS. Then manually: `go run ./examples/demo &` and
  `go run . -web -f disk 8080` (with `BROWSER=true`), open the printed URL, then the same URL
  without the `#…` fragment: both show the `disk` filter. Stop both.

- [ ] **Step 5: Commit** — `git commit -m "web: serve under any path prefix, initial options from api/config"`

---

### Task 4: Public package `dash`

**Files:**
- Create: `dash/dash.go` (package doc, `New`, `Dashboard`, options), `dash/allow.go` (predicates)
- Test: `dash/dash_test.go`, `dash/allow_test.go`, `dash/example_test.go`

**Interfaces:**
- Consumes: `scrape.Handler` (Task 1); `web.New`, `web.Config` (`Source`, `Interval`, `History`,
  `Filter`, `Denied`), `Server.Start/Close/Handler`, `web.LoopbackHost` (Tasks 2–3).
- Produces (public API):
  - `type Option func(*config)`; `func Allow(fn func(*http.Request) bool) Option` (nil → keeps
    `Localhost`); `func Interval(d time.Duration) Option` (≤0 → 2 s); `func History(n int) Option`
    (≤0 → 300); `func Filter(re string) Option`; `func Title(s string) Option`.
  - `type Dashboard struct{…}`; `func New(metrics http.Handler, opts ...Option) *Dashboard` —
    builds the `scrape.Handler` (Name = title, Timeout = min(interval, 10 s),
    UserAgent = `"promtop-dash/" + version()`), starts polling; `func (d *Dashboard) ServeHTTP(w, r)`;
    `func (d *Dashboard) Close() error` (always nil, idempotent). After `Close`, `ServeHTTP`
    answers `503`.
  - `func Localhost(r *http.Request) bool` — remote address loopback **and** `web.LoopbackHost(r.Host)`.
  - `func PrivateNetwork(r *http.Request) bool` — remote address loopback or `IsPrivate`; no Host check.
  - `func Anyone(*http.Request) bool` — true.
  - Remote address: `netip.ParseAddrPort(r.RemoteAddr)`, then `.Addr().Unmap()`; parse failure → false.
  - `version()`: the `github.com/igorrius/promtop` entry of `debug.ReadBuildInfo()` (`Deps`, or
    `Main` when promtop itself is the main module); `"dev"` when absent or `"(devel)"`.
  - 403 body (`web.Config.Denied`): `promtop/dash: access denied for this client; the dashboard
    answers to localhost by default, see dash.Allow`.

- [ ] **Step 1: Write the failing tests:**
  - `allow_test.go`, table-driven over `(RemoteAddr, Host)` → `Localhost`, `PrivateNetwork`:
    `127.0.0.1:5555`/`localhost:8080` → true,true; `[::1]:5555`/`[::1]:8080` → true,true;
    `127.0.0.1:5555`/`evil.example` → false,true; `172.17.0.1:5555`/`localhost:8080` (Docker
    bridge) → false,true; `10.1.2.3:5555`, `192.168.1.5:5555`, `[fd00::1]:5555` → false,true;
    `8.8.8.8:5555`, `[2001:db8::1]:5555` → false,false; `[::ffff:127.0.0.1]:5555`/`localhost` →
    true,true; `[::ffff:10.1.2.3]:5555` → false,true; `""` and `"@"` → false,false. `Anyone` → true.
  - `dash_test.go`:
    - `TestServe`: metrics handler writes `"# TYPE app_up gauge\napp_up 1\n"`;
      `d := New(h, Interval(10*time.Millisecond), Title("orders"))`; mount in a `ServeMux` at
      `/webmetrics/`; request `/webmetrics/api/events` with `RemoteAddr 127.0.0.1:1`, `Host localhost`
      (via `httptest.NewRecorder` + a context you cancel after the first event, or `httptest.NewServer`
      on loopback) → first frame has `source == "orders"` and a card `app_up`.
    - `TestDefaultDenies`: request with `RemoteAddr 172.17.0.1:1` → 403, body contains `dash.Allow`.
    - `TestAllowAnyone`: `New(h, Allow(Anyone))` → same request 200.
    - `TestUserAgent`: the metrics handler records `User-Agent`; it starts with `promtop-dash/`.
    - `TestFilterConfig`: `New(h, Filter("disk"))` → `api/config` returns `"filter":"disk"`.
    - `TestClose`: `Close()` twice returns nil; afterwards `ServeHTTP` → 503; metrics handler call
      count stops changing.
  - `example_test.go`: `ExampleNew` showing the README snippet (with a plain `http.HandlerFunc`
    standing in for `promhttp`, since the main module must not import it), no `// Output:`.

- [ ] **Step 2: Run** `go test ./dash/` — Expected: FAIL (package does not exist).

- [ ] **Step 3: Implement** `dash.go` and `allow.go` per the Interfaces block. Package doc comment
  carries the usage snippet from the spec (uninstrumented `promhttp.HandlerFor`), the access
  defaults and the env-switch pattern, briefly.

- [ ] **Step 4: Run** `make test` and `golangci-lint run ./...` — Expected: PASS, no findings.

- [ ] **Step 5: Commit** — `git commit -m "dash: embed the web dashboard in a Go service"`

---

### Task 5: Example service, CI, docs, `go` directive

**Files:**
- Create: `examples/embedded/go.mod`, `examples/embedded/go.sum`, `examples/embedded/main.go`,
  `examples/embedded/main_test.go`
- Modify: `.github/workflows/ci.yml`, `Makefile`, `README.md`, `ROADMAP.md`, possibly `go.mod`

**Interfaces:**
- Consumes: the `dash` public API (Task 4).

- [ ] **Step 1: Write the example module.** `go.mod`: `module github.com/igorrius/promtop/examples/embedded`,
  `require github.com/prometheus/client_golang v1.25.0`, `replace github.com/igorrius/promtop => ../..`;
  `go mod tidy` inside it. `main.go`: a few `client_golang` metrics (a request counter by path, a
  latency histogram, an in-flight gauge) updated by a ticker; `/metrics` with `promhttp.Handler()`;
  `/webmetrics/` with `dash.New(promhttp.HandlerFor(prometheus.DefaultGatherer,
  promhttp.HandlerOpts{}), dash.Title("embedded example"))`; `-addr` flag default `:8080`; logs the
  dashboard URL. Put the mux construction in a function `newMux() (*http.ServeMux, io.Closer)` so the
  test can use it.

- [ ] **Step 2: Write `main_test.go`** `TestDashboardDoesNotCountScrapes`: build the mux, serve it
  with `httptest.NewServer`, wait for at least three dashboard polls (use `dash.Interval(10*time.Millisecond)`
  via a package-level variable the test overrides, or a `newMux(interval)` parameter), then GET
  `/metrics` and assert `promhttp_metric_handler_requests_total{code="200"}` equals `0` (the only
  scrape so far is this one, counted after it is served) — and that `/webmetrics/api/config` from
  the loopback test server answers 200.

- [ ] **Step 3: Run** `cd examples/embedded && go vet ./... && go test -race ./...` — Expected: PASS.

- [ ] **Step 4: CI and Makefile.** In `ci.yml` job `test`, after `test`: a step
  `example module` running `go vet ./... && go test -race ./...` with
  `working-directory: examples/embedded`. In `Makefile` `test`: the same two commands via
  `cd examples/embedded && …`. Add `embedded:` target: `cd examples/embedded && go run .`.

- [ ] **Step 5: `go` directive.** Dependencies require at most `go 1.26.0`. Set `go 1.26.0` in the
  root `go.mod` (`go mod edit -go=1.26.0`), run `go vet ./...` (its `stdversion` check flags
  stdlib symbols newer than the directive) and `make test`. If both pass, keep it (the example
  module uses the same value); if not, restore `go 1.27.3` and note the minimum Go version in the
  README section below. Confirm `.goreleaser.yaml`/CI read the version from `go.mod` and need no change.

- [ ] **Step 6: README** — new section `## Embed in your service` after the web-dashboard section:
  the usage snippet (uninstrumented handler, with the one-sentence why), access table
  (`Localhost` default: dev machine and `kubectl port-forward` work, ingress/other pods get 403, a
  sidecar proxy on localhost looks local; `PrivateNetwork` for Docker/compose/WSL2/VMs, and in
  Kubernetes it opens the dashboard to the whole cluster network; `Anyone`; a custom predicate
  composed with `dash.Localhost`), the `DEBUG_DASH` env-switch pattern, options list with
  defaults, pointer to `examples/embedded`. **ROADMAP** — under "Web dashboard": a
  `prometheus.Gatherer` adapter for `dash` as a possible follow-up.

- [ ] **Step 7: Run** `make test`, `golangci-lint run ./...`, `gofmt -l .` (empty) — Expected: all pass.
  Manually: `make embedded`, open `http://localhost:8080/webmetrics` (no slash) → redirected,
  dashboard live with the example's metrics and title.

- [ ] **Step 8: Commit** — `git commit -m "examples/embedded, docs and CI for promtop/dash"`
