package ui

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

// PanelHeight is the fixed height of a grid panel, borders included.
const PanelHeight = 6

const contentRows = PanelHeight - 2

func kindColor(k view.Kind) string {
	switch k {
	case view.KindRate:
		return cRate
	case view.KindHistogram:
		return cHist
	case view.KindSummary:
		return cSummary
	case view.KindInfo:
		return cInfo
	case view.KindTimestamp:
		return cTime
	}
	return cGauge
}

func usageColor(f float64) string {
	switch {
	case f >= 0.9:
		return cErr
	case f >= 0.7:
		return "38;5;221"
	}
	return cOK
}

// renderPanel draws a bordered panel exactly w cells wide and PanelHeight tall.
func renderPanel(p *view.Panel, w int, selected bool, now time.Time, sty style) []string {
	bc, tc := cBorder, cTitle
	if selected {
		bc, tc = cBorderHi, cTitleHi
	}
	live := p.Family.Live()
	head := headline(p, live)
	badge := p.Kind.String()
	if p.Kind == view.KindHistogram || p.Kind == view.KindSummary {
		if len(live) > 1 {
			badge += " " + rowLine(p)
		}
	}
	if len(live) > 1 {
		badge += " · " + itoa(len(live)) + " series"
	}

	// ╭─ title ───── value ─╮
	avail := w - 8
	valW := len([]rune(head))
	if head == "" {
		valW = -2 // drops the two separator spaces
	}
	titleW := avail - valW - 1
	if titleW < 4 {
		head, valW = "", -2
		titleW = avail + 1
	}
	title := p.Title
	if n := len([]rune(title)); n < titleW {
		titleW = n
	}
	title = fitLeft(title, titleW)
	fill := w - 8 - titleW - valW
	if fill < 1 {
		fill = 1
	}
	top := sty.c(bc, "╭─ ") + sty.c(tc, title) + sty.c(bc, " "+strings.Repeat("─", fill))
	if head != "" {
		top += " " + sty.c(cValue, head) + sty.c(bc, " ─╮")
	} else {
		top += sty.c(bc, "─╮")
	}
	top = padTo(top, w)

	cw := w - 4
	body := content(p, live, cw, now, sty)
	for len(body) < contentRows {
		body = append(body, strings.Repeat(" ", cw))
	}
	lines := []string{top}
	for _, b := range body[:contentRows] {
		lines = append(lines, sty.c(bc, "│")+" "+padTo(b, cw)+" "+sty.c(bc, "│"))
	}
	badge = fit(badge, max(0, w-6))
	bfill := w - 5 - len([]rune(badge))
	if bfill < 1 {
		bfill = 1
	}
	lines = append(lines, sty.c(bc, "╰"+strings.Repeat("─", bfill)+" ")+sty.c(cDim, badge)+sty.c(bc, " ─╯"))
	return lines
}

// rowLine is the derived line plotted for each row of a multi-series
// histogram/summary panel.
func rowLine(p *view.Panel) string {
	for _, s := range p.Family.Live() {
		if q := quantileLines(s, 1); len(q) > 0 {
			return q[0]
		}
		if len(s.Lines) > 0 {
			return s.Lines[0].Name
		}
	}
	return "p99"
}

// headline is the value shown in the panel's top border.
func headline(p *view.Panel, live []*store.Series) string {
	if len(live) == 0 {
		return ""
	}
	switch p.Kind {
	case view.KindInfo, view.KindTimestamp:
		return ""
	case view.KindRate:
		sum, ok := 0.0, false
		for _, s := range live {
			if v := s.Primary().Last(); !math.IsNaN(v) {
				sum, ok = sum+v, true
			}
		}
		if !ok {
			return ""
		}
		if len(live) > 1 {
			return "Σ " + p.Format(sum)
		}
		return p.Format(sum)
	case view.KindHistogram, view.KindSummary:
		if len(live) > 1 {
			return ""
		}
		l := rowLine(p)
		if v := live[0].Line(l).Last(); !math.IsNaN(v) {
			return l + " " + p.FormatLine(l, v)
		}
		return ""
	case view.KindUsage:
		if lim := limitValue(p); lim > 0 {
			return view.Percent(live[0].Raw / lim * 100)
		}
	}
	if len(live) == 1 {
		return p.Format(live[0].Raw)
	}
	return ""
}

