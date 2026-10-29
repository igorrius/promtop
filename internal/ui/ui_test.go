package ui

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

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
	// The coloured variant draws the same glyphs.
	th := newTheme(true)
	if got := ansi.Strip(th.spark(view.KindGauge, []float64{0, 1, 2, 3, math.NaN()}, 6, 1, 0, 3)[0]); got != " ▁▃▆█ " {
		t.Errorf("spark = %q", got)
	}
}

func TestBrailleDims(t *testing.T) {
	rows := brailleText(braille([]float64{1, 2, 3, 2, 1}, 10, 3, 1, 3))
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	for _, r := range rows {
		if n := len([]rune(r)); n != 10 {
			t.Errorf("width = %d", n)
		}
	}
}

func TestFit(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abc…"},
		{"日本語テキスト", 7, "日本語…"},
		{"日本語", 5, "日本…"},
	}
	for _, c := range cases {
		if got := fit(c.in, c.w); got != c.want || vlen(got) != c.w {
			t.Errorf("fit(%q, %d) = %q (width %d), want %q", c.in, c.w, got, vlen(got), c.want)
		}
	}
	if got := fitLeft("http_request_duration_seconds", 10); got != "…n_seconds" {
		t.Errorf("fitLeft = %q", got)
	}
}

const gridInput = `# TYPE demo_requests_total counter
demo_requests_total{path="/a",code="200"} 1
demo_requests_total{path="/b",code="500"} 2
demo_requests_total{path="/検索",code="200"} 3
demo_requests_total{path="/🚀/launch",code="200"} 4
# TYPE demo_latency_seconds histogram
demo_latency_seconds_bucket{le="0.1"} 1
demo_latency_seconds_bucket{le="+Inf"} 2
demo_latency_seconds_sum 0.3
demo_latency_seconds_count 2
# TYPE demo_up gauge
demo_up{instance="a"} 1
demo_up{instance="ｂ全角"} 0
# TYPE demo_disk_avail_bytes gauge
demo_disk_avail_bytes{mount="/"} 2e9
demo_disk_avail_bytes{mount="/データ"} 9e9
# TYPE demo_disk_size_bytes gauge
demo_disk_size_bytes{mount="/"} 1e10
demo_disk_size_bytes{mount="/データ"} 1e10
# TYPE app_info gauge
app_info{version="1.0",name="サービス"} 1
# TYPE go_goroutines gauge
go_goroutines 12
# TYPE process_start_time_seconds gauge
process_start_time_seconds 1.7e9
# TYPE a_very_long_metric_name_that_will_never_fit_in_a_panel_title_bytes gauge
a_very_long_metric_name_that_will_never_fit_in_a_panel_title_bytes 123456789
`

func gridStore(t *testing.T) *store.Store {
	t.Helper()
	fams, err := expo.Parse(strings.NewReader(gridInput))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(20)
	st.Ingest(fams, time.Unix(1000, 0))
	st.Ingest(fams, time.Unix(1002, 0))
	return st
}

