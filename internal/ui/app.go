// Package ui implements the promtop terminal interface on Bubble Tea and
// Lip Gloss: the dashboard grid, the detail view and the --once snapshot.
package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/scrape"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
	webui "github.com/igorrius/promtop/internal/web"
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
	WebAddr  string // listen address of the web dashboard the w key starts
}

// App is the Bubble Tea model.
type App struct {
	cfg      Config
	ctx      context.Context
	st       *store.Store
	th       *theme
	keys     keyMap
	help     help.Model
	w, h     int
	interval time.Duration

	secs     []*view.Section
	lay      layout
	dirty    bool // sections must be rebuilt
	families int  // family count the filter suggestions were built for

	sel     int
	selName string
	scroll  int
	detail  *view.Panel
	ds      detailState

	input     textinput.Model
	filtering bool
	filter    *regexp.Regexp
	filterSrc string
	showAll   bool
	paused    bool
	showHelp  bool

	lastErr  error
	lastDur  time.Duration
	lastAt   time.Time
	inflight bool
	lastFams []*expo.Family // the last good scrape, to seed the web dashboard

	web    *webui.Server // started by the w key, fed with our scrapes
	webURL string
	webErr error
}

type (
	scrapeMsg scrape.Result
	tickMsg   struct{}
)

// New returns an app that has not scraped yet.
func New(cfg Config) *App {
	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}
	a := &App{
		cfg:      cfg,
		ctx:      context.Background(),
		st:       store.New(cfg.History),
		keys:     newKeyMap(),
		help:     help.New(),
		interval: cfg.Interval,
		showAll:  cfg.ShowAll,
		w:        120,
		h:        40,
		dirty:    true,
	}
	a.input = textinput.New()
	a.input.Prompt = "/"
	a.input.Placeholder = "regex over metric names"
	a.input.ShowSuggestions = true
	a.setTheme(true)
	a.setFilter(cfg.Filter)
	a.input.SetValue(cfg.Filter)
	return a
}

func (a *App) setTheme(dark bool) {
	a.th = newTheme(dark)
	a.help.Styles = help.DefaultStyles(dark)
	a.help.Styles.ShortKey = lipgloss.NewStyle().Foreground(a.th.accent).Bold(true)
	a.help.Styles.FullKey = a.help.Styles.ShortKey
	a.help.Styles.ShortDesc = lipgloss.NewStyle().Foreground(a.th.dim)
	a.help.Styles.FullDesc = a.help.Styles.ShortDesc
	a.help.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(a.th.faint)
	a.help.ShortSeparator = "  "
	s := textinput.DefaultStyles(dark)
	s.Focused.Prompt = lipgloss.NewStyle().Foreground(a.th.accent).Bold(true)
	s.Focused.Suggestion = lipgloss.NewStyle().Foreground(a.th.faint)
	s.Focused.Placeholder = lipgloss.NewStyle().Foreground(a.th.faint)
	a.input.SetStyles(s)
	a.dirty = true
}

