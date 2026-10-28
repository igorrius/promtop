package ui

import (
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

// Snapshot is a static frame for promtop render: a header, then either the
// grid of Sections or the details of each panel in Details.
type Snapshot struct {
	Store    *store.Store
	Sections []*view.Section
	Details  []*view.Panel
	Title    string // replaces "promtop" in the header pill
	Source   string
	Interval time.Duration
	At       time.Time // latest scrape
	Width    int       // cells
	Height   int       // chart rows of each detail view
	Dark     bool
	Now      time.Time
}

// Palette is the background and default text colour a snapshot assumes.
type Palette struct{ Background, Foreground color.Color }

// Palette returns the colours of the snapshot's theme.
func (s Snapshot) Palette() Palette {
	t := newTheme(s.Dark)
	return Palette{Background: t.bg, Foreground: t.text}
}

// Lines renders the snapshot as ANSI-styled lines of at most Width cells.
func (s Snapshot) Lines() []string {
	t := newTheme(s.Dark)
	t.fit = true
	w := max(s.Width, 30)
	lines := []string{s.header(t, w)}
	if len(s.Details) > 0 {
		chart := max(s.Height, 4)
		for i, p := range s.Details {
			if i > 0 {
				lines = append(lines, "", t.paint(t.border, strings.Repeat("─", w)))
			}
			// The image grows to fit: a dozen rows for buckets and series.
			ds := detailState{chartRows: chart, tableRows: 12}
			body := t.renderDetail(p, &ds, s.Interval.String(), w, 0, s.Now)
			// Details pad their table area; trailing blank lines waste space.
			for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
				body = body[:len(body)-1]
			}
			lines = append(lines, body...)
		}
	} else {
		lay := t.buildGrid(s.Sections, w)
		grid := t.render(lay, 0, len(lay.lines), -1, s.Now)
		for len(grid) > 0 && grid[len(grid)-1] == "" {
			grid = grid[:len(grid)-1]
		}
		if len(grid) == 0 {
			grid = []string{"", "  " + t.paint(t.dim, "no metrics match")}
		}
		lines = append(lines, grid...)
	}
	for i, l := range lines {
		lines[i] = strings.TrimRight(cut(l, w), " ")
	}
	return lines
}

func (s Snapshot) header(t *theme, w int) string {
	name := s.Title
	if name == "" {
		name = "promtop"
	}
	pill := lipgloss.NewStyle().Background(t.accent).Foreground(t.pillFg).Bold(true).Padding(0, 1).Render(name)
	left := pill + " "
	if target := view.Target(s.Store); target != "" && s.Title == "" {
		left += t.bold(t.title, target) + t.paint(t.faint, " · ")
	}
	if s.Title == "" {
		left += t.paint(t.dim, s.Source)
	}
	right := t.paint(t.dim, s.At.Local().Format("2006-01-02 15:04:05"))
	gap := w - vlen(left) - vlen(right)
	if gap < 2 {
		return cut(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}
