// Command embedded is a service with the promtop dashboard built in:
//
//	go run . &
//	open http://localhost:8080/webmetrics/
//
// It is its own module so that client_golang stays out of promtop's go.mod.
package main

import (
	"flag"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/igorrius/promtop/dash"
)

var (
	requests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "shop_http_requests_total", Help: "HTTP requests by path and status code.",
	}, []string{"path", "code"})
	latency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "shop_http_request_duration_seconds", Help: "HTTP request latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"path"})
	inFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "shop_http_requests_in_flight", Help: "Requests being served.",
	})
)

// newMux serves /metrics for Prometheus and the dashboard on /webmetrics/.
func newMux(interval time.Duration) (*http.ServeMux, io.Closer) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// HandlerFor without HandlerOpts.Registry does not count its own calls,
	// so the dashboard's polling stays out of promhttp_metric_handler_*.
	metrics := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{})
	d := dash.New(metrics, dash.Title("embedded example"), dash.Interval(interval))
	mux.Handle("/webmetrics/", d)
	return mux, d
}

// simulate makes up traffic so the dashboard has something to show.
func simulate() {
	paths := []string{"/", "/cart", "/checkout"}
	for range time.Tick(100 * time.Millisecond) {
		p := paths[rand.IntN(len(paths))]
		code := "200"
		if rand.IntN(20) == 0 {
			code = "500"
		}
		inFlight.Set(float64(rand.IntN(8)))
		requests.WithLabelValues(p, code).Inc()
		latency.WithLabelValues(p).Observe(rand.ExpFloat64() * 0.05)
	}
}

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	flag.Parse()
	go simulate()
	mux, d := newMux(2 * time.Second)
	log.Printf("dashboard on http://%s/webmetrics/", *addr)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	err := srv.ListenAndServe()
	_ = d.Close()
	log.Fatal(err)
}
