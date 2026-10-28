// Package ui implements the promtop terminal interface without third-party
// dependencies: raw ANSI output, termios raw mode and SIGWINCH handling.
package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/igorrius/promtop/internal/scrape"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

// Config configures the application.
type Config struct {
	Source   *scrape.Source
	Interval time.Duration
	History  int
	Color    bool
	ShowAll  bool
	Filter   string
	Width    int // --once: render width (0 = terminal or 120)
	Out      io.Writer
}

type App struct {
	cfg      Config
	st       *store.Store
	sty      style
	w, h     int
	interval time.Duration

	sel     int
	selName string
	scroll  int
	detail  *view.Panel
	ds      detailState

	filterEdit bool
	filterBuf  string
	filter     *regexp.Regexp
	filterErr  string
	showAll    bool
	paused     bool
	help       bool

	lastErr  error
	lastDur  time.Duration
	lastAt   time.Time
	inflight bool
}

func New(cfg Config) *App {
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	a := &App{
		cfg:      cfg,
		st:       store.New(cfg.History),
		sty:      style{on: cfg.Color},
		interval: cfg.Interval,
		showAll:  cfg.ShowAll,
	}
	a.setFilter(cfg.Filter)
	return a
}

func (a *App) setFilter(s string) {
	a.filterBuf = s
	a.filterErr = ""
	if s == "" {
		a.filter = nil
		return
	}
	re, err := regexp.Compile("(?i)" + s)
	if err != nil {
		re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(s))
	}
	a.filter = re
}

func (a *App) sections() []*view.Section {
	return view.Build(a.st, view.Options{ShowAll: a.showAll, Filter: a.filter})
}

func (a *App) ingest(r scrape.Result) {
	a.lastDur, a.lastAt = r.Duration, r.At
	if r.Err != nil {
		a.lastErr = r.Err
		return
	}
	a.lastErr = nil
	a.st.Ingest(r.Families, r.At)
}

// RunOnce scrapes twice (so rates can be computed) and prints one frame.
func (a *App) RunOnce(ctx context.Context) error {
	a.ingest(a.cfg.Source.Scrape(ctx))
	if a.lastErr != nil {
		return a.lastErr
	}
	if !a.cfg.Source.Static() {
		select {
		case <-time.After(a.interval):
		case <-ctx.Done():
			return ctx.Err()
		}
		a.ingest(a.cfg.Source.Scrape(ctx))
		if a.lastErr != nil {
			return a.lastErr
		}
	}
	w := a.cfg.Width
	if w <= 0 {
		if tw, _, ok := termSize(int(os.Stdout.Fd())); ok {
			w = tw
		} else {
			w = 120
		}
	}
	a.w = w
	lay := buildGrid(a.sections(), w, -1, time.Now(), a.sty)
	bw := bufio.NewWriter(a.cfg.Out)
	fmt.Fprintln(bw, a.header())
	for _, l := range lay.lines {
		fmt.Fprintln(bw, l)
	}
	return bw.Flush()
}

type key struct {
	name string // "up", "down", "enter", ... or "" for a rune
	r    rune
}

// Run starts the interactive UI and blocks until the user quits.
func (a *App) Run(ctx context.Context) error {
	in := int(os.Stdin.Fd())
	out := int(os.Stdout.Fd())
	if !isTerminal(out) {
		return fmt.Errorf("stdout is not a terminal; use --once for a one-shot snapshot")
	}
	restore, err := makeRaw(in)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	w := bufio.NewWriterSize(a.cfg.Out, 1<<16)
	fmt.Fprint(w, "\x1b[?1049h\x1b[?25l")
	w.Flush()
	defer func() {
		fmt.Fprint(w, "\x1b[0m\x1b[?25h\x1b[?1049l")
		w.Flush()
		restore()
	}()

	a.resize()
	keys := make(chan key, 32)
	if interactiveSupported {
		go readKeys(os.Stdin, keys)
	}
	winch := make(chan os.Signal, 1)
	notifyResize(winch)

	results := make(chan scrape.Result, 1)
	timer := time.NewTimer(0)
	defer timer.Stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a.draw(w)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			if !a.paused && !a.inflight {
				a.inflight = true
				go func() { results <- a.cfg.Source.Scrape(ctx) }()
			}
			timer.Reset(a.interval)
		case r := <-results:
			a.inflight = false
			a.ingest(r)
			a.draw(w)
		case k, ok := <-keys:
			if !ok {
				return nil
			}
			if a.handleKey(k) {
				return nil
			}
			a.draw(w)
		case <-winch:
			a.resize()
			a.draw(w)
		}
	}
}