func (a *App) setFilter(s string) {
	a.dirty = true
	a.filterSrc = s
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

func (a *App) ingest(r scrape.Result) {
	a.lastDur, a.lastAt = r.Duration, r.At
	if r.Err != nil {
		a.lastErr = r.Err
		return
	}
	a.lastErr = nil
	a.lastFams = r.Families
	a.st.Ingest(r.Families, r.At)
	a.dirty = true
	if n := len(a.st.Families); n != a.families {
		a.families = n
		names := make([]string, n)
		for i, f := range a.st.Families {
			names[i] = f.Name
		}
		a.input.SetSuggestions(names)
	}
}

// refresh rebuilds sections and layout when needed and keeps the selection
// on the same metric and inside the viewport.
func (a *App) refresh() {
	if a.dirty || a.lay.w != a.w {
		if a.dirty {
			a.secs = view.Build(a.st, view.Options{ShowAll: a.showAll, Filter: a.filter})
			a.dirty = false
		}
		a.lay = a.th.buildGrid(a.secs, a.w)
		if a.selName != "" {
			for i, c := range a.lay.cells {
				if c.panel.Name() == a.selName {
					a.sel = i
					break
				}
			}
		}
		if a.detail != nil {
			a.detail = a.findPanel(a.detail)
		}
	}
	a.sel = clamp(a.sel, len(a.lay.cells)-1)
	body := a.bodyHeight()
	if len(a.lay.cells) > 0 {
		c := a.lay.cells[a.sel]
		a.selName = c.panel.Name()
		if c.top < a.scroll {
			a.scroll = c.top
		}
		if c.line+PanelHeight > a.scroll+body {
			a.scroll = c.line + PanelHeight - body
		}
	}
	a.scroll = clamp(a.scroll, max(0, len(a.lay.lines)-body))
}

// findPanel returns the current version of panel p: the same name in the
// fresh layout, or a reclassification of its family.
func (a *App) findPanel(p *view.Panel) *view.Panel {
	for _, c := range a.lay.cells {
		if c.panel.Name() == p.Name() {
			return c.panel
		}
	}
	if f := a.st.Get(p.Name()); f != nil {
		if np := view.Classify(f, a.st); np != nil {
			return np
		}
	}
	return p
}

func (a *App) bodyHeight() int { return max(1, a.h-2) }

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
	a.w = a.cfg.Width
	if a.w <= 0 {
		a.w = 120
		if f, ok := a.cfg.Out.(*os.File); ok {
			if tw, _, err := term.GetSize(f.Fd()); err == nil && tw > 0 {
				a.w = tw
			}
		}
	}
	a.refresh()
	out := colorprofile.NewWriter(a.cfg.Out, os.Environ())
	if !a.cfg.Color {
		out.Profile = colorprofile.NoTTY
	}
	lines := append([]string{a.header()}, a.th.render(a.lay, 0, len(a.lay.lines), -1, time.Now())...)
	_, err := io.WriteString(out, strings.Join(lines, "\n")+"\n")
	return err
}

// Run starts the interactive UI and blocks until the user quits.
func (a *App) Run(ctx context.Context) error {
	f, ok := a.cfg.Out.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return errors.New("stdout is not a terminal; use --once for a one-shot snapshot")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // stops the web dashboard too
	a.ctx = ctx
	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(a.cfg.Out)}
	if !a.cfg.Color {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}
	_, err := tea.NewProgram(a, opts...).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil //nolint:nilerr // stopped by SIGTERM/SIGINT: a normal exit
	}
	return err
}

// Init starts scraping and asks the terminal for its background colour.
func (a *App) Init() tea.Cmd {
	return tea.Batch(a.scrape(), a.tick(), tea.RequestBackgroundColor)
}

func (a *App) scrape() tea.Cmd {
	a.inflight = true
	ctx, src := a.ctx, a.cfg.Source
	return func() tea.Msg { return scrapeMsg(src.Scrape(ctx)) }
}

func (a *App) tick() tea.Cmd {
	return tea.Tick(a.interval, func(time.Time) tea.Msg { return tickMsg{} })
}

// Update handles scrapes, timers, keys, mouse and terminal events.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		a.help.SetWidth(a.w - 2)
		a.input.SetWidth(max(10, a.w/2))
	case tea.BackgroundColorMsg:
		a.setTheme(msg.IsDark())
	case tickMsg:
		cmds := []tea.Cmd{a.tick()}
		if !a.paused && !a.inflight {
			cmds = append(cmds, a.scrape())
		}
		cmd = tea.Batch(cmds...)
	case scrapeMsg:
		a.inflight = false
		a.ingest(scrape.Result(msg))
		if a.web != nil {
			a.web.Ingest(scrape.Result(msg))
		}
	case tea.KeyPressMsg:
		cmd = a.handleKey(msg)
	case tea.MouseClickMsg:
		a.handleClick(msg.Mouse())
	case tea.MouseWheelMsg:
		a.handleWheel(msg.Mouse())
	default:
		if a.filtering {
			a.input, cmd = a.input.Update(msg)
		}
	}
	a.refresh()
	return a, cmd
}

