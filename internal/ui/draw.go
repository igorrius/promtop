package ui

import (
	"math"
	"strings"

	"github.com/igorrius/promtop/internal/view"
)

// bounds returns min/max ignoring NaN and Inf; ok is false when no value.
func bounds(vals []float64) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		lo, hi, ok = math.Min(lo, v), math.Max(hi, v), true
	}
	return
}

// scaleFor picks the plotting range; rates and non-negative series start at 0
// when that does not flatten the curve completely.
func scaleFor(vals []float64, zeroBased bool) (lo, hi float64) {
	lo, hi, ok := bounds(vals)
	if !ok {
		return 0, 1
	}
	// Anchor at zero only when it costs less than half of the resolution;
	// otherwise zoom in so variation stays visible.
	if zeroBased && lo >= 0 && lo < hi*0.5 {
		lo = 0
	} else if hi != lo {
		pad := (hi - lo) * 0.1
		lo -= pad
		if zeroBased && lo < 0 && hi >= 0 {
			lo = 0
		}
		hi += pad
	}
	if hi == lo {
		if hi == 0 {
			return 0, 1
		}
		d := math.Abs(hi) * 0.5
		return lo - d, hi + d
	}
	return lo, hi
}

var blocks = []rune(" ▁▂▃▄▅▆▇█")

// levels maps the last w values to bar heights in eighths of h rows; -1
// marks a gap.
func levels(vals []float64, w, h int, lo, hi float64) []int {
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	off := w - len(vals)
	out := make([]int, w)
	steps := h * 8
	for i := range out {
		out[i] = -1
	}
	for i, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		f := math.Max(0, math.Min(1, (v-lo)/(hi-lo)))
		out[off+i] = 1 + int(math.Round(f*float64(steps-1)))
	}
	return out
}

// sparkline renders the last w values as an h-row bar chart (top row first).
func sparkline(vals []float64, w, h int, lo, hi float64) []string {
	if w <= 0 || h <= 0 {
		return nil
	}
	lv := levels(vals, w, h, lo, hi)
	rows := make([]string, h)
	for r := range h {
		base := (h - 1 - r) * 8
		var b strings.Builder
		for _, l := range lv {
			b.WriteRune(barRune(l, base))
		}
		rows[r] = b.String()
	}
	return rows
}

func barRune(l, base int) rune {
	switch {
	case l < 0 || l <= base:
		return ' '
	case l >= base+8:
		return '█'
	}
	return blocks[l-base]
}

// spark renders a gradient sparkline: in a multi-row chart higher rows are
// brighter; a one-row chart colours every bar by its own height.
func (t *theme) spark(k view.Kind, vals []float64, w, h int, lo, hi float64) []string {
	if w <= 0 || h <= 0 {
		return nil
	}
	vals = t.fitTo(vals, w)
	lv := levels(vals, w, h, lo, hi)
	rows := make([]string, h)
	for r := range h {
		base := (h - 1 - r) * 8
		var b strings.Builder
		cur := ""
		for _, l := range lv {
			ch := barRune(l, base)
			if ch == ' ' {
				b.WriteRune(ch)
				continue
			}
			var p string
			if h == 1 {
				p = t.rampAt(k, float64(l)/8)
			} else {
				p = t.rampAt(k, 0.25+0.75*float64(h-r)/float64(h))
			}
			if p != cur {
				b.WriteString(p)
				cur = p
			}
			b.WriteRune(ch)
		}
		if cur != "" {
			b.WriteString(reset)
		}
		rows[r] = b.String()
	}
	return rows
}

