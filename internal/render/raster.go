package render

import (
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// DejaVu Sans Mono (Bitstream Vera licence, see fonts/LICENSE): wide glyph
// coverage, and free to embed.
var (
	//go:embed fonts/DejaVuSansMono.ttf
	regularTTF []byte
	//go:embed fonts/DejaVuSansMono-Bold.ttf
	boldTTF []byte

	fontsOnce        sync.Once
	regular, boldFnt *opentype.Font
	fontsErr         error
)

func loadFonts() error {
	fontsOnce.Do(func() {
		if regular, fontsErr = opentype.Parse(regularTTF); fontsErr != nil {
			return
		}
		boldFnt, fontsErr = opentype.Parse(boldTTF)
	})
	return fontsErr
}

// metrics is the pixel geometry of a cell grid.
type metrics struct {
	cw, ch   int // cell size
	baseline int // from the cell top
	pad      int // around the grid
	reg, bld font.Face
}

func newMetrics(size, lineHeight float64, pad int) (metrics, error) {
	if err := loadFonts(); err != nil {
		return metrics{}, err
	}
	opts := &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull}
	reg, err := opentype.NewFace(regular, opts)
	if err != nil {
		return metrics{}, err
	}
	bld, err := opentype.NewFace(boldFnt, opts)
	if err != nil {
		return metrics{}, err
	}
	adv, _ := reg.GlyphAdvance('0')
	fm := reg.Metrics()
	asc, desc := fm.Ascent.Ceil(), fm.Descent.Ceil()
	m := metrics{cw: max(1, adv.Round()), ch: int(math.Round(size * lineHeight)), pad: pad, reg: reg, bld: bld}
	m.ch = max(m.ch, asc+desc)
	m.baseline = (m.ch-(asc+desc))/2 + asc
	return m, nil
}

// raster draws a grid into an image.
func raster(g grid, m metrics, bg, fg color.RGBA) *image.RGBA {
	w := g.width*m.cw + 2*m.pad
	h := len(g.rows)*m.ch + 2*m.pad
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	for y, row := range g.rows {
		for x, c := range row {
			if c.bg.A == 0 {
				continue
			}
			cw := m.cw
			if c.wide {
				cw *= 2
			}
			r := image.Rect(m.pad+x*m.cw, m.pad+y*m.ch, m.pad+x*m.cw+cw, m.pad+(y+1)*m.ch)
			draw.Draw(img, r, image.NewUniform(c.bg), image.Point{}, draw.Src)
		}
	}
	for y, row := range g.rows {
		for x, c := range row {
			if c.cont || c.r == ' ' || c.r == 0 {
				continue
			}
			col := c.fg
			if col.A == 0 {
				col = fg
			}
			x0, y0 := m.pad+x*m.cw, m.pad+y*m.ch
			if sh := shapes(c.r, float64(m.cw), float64(m.ch)); sh != nil {
				drawShapes(img, sh, float64(x0), float64(y0), col)
				continue
			}
			face := m.reg
			if c.bold {
				face = m.bld
			}
			d := font.Drawer{Dst: img, Src: image.NewUniform(col), Face: face, Dot: fixed.P(x0, y0+m.baseline)}
			d.DrawString(string(c.r))
		}
	}
	return img
}

func drawShapes(img *image.RGBA, sh []shape, x0, y0 float64, col color.RGBA) {
	src := image.NewUniform(col)
	for _, s := range sh {
		switch s.kind {
		case rectShape:
			// Snap to whole pixels so neighbouring cells join without seams.
			r := image.Rect(int(math.Round(x0+s.x)), int(math.Round(y0+s.y)),
				int(math.Round(x0+s.x+s.w)), int(math.Round(y0+s.y+s.h)))
			draw.Draw(img, r, src, image.Point{}, draw.Over)
		case dotShape:
			fill(img, src, circle(x0+s.cx, y0+s.cy, s.r))
		case arcShape:
			fill(img, src, arcRing(x0+s.cx, y0+s.cy, s.rx, s.ry, s.t, s.q))
		}
	}
}

// fill rasterises a closed polygon with anti-aliasing.
func fill(img *image.RGBA, src image.Image, pts [][2]float64) {
	if len(pts) < 3 {
		return
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		minX, minY = math.Min(minX, p[0]), math.Min(minY, p[1])
		maxX, maxY = math.Max(maxX, p[0]), math.Max(maxY, p[1])
	}
	b := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX))+1, int(math.Ceil(maxY))+1).Intersect(img.Bounds())
	if b.Empty() {
		return
	}
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	ox, oy := float32(b.Min.X), float32(b.Min.Y)
	z.MoveTo(float32(pts[0][0])-ox, float32(pts[0][1])-oy)
	for _, p := range pts[1:] {
		z.LineTo(float32(p[0])-ox, float32(p[1])-oy)
	}
	z.ClosePath()
	z.Draw(img, b, src, image.Point{})
}

func circle(cx, cy, r float64) [][2]float64 {
	const n = 16
	pts := make([][2]float64, n)
	for i := range n {
		a := 2 * math.Pi * float64(i) / n
		pts[i] = [2]float64{cx + r*math.Cos(a), cy + r*math.Sin(a)}
	}
	return pts
}

// arcStart is the first angle of each quadrant, clockwise on screen.
var arcStart = [4]float64{math.Pi, 1.5 * math.Pi, 0, 0.5 * math.Pi}

// arcPoints samples a quarter ellipse of quadrant q.
func arcPoints(cx, cy, rx, ry float64, q int) [][2]float64 {
	const n = 12
	pts := make([][2]float64, n+1)
	for i := 0; i <= n; i++ {
		a := arcStart[q] + 0.5*math.Pi*float64(i)/n
		pts[i] = [2]float64{cx + rx*math.Cos(a), cy + ry*math.Sin(a)}
	}
	return pts
}

// arcRing is a quarter-ellipse stroke of width t as a polygon.
func arcRing(cx, cy, rx, ry, t float64, q int) [][2]float64 {
	outer := arcPoints(cx, cy, rx+t/2, ry+t/2, q)
	inner := arcPoints(cx, cy, max(0, rx-t/2), max(0, ry-t/2), q)
	for i, j := 0, len(inner)-1; i < j; i, j = i+1, j-1 {
		inner[i], inner[j] = inner[j], inner[i]
	}
	return append(outer, inner...)
}