func (a *App) handleKey(k tea.KeyPressMsg) tea.Cmd {
	km := a.keys
	if a.filtering {
		switch k.String() {
		case "enter":
			a.filtering = false
			a.input.Blur()
		case "esc":
			a.filtering = false
			a.input.Blur()
			a.input.SetValue("")
			a.setFilter("")
		case "ctrl+c":
			return tea.Quit
		default:
			var cmd tea.Cmd
			a.input, cmd = a.input.Update(k)
			if v := a.input.Value(); v != a.filterSrc {
				a.setFilter(v)
				a.sel, a.scroll = 0, 0
			}
			return cmd
		}
		return nil
	}
	if a.showHelp {
		a.showHelp = false
		if k.String() == "ctrl+c" {
			return tea.Quit
		}
		return nil
	}
	switch {
	case key.Matches(k, km.Quit):
		if a.detail != nil && k.String() != "ctrl+c" {
			a.detail = nil
			return nil
		}
		return tea.Quit
	case key.Matches(k, km.Filter):
		a.filtering = true
		a.input.CursorEnd()
		return a.input.Focus()
	case key.Matches(k, km.Pause):
		a.paused = !a.paused
	case key.Matches(k, km.All):
		a.showAll = !a.showAll
		a.dirty = true
	case key.Matches(k, km.Longer):
		a.setInterval(nextInterval(a.interval, 1))
	case key.Matches(k, km.Shorter):
		a.setInterval(nextInterval(a.interval, -1))
	case key.Matches(k, km.Web):
		a.openWeb()
	case key.Matches(k, km.Help):
		a.showHelp = true
	case key.Matches(k, km.Clear):
		a.input.SetValue("")
		a.setFilter("")
	case key.Matches(k, km.Back):
		if a.detail != nil {
			a.detail = nil
		} else if a.filter != nil {
			a.input.SetValue("")
			a.setFilter("")
		}
	case a.detail != nil:
		a.detailKey(k)
	default:
		a.gridKey(k)
	}
	return nil
}

func (a *App) setInterval(d time.Duration) {
	a.interval = d
	if a.web != nil {
		a.web.SetInterval(d)
	}
}

// openWeb starts the web dashboard on the first press and opens it in the
// browser. The page shows the scrapes this app makes from then on.
func (a *App) openWeb() {
	if a.web == nil {
		srv := webui.New(webui.Config{
			Source: a.cfg.Source, Interval: a.interval, History: a.cfg.History,
			ShowAll: a.showAll, Filter: a.filterSrc, Addr: a.cfg.WebAddr, Fed: true,
		})
		u, err := srv.Listen()
		if err != nil {
			a.webErr = err
			return
		}
		a.web, a.webURL = srv, u
		ctx := a.ctx
		go func() { _ = srv.Serve(ctx) }()
		// The latest scrape, so the page has something to show at once.
		if a.lastFams != nil {
			srv.Ingest(scrape.Result{Families: a.lastFams, At: a.lastAt, Duration: a.lastDur})
		}
	}
	a.webErr = nil
	if err := webui.OpenBrowser(a.webURL); err != nil {
		a.webErr = fmt.Errorf("no browser (%w); open %s", err, a.webURL)
	}
}

func (a *App) gridKey(k tea.KeyPressMsg) {
	km, lay := a.keys, a.lay
	page := max(1, a.bodyHeight()/PanelHeight)
	switch {
	case key.Matches(k, km.Up):
		a.sel = lay.move(a.sel, -1, 0)
	case key.Matches(k, km.Down):
		a.sel = lay.move(a.sel, 1, 0)
	case key.Matches(k, km.Left):
		a.sel = lay.move(a.sel, 0, -1)
	case key.Matches(k, km.Right):
		a.sel = lay.move(a.sel, 0, 1)
	case key.Matches(k, km.PgDown):
		a.sel = lay.move(a.sel, page, 0)
	case key.Matches(k, km.PgUp):
		a.sel = lay.move(a.sel, -page, 0)
	case key.Matches(k, km.Home):
		a.sel = 0
	case key.Matches(k, km.End):
		a.sel = max(0, len(lay.cells)-1)
	case key.Matches(k, km.NextSec):
		a.sel = lay.section(a.sel, 1)
	case key.Matches(k, km.PrevSec):
		a.sel = lay.section(a.sel, -1)
	case key.Matches(k, km.Open):
		a.open(a.sel)
	}
	if a.sel >= 0 && a.sel < len(lay.cells) {
		a.selName = lay.cells[a.sel].panel.Name()
	}
}