func limitValue(p *view.Panel) float64 {
	if p.Limit == nil {
		return 0
	}
	l := p.Limit.Live()
	if len(l) == 0 {
		return 0
	}
	return l[0].Raw
}

func content(p *view.Panel, live []*store.Series, cw int, now time.Time, sty style) []string {
	if len(live) == 0 {
		return []string{sty.c(cDim, fit("no data", cw))}
	}
	switch p.Kind {
	case view.KindInfo:
		return infoContent(p, live, cw, sty)
	case view.KindTimestamp:
		return timestampContent(p, live, cw, now, sty)
	}
	if len(live) > 1 {
		return multiContent(p, live, cw, sty)
	}
	s := live[0]
	switch p.Kind {
	case view.KindHistogram, view.KindSummary:
		return quantileContent(p, s, cw, sty)
	case view.KindUsage:
		return usageContent(p, s, cw, sty)
	}
	return singleContent(p, s, cw, sty)
}

func singleContent(p *view.Panel, s *store.Series, cw int, sty style) []string {
	vals := s.Primary().Values()
	lo, hi, ok := bounds(vals)
	var info string
	if p.Kind == view.KindRate {
		info = "total " + view.FormatValue(s.Raw, p.Unit, p.Sym, false)
		if p.Unit == view.UnitSeconds {
			info = "total " + view.Duration(s.Raw)
		}
	} else if ok && lo != hi {
		info = "min " + p.Format(lo) + " · max " + p.Format(hi)
	}
	cur := p.Format(s.Primary().Last())
	first := sty.c(cValue, cur)
	if rest := cw - len([]rune(cur)) - 2; rest > 3 && info != "" {
		first += "  " + sty.c(cDim, fit(info, rest))
	}
	out := []string{first}
	slo, shi := scaleFor(vals, p.Kind == view.KindRate)
	for _, r := range sparkline(vals, cw, contentRows-1, slo, shi) {
		out = append(out, sty.c(kindColor(p.Kind), r))
	}
	return out
}

func usageContent(p *view.Panel, s *store.Series, cw int, sty style) []string {
	lim := limitValue(p)
	f := 0.0
	if lim > 0 {
		f = s.Raw / lim
	}
	txt := p.Format(s.Raw) + " / " + p.Format(lim)
	out := []string{sty.c(cValue, fit(txt, cw))}
	out = append(out, sty.c(usageColor(f), hbar(f, cw)))
	vals := s.Primary().Values()
	lo, hi := scaleFor(vals, true)
	for _, r := range sparkline(vals, cw, contentRows-2, lo, hi) {
		out = append(out, sty.c(cGauge, r))
	}
	return out
}

// quantileLines returns up to n quantile line names (excluding rate/avg),
// preferring a tail quantile, the median and the maximum.
func quantileLines(s *store.Series, n int) []string {
	var all []string
	for _, l := range s.Lines {
		if l.Name != "rate" && l.Name != "avg" {
			all = append(all, l.Name)
		}
	}
	has := func(name string) bool {
		for _, a := range all {
			if a == name {
				return true
			}
		}
		return false
	}
	var out []string
	add := func(name string) {
		if len(out) < n && has(name) {
			for _, o := range out {
				if o == name {
					return
				}
			}
			out = append(out, name)
		}
	}
	for _, tail := range []string{"p999", "p99", "p95", "p90", "p75"} {
		if has(tail) {
			add(tail)
			break
		}
	}
	add("p90")
	add("p50")
	add("max")
	for _, a := range all {
		add(a)
	}
	// Order by quantile descending for a natural top-to-bottom read.
	rank := func(name string) float64 {
		switch name {
		case "max":
			return 2
		case "min":
			return -1
		}
		// p5 = 5%, p50 = 50%, p999 = 99.9%
		d := strings.TrimPrefix(name, "p")
		if len(d) > 2 {
			d = d[:2] + "." + d[2:]
		}
		v, _ := strconv.ParseFloat(d, 64)
		return v / 100
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) > rank(out[j]) })
	return out
}

