package render

import (
	"bufio"
	"fmt"
	"image/color"
	"io"
	"strings"
)

func hexColor(c color.RGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// svg writes a grid as SVG. Text runs are stretched to their cell width
// (textLength), so columns line up whatever monospace font the viewer
// substitutes; box drawing, blocks and braille are geometry.
func svg(w io.Writer, g grid, m metrics, size float64, bg, fg color.RGBA) error {
	bw := bufio.NewWriter(w)
	width := g.width*m.cw + 2*m.pad
	height := len(g.rows)*m.ch + 2*m.pad
	fmt.Fprintf(bw, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n", width, height, width, height)
	fmt.Fprintf(bw, `<style>text{font-family:"DejaVu Sans Mono",Menlo,Consolas,monospace;font-size:%.2fpx;white-space:pre}.b{font-weight:bold}</style>`+"\n", size)
	fmt.Fprintf(bw, `<rect width="%d" height="%d" fill="%s"/>`+"\n", width, height, hexColor(bg))
	for y, row := range g.rows {
		for x, c := range row {
			if c.bg.A == 0 || c.cont {
				continue
			}
			cw := m.cw
			if c.wide {
				cw *= 2
			}
			fmt.Fprintf(bw, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`+"\n", m.pad+x*m.cw, m.pad+y*m.ch, cw, m.ch, hexColor(c.bg))
		}
	}
	for y, row := range g.rows {
		y0 := m.pad + y*m.ch
		var run strings.Builder
		runX, runCells := 0, 0
		var runCol color.RGBA
		runBold := false
		flush := func() {
			if runCells == 0 {
				return
			}
			cls := ""
			if runBold {
				cls = ` class="b"`
			}
			fmt.Fprintf(bw, `<text x="%d" y="%d" textLength="%d" lengthAdjust="spacingAndGlyphs" fill="%s"%s>%s</text>`+"\n",
				m.pad+runX*m.cw, y0+m.baseline, runCells*m.cw, hexColor(runCol), cls, run.String())
			run.Reset()
			runCells = 0
		}
		for x, c := range row {
			if c.cont {
				continue
			}
			col := c.fg
			if col.A == 0 {
				col = fg
			}
			sh := shapes(c.r, float64(m.cw), float64(m.ch))
			if c.r == ' ' || c.r == 0 || sh != nil || col != runCol || c.bold != runBold {
				flush()
			}
			if sh != nil {
				writeShapes(bw, sh, float64(m.pad+x*m.cw), float64(y0), col)
				continue
			}
			if c.r == ' ' || c.r == 0 {
				continue
			}
			if runCells == 0 {
				runX, runCol, runBold = x, col, c.bold
			}
			run.WriteString(escapeXML(string(c.r)))
			runCells++
			if c.wide {
				runCells++
			}
		}
		flush()
	}
	if _, err := bw.WriteString("</svg>\n"); err != nil {
		return err
	}
	return bw.Flush()
}

func writeShapes(w io.Writer, sh []shape, x0, y0 float64, col color.RGBA) {
	c := hexColor(col)
	for _, s := range sh {
		switch s.kind {
		case rectShape:
			fmt.Fprintf(w, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s"/>`+"\n", x0+s.x, y0+s.y, s.w, s.h, c)
		case dotShape:
			fmt.Fprintf(w, `<circle cx="%.2f" cy="%.2f" r="%.2f" fill="%s"/>`+"\n", x0+s.cx, y0+s.cy, s.r, c)
		case arcShape:
			pts := arcPoints(x0+s.cx, y0+s.cy, s.rx, s.ry, s.q)
			a, b := pts[0], pts[len(pts)-1]
			fmt.Fprintf(w, `<path d="M%.2f %.2fA%.2f %.2f 0 0 1 %.2f %.2f" fill="none" stroke="%s" stroke-width="%.2f"/>`+"\n",
				a[0], a[1], s.rx, s.ry, b[0], b[1], c, s.t)
		}
	}
}

func escapeXML(s string) string {
	switch s {
	case "<":
		return "&lt;"
	case ">":
		return "&gt;"
	case "&":
		return "&amp;"
	case `"`:
		return "&quot;"
	}
	return s
}
