package ui

import (
	"math"
	"strings"
	"unicode/utf8"
)

// style applies ANSI SGR codes when colour is enabled.
type style struct{ on bool }

const (
	cBorder   = "38;5;240"
	cBorderHi = "1;38;5;45"
	cTitle    = "1;38;5;252"
	cTitleHi  = "1;38;5;45"
	cSection  = "1;38;5;214"
	cValue    = "1;38;5;255"
	cDim      = "38;5;244"
	cErr      = "1;38;5;203"
	cOK       = "38;5;114"
	cBar      = "48;5;236;38;5;252"
	cKey      = "1;38;5;45"
	cRate     = "38;5;39"
	cGauge    = "38;5;114"
	cHist     = "38;5;221"
	cSummary  = "38;5;177"
	cInfo     = "38;5;250"
	cTime     = "38;5;109"
)

func (s style) c(code, text string) string {
	if !s.on || code == "" || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// vlen is the visible width of s (ANSI escapes stripped, one cell per rune).
func vlen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				j++
			}
			i = j
			continue
		}
		_, sz := utf8.DecodeRuneInString(s[i:])
		i += sz
		n++
	}
	return n
}

// fit truncates (with an ellipsis) or right-pads plain text to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	n := utf8.RuneCountInString(s)
	if n == w {
		return s
	}
	if n < w {
		return s + strings.Repeat(" ", w-n)
	}
	if w == 1 {
		return "…"
	}
	r := []rune(s)
	return string(r[:w-1]) + "…"
}

// fitLeft truncates from the left, keeping the tail ("…_duration_seconds").
func fitLeft(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n <= w {
		return fit(s, w)
	}
	if w <= 1 {
		return fit("…", w)
	}
	r := []rune(s)
	return "…" + string(r[n-w+1:])
}

// rpad right-aligns plain text in w cells.
func rpad(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return fit(s, w)
	}
	return strings.Repeat(" ", w-n) + s
}

// padTo pads an already-styled string to w visible cells.
func padTo(s string, w int) string {
	if n := vlen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

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

// sparkline renders the last w values as an h-row bar chart (top row first).
func sparkline(vals []float64, w, h int, lo, hi float64) []string {
	if w <= 0 || h <= 0 {
		return nil
	}
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	off := w - len(vals)
	levels := make([]int, w)
	steps := h * 8
	for i := range levels {
		levels[i] = -1
	}
	for i, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		f := (v - lo) / (hi - lo)
		f = math.Max(0, math.Min(1, f))
		levels[off+i] = 1 + int(math.Round(f*float64(steps-1)))
	}
	rows := make([]string, h)
	for r := 0; r < h; r++ {
		base := (h - 1 - r) * 8
		var b strings.Builder
		for _, l := range levels {
			switch {
			case l < 0 || l <= base:
				b.WriteRune(' ')
			case l >= base+8:
				b.WriteRune('█')
			default:
				b.WriteRune(blocks[l-base])
			}
		}
		rows[r] = b.String()
	}
	return rows
}

// braille renders a line chart of the last 2*w values in w×h cells.
func braille(vals []float64, w, h int, lo, hi float64) []string {
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
	rows := make([]string, h)
	for r := range grid {
		var b strings.Builder
		for _, c := range grid[r] {
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

// hbar renders a horizontal bar of fraction f in w cells with eighth precision.
func hbar(f float64, w int) string {
	if w <= 0 {
		return ""
	}
	f = math.Max(0, math.Min(1, f))
	eighths := int(math.Round(f * float64(w*8)))
	full, part := eighths/8, eighths%8
	partial := []rune(" ▏▎▍▌▋▊▉")
	var b strings.Builder
	b.WriteString(strings.Repeat("█", full))
	n := full
	if part > 0 && n < w {
		b.WriteRune(partial[part])
		n++
	}
	b.WriteString(strings.Repeat(" ", w-n))
	return b.String()
}
