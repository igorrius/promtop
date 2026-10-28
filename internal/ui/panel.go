package ui

import (
	"math"
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
	head, tone := p.Headline(now)
	headColor := t.tone(tone)
	badge := p.Badge()
	rows := p.Rows()

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
	body := t.content(p, rows, cw, now)
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

// content renders the panel body from its rows, ordered by p.Rows.
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
	rows := live
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
		out = append(out, fit(lbl, lw)+" "+t.bold(t.tone(view.UsageTone(f)), rpad(view.Percent(f*100), pctW))+" "+t.hbar(f, bw, t.usageAt(f)))
	}
	if more > 0 {
		out = append(out, t.moreRows(more))
	}
	return out
}

func (t *theme) quantileContent(p *view.Panel, s *store.Series, cw int) []string {
	const nameW, valW = 4, 9
	sw := cw - nameW - valW - 2
	names := view.QuantileLines(s, contentRows-1)
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

func (t *theme) multiContent(p *view.Panel, live []*store.Series, cw int) []string {
	line := p.RowLine()
	rows := live
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
	ring := func(s *store.Series) *store.Ring { return view.Ring(s, line) }
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
		out = append(out, t.moreRows(more))
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
	chips := make([]string, 0, len(live))
	for _, s := range live {
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
		out = append(out, t.moreRows(0))
	}
	if len(out) == 0 {
		out = append(out, t.paint(t.dim, "value "+p.Format(live[0].Raw)))
	}
	return out
}

func (t *theme) timestampContent(p *view.Panel, live []*store.Series, cw int, now time.Time) []string {
	if len(live) == 1 {
		s := live[0]
		return []string{
			t.bold(t.value, p.Age(s, now)),
			t.paint(t.dim, p.Time(s).Local().Format("2006-01-02 15:04:05 MST")),
		}
	}
	var out []string
	for i, s := range live {
		if i == contentRows-1 && len(live) > contentRows {
			out = append(out, t.moreRows(len(live)-i))
			break
		}
		age := p.Age(s, now)
		out = append(out, fit(view.RowLabel(p.Family, s), cw-vlen(age)-1)+" "+t.bold(t.value, age))
	}
	return out
}

// moreRows notes rows a panel could not show (n = 0: an unknown number). Live
// views point at the details; a snapshot has no keys to press.
func (t *theme) moreRows(n int) string {
	switch {
	case n > 0 && t.fit:
		return t.paint(t.dim, "+"+itoa(n)+" more")
	case n > 0:
		return t.paint(t.dim, "+"+itoa(n)+" more · enter for all")
	case t.fit:
		return t.paint(t.dim, "…")
	}
	return t.paint(t.dim, "… enter for all")
}
