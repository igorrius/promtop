package dash_test

import (
	"io"
	"net/http"
	"time"

	"github.com/igorrius/promtop/dash"
)

func ExampleNew() {
	// With client_golang this is
	// promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{}).
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "# TYPE jobs_done_total counter\njobs_done_total 42\n")
	})

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics)
	d := dash.New(metrics, dash.Title("orders-svc"))
	mux.Handle("/webmetrics/", d) // http://localhost:8080/webmetrics/

	srv := &http.Server{Addr: "localhost:8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	srv.RegisterOnShutdown(func() { _ = d.Close() }) // open tabs would hold up Shutdown
	_ = srv                                          // srv.ListenAndServe()
}
