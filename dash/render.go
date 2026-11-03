package dash

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// RenderOptions choose what Render draws.
type RenderOptions struct {
	Format  Format
	Metrics []string // name globs, as `promtop render -m`; none draws the whole dashboard
	Title   string   // the picture's header; default the dashboard's Title
	Scale   float64  // PNG scale; 0 means 2, which suits phones
	Notable bool     // summary: only what stands out
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
		secs := view.Build(h.Store, view.Options{Filter: names, GroupFiltered: true})
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
