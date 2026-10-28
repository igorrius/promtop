package ui

import (
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

// detailState selects what the detail view plots.
type detailState struct {
	series int // index into sorted live series
	line   int // index into the series' lines
	scroll int // table scroll offset
}

// renderDetail draws the fullscreen view of one panel in w×h cells.
func (t *theme) renderDetail(p *view.Panel, ds *detailState, interval string, w, h int, now time.Time) []string {
	f := p.Family
	series := p.Rows()
	out := []string{" " + t.bold(t.title, f.Name) + "  " + t.paint(t.dim, p.Describe())}
	if f.Help != "" {
		help := lipgloss.Wrap(strings.ReplaceAll(f.Help, "\n", " "), w-2, " ")
		for i, l := range strings.Split(help, "\n") {
			if i == 2 {
				break
			}
			out = append(out, " "+t.paint(t.dim, l))
		}
	}
	if len(series) == 0 {
		return append(out, "", " "+t.paint(t.dim, "no data in the latest scrape"))
	}
	switch p.Kind {
	case view.KindInfo, view.KindTimestamp:
		return append(out, t.labelTable(p, series, ds, w, h-len(out)-1, now)...)
	case view.KindState:
		return append(out, t.stateDetail(p, series, ds, w, h-len(out)-1)...)
	}
	ds.series = clamp(ds.series, len(series)-1)
	s := series[ds.series]
	ds.line = clamp(ds.line, len(s.Lines)-1)
	ln := s.Lines[ds.line]

	lbl := view.LabelText(view.VisibleLabels(f, s), true)
	if lbl == "" {
		lbl = "{}"
	}
	sel := t.paint(t.dim, "series ") + t.bold(t.value, itoa(ds.series+1)+"/"+itoa(len(series))) + " " + fit(lbl, max(10, w/2))
	tabs := make([]string, len(s.Lines))
	for i, l := range s.Lines {
		if i == ds.line {
			tabs[i] = t.bold(t.accent, "["+l.Name+"]")
		} else {
			tabs[i] = t.paint(t.dim, l.Name)
		}
	}
	out = append(out, " "+sel+"   "+strings.Join(tabs, " "), "")

	// Bottom area: bucket distribution for histograms, usage bars, or a
	// series table.
	var bottom []string
	tableH := max(4, h/3)
	if p.Kind == view.KindHistogram {
		bottom = t.distribution(p, s, w, tableH)
	}
	if p.Kind == view.KindUsage {
		bottom = t.usageTable(p, series, ds, w, tableH)
	} else if len(series) > 1 {
		if len(bottom) > 0 {
			bottom = append(bottom, "")
		}
		bottom = append(bottom, t.seriesTable(p, series, ds, w, tableH)...)
	}

	chartH := max(3, h-len(out)-len(bottom)-2)
	const axisW = 10
	cw := w - axisW - 3
	vals := ln.Ring.Values()
	// Never put a negative value on the axis of a series that has none.
	vlo, _, _ := bounds(vals)
	zero := p.Kind == view.KindRate || ln.Name == "rate" || vlo >= 0
	lo, hi := scaleFor(vals, zero)
	for i, r := range t.brailleRows(p.Kind, vals, cw, chartH, lo, hi) {
		var axis string
		switch i {
		case 0:
			axis = p.FormatLine(ln.Name, hi)
		case chartH - 1:
			axis = p.FormatLine(ln.Name, lo)
		case chartH / 2:
			axis = p.FormatLine(ln.Name, (lo+hi)/2)
		}
		out = append(out, t.paint(t.dim, rpad(axis, axisW)+" ┤")+" "+r)
	}
	cur := p.FormatLine(ln.Name, ln.Ring.Last())
	foot := t.paint(t.dim, strings.Repeat(" ", axisW)+" └ "+itoa(len(vals))+" samples @ "+interval+" · now ") + t.bold(t.value, cur)
	if vlo, vhi, ok := bounds(vals); ok {
		foot += t.paint(t.dim, " · min "+p.FormatLine(ln.Name, vlo)+" · max "+p.FormatLine(ln.Name, vhi))
	}
	out = append(out, cut(foot, w), "")
	return append(out, bottom...)
}

func (t *theme) distribution(p *view.Panel, s *store.Series, w, rows int) []string {
	b := s.Buckets
	first, last := -1, -1
	for i, x := range b {
		if x.Count > 0 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	title := " " + t.bold(t.section, "Distribution") + t.paint(t.dim, " · observations per bucket, last interval")
	if first < 0 {
		return []string{title, " " + t.paint(t.dim, "no observations")}
	}
	b = b[first : last+1]
	rows = max(1, rows-1)
	// Merge adjacent buckets so the range fits.
	per := int(math.Ceil(float64(len(b)) / float64(rows)))
	type grp struct {
		le    float64
		count float64
	}
	var gs []grp
	maxC := 0.0
	for i := 0; i < len(b); i += per {
		g := grp{}
		for j := i; j < min(i+per, len(b)); j++ {
			g.count += b[j].Count
			g.le = b[j].LE
		}
		gs = append(gs, g)
		maxC = math.Max(maxC, g.count)
	}
	out := []string{title}
	const leW, cntW = 11, 8
	bw := w - leW - cntW - 6
	for _, g := range gs {
		le := "≤ " + p.Format(g.le)
		if math.IsInf(g.le, 1) {
			le = "+Inf"
		}
		f := g.count / maxC
		out = append(out, " "+t.paint(t.dim, rpad(le, leW))+" "+t.hbar(f, bw, t.rampAt(view.KindHistogram, 0.4+0.6*f))+" "+rpad(view.SI(g.count), cntW))
	}
	return out
}

// tableWindow keeps the selected row visible and returns the visible range.
func tableWindow(ds *detailState, n, rows int) (start, end int) {
	ds.series = clamp(ds.series, n-1)
	if ds.series < ds.scroll {
		ds.scroll = ds.series
	}
	if ds.series >= ds.scroll+rows {
		ds.scroll = ds.series - rows + 1
	}
	ds.scroll = clamp(ds.scroll, max(0, n-rows))
	return ds.scroll, min(n, ds.scroll+rows)
}

func (t *theme) mark(selected bool) string {
	if selected {
		return t.bold(t.accent, "▸ ")
	}
	return "  "
}

func (t *theme) more(n int) string {
	return t.paint(t.dim, "  … "+itoa(n)+" more (↑/↓)")
}

func (t *theme) seriesTable(p *view.Panel, series []*store.Series, ds *detailState, w, rows int) []string {
	cols := make([]string, 0, len(series[0].Lines))
	for _, l := range series[0].Lines {
		cols = append(cols, l.Name)
	}
	if p.Kind == view.KindGauge || p.Kind == view.KindRate {
		cols = []string{cols[0], "min", "max"}
	}
	const vw = 10
	lw := w - 4 - len(cols)*(vw+1)
	var hdr strings.Builder
	hdr.WriteString("  " + fit("labels", lw))
	for _, c := range cols {
		hdr.WriteString(" " + rpad(c, vw))
	}
	out := []string{t.paint(t.dim, hdr.String())}
	start, end := tableWindow(ds, len(series), max(1, rows-1))
	for i := start; i < end; i++ {
		s := series[i]
		var row strings.Builder
		row.WriteString(t.mark(i == ds.series) + fit(view.RowLabel(p.Family, s), lw))
		for _, c := range cols {
			var v string
			switch c {
			case "min", "max":
				lo, hi, ok := bounds(s.Primary().Values())
				switch {
				case !ok:
					v = "–"
				case c == "min":
					v = p.Format(lo)
				default:
					v = p.Format(hi)
				}
			default:
				v = p.FormatLine(c, s.Line(c).Last())
			}
			row.WriteString(" " + rpad(v, vw))
		}
		out = append(out, row.String())
	}
	if end < len(series) {
		out = append(out, t.more(len(series)-end))
	}
	return out
}

func (t *theme) usageTable(p *view.Panel, series []*store.Series, ds *detailState, w, rows int) []string {
	const vw = 10
	lw := min(30, w/3)
	bw := w - lw - 3*(vw+1) - 4
	hdr := "  " + fit("labels", lw) + " " + rpad("used", vw) + " " + rpad("limit", vw) + " " + rpad("%", vw)
	out := []string{t.paint(t.dim, hdr)}
	start, end := tableWindow(ds, len(series), max(1, rows-1))
	for i := start; i < end; i++ {
		s := series[i]
		used, lim := p.UsageValues(s)
		f := p.Usage(s)
		row := t.mark(i == ds.series) + fit(view.RowLabel(p.Family, s), lw) +
			" " + rpad(p.Format(used), vw) + " " + rpad(p.Format(lim), vw) +
			" " + t.bold(t.tone(view.UsageTone(f)), rpad(view.Percent(f*100), vw))
		if bw > 4 {
			row += " " + t.hbar(f, bw, t.usageAt(f))
		}
		out = append(out, row)
	}
	if end < len(series) {
		out = append(out, t.more(len(series)-end))
	}
	return out
}

// labelTable lists every series of an info or timestamp family.
func (t *theme) labelTable(p *view.Panel, series []*store.Series, ds *detailState, w, rows int, now time.Time) []string {
	out := []string{""}
	start, end := tableWindow(ds, len(series), max(1, rows-2))
	for i := start; i < end; i++ {
		s := series[i]
		val := "= " + view.SI(s.Raw)
		if p.Kind == view.KindTimestamp {
			val = p.Age(s, now) + "  " + p.Time(s).Local().Format("2006-01-02 15:04:05")
		}
		lbl := view.LabelText(view.VisibleLabels(p.Family, s), true)
		out = append(out, t.mark(i == ds.series)+fit(lbl, w-vlen(val)-5)+"  "+t.bold(t.value, val))
	}
	if end < len(series) {
		out = append(out, t.more(len(series)-end))
	}
	return out
}

// stateDetail lists every flag with its history, problems first.
func (t *theme) stateDetail(p *view.Panel, series []*store.Series, ds *detailState, w, rows int) []string {
	rows = max(1, rows-2)
	ordered := series // problems first, see view.Panel.Rows
	lw := min(40, w/3)
	out := []string{""}
	start, end := tableWindow(ds, len(ordered), rows)
	for i := start; i < end; i++ {
		s := ordered[i]
		st := t.paint(t.ok, "● up  ")
		switch {
		case p.Family.Type == expo.StateSet && s.Raw == 1:
			st = t.bold(t.accent, "◆ on  ")
		case p.Family.Type == expo.StateSet:
			st = t.paint(t.dim, "◇ off ")
		case !view.StateUp(s):
			st = t.bold(t.err, "○ down")
		}
		lbl := view.RowLabel(p.Family, s)
		if lbl == "" {
			lbl = "{}"
		}
		out = append(out, t.mark(i == ds.series)+fit(lbl, lw)+" "+st+" "+t.stateHistory(s.Primary().Values(), max(0, w-lw-12)))
	}
	if end < len(ordered) {
		out = append(out, t.more(len(ordered)-end))
	}
	return out
}
