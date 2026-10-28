package render

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/ui"
	"github.com/igorrius/promtop/internal/view"
)

func TestParseCells(t *testing.T) {
	g := parse([]string{"a\x1b[1;38;2;1;2;3mb\x1b[m日\x1b[48;2;9;9;9mc"})
	row := g.rows[0]
	if len(row) != 5 || g.width != 5 {
		t.Fatalf("cells = %d", len(row))
	}
	if row[1].r != 'b' || !row[1].bold || row[1].fg.R != 1 || row[1].fg.B != 3 {
		t.Errorf("styled cell = %+v", row[1])
	}
	if !row[2].wide || !row[3].cont || row[2].bold {
		t.Errorf("wide glyph = %+v %+v", row[2], row[3])
	}
	if row[4].bg.R != 9 {
		t.Errorf("background = %+v", row[4])
	}
}

func TestShapes(t *testing.T) {
	for _, r := range "─│┤└╭╮╯╰▁▄█▏▍▉" {
		if shapes(r, 9, 18) == nil {
			t.Errorf("%q has no geometry", r)
		}
	}
	if n := len(shapes('⣿', 9, 18)); n != 8 {
		t.Errorf("full braille cell has %d dots", n)
	}
	if shapes('a', 9, 18) != nil {
		t.Error("letters are glyphs")
	}
}

func snapshot(t *testing.T, details bool) ui.Snapshot {
	t.Helper()
	in := `# TYPE app_requests_total counter
app_requests_total{path="/a"} %d
# TYPE app_latency_seconds histogram
app_latency_seconds_bucket{le="0.1"} %d
app_latency_seconds_bucket{le="+Inf"} %d
app_latency_seconds_sum 1
app_latency_seconds_count %d
# TYPE app_temp_celsius gauge
app_temp_celsius{room="кухня"} 21.5
app_temp_celsius{room="спальня"} 19
`
	st := store.New(20)
	for i := range 6 {
		n := strings.NewReplacer("%d", string(rune('0'+i))).Replace(in)
		fams, err := expo.Parse(strings.NewReader(n))
		if err != nil {
			t.Fatal(err)
		}
		st.Ingest(fams, time.Unix(int64(1000+10*i), 0))
	}
	secs := view.Build(st, view.Options{})
	s := ui.Snapshot{Store: st, Sections: secs, Source: "test", Interval: 10 * time.Second, At: time.Unix(1050, 0), Width: 90, Height: 8, Dark: true, Now: time.Unix(1050, 0)}
	if details {
		for _, sec := range secs {
			s.Details = append(s.Details, sec.Panels...)
		}
	}
	return s
}

func TestRenderFormats(t *testing.T) {
	for _, details := range []bool{false, true} {
		s := snapshot(t, details)
		var buf bytes.Buffer
		if err := Render(&buf, s, "terminal", PNG, Options{Scale: 1}); err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(&buf)
		if err != nil {
			t.Fatal(err)
		}
		lines := s.Lines()
		m, _ := newMetrics(15, 1.2, 16)
		if want := len(lines)*m.ch + 2*m.pad; img.Bounds().Dy() != want {
			t.Errorf("details=%v: height %d, want %d", details, img.Bounds().Dy(), want)
		}

		buf.Reset()
		if err := Render(&buf, s, "terminal", SVG, Options{Scale: 2}); err != nil {
			t.Fatal(err)
		}
		d := xml.NewDecoder(&buf)
		for {
			if _, err := d.Token(); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				t.Fatalf("svg is not XML: %v", err)
			}
		}

		buf.Reset()
		if err := Render(&buf, s, "terminal", JSON, Options{}); err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
			t.Fatalf("json: %v", err)
		}
		if details && doc["details"] == nil {
			t.Error("details missing from JSON")
		}

		buf.Reset()
		if err := Render(&buf, s, "terminal", Text, Options{}); err != nil {
			t.Fatal(err)
		}
		if out := buf.String(); strings.Contains(out, "\x1b") || !strings.Contains(out, "кухня") {
			t.Errorf("text output:\n%s", out)
		}
	}
	if err := Render(io.Discard, snapshot(t, false), "cards", PNG, Options{}); err == nil {
		t.Error("unknown style should fail")
	}
}

func TestFormatFor(t *testing.T) {
	for in, want := range map[string]Format{"a.PNG": PNG, "x/y.svg": SVG, "d.json": JSON, "t.txt": Text, "-": "", "f.gif": ""} {
		if got := FormatFor(in); got != want {
			t.Errorf("FormatFor(%q) = %q, want %q", in, got, want)
		}
	}
}