// braille renders a line chart of the last 2*w values in w×h cells, as dot
// masks (0 = empty cell).
func braille(vals []float64, w, h int, lo, hi float64) [][]uint8 {
	if w <= 0 || h <= 0 {
		return nil
	}
	pts := 2 * w
	if len(vals) > pts {
		vals = vals[len(vals)-pts:]
	}
	off := pts - len(vals)
	grid := make([][]uint8, h)
	for i := range grid {
		grid[i] = make([]uint8, w)
	}
	dotRows := 4 * h
	ys := make([]int, pts)
	for i := range ys {
		ys[i] = -1
	}
	for i, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		f := math.Max(0, math.Min(1, (v-lo)/(hi-lo)))
		ys[off+i] = int(math.Round((1 - f) * float64(dotRows-1)))
	}
	set := func(x, y int) {
		cx, cy := x/2, y/4
		bit := [2][4]uint8{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}[x%2][y%4]
		grid[cy][cx] |= bit
	}
	prev := -1
	for x, y := range ys {
		if y < 0 {
			prev = -1
			continue
		}
		a, b := y, y
		if prev >= 0 {
			// connect to the previous point (excluding its own dot)
			if prev < y {
				a = prev + 1
			} else if prev > y {
				b = prev - 1
			}
		}
		if a > b {
			a, b = b, a
		}
		for yy := a; yy <= b; yy++ {
			set(x, yy)
		}
		prev = y
	}
	return grid
}

// brailleRows draws a braille chart with a vertical gradient (brighter at
// the top) and a faint dotted grid line every quarter of the height.
func (t *theme) brailleRows(k view.Kind, vals []float64, w, h int, lo, hi float64) []string {
	grid := braille(t.fitTo(vals, 2*w), w, h, lo, hi)
	rows := make([]string, len(grid))
	for r, cells := range grid {
		var b strings.Builder
		guide := h >= 8 && r > 0 && r%(h/4) == 0
		p := t.rampAt(k, 0.3+0.7*float64(h-r)/float64(h))
		cur := ""
		for x, c := range cells {
			switch {
			case c != 0:
				if cur != p {
					b.WriteString(p)
					cur = p
				}
				b.WriteRune(rune(0x2800 + int(c)))
			case guide && x%4 == 0:
				if f := t.fg(t.faint); cur != f {
					b.WriteString(f)
					cur = f
				}
				b.WriteRune('⠂')
			default:
				b.WriteRune(' ')
			}
		}
		if cur != "" {
			b.WriteString(reset)
		}
		rows[r] = b.String()
	}
	return rows
}

// brailleText renders braille dot masks without colour (for tests).
func brailleText(grid [][]uint8) []string {
	rows := make([]string, len(grid))
	for r, cells := range grid {
		var b strings.Builder
		for _, c := range cells {
			if c == 0 {
				b.WriteRune(' ')
			} else {
				b.WriteRune(rune(0x2800 + int(c)))
			}
		}
		rows[r] = b.String()
	}
	return rows
}

var partial = []rune(" ▏▎▍▌▋▊▉")

// hbar renders a horizontal bar of fraction f in w cells with eighth
// precision, followed by a thin track for the unfilled part.
func (t *theme) hbar(f float64, w int, fill string) string {
	if w <= 0 {
		return ""
	}
	if math.IsNaN(f) {
		f = 0
	}
	f = math.Max(0, math.Min(1, f))
	eighths := int(math.Round(f * float64(w*8)))
	full, part := eighths/8, eighths%8
	var b strings.Builder
	b.WriteString(fill)
	b.WriteString(strings.Repeat("█", full))
	n := full
	if part > 0 && n < w {
		b.WriteRune(partial[part])
		n++
	}
	b.WriteString(reset)
	if n < w {
		b.WriteString(t.fg(t.faint))
		b.WriteString(strings.Repeat("─", w-n))
		b.WriteString(reset)
	}
	return b.String()
}

// fitTo stretches or squeezes a history to n points when the theme draws
// snapshots: a picture shows the whole window across its width, while the
// live view scrolls one sample per column.
func (t *theme) fitTo(vals []float64, n int) []float64 {
	if !t.fit || len(vals) < 4 || len(vals) == n || n < 2 {
		return vals
	}
	out := make([]float64, n)
	last := float64(len(vals) - 1)
	for i := range out {
		pos := float64(i) * last / float64(n-1)
		j := int(pos)
		if j >= len(vals)-1 {
			out[i] = vals[len(vals)-1]
			continue
		}
		a, b, f := vals[j], vals[j+1], pos-float64(j)
		switch {
		case math.IsNaN(a) || math.IsNaN(b):
			out[i] = math.NaN()
		default:
			out[i] = a + (b-a)*f
		}
	}
	return out
}
