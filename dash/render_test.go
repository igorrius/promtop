package dash

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestRenderPNG(t *testing.T) {
	d := New(metrics(nil), Interval(fast), Title("orders"))
	defer func() { _ = d.Close() }()
	var b bytes.Buffer
	if err := d.Render(t.Context(), &b, RenderOptions{Format: FormatPNG}); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("not a PNG: % x", b.Bytes()[:min(8, b.Len())])
	}
}

func TestRenderSummaryNotable(t *testing.T) {
	d := New(metrics(nil), Interval(fast))
	defer func() { _ = d.Close() }()
	var b bytes.Buffer
	if err := d.Render(t.Context(), &b, RenderOptions{Format: FormatSummary, Notable: true, Metrics: []string{"app_*"}}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil || doc.Schema != "promtop.summary/v1" {
		t.Fatalf("summary %s: %v", b.String(), err)
	}
}

// Render needs no scrape loop to have run: with no history it reads the
// metrics once itself.
func TestRenderBeforeFirstScrape(t *testing.T) {
	var calls atomic.Int64
	d := New(metrics(&calls), Interval(time.Hour))
	defer func() { _ = d.Close() }()
	for calls.Load() == 0 { // the loop's own first scrape; it waits an hour for the next
		time.Sleep(time.Millisecond)
	}
	var b bytes.Buffer
	if err := d.Render(t.Context(), &b, RenderOptions{Format: FormatSummary}); err != nil {
		t.Fatal(err)
	}
	if b.Len() == 0 {
		t.Fatal("empty summary")
	}
	blocked := New(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }),
		Interval(time.Hour))
	defer func() { _ = blocked.Close() }()
	if err := blocked.Render(t.Context(), &b, RenderOptions{Format: FormatPNG}); err == nil {
		t.Fatal("rendered with no metrics read")
	}
}

func TestRenderClosed(t *testing.T) {
	d := New(metrics(nil), Interval(fast))
	_ = d.Close()
	if err := d.Render(t.Context(), &bytes.Buffer{}, RenderOptions{Format: FormatPNG}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Render after Close = %v, want ErrClosed", err)
	}
	d2 := New(metrics(nil), Interval(fast))
	defer func() { _ = d2.Close() }()
	if err := d2.Render(t.Context(), &bytes.Buffer{}, RenderOptions{Format: "gif"}); err == nil {
		t.Fatal("an unknown format rendered")
	}
}