func (a *App) resize() {
	if w, h, ok := termSize(int(os.Stdout.Fd())); ok {
		a.w, a.h = w, h
	} else {
		a.w, a.h = 120, 40
	}
}

// handleKey updates state; it returns true to quit.
func (a *App) handleKey(k key) bool {
	if a.filterEdit {
		switch k.name {
		case "enter":
			a.filterEdit = false
		case "esc":
			a.filterEdit = false
			a.setFilter("")
		case "backspace":
			if r := []rune(a.filterBuf); len(r) > 0 {
				a.setFilter(string(r[:len(r)-1]))
			}
		case "ctrl-c":
			return true
		case "":
			a.setFilter(a.filterBuf + string(k.r))
		}
		a.sel, a.scroll = 0, 0
		return false
	}
	if a.help {
		a.help = false
		return k.name == "ctrl-c"
	}
	if k.name == "" {
		switch k.r {
		case 'q':
			if a.detail != nil {
				a.detail = nil
				return false
			}
			return true
		case 'k':
			k.name = "up"
		case 'j':
			k.name = "down"
		case 'h':
			k.name = "left"
		case 'l':
			k.name = "right"
		case 'g':
			k.name = "home"
		case 'G':
			k.name = "end"
		case '/':
			a.filterEdit = true
			return false
		case 'p', ' ':
			a.paused = !a.paused
			return false
		case 'a':
			a.showAll = !a.showAll
			return false
		case '+', '=':
			a.interval = nextInterval(a.interval, 1)
			return false
		case '-', '_':
			a.interval = nextInterval(a.interval, -1)
			return false
		case '?':
			a.help = true
			return false
		case 'c':
			a.setFilter("")
			return false
		}
	}
	switch k.name {
	case "ctrl-c":
		return true
	case "esc":
		if a.detail != nil {
			a.detail = nil
		} else if a.filter != nil {
			a.setFilter("")
		}
		return false
	}
	if a.detail != nil {
		return a.detailKey(k)
	}
	lay := buildGrid(a.sections(), a.w, a.sel, time.Now(), style{})
	switch k.name {
	case "up":
		a.sel = lay.move(a.sel, -1, 0)
	case "down":
		a.sel = lay.move(a.sel, 1, 0)
	case "left":
		a.sel = lay.move(a.sel, 0, -1)
	case "right", "tab":
		a.sel = lay.move(a.sel, 0, 1)
	case "shift-tab":
		a.sel = lay.move(a.sel, 0, -1)
	case "pgdn":
		a.sel = lay.move(a.sel, max(1, (a.h-2)/PanelHeight), 0)
	case "pgup":
		a.sel = lay.move(a.sel, -max(1, (a.h-2)/PanelHeight), 0)
	case "home":
		a.sel = 0
	case "end":
		a.sel = max(0, len(lay.cells)-1)
	case "enter":
		if a.sel >= 0 && a.sel < len(lay.cells) {
			a.detail = lay.cells[a.sel].panel
			a.ds = detailState{}
		}
	}
	if a.sel >= 0 && a.sel < len(lay.cells) {
		a.selName = lay.cells[a.sel].panel.Name()
	}
	return false
}

func (a *App) detailKey(k key) bool {
	switch k.name {
	case "up":
		a.ds.series--
	case "down":
		a.ds.series++
	case "left", "shift-tab":
		a.ds.line--
	case "right", "tab":
		a.ds.line++
	case "home":
		a.ds.series = 0
	case "end":
		a.ds.series = 1 << 30
	case "backspace":
		a.detail = nil
	}
	if a.ds.series < 0 {
		a.ds.series = 0
	}
	if a.ds.line < 0 {
		a.ds.line = 0
	}
	return false
}

var intervals = []time.Duration{
	250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second,
	5 * time.Second, 10 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute,
}

