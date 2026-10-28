package scrape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNormalizeTarget(t *testing.T) {
	cases := map[string]string{
		"8080":                       "http://localhost:8080/metrics",
		":9100":                      "http://localhost:9100/metrics",
		"db-1:9187":                  "http://db-1:9187/metrics",
		"https://svc.example/x/prom": "https://svc.example/x/prom",
		"http://localhost:2112/":     "http://localhost:2112/metrics",
		"":                           "http://localhost:8080/metrics",
	}
	for in, want := range cases {
		got, file, err := NormalizeTarget(in)
		if err != nil || file != "" || got != want {
			t.Errorf("NormalizeTarget(%q) = %q, %q, %v; want %q", in, got, file, err, want)
		}
	}
	if _, file, err := NormalizeTarget("../expo/testdata/otel_go_runtime.prom"); err != nil || file == "" {
		t.Errorf("file target: %q %v", file, err)
	}
}

func TestScrapeHTTP(t *testing.T) {
	var gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		_, _ = w.Write([]byte("# TYPE up gauge\nup 1\n"))
	}))
	defer srv.Close()
	src := &Source{URL: srv.URL + "/metrics", Header: http.Header{"Authorization": {"Bearer t"}}, Timeout: time.Second}
	res := src.Scrape(context.Background())
	if res.Err != nil || len(res.Families) != 1 {
		t.Fatalf("scrape: %+v", res)
	}
	if gotAuth != "Bearer t" || gotAccept == "" {
		t.Errorf("headers: auth=%q accept=%q", gotAuth, gotAccept)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer bad.Close()
	if res := (&Source{URL: bad.URL}).Scrape(context.Background()); res.Err == nil {
		t.Error("want error on 401")
	}
}