func quantileContent(p *view.Panel, s *store.Series, cw int, sty style) []string {
	const nameW, valW = 4, 9
	sw := cw - nameW - valW - 2
	names := quantileLines(s, contentRows-1)
	// Common scale so the lines are comparable.
	var all []float64
	for _, n := range names {
		all = append(all, s.Line(n).Values()...)
	}
	lo, hi := scaleFor(all, true)
	var out []string
	for _, n := range names {
		r := s.Line(n)
		row := sty.c(cDim, fit(n, nameW)) + " " + sty.c(cValue, rpad(p.FormatLine(n, r.Last()), valW)) + " "
		if sw > 0 {
			row += sty.c(kindColor(p.Kind), sparkline(r.Values(), sw, 1, lo, hi)[0])
		}
		out = append(out, row)
	}
	footer := "rate " + p.FormatLine("rate", s.Line("rate").Last())
	if avg := s.Line("avg"); avg != nil {
		footer += " · avg " + p.Format(avg.Last())
	}
	footer += " · n " + view.SI(s.Raw)
	out = append(out, sty.c(cDim, fit(footer, cw)))
	return out
}

func sortedByPrimary(live []*store.Series, line string) []*store.Series {
	out := append([]*store.Series(nil), live...)
	val := func(s *store.Series) float64 {
		r := s.Primary()
		if line != "" {
			r = s.Line(line)
		}
		v := r.Last()
		if math.IsNaN(v) {
			return math.Inf(-1)
		}
		return v
	}
	sort.SliceStable(out, func(i, j int) bool { return val(out[i]) > val(out[j]) })
	return out
}

func multiContent(p *view.Panel, live []*store.Series, cw int, sty style) []string {
	line := ""
	if p.Kind == view.KindHistogram || p.Kind == view.KindSummary {
		line = rowLine(p)
	}
	rows := sortedByPrimary(live, line)
	n := contentRows
	more := 0
	if len(rows) > n {
		more = len(rows) - (n - 1)
		rows = rows[:n-1]
	}
	const valW = 9
	sw := max(6, cw/4)
	lw := cw - valW - sw - 2
	if lw < 6 {
		sw = max(0, cw-valW-8)
		lw = cw - valW - sw - 2
	}
	// Shared scale across rows so bars compare.
	var all []float64
	for _, s := range rows {
		r := s.Primary()
		if line != "" {
			r = s.Line(line)
		}
		all = append(all, r.Values()...)
	}
	lo, hi := scaleFor(all, true)
	var out []string
	for _, s := range rows {
		r := s.Primary()
		if line != "" {
			r = s.Line(line)
		}
		lbl := view.RowLabel(p.Family, s)
		if lbl == "" {
			lbl = "{}"
		}
		row := fit(lbl, lw) + " " + sty.c(cValue, rpad(p.FormatLine(line, r.Last()), valW)) + " "
		if sw > 0 {
			row += sty.c(kindColor(p.Kind), sparkline(r.Values(), sw, 1, lo, hi)[0])
		}
		out = append(out, row)
	}
	if more > 0 {
		out = append(out, sty.c(cDim, fit("+"+itoa(more)+" more · enter for all", cw)))
	}
	return out
}

func infoContent(p *view.Panel, live []*store.Series, cw int, sty style) []string {
	var out []string
	if len(live) == 1 {
		for _, l := range view.VisibleLabels(p.Family, live[0]) {
			k := fit(l.Name, min(18, cw/2))
			out = append(out, sty.c(cDim, k)+" "+fit(l.Value, cw-len([]rune(k))-1))
		}
	} else {
		for _, s := range live {
			out = append(out, fit(view.LabelText(view.VisibleLabels(p.Family, s), true), cw))
		}
	}
	if len(out) > contentRows {
		out = out[:contentRows-1]
		out = append(out, sty.c(cDim, fit("… enter for all", cw)))
	}
	if len(out) == 0 {
		out = append(out, sty.c(cDim, fit("value "+p.Format(live[0].Raw), cw)))
	}
	return out
}

func timestampContent(p *view.Panel, live []*store.Series, cw int, now time.Time, sty style) []string {
	s := live[0]
	t := time.Unix(0, int64(s.Raw*1e9))
	d := now.Sub(t)
	var main string
	switch {
	case strings.HasSuffix(p.Name(), "start_time_seconds"):
		main = "up " + view.Age(d)
	case d >= 0:
		main = view.Age(d) + " ago"
	default:
		main = "in " + view.Age(-d)
	}
	return []string{
		sty.c(cValue, fit(main, cw)),
		sty.c(cDim, fit(t.Local().Format("2006-01-02 15:04:05 MST"), cw)),
	}
}

func itoa(n int) string {
	return strings.TrimSpace(view.SI(float64(n)))
}