func nextInterval(cur time.Duration, dir int) time.Duration {
	if dir > 0 {
		for _, iv := range intervals {
			if iv > cur {
				return iv
			}
		}
		return cur
	}
	for i := len(intervals) - 1; i >= 0; i-- {
		if intervals[i] < cur {
			return intervals[i]
		}
	}
	return cur
}

func (a *App) header() string {
	s := a.sty
	name := " promtop "
	target := view.Target(a.st)
	left := s.c("1;48;5;45;38;5;16", name) + " "
	if target != "" {
		left += s.c(cTitle, target) + s.c(cDim, " · ")
	}
	left += s.c(cDim, a.cfg.Source.String())
	state := s.c(cOK, "● ") + s.c(cDim, a.interval.String())
	if a.paused {
		state = s.c("1;38;5;221", "❚❚ paused")
	}
	if a.lastErr != nil {
		state = s.c(cErr, "● ") + s.c(cDim, a.interval.String())
	}
	nf, ns := 0, 0
	for _, f := range a.st.Families {
		if l := len(f.Live()); l > 0 {
			nf++
			ns += l
		}
	}
	right := state + s.c(cDim, fmt.Sprintf("  %d metrics · %d series · %s · %s",
		nf, ns, a.lastDur.Round(time.Millisecond), a.lastAt.Format("15:04:05")))
	gap := a.w - vlen(left) - vlen(right) - 1
	if gap < 1 {
		// Narrow terminal: drop the right-hand stats.
		return padTo(left, a.w)
	}
	return left + strings.Repeat(" ", gap) + right + " "
}

func (a *App) footer() string {
	s := a.sty
	if a.lastErr != nil {
		return " " + s.c(cErr, fit("scrape failed: "+a.lastErr.Error(), a.w-2))
	}
	if a.filterEdit {
		return " " + s.c(cKey, "/") + a.filterBuf + s.c(cKey, "▏") + s.c(cDim, "  enter apply · esc clear")
	}
	k := func(key, desc string) string { return s.c(cKey, key) + s.c(cDim, " "+desc+"  ") }
	var parts []string
	if a.detail != nil {
		parts = []string{k("↑↓", "series"), k("←→/tab", "line"), k("esc", "back"), k("p", "pause"), k("+/-", "interval"), k("q", "back")}
	} else {
		all := "all"
		if a.showAll {
			all = "curated"
		}
		parts = []string{k("←↑↓→", "move"), k("enter", "details"), k("/", "filter"), k("a", all), k("p", "pause"), k("+/-", "interval"), k("?", "help"), k("q", "quit")}
		if a.filter != nil {
			parts = append([]string{s.c("1;38;5;221", "filter: "+a.filterBuf) + "  " + k("c", "clear")}, parts...)
		}
	}
	return " " + strings.Join(parts, "")
}

var helpText = []string{
	"promtop — live terminal view of a Prometheus /metrics endpoint",
	"",
	"Widgets are chosen from the metric type and name:",
	"  counter     → per-second rate (bytes/s, CPU % for *_seconds_total)",
	"  gauge       → current value, min/max and history",
	"  histogram   → p50/p90/p99 from bucket deltas, rate and average",
	"  summary     → client-side quantiles, rate and average",
	"  *_info      → label table;  *_timestamp / start_time → age / uptime",
	"  open/max    → usage bar (e.g. process_open_fds vs process_max_fds)",
	"",
	"Keys",
	"  ←↑↓→ hjkl   move between panels      enter   open details",
	"  /           filter by regex           c       clear filter",
	"  a           show all / curated        p space pause",
	"  + -         change scrape interval    g G     first / last",
	"  esc         back                      q       quit",
	"",
	"Details: ↑↓ choose series, ←→ or tab choose line (p99, rate, avg …)",
	"",
	"press any key",
}

