package dash

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/render"
	"github.com/igorrius/promtop/internal/summary"
	"github.com/igorrius/promtop/internal/ui"
	"github.com/igorrius/promtop/internal/view"
	"github.com/igorrius/promtop/internal/web"
)

// Format is what Render writes.
type Format string

// Formats of Render.
const (
	FormatPNG     Format = "png"     // the dashboard as a picture, as `promtop render -o dash.png`
	FormatSummary Format = "summary" // the promtop.summary/v1 JSON document for a model or a script
)

// ErrClosed is returned by Render after Close.
var ErrClosed = errors.New("promtop/dash: dashboard closed")

// ErrUnknownSection is a Sections name no section of the dashboard has.
var ErrUnknownSection = errors.New("promtop/dash: no such section")

// Section is one of the dashboard's sections, as Sections lists them.
type Section struct {
	Key   string // e.g. "go", "process", or a metric prefix such as "orders"
	Title string // e.g. "Go runtime"
}

// RenderOptions choose what Render draws.
type RenderOptions struct {
	Format  Format
	Metrics []string // name globs, as `promtop render -m`; none draws the whole dashboard
	Title   string   // the picture's header; default the dashboard's Title
	Scale   float64  // PNG scale; 0 means 2, which suits phones
	Notable bool     // summary: only what stands out
	// ShowAll includes what the dashboard hides by default: idle metrics
	// and runtime noise, as `promtop render -a`.
	ShowAll bool
	// Sections keeps only these sections, each named by its key, its
	// title or a word of its title ("runtime" is "Go runtime"); case does
	// not matter. A name no section has is ErrUnknownSection.
	Sections []string
}

// Render writes the dashboard's own history — what the web page shows —
// as a picture or a summary, with no network and no scrape of its own;
// with no scrape yet, it reads the metrics handler once first. A service
// can send its metrics to a chat this way without exposing /metrics.
func (d *Dashboard) Render(ctx context.Context, w io.Writer, o RenderOptions) error {
	if d.closed.Load() {
		return ErrClosed
	}
	if o.Format != FormatPNG && o.Format != FormatSummary {
		return fmt.Errorf("promtop/dash: unknown format %q, want %q or %q", o.Format, FormatPNG, FormatSummary)
	}
	names, err := view.Selector{Globs: o.Metrics}.NameFilter()
	if err != nil {
		return fmt.Errorf("promtop/dash: %w", err)
	}
	title := o.Title
	if title == "" {
		title = d.title
	}
	return d.srv.Draw(ctx, func(h web.History) error {
		secs, err := pick(view.Build(h.Store, view.Options{ShowAll: o.ShowAll, Filter: names, GroupFiltered: true}), o.Sections)
		if err != nil {
			return err
		}
		now := time.Now()
		if o.Format == FormatSummary {
			return summary.Write(w, h.Store, secs, summary.Options{
				Source: title, From: h.From, To: h.To, Interval: h.Interval, Now: now, Notable: o.Notable,
			})
		}
		scale := o.Scale
		if scale <= 0 {
			scale = 2
		}
		snap := ui.Snapshot{Store: h.Store, Sections: secs, Title: title, Source: title, Interval: h.Interval,
			At: h.To, Width: 90, Now: now}
		return render.Render(w, snap, "terminal", render.PNG, render.Options{Scale: scale})
	})
}

// Sections lists the dashboard's sections, in its order.
func (d *Dashboard) Sections(ctx context.Context) ([]Section, error) {
	if d.closed.Load() {
		return nil, ErrClosed
	}
	var out []Section
	err := d.srv.Draw(ctx, func(h web.History) error {
		for _, s := range view.Build(h.Store, view.Options{ShowAll: true}) {
			out = append(out, Section{Key: s.Key, Title: s.Title})
		}
		return nil
	})
	return out, err
}

// pick keeps the sections names name; none keeps them all.
func pick(secs []*view.Section, names []string) ([]*view.Section, error) {
	if len(names) == 0 {
		return secs, nil
	}
	var out []*view.Section
	for _, n := range names {
		found := false
		for _, s := range secs {
			if matches(s, n) && !slices.Contains(out, s) {
				out, found = append(out, s), true
			}
		}
		if !found {
			keys := make([]string, len(secs))
			for i, s := range secs {
				keys[i] = s.Key
			}
			return nil, fmt.Errorf("%w %q; there are %s", ErrUnknownSection, n, strings.Join(keys, ", "))
		}
	}
	return out, nil
}

func matches(s *view.Section, name string) bool {
	if strings.EqualFold(s.Key, name) || strings.EqualFold(s.Title, name) {
		return true
	}
	for w := range strings.FieldsSeq(s.Title) {
		if strings.EqualFold(w, name) {
			return true
		}
	}
	return false
}
