package render

import (
	"image/color"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// cell is one terminal cell of a rendered frame.
type cell struct {
	r      rune
	fg, bg color.RGBA // zero alpha: the palette default
	bold   bool
	wide   bool // the glyph spans this cell and the next
	cont   bool // the right half of a wide glyph
}

// grid is a frame as rows of cells.
type grid struct {
	rows  [][]cell
	width int // widest row, in cells
}

// parse turns ANSI-styled lines into cells. It understands what promtop
// emits: bold, 24-bit colours and resets.
func parse(lines []string) grid {
	var g grid
	for _, l := range lines {
		row := parseLine(l)
		g.rows = append(g.rows, row)
		g.width = max(g.width, len(row))
	}
	return g
}

func parseLine(s string) []cell {
	var row []cell
	var fg, bg color.RGBA
	bold := false
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j >= len(s) {
				break
			}
			if s[j] == 'm' {
				fg, bg, bold = sgr(s[i+2:j], fg, bg, bold)
			}
			i = j + 1
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		switch ansi.StringWidth(string(r)) {
		case 0:
			continue // combining marks and controls
		case 2:
			row = append(row, cell{r: r, fg: fg, bg: bg, bold: bold, wide: true}, cell{cont: true, bg: bg})
		default:
			row = append(row, cell{r: r, fg: fg, bg: bg, bold: bold})
		}
	}
	return row
}

func sgr(params string, fg, bg color.RGBA, bold bool) (color.RGBA, color.RGBA, bool) {
	ps := strings.Split(params, ";")
	for k := 0; k < len(ps); k++ {
		switch ps[k] {
		case "", "0":
			fg, bg, bold = color.RGBA{}, color.RGBA{}, false
		case "1":
			bold = true
		case "22":
			bold = false
		case "39":
			fg = color.RGBA{}
		case "49":
			bg = color.RGBA{}
		case "38", "48":
			if k+4 < len(ps) && ps[k+1] == "2" {
				c := color.RGBA{num(ps[k+2]), num(ps[k+3]), num(ps[k+4]), 0xff}
				if ps[k] == "38" {
					fg = c
				} else {
					bg = c
				}
				k += 4
			}
		}
	}
	return fg, bg, bold
}

func num(s string) uint8 {
	n, _ := strconv.Atoi(s)
	return uint8(min(max(n, 0), 255))
}

// shape is a piece of geometry replacing a glyph, in cell units scaled to
// pixels by the caller: box drawing, block elements and braille are drawn
// like terminals do, so they join up seamlessly at any font size.
type shape struct {
	kind       shapeKind
	x, y, w, h float64 // rect
	cx, cy, r  float64 // dot
	// arc: a quarter ellipse centred on (cx, cy) with radii (rx, ry) and
	// stroke t, through quadrant q (0 = top-left of the centre … see arcPoints)
	rx, ry, t float64
	q         int
}

type shapeKind int

const (
	rectShape shapeKind = iota
	dotShape
	arcShape
)

// shapes returns the geometry for r in a w×h cell at the origin, or nil when
// r is an ordinary glyph.
func shapes(r rune, w, h float64) []shape {
	t := max(1, math.Round(w/7)) // stroke of box-drawing lines: bold enough to survive downscaling
	cx, cy := w/2, h/2
	hline := func(x0, x1 float64) shape { return shape{kind: rectShape, x: x0, y: cy - t/2, w: x1 - x0, h: t} }
	vline := func(y0, y1 float64) shape { return shape{kind: rectShape, x: cx - t/2, y: y0, w: t, h: y1 - y0} }
	switch {
	case r == '─':
		return []shape{hline(0, w)}
	case r == '│':
		return []shape{vline(0, h)}
	case r == '┤':
		return []shape{vline(0, h), hline(0, cx+t/2)}
	case r == '├':
		return []shape{vline(0, h), hline(cx-t/2, w)}
	case r == '└':
		return []shape{vline(0, cy+t/2), hline(cx-t/2, w)}
	case r == '┘':
		return []shape{vline(0, cy+t/2), hline(0, cx+t/2)}
	case r == '┌':
		return []shape{vline(cy-t/2, h), hline(cx-t/2, w)}
	case r == '┐':
		return []shape{vline(cy-t/2, h), hline(0, cx+t/2)}
	case r == '╭': // centre of the curve: bottom-right corner
		return []shape{{kind: arcShape, cx: w, cy: h, rx: w - cx, ry: h - cy, t: t, q: 0}}
	case r == '╮':
		return []shape{{kind: arcShape, cx: 0, cy: h, rx: cx, ry: h - cy, t: t, q: 1}}
	case r == '╯':
		return []shape{{kind: arcShape, cx: 0, cy: 0, rx: cx, ry: cy, t: t, q: 2}}
	case r == '╰':
		return []shape{{kind: arcShape, cx: w, cy: 0, rx: w - cx, ry: cy, t: t, q: 3}}
	case r >= '▁' && r <= '█': // lower eighths
		f := float64(r-'▁'+1) / 8
		return []shape{{kind: rectShape, x: 0, y: h * (1 - f), w: w, h: h * f}}
	case r >= '▉' && r <= '▏': // left eighths: ▉ is 7/8 … ▏ is 1/8
		f := float64('▏'-r+1) / 8
		return []shape{{kind: rectShape, x: 0, y: 0, w: w * f, h: h}}
	case r == '▌':
		return []shape{{kind: rectShape, x: 0, y: 0, w: w / 2, h: h}}
	case r > 0x2800 && r <= 0x28ff:
		// Dot bits: 1,2,3,7 down the left column; 4,5,6,8 down the right.
		bits := [8][2]int{{0, 0}, {0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}, {0, 3}, {1, 3}}
		rad := min(w/4, h/8) * 0.95
		var out []shape
		for b, pos := range bits {
			if int(r-0x2800)&(1<<b) != 0 {
				out = append(out, shape{kind: dotShape, cx: w * (0.25 + 0.5*float64(pos[0])), cy: h * (0.125 + 0.25*float64(pos[1])), r: rad})
			}
		}
		return out
	}
	return nil
}