func (a *App) open(i int) {
	if i >= 0 && i < len(a.lay.cells) {
		a.detail = a.lay.cells[i].panel
		a.ds = detailState{}
	}
}

func (a *App) detailKey(k tea.KeyPressMsg) {
	switch k.String() {
	case "up", "k":
		a.ds.series--
	case "down", "j":
		a.ds.series++
	case "pgup":
		a.ds.series -= 10
	case "pgdown":
		a.ds.series += 10
	case "left", "h", "shift+tab":
		a.ds.line--
	case "right", "l", "tab":
		a.ds.line++
	case "home", "g":
		a.ds.series = 0
	case "end", "G":
		a.ds.series = 1 << 30
	}
	a.ds.series = max(0, a.ds.series)
	a.ds.line = max(0, a.ds.line)
}

func (a *App) handleClick(m tea.Mouse) {
	if a.detail != nil {
		if m.Button == tea.MouseRight {
			a.detail = nil
		}
		return
	}
	if m.Y < 1 || m.Y > a.bodyHeight() || m.Button != tea.MouseLeft {
		return
	}
	i := a.lay.hit(a.scroll+m.Y-1, m.X)
	switch {
	case i < 0:
	case i == a.sel:
		a.open(i)
	default:
		a.sel = i
		a.selName = a.lay.cells[i].panel.Name()
	}
}

func (a *App) handleWheel(m tea.Mouse) {
	var dir int
	switch m.Button {
	case tea.MouseWheelUp:
		dir = -1
	case tea.MouseWheelDown:
		dir = 1
	default:
		return
	}
	if a.detail != nil {
		a.ds.series = max(0, a.ds.series+dir)
		return
	}
	a.sel = a.lay.move(a.sel, dir, 0)
	if a.sel < len(a.lay.cells) {
		a.selName = a.lay.cells[a.sel].panel.Name()
	}
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
	for _, interval := range slices.Backward(intervals) {
		if interval < cur {
			return interval
		}
	}
	return cur
}

func (a *App) header() string {
	t := a.th
	pill := lipgloss.NewStyle().Background(t.accent).Foreground(t.pillFg).Bold(true).Padding(0, 1).Render("promtop")
	left := pill + " "
	if target := view.Target(a.st); target != "" {
		left += t.bold(t.title, target) + t.paint(t.faint, " · ")
	}
	left += t.paint(t.dim, a.cfg.Source.String())
	state := t.paint(t.ok, "● ") + t.paint(t.dim, a.interval.String())
	switch {
	case a.lastErr != nil:
		state = t.bold(t.err, "● error ") + t.paint(t.dim, a.interval.String())
	case a.paused:
		state = t.bold(t.warn, "❚❚ paused")
	}
	nf, ns, hidden := 0, 0, 0
	for _, f := range a.st.Families {
		if l := len(f.Live()); l > 0 {
			nf++
			ns += l
		}
	}
	for _, s := range a.secs {
		hidden += s.Hidden
	}
	stats := fmt.Sprintf("  %d metrics · %d series", nf, ns)
	if hidden > 0 && !a.showAll {
		stats += fmt.Sprintf(" · %d hidden", hidden)
	}
	stats += fmt.Sprintf(" · %s · %s", a.lastDur.Round(time.Millisecond), a.lastAt.Format("15:04:05"))
	right := state + t.paint(t.dim, stats)
	gap := a.w - vlen(left) - vlen(right) - 1
	if gap < 1 {
		// Narrow terminal: drop the right-hand stats.
		return cut(left, a.w)
	}
	return left + strings.Repeat(" ", gap) + right + " "
}

