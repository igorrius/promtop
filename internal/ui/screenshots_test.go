package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/igorrius/promtop/internal/scrape"
)

// TestScreenshots writes the frames behind the README screenshots as HTML
// pages styled like a terminal window. It scrapes a live target, so it only
// runs when asked to:
//
//	go run ./examples/demo &
//	PROMTOP_SHOTS=dir go test ./internal/ui -run Screenshots
//
// scripts/screenshots.sh does that and turns the pages into PNGs and a GIF.
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("PROMTOP_SHOTS")
	if dir == "" {
		t.Skip("set PROMTOP_SHOTS to a directory to write screenshot frames")
	}
	target := os.Getenv("PROMTOP_SHOTS_TARGET")
	if target == "" {
		target = "localhost:8080"
	}
	rawURL, _, err := scrape.NormalizeTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	const step = 250 * time.Millisecond
	a := New(Config{
		Source:   &scrape.Source{URL: rawURL, Timeout: time.Second},
		Interval: step, History: 300, Color: true,
	})
	a.interval = 2 * time.Second // as shown in the header
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 38})
	scrapeOnce := func() {
		a.Update(scrapeMsg(a.cfg.Source.Scrape(t.Context())))
		if a.lastErr != nil {
			t.Fatal(a.lastErr)
		}
		time.Sleep(step)
	}
	// Enough history for the sparklines to fill a panel.
	for range 120 {
		scrapeOnce()
	}

	write := func(name string) {
		t.Helper()
		page := framePage(a.frame(), a.th)
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(page), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	selectPanel := func(name string) {
		t.Helper()
		for i, c := range a.lay.cells {
			if c.panel.Name() == name {
				a.sel, a.selName = i, name
				a.refresh()
				return
			}
		}
		t.Fatalf("no panel %s", name)
	}

	// Still screenshots.
	write("grid")
	selectPanel("demo_http_request_duration_seconds")
	press(a, "enter")
	write("detail-histogram")
	press(a, "esc")
	selectPanel("demo_disk_avail_bytes")
	press(a, "enter")
	write("detail-usage")
	press(a, "esc", "?")
	write("help")
	press(a, "x")

	// The animation: live updates, navigation, details, filter.
	n := 0
	frame := func(hold int) {
		for range hold {
			write(fmt.Sprintf("anim-%03d", n))
			n++
		}
	}
	a.sel, a.scroll = 0, 0
	a.refresh()
	for range 6 {
		scrapeOnce()
		frame(1)
	}
	for _, k := range []string{"right", "down", "down", "right", "down"} {
		press(a, k)
		scrapeOnce()
		frame(2)
	}
	selectPanel("demo_http_request_duration_seconds")
	frame(2)
	press(a, "enter")
	for range 4 {
		scrapeOnce()
		frame(1)
	}
	press(a, "right")
	frame(3)
	press(a, "down")
	frame(3)
	press(a, "esc")
	frame(2)
	press(a, "/")
	for _, r := range "disk" {
		press(a, string(r))
		frame(1)
	}
	frame(3)
	press(a, "enter", "enter")
	frame(4)
	press(a, "esc", "esc", "c")
	frame(2)
	t.Logf("wrote %d animation frames to %s", n, dir)
}

// Page geometry; scripts/screenshots.sh sizes the browser window to match.
const (
	shotFont   = 15 // px
	shotLine   = 17 // px: just below the glyph height, so box drawing joins up
	shotPad    = 18
	shotBar    = 34
	shotMargin = 28
)

// framePage renders ANSI-styled lines as an HTML terminal window.
func framePage(lines []string, th *theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><meta charset="utf-8"><style>
html,body{margin:0;background:#0f1017}
.win{margin:%dpx;display:inline-block;background:#1a1b26;border-radius:10px;box-shadow:0 12px 40px #0008;border:1px solid #2a2e42}
.bar{height:%dpx;display:flex;align-items:center;gap:8px;padding-left:14px}
.bar i{width:12px;height:12px;border-radius:50%%;display:block}
.c{display:inline-block;width:1ch}
pre{margin:0;padding:0 %dpx %dpx;font:%dpx/%dpx "DejaVu Sans Mono",monospace;color:%s;white-space:pre}
</style><div class="win"><div class="bar"><i style="background:#f7768e"></i><i style="background:#e0af68"></i><i style="background:#9ece6a"></i></div><pre>`,
		shotMargin, shotBar, shotPad, shotPad, shotFont, shotLine, hex(th.value))
	for _, l := range lines {
		b.WriteString(ansiHTML(l))
		b.WriteByte('\n')
	}
	b.WriteString("</pre></div>")
	return b.String()
}

func hex(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, bl, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, bl>>8)
}

// ansiHTML converts the SGR sequences promtop emits (bold, 24-bit colours,
// resets) into styled spans.
func ansiHTML(s string) string {
	var b strings.Builder
	var fg, bg string
	bold, open := false, false
	flush := func() {
		if open {
			b.WriteString("</span>")
			open = false
		}
		var css []string
		if fg != "" {
			css = append(css, "color:"+fg)
		}
		if bg != "" {
			css = append(css, "background:"+bg)
		}
		if bold {
			css = append(css, "font-weight:bold")
		}
		if len(css) > 0 {
			b.WriteString(`<span style="` + strings.Join(css, ";") + `">`)
			open = true
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
			switch s[i] {
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			case '&':
				b.WriteString("&amp;")
			default:
				// Browsers take braille from a fallback font with its own
				// advance; pin it to the cell width like a terminal does.
				if r, n := utf8.DecodeRuneInString(s[i:]); r >= 0x2800 && r <= 0x28ff {
					b.WriteString(`<span class="c">` + string(r) + `</span>`)
					i += n - 1
					break
				}
				b.WriteByte(s[i])
			}
			continue
		}
		j := i + 2
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j >= len(s) {
			break
		}
		if s[j] == 'm' {
			ps := strings.Split(s[i+2:j], ";")
			for k := 0; k < len(ps); k++ {
				switch ps[k] {
				case "", "0":
					fg, bg, bold = "", "", false
				case "1":
					bold = true
				case "22":
					bold = false
				case "39":
					fg = ""
				case "49":
					bg = ""
				case "38", "48":
					if k+4 < len(ps) && ps[k+1] == "2" {
						var r, g, bl int
						_, _ = fmt.Sscan(ps[k+2], &r)
						_, _ = fmt.Sscan(ps[k+3], &g)
						_, _ = fmt.Sscan(ps[k+4], &bl)
						c := fmt.Sprintf("#%02x%02x%02x", r, g, bl)
						if ps[k] == "38" {
							fg = c
						} else {
							bg = c
						}
						k += 4
					}
				}
			}
			flush()
		}
		i = j
	}
	if open {
		b.WriteString("</span>")
	}
	return b.String()
}
