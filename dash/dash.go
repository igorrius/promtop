// Package dash embeds the promtop web dashboard in a Go service: one
// http.Handler next to /metrics, and the service's own metrics are live in
// the browser, with no Prometheus, Grafana or second process.
//
//	mux.Handle("/metrics", promhttp.Handler())
//
//	// HandlerFor without a Registry in HandlerOpts does not count its own
//	// calls, so the dashboard's polling stays out of what scrapers see.
//	metrics := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{})
//	mux.Handle("/webmetrics/", dash.New(metrics))
//
// The metrics handler is called in-process every interval; anything that
// serves the Prometheus text format works (client_golang, OpenTelemetry's
// Prometheus exporter, VictoriaMetrics/metrics).
//
// Only loopback clients see the dashboard unless Allow says otherwise; see
// Localhost, PrivateNetwork and Anyone. Switching it on per environment is
// the service's call:
//
//	if os.Getenv("DEBUG_DASH") != "" {
//		mux.Handle("/webmetrics/", dash.New(metrics))
//	}
package dash

import (
	"net/http"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/igorrius/promtop/internal/scrape"
	"github.com/igorrius/promtop/internal/web"
)

const (
	defaultInterval = 2 * time.Second
	defaultHistory  = 300
	maxTimeout      = 10 * time.Second
	denied          = "promtop/dash: access denied for this client; the dashboard answers to localhost by default, see dash.Allow"
)

type config struct {
	allow    func(*http.Request) bool
	interval time.Duration
	history  int
	filter   string
	title    string
}

// Option configures a Dashboard.
type Option func(*config)

// Allow sets who may see the dashboard; nil keeps the default, Localhost.
func Allow(fn func(*http.Request) bool) Option {
	return func(c *config) {
		if fn != nil {
			c.allow = fn
		}
	}
}

// Interval sets how often the metrics are read; default 2s.
func Interval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.interval = d
		}
	}
}

// History sets how many samples each series keeps; default 300.
func History(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.history = n
		}
	}
}

// Filter sets the initial filter, a case-insensitive regular expression.
// A filter in the page's URL wins.
func Filter(re string) Option { return func(c *config) { c.filter = re } }

// Title names the service in the page header; default "this process".
func Title(s string) Option { return func(c *config) { c.title = s } }

// Dashboard is the web dashboard as an http.Handler. It reads the metrics
// from New until Close.
type Dashboard struct {
	srv    *web.Server
	h      http.Handler
	closed atomic.Bool
}

// New starts reading metrics, an http.Handler serving the Prometheus text
// format, and returns the dashboard. Mount it under any path; the page
// needs the trailing slash and redirects to it.
func New(metrics http.Handler, opts ...Option) *Dashboard {
	c := config{allow: Localhost, interval: defaultInterval, history: defaultHistory}
	for _, o := range opts {
		o(&c)
	}
	srv := web.New(web.Config{
		Source: &scrape.Handler{
			H: metrics, Name: c.title, UserAgent: "promtop-dash/" + version(),
			Timeout: min(c.interval, maxTimeout),
		},
		Interval: c.interval, History: c.history, Filter: c.filter, Denied: denied,
	})
	srv.Start()
	return &Dashboard{srv: srv, h: srv.Handler(c.allow)}
}

// ServeHTTP serves the page and its API.
func (d *Dashboard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if d.closed.Load() {
		http.Error(w, "promtop/dash: dashboard closed", http.StatusServiceUnavailable)
		return
	}
	d.h.ServeHTTP(w, r)
}

// Close stops reading metrics and ends open event streams. Later requests
// get 503. It always returns nil and may be called more than once.
//
// A dashboard normally lives as long as the process. Call Close on graceful
// shutdown, e.g. from http.Server.RegisterOnShutdown, so open browser tabs
// do not keep Shutdown waiting.
func (d *Dashboard) Close() error {
	d.closed.Store(true)
	d.srv.Close()
	return nil
}

// version is promtop's module version as built into the program.
func version() string {
	const mod = "github.com/igorrius/promtop"
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	v := ""
	if bi.Main.Path == mod {
		v = bi.Main.Version
	}
	for _, d := range bi.Deps {
		if d.Path == mod {
			v = d.Version
		}
	}
	if v == "" || v == "(devel)" {
		return "dev"
	}
	return v
}
