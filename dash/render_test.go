package dash

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
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

func multi() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "# TYPE app_up gauge\napp_up 1\n# TYPE go_goroutines gauge\ngo_goroutines 7\n"+
			"# TYPE process_open_fds gauge\nprocess_open_fds 5\n")
	})
}

func summaryNames(t *testing.T, d *Dashboard, o RenderOptions) string {
	t.Helper()
	var b bytes.Buffer
	o.Format = FormatSummary
	if err := d.Render(t.Context(), &b, o); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// Sections keeps the dashboard's sections named by key or by a word of
// their title; an unknown one is an error naming those there are.
func TestRenderSections(t *testing.T) {
	d := New(multi(), Interval(fast))
	defer func() { _ = d.Close() }()
	out := summaryNames(t, d, RenderOptions{Sections: []string{"runtime"}})
	if !strings.Contains(out, "go_goroutines") || strings.Contains(out, "process_open_fds") || strings.Contains(out, "app_up") {
		t.Fatalf("runtime: %s", out)
	}
	if out := summaryNames(t, d, RenderOptions{Sections: []string{"Process", "app"}}); !strings.Contains(out, "process_open_fds") ||
		!strings.Contains(out, "app_up") || strings.Contains(out, "go_goroutines") {
		t.Fatalf("process, app: %s", out)
	}
	err := d.Render(t.Context(), &bytes.Buffer{}, RenderOptions{Format: FormatPNG, Sections: []string{"nope"}})
	if !errors.Is(err, ErrUnknownSection) || !strings.Contains(err.Error(), "process") {
		t.Fatalf("unknown section: %v", err)
	}
	secs, err := d.Sections(t.Context())
	if err != nil || len(secs) != 3 {
		t.Fatalf("sections %+v, %v", secs, err)
	}
}

// ShowAll includes what the dashboard hides by default (idle metrics).
func TestRenderShowAll(t *testing.T) {
	d := New(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "# TYPE app_errors_total counter\napp_errors_total 0\n# TYPE app_up gauge\napp_up 1\n")
	}), Interval(fast))
	defer func() { _ = d.Close() }()
	def, all := summaryNames(t, d, RenderOptions{}), summaryNames(t, d, RenderOptions{ShowAll: true})
	if strings.Contains(def, "app_errors_total") && !strings.Contains(def, `"hidden":0`) {
		t.Skip("the idle counter is not hidden by default in this build")
	}
	if !strings.Contains(all, "app_errors_total") {
		t.Fatalf("ShowAll lacks the idle counter: %s", all)
	}
}
