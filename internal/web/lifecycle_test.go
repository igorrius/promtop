package web

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/scrape"
)

// fakeScraper counts its calls and always returns the same families.
type fakeScraper struct {
	calls atomic.Int64
	fams  []*expo.Family
}

func (f *fakeScraper) Scrape(context.Context) scrape.Result {
	f.calls.Add(1)
	return scrape.Result{Families: f.fams, At: time.Now()}
}

func (f *fakeScraper) String() string { return "fake" }

func newFake(t *testing.T) *fakeScraper {
	t.Helper()
	fams, err := expo.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeScraper{fams: fams}
}

func TestAllowNil(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/panel?id=app_up", nil)
	req.Host = "evil.example:80"
	rec := httptest.NewRecorder()
	newServer(t).Handler(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("nil predicate: %d", rec.Code)
	}
}

func TestDeniedText(t *testing.T) {
	s := New(Config{Source: newFake(t), Interval: time.Second, History: 10, Denied: "nope"})
	rec := httptest.NewRecorder()
	s.Handler(func(*http.Request) bool { return false }).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "nope") {
		t.Errorf("denied: %d %q", rec.Code, rec.Body.String())
	}
}

func TestCloseEndsStreams(t *testing.T) {
	src := newFake(t)
	s := New(Config{Source: src, Interval: 10 * time.Millisecond, History: 10})
	s.Start()
	srv := httptest.NewServer(s.Handler(nil))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	br := bufio.NewReader(res.Body)
	if _, err := br.ReadString('\n'); err != nil {
		t.Fatal(err)
	}

	s.Close()
	ended := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, br); close(ended) }()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("event stream still open after Close")
	}
	s.Close() // idempotent

	n := src.calls.Load()
	time.Sleep(30 * time.Millisecond)
	if m := src.calls.Load(); m != n {
		t.Errorf("still scraping after Close: %d → %d", n, m)
	}
}

func TestCloseWithoutStart(t *testing.T) {
	done := make(chan struct{})
	go func() {
		New(Config{Source: newFake(t), Interval: time.Second, History: 10}).Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close without Start blocked")
	}
}
