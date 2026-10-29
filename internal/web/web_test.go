package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/scrape"
)

const input = `# TYPE app_requests_total counter
app_requests_total{path="/a"} 10
app_requests_total{path="/b"} 20
# TYPE app_latency_seconds histogram
app_latency_seconds_bucket{le="0.1"} 1
app_latency_seconds_bucket{le="1"} 3
app_latency_seconds_bucket{le="+Inf"} 4
app_latency_seconds_sum 2
app_latency_seconds_count 4
# TYPE app_up gauge
app_up{dep="db"} 1
app_up{dep="cache"} 0
# TYPE app_disk_avail_bytes gauge
app_disk_avail_bytes{mount="/"} 2e9
# TYPE app_disk_size_bytes gauge
app_disk_size_bytes{mount="/"} 1e10
# TYPE app_build_info gauge
app_build_info{version="1.2"} 1
# TYPE app_queue gauge
app_queue NaN
`

func newServer(t *testing.T) *Server {
	t.Helper()
	s := New(Config{Source: &scrape.Source{File: "unused"}, Interval: time.Second, History: 10, Out: io.Discard})
	fams, err := expo.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	s.ingest(scrape.Result{Families: fams, At: time.Unix(1000, 0)})
	s.ingest(scrape.Result{Families: fams, At: time.Unix(1001, 0)})
	return s
}

func TestFrame(t *testing.T) {
	s := newServer(t)
	s.cfg.Version = "0.3.0"
	s.mu.Lock()
	b := s.frame(options{key: "x"})
	s.mu.Unlock()
	var fr Frame
	if err := json.Unmarshal(b, &fr); err != nil {
		t.Fatalf("frame is not valid JSON (NaN must encode as null): %v\n%s", err, b)
	}
	views := map[string]string{}
	for _, sec := range fr.Sections {
		for _, c := range sec.Cards {
			views[c.ID] = c.View
		}
	}
	want := map[string]string{
		"app_requests_total":   "rows",
		"app_latency_seconds":  "quantiles",
		"app_up":               "state",
		"app_disk_avail_bytes": "usage",
		"app_build_info":       "info",
		"app_queue":            "single",
	}
	for id, v := range want {
		if views[id] != v {
			t.Errorf("%s: view %q, want %q", id, views[id], v)
		}
	}
	if fr.Metrics == 0 || fr.Scrapes != 2 || len(fr.Names) == 0 || fr.Version != "0.3.0" {
		t.Errorf("header = %+v", fr)
	}
}

func TestEventsAndPanel(t *testing.T) {
	s := newServer(t)
	srv := httptest.NewServer(s.Handler(HostOnly))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events?filter=disk", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var fr Frame
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &fr); err != nil {
		t.Fatalf("event: %v: %s", err, line)
	}
	for _, sec := range fr.Sections {
		for _, c := range sec.Cards {
			if !strings.Contains(c.ID, "disk") {
				t.Errorf("filtered stream contains %s", c.ID)
			}
		}
	}

	get := func(path string) (int, string) {
		t.Helper()
		r, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	code, body := get("/api/panel?id=app_latency_seconds")
	var d Detail
	if code != http.StatusOK || json.Unmarshal([]byte(body), &d) != nil {
		t.Fatalf("panel: %d %s", code, body)
	}
	if d.Kind != "histogram" || len(d.Series) != 1 || len(d.Series[0].Buckets) == 0 || d.Series[0].Values["p99"] == nil {
		t.Errorf("detail = %+v", d)
	}
	if code, _ := get("/api/panel?id=nope"); code != http.StatusNotFound {
		t.Errorf("unknown panel: %d", code)
	}
	for _, asset := range []string{"/", "/app.js", "/style.css", "/uPlot.iife.min.js"} {
		if code, _ := get(asset); code != http.StatusOK {
			t.Errorf("%s: %d", asset, code)
		}
	}
}

// A page on another origin that resolves its name to 127.0.0.1 (DNS
// rebinding) must not read the metrics.
func TestHostCheck(t *testing.T) {
	h := newServer(t).Handler(HostOnly)
	for host, want := range map[string]int{
		"localhost:9090":   http.StatusOK,
		"127.0.0.1:9090":   http.StatusOK,
		"[::1]:9090":       http.StatusOK,
		"evil.example:80":  http.StatusForbidden,
		"192.168.1.5:9090": http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/panel?id=app_up", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("host %s: %d, want %d", host, rec.Code, want)
		}
	}
}
