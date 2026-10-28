package ui

import (
	"math"
	"strings"

	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

// detailState selects what the detail view plots.
type detailState struct {
	series int // index into sorted live series
	line   int // index into the series' lines
	scroll int // table scroll offset
}

func detailSeries(p *view.Panel) []*store.Series {
	line := ""
	if p.Kind == view.KindHistogram || p.Kind == view.KindSummary {
		line = rowLine(p)
	}
	return sortedByPrimary(p.Family.Live(), line)
}

// renderDetail draws the fullscreen view of one panel in w×h cells.
func renderDetail(p *view.Panel, ds *detailState, interval string, w, h int, sty style) []string {
	f := p.Family
	series := detailSeries(p)
	var out []string
	unit := unitName(p)
	head := sty.c(cTitle, f.Name) + "  " + sty.c(cDim, string(f.Type)+" · "+p.Kind.String()+unit)
	out = append(out, " "+head)
	if f.Help != "" {
		out = append(out, " "+sty.c(cDim, fit(strings.ReplaceAll(f.Help, "\n", " "), w-2)))
	}
	if len(series) == 0 {
		return append(out, "", " "+sty.c(cDim, "no data in the latest scrape"))
	}
	if p.Kind == view.KindInfo || p.Kind == view.KindTimestamp {
		out = append(out, "")
		for _, s := range series {
			out = append(out, " "+fit(view.LabelText(view.VisibleLabels(f, s), true)+"  = "+view.SI(s.Raw), w-2))
		}
		return out
	}
	ds.series = clamp(ds.series, 0, len(series)-1)
	s := series[ds.series]
	ds.line = clamp(ds.line, 0, len(s.Lines)-1)
	ln := s.Lines[ds.line]

	lbl := view.LabelText(view.VisibleLabels(f, s), true)
	if lbl == "" {
		lbl = "{}"
	}
	sel := sty.c(cDim, "series ") + sty.c(cValue, itoa(ds.series+1)+"/"+itoa(len(series))) + " " + fit(lbl, max(10, w/2))
	lines := make([]string, len(s.Lines))
	for i, l := range s.Lines {
		if i == ds.line {
			lines[i] = sty.c(cKey, "["+l.Name+"]")
		} else {
			lines[i] = sty.c(cDim, l.Name)
		}
	}
	out = append(out, " "+sel+"   "+strings.Join(lines, " "))
	out = append(out, "")

	// Bottom area: bucket distribution for histograms, otherwise a series table.
	var bottom []string
	tableH := max(4, h/3)
	if p.Kind == view.KindHistogram {
		bottom = distribution(p, s, w, tableH, sty)
	}
	if len(series) > 1 {
		if len(bottom) > 0 {
			bottom = append(bottom, "")
		}
		bottom = append(bottom, seriesTable(p, series, ds, w, tableH, sty)...)
	}

	chartH := h - len(out) - len(bottom) - 2
	if chartH < 3 {
		chartH = 3
	}
	const axisW = 10
	cw := w - axisW - 3
	vals := ln.Ring.Values()
	zero := p.Kind == view.KindRate || ln.Name == "rate"
	lo, hi := scaleFor(vals, zero)
	chart := braille(vals, cw, chartH, lo, hi)
	for i, r := range chart {
		var axis string
		switch i {
		case 0:
			axis = p.FormatLine(ln.Name, hi)
		case chartH - 1:
			axis = p.FormatLine(ln.Name, lo)
		case chartH / 2:
			axis = p.FormatLine(ln.Name, (lo+hi)/2)
		}
		out = append(out, sty.c(cDim, rpad(axis, axisW)+" ┤")+" "+sty.c(kindColor(p.Kind), r))
	}
	cur := p.FormatLine(ln.Name, ln.Ring.Last())
	foot := strings.Repeat(" ", axisW) + " └ " + itoa(len(vals)) + " samples @ " + interval + " · now " + cur
	out = append(out, sty.c(cDim, fit(foot, w)))
	out = append(out, "")
	return append(out, bottom...)
}

func unitName(p *view.Panel) string {
	switch p.Unit {
	case view.UnitBytes:
		return " · bytes"
	case view.UnitSeconds:
		return " · seconds"
	case view.UnitRatio:
		return " · ratio"
	case view.UnitPercent:
		return " · percent"
	case view.UnitOther:
		return " · " + p.Sym
	}
	return ""
}

func distribution(p *view.Panel, s *store.Series, w, rows int, sty style) []string {
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
	title := " " + sty.c(cSection, "Distribution") + sty.c(cDim, " · observations per bucket, last interval")
	if first < 0 {
		return []string{title, " " + sty.c(cDim, "no observations")}
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
		out = append(out, " "+sty.c(cDim, rpad(le, leW))+" "+sty.c(cHist, hbar(g.count/maxC, bw))+" "+rpad(view.SI(g.count), cntW))
	}
	return out
}

func seriesTable(p *view.Panel, series []*store.Series, ds *detailState, w, rows int, sty style) []string {
	cols := []string{}
	for _, l := range series[0].Lines {
		cols = append(cols, l.Name)
	}
	if p.Kind == view.KindGauge || p.Kind == view.KindRate || p.Kind == view.KindUsage {
		cols = []string{cols[0], "min", "max"}
	}
	const vw = 10
	lw := w - 4 - len(cols)*(vw+1)
	hdr := "  " + fit("labels", lw)
	for _, c := range cols {
		hdr += " " + rpad(c, vw)
	}
	out := []string{sty.c(cDim, hdr)}
	rows = max(1, rows-1)
	if ds.series < ds.scroll {
		ds.scroll = ds.series
	}
	if ds.series >= ds.scroll+rows {
		ds.scroll = ds.series - rows + 1
	}
	end := min(len(series), ds.scroll+rows)
	for i := ds.scroll; i < end; i++ {
		s := series[i]
		mark := "  "
		if i == ds.series {
			mark = sty.c(cKey, "▸ ")
		}
		row := mark + fit(view.RowLabel(p.Family, s), lw)
		for _, c := range cols {
			var v string
			switch c {
			case "min", "max":
				lo, hi, ok := bounds(s.Primary().Values())
				if !ok {
					v = "–"
				} else if c == "min" {
					v = p.Format(lo)
				} else {
					v = p.Format(hi)
				}
			default:
				v = p.FormatLine(c, s.Line(c).Last())
			}
			row += " " + rpad(v, vw)
		}
		out = append(out, row)
	}
	if end < len(series) {
		out = append(out, sty.c(cDim, "  … "+itoa(len(series)-end)+" more (↑/↓)"))
	}
	return out
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
