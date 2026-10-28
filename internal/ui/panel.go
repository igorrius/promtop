package ui

import (
	"image/color"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

// PanelHeight is the fixed height of a grid panel, borders included.
const PanelHeight = 6

const contentRows = PanelHeight - 2

// renderPanel draws a bordered panel exactly w cells wide and PanelHeight tall.
func (t *theme) renderPanel(p *view.Panel, w int, selected bool, now time.Time) []string {
	bc, tc := t.border, t.title
	if selected {
		bc, tc = t.accent, t.accent
	}
	live := p.Family.Live()
	head, headColor := t.headline(p, live)
	badge := p.Kind.String()
	if p.Group {
		badge = "group · " + badge
	}
	if (p.Kind == view.KindHistogram || p.Kind == view.KindSummary) && len(live) > 1 {
		badge += " " + rowLine(p)
	}
	if len(live) > 1 {
		noun := " series"
		if p.Group {
			noun = " metrics"
		}
		badge += " · " + itoa(len(live)) + noun
	}

	// ╭─ title ───── value ─╮
	avail := w - 8
	valW := vlen(head)
	if head == "" {
		valW = -2 // drops the two separator spaces
	}
	titleW := avail - valW - 1
	if titleW < 4 {
		head, valW = "", -2
		titleW = avail + 1
	}
	titleW = min(titleW, vlen(p.Title))
	title := fitLeft(p.Title, titleW)
	fill := max(1, w-8-titleW-valW)
	top := t.paint(bc, "╭─ ") + t.bold(tc, title) + t.paint(bc, " "+strings.Repeat("─", fill))
	if head != "" {
		top += " " + t.bold(headColor, head) + t.paint(bc, " ─╮")
	} else {
		top += t.paint(bc, "─╮")
	}
	top = cut(top, w)

	cw := w - 4
	body := t.content(p, live, cw, now)
	lines := []string{top}
	for i := range contentRows {
		b := ""
		if i < len(body) {
			b = body[i]
		}
		lines = append(lines, t.paint(bc, "│")+" "+cut(b, cw)+" "+t.paint(bc, "│"))
	}
	badge = fit(badge, max(0, w-6))
	badge = strings.TrimRight(badge, " ")
	bfill := max(1, w-5-vlen(badge))
	lines = append(lines, t.paint(bc, "╰"+strings.Repeat("─", bfill)+" ")+t.paint(t.dim, badge)+t.paint(bc, " ─╯"))
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
func (t *theme) headline(p *view.Panel, live []*store.Series) (string, color.Color) {
	if len(live) == 0 {
		return "", t.value
	}
	switch p.Kind {
	case view.KindInfo:
		return "", t.value
	case view.KindTimestamp:
		if len(live) == 1 {
			return ageText(p, live[0], time.Now()), t.value
		}
		return "", t.value
	case view.KindState:
		if p.Family.Type == expo.StateSet {
			return activeStates(p, live), t.accent
		}
		up := 0
		for _, s := range live {
			if view.StateUp(s) {
				up++
			}
		}
		c := t.ok
		if up < len(live) {
			c = t.err
		}
		if len(live) == 1 {
			if up == 1 {
				return "up", c
			}
			return "down", c
		}
		return itoa(up) + "/" + itoa(len(live)) + " up", c
	case view.KindRate:
		if p.Group {
			return "", t.value // different counters do not add up
		}
		sum, ok := 0.0, false
		for _, s := range live {
			if v := s.Primary().Last(); !math.IsNaN(v) {
				sum, ok = sum+v, true
			}
		}
		if !ok {
			return "", t.value
		}
		if len(live) > 1 {
			return "Σ " + p.Format(sum), t.value
		}
		return p.Format(sum), t.value
	case view.KindHistogram, view.KindSummary:
		if len(live) > 1 {
			return "", t.value
		}
		l := rowLine(p)
		if v := live[0].Line(l).Last(); !math.IsNaN(v) {
			return l + " " + p.FormatLine(l, v), t.value
		}
		return "", t.value
	case view.KindUsage:
		worst := math.NaN()
		for _, s := range live {
			if u := p.Usage(s); !math.IsNaN(u) && (math.IsNaN(worst) || u > worst) {
				worst = u
			}
		}
		if math.IsNaN(worst) {
			return "", t.value
		}
		txt := view.Percent(worst * 100)
		if len(live) > 1 {
			txt = "max " + txt
		}
		return txt, t.usageColor(worst)
	}
	if len(live) == 1 {
		return p.Format(live[0].Raw), t.value
	}
	return "", t.value
}

func (t *theme) usageColor(f float64) color.Color {
	switch {
	case f >= 0.9:
		return t.err
	case f >= 0.7:
		return t.warn
	}
	return t.ok
}

func (t *theme) content(p *view.Panel, live []*store.Series, cw int, now time.Time) []string {
	if len(live) == 0 {
		return []string{t.paint(t.dim, "no data")}
	}
	switch p.Kind {
	case view.KindInfo:
		return t.infoContent(p, live, cw)
	case view.KindTimestamp:
		return t.timestampContent(p, live, cw, now)
	case view.KindState:
		return t.stateContent(p, live, cw)
	case view.KindUsage:
		if len(live) > 1 {
			return t.usageRows(p, live, cw)
		}
		return t.usageContent(p, live[0], cw)
	}
	if len(live) > 1 {
		return t.multiContent(p, live, cw)
	}
	s := live[0]
	switch p.Kind {
	case view.KindHistogram, view.KindSummary:
		return t.quantileContent(p, s, cw)
	}
	return t.singleContent(p, s, cw)
}

func (t *theme) singleContent(p *view.Panel, s *store.Series, cw int) []string {
	vals := s.Primary().Values()
	lo, hi, ok := bounds(vals)
	var info string
	if p.Kind == view.KindRate {
		info = "total " + p.FormatTotal(s.Raw)
	} else if ok && lo != hi {
		info = "min " + p.Format(lo) + " · max " + p.Format(hi)
	}
	cur := p.Format(s.Primary().Last())
	first := t.bold(t.value, cur)
	if rest := cw - vlen(cur) - 2; rest > 3 && info != "" {
		first += "  " + t.paint(t.dim, fit(info, rest))
	}
	out := []string{first}
	slo, shi := scaleFor(vals, p.Kind == view.KindRate)
	return append(out, t.spark(p.Kind, vals, cw, contentRows-1, slo, shi)...)
}

func (t *theme) usageContent(p *view.Panel, s *store.Series, cw int) []string {
	used, lim := p.UsageValues(s)
	f := used / lim
	var txt string
	if p.LimitInvert {
		txt = "free " + p.Format(s.Raw) + " of " + p.Format(lim)
	} else {
		txt = p.Format(used) + " / " + p.Format(lim)
	}
	out := []string{t.bold(t.value, fit(txt, cw))}
	out = append(out, t.hbar(f, cw, t.usageAt(f)))
	vals := s.Primary().Values()
	lo, hi := scaleFor(vals, true)
	return append(out, t.spark(view.KindUsage, vals, cw, contentRows-2, lo, hi)...)
}

// usageRows lists the series with the fullest first: label, percentage, bar.
func (t *theme) usageRows(p *view.Panel, live []*store.Series, cw int) []string {
	rows := append([]*store.Series(nil), live...)
	u := func(s *store.Series) float64 {
		if v := p.Usage(s); !math.IsNaN(v) {
			return v
		}
		return -1
	}
	sort.SliceStable(rows, func(i, j int) bool { return u(rows[i]) > u(rows[j]) })
	n := contentRows
	more := 0
	if len(rows) > n {
		more = len(rows) - (n - 1)
		rows = rows[:n-1]
	}
	const pctW = 6
	lw := min(max(8, cw/3), cw-pctW-8)
	bw := cw - lw - pctW - 2
	var out []string
	for _, s := range rows {
		f := p.Usage(s)
		lbl := view.RowLabel(p.Family, s)
		out = append(out, fit(lbl, lw)+" "+t.bold(t.usageColor(f), rpad(view.Percent(f*100), pctW))+" "+t.hbar(f, bw, t.usageAt(f)))
	}
	if more > 0 {
		out = append(out, t.paint(t.dim, "+"+itoa(more)+" more · enter for all"))
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
		return slices.Contains(all, name)
	}
	var out []string
	add := func(name string) {
		if len(out) < n && has(name) {
			if slices.Contains(out, name) {
				return
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

func (t *theme) quantileContent(p *view.Panel, s *store.Series, cw int) []string {
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
		row := t.paint(t.dim, fit(n, nameW)) + " " + t.bold(t.value, rpad(p.FormatLine(n, r.Last()), valW)) + " "
		if sw > 0 {
			row += t.spark(p.Kind, r.Values(), sw, 1, lo, hi)[0]
		}
		out = append(out, row)
	}
	footer := "rate " + p.FormatLine("rate", s.Line("rate").Last())
	if avg := s.Line("avg"); avg != nil {
		footer += " · avg " + p.Format(avg.Last())
	}
	footer += " · n " + view.SI(s.Raw)
	return append(out, t.paint(t.dim, fit(footer, cw)))
}

// sortedRows orders the series of a panel for display: by the plotted value,
// descending — except groups of gauges, which keep their name order so the
// rows read like a table.
func sortedRows(p *view.Panel, live []*store.Series, line string) []*store.Series {
	out := append([]*store.Series(nil), live...)
	if p.Group && p.Kind == view.KindGauge {
		return out
	}
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

func (t *theme) multiContent(p *view.Panel, live []*store.Series, cw int) []string {
	line := ""
	if p.Kind == view.KindHistogram || p.Kind == view.KindSummary {
		line = rowLine(p)
	}
	rows := sortedRows(p, live, line)
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
	ring := func(s *store.Series) *store.Ring {
		if line != "" {
			return s.Line(line)
		}
		return s.Primary()
	}
	// Shared scale across rows so bars compare — except in groups, whose
	// members measure different things.
	var all []float64
	for _, s := range rows {
		all = append(all, ring(s).Values()...)
	}
	lo, hi := scaleFor(all, true)
	var out []string
	for _, s := range rows {
		r := ring(s)
		lbl := view.RowLabel(p.Family, s)
		if lbl == "" {
			lbl = "{}"
		}
		row := fit(lbl, lw) + " " + t.bold(t.value, rpad(p.FormatLine(line, r.Last()), valW)) + " "
		if sw > 0 {
			slo, shi := lo, hi
			if p.Group {
				slo, shi = scaleFor(r.Values(), true)
			}
			row += t.spark(p.Kind, r.Values(), sw, 1, slo, shi)[0]
		}
		out = append(out, row)
	}
	if more > 0 {
		out = append(out, t.paint(t.dim, "+"+itoa(more)+" more · enter for all"))
	}
	return out
}

// stateContent shows health flags as chips, problems first; a single flag
// gets a large indicator and its history.
func (t *theme) stateContent(p *view.Panel, live []*store.Series, cw int) []string {
	if p.Family.Type == expo.StateSet {
		return t.stateSetContent(p, live, cw)
	}
	if len(live) == 1 {
		s := live[0]
		mark, c := "● up", t.ok
		if !view.StateUp(s) {
			mark, c = "○ down", t.err
		}
		out := []string{t.bold(c, mark)}
		return append(out, "", t.stateHistory(s.Primary().Values(), cw))
	}
	rows := append([]*store.Series(nil), live...)
	sort.SliceStable(rows, func(i, j int) bool { return !view.StateUp(rows[i]) && view.StateUp(rows[j]) })
	chips := make([]string, 0, len(rows))
	for _, s := range rows {
		lbl := view.RowLabel(p.Family, s)
		if lbl == "" {
			lbl = "{}"
		}
		if view.StateUp(s) {
			chips = append(chips, t.paint(t.ok, "●")+" "+lbl)
		} else {
			chips = append(chips, t.bold(t.err, "○ "+lbl))
		}
	}
	return flow(chips, cw, contentRows, t.paint(t.dim, "…"))
}

// stateHistory renders 0/1 history as a strip: tall green bars for up, low
// red ones for down.
func (t *theme) stateHistory(vals []float64, w int) string {
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", w-len(vals)))
	for _, v := range vals {
		switch {
		case math.IsNaN(v):
			b.WriteRune(' ')
		case v == 1:
			b.WriteString(t.paint(t.ok, "▆"))
		default:
			b.WriteString(t.paint(t.err, "▂"))
		}
	}
	return b.String()
}

// stateSetContent lists a stateset's states, the active ones highlighted.
func (t *theme) stateSetContent(p *view.Panel, live []*store.Series, cw int) []string {
	chips := make([]string, 0, len(live))
	for _, s := range live {
		lbl := view.RowLabel(p.Family, s)
		if s.Raw == 1 {
			chips = append(chips, t.bold(t.accent, "◆ "+lbl))
		} else {
			chips = append(chips, t.paint(t.dim, "◇ "+lbl))
		}
	}
	return flow(chips, cw, contentRows, t.paint(t.dim, "…"))
}

func activeStates(p *view.Panel, live []*store.Series) string {
	var on []string
	for _, s := range live {
		if s.Raw == 1 {
			on = append(on, view.RowLabel(p.Family, s))
		}
	}
	return strings.Join(on, ", ")
}

// flow lays chips out left to right over at most rows lines of width w,
// ending with more when some do not fit.
func flow(chips []string, w, rows int, more string) []string {
	var out []string
	line := ""
	for i, c := range chips {
		cand := c
		if line != "" {
			cand = line + "  " + c
		}
		if vlen(cand) <= w {
			line = cand
			continue
		}
		if len(out) == rows-1 {
			// Last line: keep what fits and mark the rest.
			if line == "" {
				line = cut(c, w-2)
			}
			if i < len(chips) {
				line = cut(line, w-2) + " " + more
			}
			return append(out, line)
		}
		if line != "" {
			out = append(out, line)
		}
		line = cut(c, w)
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

func (t *theme) infoContent(p *view.Panel, live []*store.Series, cw int) []string {
	var out []string
	if len(live) == 1 {
		for _, l := range view.VisibleLabels(p.Family, live[0]) {
			k := fit(l.Name, min(18, cw/2))
			out = append(out, t.paint(t.dim, k)+" "+fit(l.Value, cw-vlen(k)-1))
		}
	} else {
		for _, s := range live {
			out = append(out, fit(view.LabelText(view.VisibleLabels(p.Family, s), true), cw))
		}
	}
	if len(out) > contentRows {
		out = out[:contentRows-1]
		out = append(out, t.paint(t.dim, "… enter for all"))
	}
	if len(out) == 0 {
		out = append(out, t.paint(t.dim, "value "+p.Format(live[0].Raw)))
	}
	return out
}

// ageText renders a timestamp relative to now: "up 3d04h", "5m ago", "in 2h".
func ageText(p *view.Panel, s *store.Series, now time.Time) string {
	t := tsTime(p, s)
	d := now.Sub(t)
	name := strings.ToLower(p.Name())
	switch {
	case strings.Contains(name, "start") || strings.Contains(name, "boot"):
		return "up " + view.Age(d)
	case d >= 0:
		return view.Age(d) + " ago"
	}
	return "in " + view.Age(-d)
}

func tsTime(p *view.Panel, s *store.Series) time.Time {
	sec := s.Raw
	if p.Scale != 0 {
		sec *= p.Scale
	}
	return time.Unix(0, int64(sec*1e9))
}

func (t *theme) timestampContent(p *view.Panel, live []*store.Series, cw int, now time.Time) []string {
	if len(live) == 1 {
		s := live[0]
		return []string{
			t.bold(t.value, ageText(p, s, now)),
			t.paint(t.dim, tsTime(p, s).Local().Format("2006-01-02 15:04:05 MST")),
		}
	}
	rows := append([]*store.Series(nil), live...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Raw > rows[j].Raw })
	var out []string
	for i, s := range rows {
		if i == contentRows-1 && len(rows) > contentRows {
			out = append(out, t.paint(t.dim, "+"+itoa(len(rows)-i)+" more · enter for all"))
			break
		}
		age := ageText(p, s, now)
		out = append(out, fit(view.RowLabel(p.Family, s), cw-vlen(age)-1)+" "+t.bold(t.value, age))
	}
	return out
}
