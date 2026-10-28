package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/scrape"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

func TestSparkline(t *testing.T) {
	rows := sparkline([]float64{0, 1, 2, 3, math.NaN()}, 6, 1, 0, 3)
	if got := rows[0]; got != " ▁▃▆█ " {
		t.Errorf("sparkline = %q", got)
	}
	tall := sparkline([]float64{0, 3}, 2, 2, 0, 3)
	if tall[0] != " █" || tall[1] != "▁█" {
		t.Errorf("tall = %q", tall)
	}
}

func TestBrailleDims(t *testing.T) {
	rows := braille([]float64{1, 2, 3, 2, 1}, 10, 3, 1, 3)
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, r := range rows {
		if n := len([]rune(r)); n != 10 {
			t.Errorf("width = %d", n)
		}
	}
}

func TestDecodeKeys(t *testing.T) {
	ks := decodeKeys([]byte("\x1b[A\x1b[B\rq\x1b\x1b[5~é\x03"))
	want := []string{"up", "down", "enter", "", "esc", "pgup", "", "ctrl-c"}
	if len(ks) != len(want) {
		t.Fatalf("keys = %+v", ks)
	}
	for i, k := range ks {
		if k.name != want[i] {
			t.Errorf("key %d = %q, want %q", i, k.name, want[i])
		}
	}
	if ks[3].r != 'q' || ks[6].r != 'é' {
		t.Errorf("runes = %q %q", ks[3].r, ks[6].r)
	}
}

// Every rendered grid line must have exactly the terminal width (or less for
// section headers), otherwise the layout breaks.
func TestGridWidths(t *testing.T) {
	in := `# TYPE demo_requests_total counter
demo_requests_total{path="/a",code="200"} 1
demo_requests_total{path="/b",code="500"} 2
# TYPE demo_latency_seconds histogram
demo_latency_seconds_bucket{le="0.1"} 1
demo_latency_seconds_bucket{le="+Inf"} 2
demo_latency_seconds_sum 0.3
demo_latency_seconds_count 2
# TYPE go_goroutines gauge
go_goroutines 12
# TYPE process_start_time_seconds gauge
process_start_time_seconds 1.7e9
# TYPE a_very_long_metric_name_that_will_never_fit_in_a_panel_title_bytes gauge
a_very_long_metric_name_that_will_never_fit_in_a_panel_title_bytes 123456789
`
	fams, err := expo.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(20)
	st.Ingest(fams, time.Unix(1000, 0))
	st.Ingest(fams, time.Unix(1002, 0))
	for _, w := range []int{30, 41, 80, 120, 133, 200} {
		lay := buildGrid(view.Build(st, view.Options{}), w, 0, time.Now(), style{on: true})
		for i, l := range lay.lines {
			if n := vlen(l); n > w {
				t.Errorf("w=%d line %d has width %d: %q", w, i, n, l)
			}
		}
		// All lines of a panel row share one width (borders line up).
		for _, c := range lay.cells {
			want := vlen(lay.lines[c.line])
			for l := c.line; l < c.line+PanelHeight; l++ {
				if got := vlen(lay.lines[l]); got != want {
					t.Errorf("w=%d line %d width %d, want %d: %q", w, l, got, want, lay.lines[l])
				}
			}
		}
	}
}

func TestRunOnceFixture(t *testing.T) {
	var out strings.Builder
	app := New(Config{
		Source:   &scrape.Source{File: "../expo/testdata/otel_go_runtime.prom"},
		Interval: time.Second, History: 10, Width: 100, Out: &out,
	})
	if err := app.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"pape", "Go runtime", "goroutine_count", "365", "schedule_duration_seconds", "p99", "428MiB"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "otel_scope") {
		t.Errorf("otel scope labels should be hidden:\n%s", got)
	}
}
