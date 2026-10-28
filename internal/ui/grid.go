package ui

import (
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/view"
)

// MinPanelWidth controls how many columns fit on a line.
const MinPanelWidth = 40

const gap = 1

// cell is a panel's position in the grid layout.
type cell struct {
	panel *view.Panel
	row   int // global panel-row index
	col   int
	line  int // first line of the panel within the full grid
	top   int // line to scroll to: the section header for a section's first row
	x, w  int // horizontal extent, for mouse hits
}

// gridLine is one line of the grid: fixed text, or line sub of panel row row.
type gridLine struct {
	text string
	row  int // -1 for text lines
	sub  int
}

// layout is the geometry of the grid. Panels are only rendered for the
// lines actually shown, which keeps exporters with hundreds of metrics cheap.
type layout struct {
	w        int
	lines    []gridLine
	cells    []cell
	rows     [][]int // row index -> indices into cells
	sections []int   // index of the first cell of every section with panels
}

func columns(w int) int {
	return max(1, (w+gap)/(MinPanelWidth+gap))
}

// buildGrid lays out sections in width w.
func (t *theme) buildGrid(sections []*view.Section, w int) layout {
	lay := layout{w: w}
	text := func(s string) { lay.lines = append(lay.lines, gridLine{text: s, row: -1}) }
	cols := columns(w)
	pw := (w - (cols-1)*gap) / cols
	for _, sec := range sections {
		headLine := len(lay.lines)
		head := " " + t.bold(t.section, "▍"+sec.Title)
		if sec.Hidden > 0 {
			head += t.paint(t.faint, "  "+itoa(sec.Hidden)+" hidden")
		}
		text(cut(head, w))
		if len(sec.Panels) > 0 {
			lay.sections = append(lay.sections, len(lay.cells))
		}
		for start := 0; start < len(sec.Panels); start += cols {
			end := min(start+cols, len(sec.Panels))
			rowIdx := len(lay.rows)
			lay.rows = append(lay.rows, nil)
			first := len(lay.lines)
			x := 0
			for i := start; i < end; i++ {
				width := pw
				if i-start == cols-1 {
					width = w - (cols-1)*(pw+gap) // last column takes the remainder
				}
				top := first
				if start == 0 {
					top = headLine
				}
				lay.rows[rowIdx] = append(lay.rows[rowIdx], len(lay.cells))
				lay.cells = append(lay.cells, cell{panel: sec.Panels[i], row: rowIdx, col: i - start, line: first, top: top, x: x, w: width})
				x += width + gap
			}
			for l := range PanelHeight {
				lay.lines = append(lay.lines, gridLine{row: rowIdx, sub: l})
			}
		}
		if len(sec.Panels) > 0 {
			text("")
		}
	}
	return lay
}

// render returns grid lines [from, to) with panel sel highlighted.
func (t *theme) render(lay layout, from, to, sel int, now time.Time) []string {
	from, to = clamp(from, len(lay.lines)), clamp(to, len(lay.lines))
	rows := map[int][][]string{}
	out := make([]string, 0, to-from)
	for _, gl := range lay.lines[from:to] {
		if gl.row < 0 {
			out = append(out, gl.text)
			continue
		}
		blocks, ok := rows[gl.row]
		if !ok {
			for _, ci := range lay.rows[gl.row] {
				c := lay.cells[ci]
				blocks = append(blocks, t.renderPanel(c.panel, c.w, ci == sel, now))
			}
			rows[gl.row] = blocks
		}
		parts := make([]string, len(blocks))
		for b := range blocks {
			parts[b] = blocks[b][gl.sub]
		}
		out = append(out, strings.Join(parts, strings.Repeat(" ", gap)))
	}
	return out
}

// move returns the new selection after moving by (dr, dc) in the grid.
func (lay layout) move(sel, dr, dc int) int {
	if len(lay.cells) == 0 || sel < 0 || sel >= len(lay.cells) {
		return 0
	}
	c := lay.cells[sel]
	if dc != 0 {
		return clamp(sel+dc, len(lay.cells)-1)
	}
	r := clamp(c.row+dr, len(lay.rows)-1)
	row := lay.rows[r]
	return row[min(c.col, len(row)-1)]
}

// section returns the first cell of the section dir sections away from sel.
func (lay layout) section(sel, dir int) int {
	if len(lay.sections) == 0 {
		return sel
	}
	cur := 0
	for i, first := range lay.sections {
		if first <= sel {
			cur = i
		}
	}
	// Moving back from inside a section goes to its start first.
	if dir < 0 && lay.sections[cur] < sel {
		return lay.sections[cur]
	}
	return lay.sections[clamp(cur+dir, len(lay.sections)-1)]
}

// hit returns the cell at grid line l and column x, or -1.
func (lay layout) hit(l, x int) int {
	for i, c := range lay.cells {
		if l >= c.line && l < c.line+PanelHeight && x >= c.x && x < c.x+c.w {
			return i
		}
	}
	return -1
}