// frame renders the whole screen.
func (a *App) frame() []string {
	lines := []string{a.header()}
	body := a.h - 2
	switch {
	case a.help:
		for _, l := range helpText {
			lines = append(lines, "  "+l)
		}
	case a.detail != nil:
		// Refresh the panel so it reflects the current store.
		p := view.Classify(a.detail.Family, a.st)
		lines = append(lines, renderDetail(p, &a.ds, a.interval.String(), a.w, body, a.sty)...)
	default:
		secs := a.sections()
		lay := buildGrid(secs, a.w, -1, time.Now(), style{})
		// Keep the selection on the same metric across rebuilds.
		if a.selName != "" {
			for i, c := range lay.cells {
				if c.panel.Name() == a.selName {
					a.sel = i
					break
				}
			}
		}
		a.sel = clamp(a.sel, 0, len(lay.cells)-1)
		lay = buildGrid(secs, a.w, a.sel, time.Now(), a.sty)
		if len(lay.cells) > 0 {
			c := lay.cells[a.sel]
			a.selName = c.panel.Name()
			top := c.line
			// Show the section header above the first row of a section.
			if top > 0 && !strings.Contains(lay.lines[top-1], "╯") && lay.lines[top-1] != "" {
				top--
			}
			if top < a.scroll {
				a.scroll = top
			}
			if c.line+PanelHeight > a.scroll+body {
				a.scroll = c.line + PanelHeight - body
			}
		}
		a.scroll = clamp(a.scroll, 0, max(0, len(lay.lines)-body))
		if a.st.Scrapes == 0 {
			lines = append(lines, "", "  "+a.sty.c(cDim, "waiting for the first scrape…"))
		} else if len(lay.cells) == 0 {
			lines = append(lines, "", "  "+a.sty.c(cDim, "no metrics match"))
		}
		end := min(len(lay.lines), a.scroll+body)
		lines = append(lines, lay.lines[a.scroll:end]...)
	}
	for len(lines) < a.h-1 {
		lines = append(lines, "")
	}
	if len(lines) > a.h-1 {
		lines = lines[:a.h-1]
	}
	return append(lines, a.footer())
}

func (a *App) draw(w *bufio.Writer) {
	w.WriteString("\x1b[H")
	for i, l := range a.frame() {
		if i > 0 {
			w.WriteString("\r\n")
		}
		w.WriteString(l)
		w.WriteString("\x1b[0m\x1b[K")
	}
	w.WriteString("\x1b[J")
	w.Flush()
}

// readKeys decodes terminal input into key events.
func readKeys(r io.Reader, out chan<- key) {
	defer close(out)
	buf := make([]byte, 256)
	for {
		n, err := r.Read(buf)
		if err != nil {
			return
		}
		for _, k := range decodeKeys(buf[:n]) {
			out <- k
		}
	}
}

func decodeKeys(b []byte) []key {
	var keys []key
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			if i+1 < len(b) && (b[i+1] == '[' || b[i+1] == 'O') {
				j := i + 2
				for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
					j++
				}
				if j >= len(b) {
					keys = append(keys, key{name: "esc"})
					return keys
				}
				params, final := string(b[i+2:j]), b[j]
				i = j + 1
				switch final {
				case 'A':
					keys = append(keys, key{name: "up"})
				case 'B':
					keys = append(keys, key{name: "down"})
				case 'C':
					keys = append(keys, key{name: "right"})
				case 'D':
					keys = append(keys, key{name: "left"})
				case 'H':
					keys = append(keys, key{name: "home"})
				case 'F':
					keys = append(keys, key{name: "end"})
				case 'Z':
					keys = append(keys, key{name: "shift-tab"})
				case '~':
					switch params {
					case "5":
						keys = append(keys, key{name: "pgup"})
					case "6":
						keys = append(keys, key{name: "pgdn"})
					case "1", "7":
						keys = append(keys, key{name: "home"})
					case "4", "8":
						keys = append(keys, key{name: "end"})
					}
				}
				continue
			}
			keys = append(keys, key{name: "esc"})
			i++
		case c == 3:
			keys = append(keys, key{name: "ctrl-c"})
			i++
		case c == '\r' || c == '\n':
			keys = append(keys, key{name: "enter"})
			i++
		case c == 0x7f || c == 0x08:
			keys = append(keys, key{name: "backspace"})
			i++
		case c == '\t':
			keys = append(keys, key{name: "tab"})
			i++
		case c < 0x20:
			i++
		default:
			r, sz := utf8.DecodeRune(b[i:])
			keys = append(keys, key{r: r})
			i += sz
		}
	}
	return keys
}
