package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/history"
	"github.com/igorrius/promtop/internal/render"
	"github.com/igorrius/promtop/internal/scrape"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/summary"
	"github.com/igorrius/promtop/internal/ui"
	"github.com/igorrius/promtop/internal/view"
)

const renderUsage = `promtop render — draw metrics once, into an image, JSON or text

Usage:
  promtop render [flags] [target]

Examples:
  promtop render -o dash.png 9100
  promtop render -m 'battery_*' -m 'inverter_*' -title Inverter -o - 192.168.1.50:9100 \
    | curl -sF chat_id=$CHAT -F photo=@- "https://api.telegram.org/bot$TOKEN/sendPhoto"
  promtop render -m node_load1 -for 1m -o load.svg 9100      one metric: a detail chart
  promtop render -m 'temp_*' -state ~/.cache/temp.state -o temp.png sensors:9100
  promtop render -format json -m 'battery_*' 9100            the data, for bots
  promtop render -format summary -for 1m 9100                numbers and what changed, for agents
  promtop render -format summary -list 9100                  which metrics there are

-format summary is a compact, versioned JSON document for AI agents and
scripts (schema promtop.summary/v1): values in base units, labels as
objects, last/avg/min/max/trend over the sampled window, and a "notable"
list of series that are down, near a limit, erroring or moving.

Without -state or -for promtop scrapes twice, -i apart, so rates have a value
but charts have only two points; -for samples longer, and -state draws the
history that "promtop record" collects between runs.

Flags:
`

const recordUsage = `promtop record — add one scrape to a history file

Usage:
  promtop record -state file [flags] [target]

Run it from cron to collect history without a Prometheus server, then draw
it with "promtop render -state file":

  */5 * * * *  promtop record -state ~/.cache/inverter.state -m 'battery_*' 192.168.1.50:9100

-m, -f and -l keep the file small by storing only the selected metrics and
series; include the limit of a usage panel (node_filesystem_size_bytes for
node_filesystem_avail_bytes) if you want its bar.

Flags:
`

