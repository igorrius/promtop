package scrape

import (
	"net/http"
	"strings"
	"testing"
)

func scrapeHandler(t *testing.T, h http.HandlerFunc) Result {
	t.Helper()
	return (&Handler{H: h, UserAgent: "promtop-dash/test"}).Scrape(t.Context())
}

func TestHandlerScrape(t *testing.T) {
	var got *http.Request
	res := scrapeHandler(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte("# TYPE up gauge\nup 1\n"))
	})
	if res.Err != nil || len(res.Families) != 1 {
		t.Fatalf("scrape: %+v", res)
	}
	if got.Method != http.MethodGet || got.Header.Get("Accept") != accept ||
		got.Header.Get("Accept-Encoding") != "" || got.Header.Get("User-Agent") != "promtop-dash/test" {
		t.Errorf("request: %s %v", got.Method, got.Header)
	}
	if res.At.IsZero() {
		t.Error("At not set")
	}
}

func TestHandlerStatus(t *testing.T) {
	res := scrapeHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "500") {
		t.Errorf("err = %v", res.Err)
	}
}

func TestHandlerGzip(t *testing.T) {
	res := scrapeHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write([]byte{0x1f, 0x8b, 0, 0})
	})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "Content-Encoding") {
		t.Errorf("err = %v", res.Err)
	}
}

func TestHandlerParseError(t *testing.T) {
	res := scrapeHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not a metric line {\n"))
	})
	if res.Err == nil {
		t.Errorf("parsed garbage: %+v", res.Families)
	}
}

func TestHandlerPanic(t *testing.T) {
	res := scrapeHandler(t, func(http.ResponseWriter, *http.Request) { panic("kaboom") })
	if res.Err == nil || !strings.Contains(res.Err.Error(), "kaboom") {
		t.Errorf("err = %v", res.Err)
	}
}

func TestHandlerTooLarge(t *testing.T) {
	chunk := []byte(strings.Repeat("# filler\n", 1<<20/9))
	res := scrapeHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		for written := 0; written <= maxBody; written += len(chunk) {
			_, _ = w.Write(chunk)
		}
	})
	if res.Err == nil {
		t.Error("oversized body accepted")
	}
}

func TestHandlerString(t *testing.T) {
	if s := (&Handler{Name: "orders"}).String(); s != "orders" {
		t.Errorf("named: %q", s)
	}
	if s := (&Handler{}).String(); s != "this process" {
		t.Errorf("unnamed: %q", s)
	}
}
