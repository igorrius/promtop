// Package render turns a dashboard snapshot into a file: an image to send
// to a chat, a JSON document for a bot, or text. Styles decide what the
// picture looks like; formats how it is encoded.
package render

import (
	"encoding/json"
	"fmt"
	"image/color"
	"image/png"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/igorrius/promtop/internal/ui"
	"github.com/igorrius/promtop/internal/web"
)

// Format is an output encoding.
type Format string

// Formats.
const (
	PNG  Format = "png"
	SVG  Format = "svg"
	JSON Format = "json" // the data behind the picture, style-independent
	Text Format = "txt"  // plain text
	ANSI Format = "ansi" // text with terminal colours
)

// FormatFor infers a format from a file name, or returns "".
func FormatFor(path string) Format {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return PNG
	case ".svg":
		return SVG
	case ".json":
		return JSON
	case ".txt":
		return Text
	case ".ansi":
		return ANSI
	}
	return ""
}

// Binary reports whether the format must not be written to a terminal.
func (f Format) Binary() bool { return f == PNG }

// Options size the picture.
type Options struct {
	Scale float64 // 1 = 15px font; 2 suits phones and high-DPI screens
}

// Style draws snapshots. New looks — say, cards like the web dashboard —
// are new styles; the command line and formats stay the same.
type Style interface {
	Formats() []Format
	Render(w io.Writer, s ui.Snapshot, f Format, o Options) error
}

// Styles are the available styles by name.
var Styles = map[string]Style{"terminal": Terminal{}}

// StyleNames lists the styles, sorted.
func StyleNames() []string {
	out := make([]string, 0, len(Styles))
	for n := range Styles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Render writes s in format f using the named style. JSON does not depend
// on the style.
func Render(w io.Writer, s ui.Snapshot, style string, f Format, o Options) error {
	if f == JSON {
		return writeJSON(w, s)
	}
	st, ok := Styles[style]
	if !ok {
		return fmt.Errorf("unknown style %q (have %s)", style, strings.Join(StyleNames(), ", "))
	}
	if slices.Contains(st.Formats(), f) {
		return st.Render(w, s, f, o)
	}
	return fmt.Errorf("style %s cannot write %s", style, f)
}

func writeJSON(w io.Writer, s ui.Snapshot) error {
	doc := web.Snapshot(s.Store, s.Sections, s.Details, s.Interval, s.At, s.Now)
	doc.Source = s.Source
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// Terminal draws the snapshot like the terminal UI: the same panels and
// charts, rasterised cell by cell.
type Terminal struct{}

// Formats lists what Terminal writes.
func (Terminal) Formats() []Format { return []Format{PNG, SVG, Text, ANSI} }

// Render writes the snapshot.
func (Terminal) Render(w io.Writer, s ui.Snapshot, f Format, o Options) error {
	lines := s.Lines()
	switch f {
	case ANSI:
		_, err := io.WriteString(w, strings.Join(lines, "\x1b[m\n")+"\x1b[m\n")
		return err
	case Text:
		for i, l := range lines {
			lines[i] = ansi.Strip(l)
		}
		_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
		return err
	}
	scale := o.Scale
	if scale <= 0 {
		scale = 1
	}
	size := 15 * scale
	m, err := newMetrics(size, 1.2, int(16*scale))
	if err != nil {
		return err
	}
	pal := s.Palette()
	bg, fg := rgba(pal.Background), rgba(pal.Foreground)
	g := parse(lines)
	if f == SVG {
		return svg(w, g, m, size, bg, fg)
	}
	return png.Encode(w, raster(g, m, bg, fg))
}

func rgba(c color.Color) color.RGBA {
	r, g, b, a := c.RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}
