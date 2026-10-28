package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/igorrius/promtop/internal/view"
)

// vlen is the display width of s: ANSI escapes are skipped and wide
// graphemes (CJK, emoji) count as two cells.
func vlen(s string) int { return ansi.StringWidth(s) }

// fit truncates (with an ellipsis) or right-pads plain text to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if n := vlen(s); n > w {
		s = ansi.Truncate(s, w, "…")
	}
	return padTo(s, w)
}

// fitLeft truncates from the left, keeping the tail ("…_duration_seconds").
func fitLeft(s string, w int) string {
	n := vlen(s)
	if n <= w {
		return fit(s, w)
	}
	if w <= 1 {
		return fit("…", w)
	}
	return padTo(ansi.TruncateLeft(s, n-w+1, "…"), w)
}

// rpad right-aligns plain text in w cells.
func rpad(s string, w int) string {
	n := vlen(s)
	if n >= w {
		return fit(s, w)
	}
	return strings.Repeat(" ", w-n) + s
}

// padTo pads a (possibly styled) string to w display cells. Truncation can
// leave a wide grapheme one cell short; padding fixes that too.
func padTo(s string, w int) string {
	if n := vlen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// cut truncates a styled string to w cells and pads it to exactly w.
func cut(s string, w int) string {
	if vlen(s) > w {
		s = ansi.Truncate(s, w, "")
	}
	return padTo(s, w)
}

func itoa(n int) string {
	return strings.TrimSpace(view.SI(float64(n)))
}

// clamp limits v to [0, hi]; a negative hi (an empty list) gives 0.
func clamp(v, hi int) int {
	return max(min(v, hi), 0)
}
