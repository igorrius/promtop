package ui

import (
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/view"
)

// MinPanelWidth controls how many columns fit on a line.
const MinPanelWidth = 40

// cell is a panel's position in the grid layout.
type cell struct {
	panel *view.Panel
	row   int // global panel-row index
	col   int
	line  int // first line of the panel within the full grid
}

type layout struct {
	lines []string // all grid lines (styled)
	cells []cell
	rows  [][]int // row index -> indices into cells
}

func columns(w int) int {
	c := (w + 1) / (MinPanelWidth + 1)
	if c < 1 {
		c = 1
	}
	return c
}

// buildGrid lays out sections into lines of width w. sel is the selected
// panel index (or -1).
func buildGrid(sections []*view.Section, w int, sel int, now time.Time, sty style) layout {
	var lay layout
	cols := columns(w)
	gap := 1
	pw := (w - (cols-1)*gap) / cols
	idx := 0
	for _, sec := range sections {
		head := " " + sty.c(cSection, "▍"+sec.Title)
		if sec.Hidden > 0 {
			head += sty.c(cDim, "  ("+itoa(sec.Hidden)+" more hidden · press a)")
		}
		lay.lines = append(lay.lines, head)
		for start := 0; start < len(sec.Panels); start += cols {
			end := min(start+cols, len(sec.Panels))
			rowIdx := len(lay.rows)
			lay.rows = append(lay.rows, nil)
			blocks := make([][]string, 0, cols)
			for i := start; i < end; i++ {
				width := pw
				if i-start == cols-1 {
					width = w - (cols-1)*(pw+gap) // last column takes the remainder
				}
				blocks = append(blocks, renderPanel(sec.Panels[i], width, idx == sel, now, sty))
				lay.rows[rowIdx] = append(lay.rows[rowIdx], len(lay.cells))
				lay.cells = append(lay.cells, cell{panel: sec.Panels[i], row: rowIdx, col: i - start, line: len(lay.lines)})
				idx++
			}
			for l := 0; l < PanelHeight; l++ {
				parts := make([]string, len(blocks))
				for b := range blocks {
					parts[b] = blocks[b][l]
				}
				lay.lines = append(lay.lines, strings.Join(parts, strings.Repeat(" ", gap)))
			}
		}
		lay.lines = append(lay.lines, "")
	}
	return lay
}

// move returns the new selection after moving by (dr, dc) in the grid.
func (lay layout) move(sel, dr, dc int) int {
	if len(lay.cells) == 0 {
		return 0
	}
	if sel < 0 || sel >= len(lay.cells) {
		return 0
	}
	c := lay.cells[sel]
	if dc != 0 {
		n := sel + dc
		if n < 0 {
			n = 0
		}
		if n >= len(lay.cells) {
			n = len(lay.cells) - 1
		}
		return n
	}
	r := c.row + dr
	if r < 0 {
		r = 0
	}
	if r >= len(lay.rows) {
		r = len(lay.rows) - 1
	}
	row := lay.rows[r]
	col := min(c.col, len(row)-1)
	return row[col]
}
