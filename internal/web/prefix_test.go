package web

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrefixes(t *testing.T) {
	s := New(Config{Source: newFake(t), Interval: 10 * time.Millisecond, History: 10, ShowAll: true, Filter: "disk"})
	s.Start()
	defer s.Close()
	h := s.Handler(nil)

	inMux := http.NewServeMux()
	inMux.Handle("/webmetrics/", h)
	stripped := http.NewServeMux()
	stripped.Handle("/webmetrics/", http.StripPrefix("/webmetrics", h))
	for name, m := range map[string]struct {
		h      http.Handler
		prefix string
	}{
		"root":        {h, ""},
		"mux":         {inMux, "/webmetrics"},
		"stripprefix": {stripped, "/webmetrics"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(m.h)
			defer srv.Close()
			get := func(path string) (*http.Response, string) {
				t.Helper()
				res, err := http.Get(srv.URL + m.prefix + path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = res.Body.Close() }()
				b, _ := io.ReadAll(res.Body)
				return res, string(b)
			}
			if res, _ := get("/"); res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
				t.Errorf("index: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
			}
			for _, p := range []string{"/app.js", "/style.css", "/uPlot.iife.min.js", "/api/panel?id=app_up"} {
				if res, _ := get(p); res.StatusCode != http.StatusOK {
					t.Errorf("%s: %d", p, res.StatusCode)
				}
			}
			res, body := get("/api/config")
			var cfg struct {
				All    bool   `json:"all"`
				Filter string `json:"filter"`
			}
			if res.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &cfg) != nil || !cfg.All || cfg.Filter != "disk" {
				t.Errorf("config: %d %s", res.StatusCode, body)
			}

			ev, err := http.Get(srv.URL + m.prefix + "/api/events")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ev.Body.Close() }()
			line, err := bufio.NewReader(ev.Body).ReadString('\n')
			var fr Frame
			if err != nil || json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &fr) != nil {
				t.Errorf("events: %v %q", err, line)
			}
		})
	}
}

func TestSlashRedirect(t *testing.T) {
	s := New(Config{Source: newFake(t), Interval: time.Second, History: 10})
	mux := http.NewServeMux()
	mux.Handle("/webmetrics/", s.Handler(nil))
	for name, h := range map[string]http.Handler{"mux": mux, "direct": s.Handler(nil)} {
		req := httptest.NewRequest(http.MethodGet, "/webmetrics", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		loc, err := req.URL.Parse(rec.Header().Get("Location"))
		if rec.Code/100 != 3 || err != nil || loc.Path != "/webmetrics/" {
			t.Errorf("%s: %d Location %q", name, rec.Code, rec.Header().Get("Location"))
		}
	}
}
