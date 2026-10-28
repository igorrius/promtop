package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The dashboard polls an uninstrumented handler, so promhttp's own counter
// only sees real scrapes.
func TestDashboardDoesNotCountScrapes(t *testing.T) {
	mux, d := newMux(10 * time.Millisecond)
	defer func() { _ = d.Close() }()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	time.Sleep(50 * time.Millisecond) // several dashboard polls

	get := func(path string) (int, string) {
		t.Helper()
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	_, body := get("/metrics")
	if !strings.Contains(body, `promhttp_metric_handler_requests_total{code="200"} 0`) {
		t.Errorf("dashboard polls were counted:\n%s", grep(body, "promhttp_metric_handler_requests_total"))
	}
	if code, _ := get("/webmetrics/api/config"); code != http.StatusOK {
		t.Errorf("dashboard: %d", code)
	}
}

func grep(s, sub string) string {
	var out []string
	for line := range strings.Lines(s) {
		if strings.Contains(line, sub) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "")
}