func runRender(args []string) int {
	fs := flag.NewFlagSet("promtop render", flag.ContinueOnError)
	sf := addSourceFlags(fs, 2*time.Second)
	sel := addSelectFlags(fs)
	var (
		out       = fs.String("o", "-", "output file, - for stdout")
		format    = fs.String("format", "", "png, svg, json, summary, txt or ansi (default: from the -o extension, else png)")
		style     = fs.String("style", "terminal", "picture style: "+strings.Join(render.StyleNames(), ", "))
		viewMode  = fs.String("view", "auto", "grid, detail, or auto: details when one metric is selected")
		all       = fs.Bool("a", false, "include metrics hidden in curated mode (idle, merged limits, runtime internals)")
		sample    = fs.Duration("for", 0, "keep scraping this long, every -i, before drawing")
		statePath = fs.String("state", "", "history file: add this scrape to it and draw its history")
		keep      = fs.Int("keep", 300, "scrapes kept in -state")
		offline   = fs.Bool("offline", false, "with -state: draw the stored history without scraping")
		title     = fs.String("title", "", "header title instead of promtop and the target")
		theme     = fs.String("theme", "dark", "dark or light")
		width     = fs.Int("width", 0, "width in cells (default: 90 for a grid, two columns; 100 for details)")
		height    = fs.Int("height", 12, "chart height in cells of each detail view")
		scale     = fs.Float64("scale", 2, "image scale: 1 means a 15px font")
		top       = fs.Int("top", 5, "-format summary: series per metric, 0 for all")
		list      = fs.Bool("list", false, "-format summary: only names, types, units and series counts")
		notable   = fs.Bool("notable", false, "-format summary: only the notable list")
	)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), renderUsage)
		fs.PrintDefaults()
	}
	target, code := parseArgs(fs, args)
	if code >= 0 {
		return code
	}

	f := render.Format(strings.ToLower(*format))
	if f == "" {
		if f = render.FormatFor(*out); f == "" {
			f = render.PNG
		}
	}
	switch f {
	case render.PNG, render.SVG, render.JSON, render.Text, render.ANSI, formatSummary:
	default:
		return usageErr(fmt.Errorf("unknown format %q", *format))
	}
	if f != formatSummary {
		var only error
		fs.Visit(func(fl *flag.Flag) {
			if fl.Name == "top" || fl.Name == "list" || fl.Name == "notable" {
				only = fmt.Errorf("-%s needs -format summary", fl.Name)
			}
		})
		if only != nil {
			return usageErr(only)
		}
	}
	if *list && *notable {
		return usageErr(errors.New("-list and -notable do not combine"))
	}
	if *out == "-" && f.Binary() && term.IsTerminal(os.Stdout.Fd()) {
		return usageErr(errors.New("refusing to write PNG to a terminal; use -o file.png or a pipe"))
	}
	if *theme != "dark" && *theme != "light" {
		return usageErr(fmt.Errorf("unknown theme %q, want dark or light", *theme))
	}
	if *offline && *statePath == "" {
		return usageErr(errors.New("-offline needs -state"))
	}
	selector, err := sel.selector()
	if err != nil {
		return usageErr(err)
	}
	names, err := selector.NameFilter()
	if err != nil {
		return usageErr(err)
	}
	src, err := sf.source(target)
	if err != nil {
		return usageErr(err)
	}

	ctx, stop := signalContext()
	defer stop()
	st := store.New(*keep)
	interval := *sf.interval
	// raw is the history before the label matchers: what the summary's error
	// share is taken from, so -l 'code=~5..' does not make it 100%.
	var raw []history.Scrape
	fresh := 0
	if *statePath != "" {
		if raw, err = history.Load(*statePath); err != nil {
			return fail(err)
		}
	}
	if !*offline {
		// A list needs names, not rates: one scrape is enough.
		got, err := sampleScrapes(ctx, src, interval, *sample, len(raw) > 0 || *list)
		switch {
		case err != nil && len(raw) == 0:
			return fail(err)
		case err != nil:
			fmt.Fprintf(os.Stderr, "promtop: %v; drawing the stored history\n", err)
		}
		fresh = len(got)
		for _, s := range got {
			// A history file stores only the selection; a one-off render keeps
			// every family, so usage panels still find their limits.
			if *statePath != "" {
				if s.Families, err = keepSelected(s.Families, names, view.Selector{}); err != nil {
					return usageErr(err)
				}
			}
			raw = history.Append(raw, s)
		}
	}
	scrapes := make([]history.Scrape, len(raw))
	for i, s := range raw {
		if s.Families, err = selector.Apply(s.Families); err != nil {
			return usageErr(err)
		}
		scrapes[i] = s
	}
	if *statePath != "" && fresh > 0 {
		if err := history.Save(*statePath, scrapes, *keep); err != nil {
			return fail(err)
		}
	}
	if len(scrapes) == 0 {
		return fail(errors.New("no scrapes to draw"))
	}
	history.Replay(st, scrapes)
	if iv := history.Interval(scrapes); iv > 0 {
		interval = iv
	}
	if interval >= time.Second {
		interval = interval.Round(time.Second)
	} else {
		interval = interval.Round(10 * time.Millisecond)
	}

	secs := view.Build(st, view.Options{ShowAll: *all, Filter: names, GroupFiltered: true})
	var panels []*view.Panel
	for _, s := range secs {
		panels = append(panels, s.Panels...)
	}
	snap := ui.Snapshot{
		Store: st, Sections: secs, Title: *title, Source: src.String(), Interval: interval,
		At: scrapes[len(scrapes)-1].At, Width: *width, Height: *height, Dark: *theme == "dark", Now: time.Now(),
	}
	switch *viewMode {
	case "grid":
	case "detail":
		snap.Details = panels
	case "auto":
		if len(panels) == 1 {
			snap.Details = panels
		}
	default:
		return usageErr(fmt.Errorf("unknown view %q, want grid, detail or auto", *viewMode))
	}
	if len(snap.Details) > 12 {
		fmt.Fprintf(os.Stderr, "promtop: drawing details of the first 12 of %d metrics; narrow them down with -m or -f\n", len(snap.Details))
		snap.Details = snap.Details[:12]
	}
	if snap.Width <= 0 {
		snap.Width = 90
		if len(snap.Details) > 0 {
			snap.Width = 100
		}
	}
	if len(panels) == 0 && !selector.Empty() {
		fmt.Fprintln(os.Stderr, "promtop: no metric matches the selection")
	}

	var w io.Writer = os.Stdout
	if *out != "-" {
		file, err := os.Create(*out)
		if err != nil {
			return fail(err)
		}
		defer func() { _ = file.Close() }()
		w = file
	}
	if f == formatSummary {
		so := summary.Options{
			Source: src.String(), From: scrapes[0].At, To: snap.At, Interval: interval, Now: snap.Now,
			Top: *top, List: *list, Notable: *notable,
		}
		if len(selector.Labels) > 0 {
			so.Unfiltered = store.New(*keep)
			history.Replay(so.Unfiltered, raw)
		}
		err = summary.Write(w, st, secs, so)
	} else {
		err = render.Render(w, snap, *style, f, render.Options{Scale: *scale})
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

// formatSummary is the agent document of package summary; it is not a
// picture of the dashboard, so it lives next to render's formats, not in them.
const formatSummary render.Format = "summary"

func runRecord(args []string) int {
	fs := flag.NewFlagSet("promtop record", flag.ContinueOnError)
	sf := addSourceFlags(fs, 2*time.Second)
	sel := addSelectFlags(fs)
	statePath := fs.String("state", "", "history file to add the scrape to (required)")
	keep := fs.Int("keep", 300, "scrapes kept in the file")
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), recordUsage)
		fs.PrintDefaults()
	}
	target, code := parseArgs(fs, args)
	if code >= 0 {
		return code
	}
	if *statePath == "" {
		return usageErr(errors.New("record needs -state file"))
	}
	selector, err := sel.selector()
	if err != nil {
		return usageErr(err)
	}
	names, err := selector.NameFilter()
	if err != nil {
		return usageErr(err)
	}
	src, err := sf.source(target)
	if err != nil {
		return usageErr(err)
	}
	ctx, stop := signalContext()
	defer stop()
	scrapes, err := history.Load(*statePath)
	if err != nil {
		return fail(err)
	}
	r := src.Scrape(ctx)
	if r.Err != nil {
		return fail(r.Err)
	}
	fams, err := keepSelected(r.Families, names, selector)
	if err != nil {
		return usageErr(err)
	}
	if err := history.Save(*statePath, history.Append(scrapes, history.Scrape{At: r.At, Families: fams}), *keep); err != nil {
		return fail(err)
	}
	return 0
}

// sampleScrapes scrapes src: for d when d > 0, otherwise twice (once when
// history already provides the earlier sample, or the source is a file).
func sampleScrapes(ctx context.Context, src *scrape.Source, interval, d time.Duration, haveHistory bool) ([]history.Scrape, error) {
	want := 2
	if haveHistory || src.Static() {
		want = 1
	}
	var out []history.Scrape
	start := time.Now()
	for {
		r := src.Scrape(ctx)
		if r.Err != nil {
			return out, r.Err
		}
		out = append(out, history.Scrape{At: r.At, Families: r.Families})
		if src.Static() || (d > 0 && time.Since(start)+interval > d) || (d <= 0 && len(out) >= want) {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return out, nil
		case <-time.After(interval):
		}
	}
}

// keepSelected drops families the names do not select (when names are
// given) and applies the label matchers.
func keepSelected(fams []*expo.Family, names *regexp.Regexp, sel view.Selector) ([]*expo.Family, error) {
	if names != nil {
		kept := fams[:0:0]
		for _, f := range fams {
			if names.MatchString(f.Name) {
				kept = append(kept, f)
			}
		}
		fams = kept
	}
	return sel.Apply(fams)
}