// Every rendered grid line must fit the terminal width exactly, wide
// characters in labels included, otherwise borders do not line up.
func TestGridWidths(t *testing.T) {
	st := gridStore(t)
	for _, dark := range []bool{true, false} {
		th := newTheme(dark)
		for _, w := range []int{30, 41, 80, 120, 133, 200} {
			lay := th.buildGrid(view.Build(st, view.Options{}), w)
			lines := th.render(lay, 0, len(lay.lines), 0, time.Now())
			for i, l := range lines {
				n := vlen(l)
				if r := lay.lines[i].row; r >= 0 {
					last := lay.cells[lay.rows[r][len(lay.rows[r])-1]]
					if want := last.x + last.w; n != want {
						t.Errorf("w=%d line %d has width %d, want %d: %q", w, i, n, want, ansi.Strip(l))
					}
				}
				if n > w {
					t.Errorf("w=%d line %d has width %d: %q", w, i, n, ansi.Strip(l))
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
	if strings.Contains(got, "\x1b[") {
		t.Errorf("colour disabled but output has escapes")
	}
	for _, want := range []string{"pape", "Go runtime", "goroutine_count", "365", "schedule_duration_seconds", "p99", "428MiB"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "otel_scope") {
		t.Errorf("otel scope labels should be hidden:\n%s", got)
	}
}

// newTestApp returns an app that has ingested the grid fixture twice and
// knows its window size.
func newTestApp(t *testing.T, w, h int) *App {
	t.Helper()
	a := New(Config{Source: &scrape.Source{File: "unused"}, Interval: time.Second, History: 20})
	fams, err := expo.Parse(strings.NewReader(gridInput))
	if err != nil {
		t.Fatal(err)
	}
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	a.Update(scrapeMsg{Families: fams, At: time.Unix(1000, 0)})
	a.Update(scrapeMsg{Families: fams, At: time.Unix(1002, 0)})
	return a
}

func press(a *App, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "right":
			msg = tea.KeyPressMsg{Code: tea.KeyRight}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		a.Update(msg)
	}
}

func TestKeys(t *testing.T) {
	a := newTestApp(t, 120, 40)
	if len(a.lay.cells) < 4 {
		t.Fatalf("cells = %d", len(a.lay.cells))
	}
	first := a.selName
	press(a, "right")
	if a.sel != 1 || a.selName == first {
		t.Errorf("right: sel = %d (%s)", a.sel, a.selName)
	}
	press(a, "down", "up")
	if a.sel != 1 {
		t.Errorf("down/up: sel = %d", a.sel)
	}
	press(a, "enter")
	if a.detail == nil || a.detail.Name() != a.selName {
		t.Fatalf("enter did not open %s", a.selName)
	}
	press(a, "esc")
	if a.detail != nil {
		t.Error("esc did not close details")
	}
	press(a, "]")
	if a.lay.cells[a.sel].panel.Name() == first {
		t.Error("] did not move to the next section")
	}

	press(a, "/", "u", "p")
	if !a.filtering || a.filterSrc != "up" {
		t.Fatalf("filter = %q (editing %v)", a.filterSrc, a.filtering)
	}
	for _, c := range a.lay.cells {
		if !strings.Contains(c.panel.Name(), "up") {
			t.Errorf("filtered layout shows %s", c.panel.Name())
		}
	}
	press(a, "enter")
	if a.filtering || a.filter == nil {
		t.Error("enter should keep the filter and stop editing")
	}
	press(a, "c")
	if a.filter != nil {
		t.Error("c should clear the filter")
	}
	press(a, "a")
	if !a.showAll {
		t.Error("a should toggle show-all")
	}
}

func TestMouse(t *testing.T) {
	a := newTestApp(t, 120, 40)
	c := a.lay.cells[1]
	y := c.line - a.scroll + 1 + 2 // inside the panel, below the header line
	a.Update(tea.MouseClickMsg{X: c.x + 3, Y: y, Button: tea.MouseLeft})
	if a.sel != 1 {
		t.Fatalf("click selected %d, want 1", a.sel)
	}
	a.Update(tea.MouseClickMsg{X: c.x + 3, Y: y, Button: tea.MouseLeft})
	if a.detail == nil {
		t.Fatal("second click should open details")
	}
	a.Update(tea.MouseClickMsg{X: 1, Y: 5, Button: tea.MouseRight})
	if a.detail != nil {
		t.Error("right click should close details")
	}
	a.Update(tea.MouseWheelMsg{X: 1, Y: 5, Button: tea.MouseWheelDown})
	if a.lay.cells[a.sel].row != a.lay.cells[1].row+1 {
		t.Errorf("wheel down: sel row %d", a.lay.cells[a.sel].row)
	}
}

// Every screen fills the window exactly: grid, details of every panel, help.
func TestFrames(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {60, 20}, {200, 60}} {
		a := newTestApp(t, size[0], size[1])
		check := func(what string) {
			t.Helper()
			fr := a.frame()
			if len(fr) != size[1] {
				t.Errorf("%dx%d %s: %d lines", size[0], size[1], what, len(fr))
			}
			for i, l := range fr {
				if n := vlen(l); n > size[0] {
					t.Errorf("%dx%d %s: line %d width %d: %q", size[0], size[1], what, i, n, ansi.Strip(l))
				}
			}
		}
		check("grid")
		for i := range a.lay.cells {
			a.open(i)
			check("detail " + a.detail.Name())
			press(a, "down", "right")
			check("detail moved " + a.detail.Name())
			a.detail = nil
		}
		press(a, "?")
		check("help")
	}
}

// BenchmarkFrame measures one frame of a large exporter: PROMTOP_BENCH names
// an exposition file (node_exporter output, say); a synthetic one is used
// otherwise.
func BenchmarkFrame(b *testing.B) {
	in := benchInput(b)
	fams, err := expo.Parse(strings.NewReader(in))
	if err != nil {
		b.Fatal(err)
	}
	a := New(Config{Source: &scrape.Source{File: "unused"}, Interval: time.Second, History: 300})
	a.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	for i := range 3 {
		a.Update(scrapeMsg{Families: fams, At: time.Unix(int64(1000+i), 0)})
	}
	b.Logf("%d families, %d panels", len(fams), len(a.lay.cells))
	b.Run("scrape+frame", func(b *testing.B) {
		for i := range b.N {
			a.Update(scrapeMsg{Families: fams, At: time.Unix(int64(2000+i), 0)})
			a.View()
		}
	})
	b.Run("key+frame", func(b *testing.B) {
		for i := range b.N {
			if i%2 == 0 {
				press(a, "down")
			} else {
				press(a, "up")
			}
			a.View()
		}
	})
}

func benchInput(b *testing.B) string {
	if p := os.Getenv("PROMTOP_BENCH"); p != "" {
		data, err := os.ReadFile(p)
		if err != nil {
			b.Fatal(err)
		}
		return string(data)
	}
	var sb strings.Builder
	for i := range 1000 {
		fmt.Fprintf(&sb, "# TYPE app_sub%d_metric%d_total counter\n", i%20, i)
		for j := range 3 {
			fmt.Fprintf(&sb, "app_sub%d_metric%d_total{dev=\"d%d\"} %d\n", i%20, i, j, i*j)
		}
	}
	return sb.String()
}

// The footer lists every short-help entry at full width.
func TestFooter(t *testing.T) {
	a := newTestApp(t, 160, 40)
	got := ansi.Strip(a.footer())
	for _, want := range []string{"move", "details", "filter", "section", "pause", "interval", "help", "quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("footer lacks %q: %s", want, got)
		}
	}
}

// The detail chart of a non-negative series never shows a negative axis.
func TestDetailAxis(t *testing.T) {
	a := newTestApp(t, 120, 40)
	for i, c := range a.lay.cells {
		if c.panel.Name() == "demo_latency_seconds" {
			a.open(i)
		}
	}
	if a.detail == nil {
		t.Fatal("no histogram panel")
	}
	got := ansi.Strip(strings.Join(a.frame(), "\n"))
	for l := range strings.SplitSeq(got, "\n") {
		if f := strings.Fields(l); strings.Contains(l, "┤") && strings.HasPrefix(f[0], "-") {
			t.Errorf("negative axis label: %q", l)
		}
	}
	if strings.Contains(got, "histogram · histogram") {
		t.Error("type and widget repeated in the title")
	}
}

func TestQuitAnyCase(t *testing.T) {
	for _, k := range []string{"q", "Q"} {
		a := newTestApp(t, 120, 40)
		if _, cmd := a.Update(tea.KeyPressMsg{Code: []rune(k)[0], Text: k}); cmd == nil {
			t.Errorf("%s did not quit", k)
		}
		press(a, "enter", k)
		if a.detail != nil {
			t.Errorf("%s did not close details", k)
		}
	}
}

func TestWebKey(t *testing.T) {
	t.Setenv("BROWSER", "true")
	a := newTestApp(t, 120, 40)
	a.cfg.WebAddr = "127.0.0.1:0"
	a.ctx = t.Context()
	press(a, "w")
	if a.web == nil || a.webErr != nil {
		t.Fatalf("w: web = %v, err = %v", a.web, a.webErr)
	}
	defer a.web.Close()
	if !strings.Contains(a.footer(), a.webURL) {
		t.Errorf("footer does not show %s: %q", a.webURL, a.footer())
	}
	get := func(path string) string {
		t.Helper()
		res, err := http.Get(strings.TrimSuffix(a.webURL, "/") + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", path, res.StatusCode, b)
		}
		return string(b)
	}
	// Seeded with the last scrape, then fed with ours.
	if body := get("/api/panel?id=demo_requests_total"); !strings.Contains(body, "demo_requests_total") {
		t.Errorf("panel = %s", body)
	}
	srv := a.web
	press(a, "w")
	if a.web != srv {
		t.Error("second w started another server")
	}
	press(a, "+")
	if !strings.Contains(get("/api/panel?id=demo_requests_total"), fmt.Sprintf(`"interval":%g`, a.interval.Seconds())) {
		t.Error("interval change did not reach the web dashboard")
	}
}