func (a *App) footer() string {
	t := a.th
	if a.filtering {
		return " " + a.input.View() + "  " + t.paint(t.faint, "enter apply · esc clear · tab complete")
	}
	if a.lastErr != nil {
		return " " + t.bold(t.err, fit("scrape failed: "+a.lastErr.Error(), a.w-2))
	}
	bindings := a.keys.gridHelp()
	prefix := ""
	switch {
	case a.webErr != nil:
		prefix = t.bold(t.err, "web: "+a.webErr.Error()) + "  "
	case a.web != nil:
		prefix = t.bold(t.ok, "web ") + t.paint(t.dim, a.webURL) + "  "
	}
	if a.detail != nil {
		bindings = a.keys.detailHelp()
	} else if a.filter != nil {
		prefix += t.bold(t.warn, "filter: "+a.input.Value()) + "  " + t.bold(t.accent, "c") + t.paint(t.dim, " clear") + "  "
	}
	a.help.SetWidth(a.w - 2 - vlen(prefix))
	return " " + prefix + a.help.ShortHelpView(bindings)
}

var legend = []string{
	"Widgets are chosen from the metric type, name and values:",
	"  counter    per-second rate (bytes/s; utilisation for *_seconds_total)",
	"  gauge      current value, min/max and history",
	"  histogram  p50/p90/p99 from bucket deltas, rate and average",
	"  summary    client-side quantiles, rate and average",
	"  usage      gauge against a sibling limit (open/max, avail/size, MemFree/MemTotal)",
	"  state      0/1 health flags (up, *_success, *_ready) and statesets",
	"  info       label table;  time  unix timestamps as age / uptime",
	"",
	"Big exporters are split into sections by name prefix; single-series metrics",
	"of a crowded section are grouped by unit. Metrics that stayed at zero, limits",
	"merged into a usage panel and runtime internals are hidden until you press a.",
}

func (a *App) helpScreen() []string {
	t := a.th
	out := []string{"", "  " + t.bold(t.title, "promtop") + t.paint(t.dim, " — live terminal view of a Prometheus /metrics endpoint"), ""}
	for _, l := range legend {
		out = append(out, "  "+t.paint(t.dim, l))
	}
	out = append(out, "")
	for l := range strings.SplitSeq(a.help.FullHelpView(a.keys.fullHelp()), "\n") {
		out = append(out, "  "+l)
	}
	return append(out, "", "  "+t.paint(t.faint, "press any key"))
}

// frame renders the whole screen.
func (a *App) frame() []string {
	t := a.th
	lines := []string{a.header()}
	body := a.bodyHeight()
	now := time.Now()
	switch {
	case a.showHelp:
		lines = append(lines, a.helpScreen()...)
	case a.detail != nil:
		lines = append(lines, t.renderDetail(a.detail, &a.ds, a.interval.String(), a.w, body, now)...)
	case a.st.Scrapes == 0 && a.lastErr == nil:
		lines = append(lines, "", "  "+t.paint(t.dim, "waiting for the first scrape…"))
	case len(a.lay.cells) == 0 && a.filter != nil:
		lines = append(lines, "", "  "+t.paint(t.dim, "no metrics match"))
	default:
		lines = append(lines, t.render(a.lay, a.scroll, a.scroll+body, a.sel, now)...)
	}
	for len(lines) < a.h-1 {
		lines = append(lines, "")
	}
	lines = lines[:a.h-1]
	for i, l := range lines {
		lines[i] = cut(l, a.w)
	}
	return append(lines, cut(a.footer(), a.w))
}

// View renders the screen.
func (a *App) View() tea.View {
	v := tea.NewView(strings.Join(a.frame(), "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "promtop · " + a.cfg.Source.String()
	return v
}
