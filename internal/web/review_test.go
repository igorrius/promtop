package web

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A browser that connects before the first scrape gets an empty frame, and
// the server stays usable.
func TestEventsBeforeFirstScrape(t *testing.T) {
	s := New(Config{Source: newFake(t), Interval: time.Second, History: 10})
	srv := httptest.NewServer(s.Handler(nil))
	defer srv.Close()
	for range 2 {
		res, err := http.Get(srv.URL + "/api/events")
		if err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(res.Body).ReadString('\n')
		_ = res.Body.Close()
		var fr Frame
		if err != nil || json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &fr) != nil {
			t.Fatalf("events before first scrape: %v %q", err, line)
		}
	}
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked")
	}
}

// The slash redirect stays under the mount point whatever the last segment.
func TestRedirectStaysLocal(t *testing.T) {
	s := New(Config{Source: newFake(t), Interval: time.Second, History: 10})
	for _, target := range []string{`/webmetrics/https:%5C%5C3232235777`, `/webmetrics/%2F%2Fevil.example`, `/webmetrics/a%3Fb`} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		s.Handler(nil).ServeHTTP(rec, req)
		raw := rec.Header().Get("Location")
		loc, err := req.URL.Parse(raw)
		if rec.Code/100 == 3 && (err != nil || !strings.HasPrefix(raw, "./") || loc.Host != "" || !strings.HasPrefix(loc.Path, "/webmetrics/")) {
			t.Errorf("%s: Location %q → %v", target, raw, loc)
		}
	}
}

// http.StripPrefix with the trailing slash leaves paths without a leading
// one; chi-style mounting strips the mount point to "".
func TestStripPrefixVariants(t *testing.T) {
	s := New(Config{Source: newFake(t), Interval: time.Second, History: 10, Filter: "disk"})
	h := http.StripPrefix("/webmetrics/", s.Handler(nil))
	for path, want := range map[string]int{
		"/webmetrics/":                       http.StatusOK,
		"/webmetrics/app.js":                 http.StatusOK,
		"/webmetrics/api/config":             http.StatusOK,
		"/webmetrics/api/panel?id=app_queue": http.StatusNotFound, // routed to panel; no data yet
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want || (strings.Contains(path, "config") && !strings.Contains(rec.Body.String(), "disk")) {
			t.Errorf("StripPrefix(/webmetrics/) %s: %d %q", path, rec.Code, rec.Body.String())
		}
	}

	chi := http.StripPrefix("/webmetrics", s.Handler(nil))
	req := httptest.NewRequest(http.MethodGet, "/webmetrics", nil)
	rec := httptest.NewRecorder()
	chi.ServeHTTP(rec, req)
	loc, err := req.URL.Parse(rec.Header().Get("Location"))
	if rec.Code/100 != 3 || err != nil || loc.Path != "/webmetrics/" {
		t.Errorf("StripPrefix(/webmetrics) /webmetrics: %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
}
