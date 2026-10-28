package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/igorrius/promtop/internal/view"
)

// rampSteps is the number of shades in a chart gradient.
const rampSteps = 8

// theme holds the palette. Colours are true colour; Bubble Tea (and the
// colorprofile writer for --once) downsample them to what the terminal
// supports, or strip them for NO_COLOR.
type theme struct {
	dark bool
	fit  bool // stretch histories across charts (snapshots)

	bg, text                                          color.Color // for images; terminals use their own
	accent, border, title, dim, faint, value, section color.Color
	ok, warn, err, pillFg                             color.Color
	kinds                                             map[view.Kind]color.Color

	ramps map[view.Kind][]string // SGR prefixes, faint → bright
	usage []string               // SGR prefixes, ok → warn → err
	sgr   map[color.Color]string
}

func newTheme(dark bool) *theme {
	ld := lipgloss.LightDark(dark)
	c := lipgloss.Color
	t := &theme{
		dark:    dark,
		bg:      ld(c("#FFFFFF"), c("#1A1B26")),
		text:    ld(c("#343B58"), c("#C0CAF5")),
		accent:  ld(c("#2E59C9"), c("#7AA2F7")),
		border:  ld(c("#B8BDD1"), c("#4B5274")),
		title:   ld(c("#343B58"), c("#C0CAF5")),
		dim:     ld(c("#6A6F8A"), c("#737AA2")),
		faint:   ld(c("#9DA1B8"), c("#4A5073")),
		value:   ld(c("#1A1B26"), c("#E6E9FF")),
		section: ld(c("#B15C00"), c("#FF9E64")),
		ok:      ld(c("#387A1C"), c("#9ECE6A")),
		warn:    ld(c("#8F5E15"), c("#E0AF68")),
		err:     ld(c("#C53B53"), c("#F7768E")),
		pillFg:  ld(c("#FFFFFF"), c("#16161E")),
		kinds: map[view.Kind]color.Color{
			view.KindRate:      ld(c("#0F7FA8"), c("#7DCFFF")),
			view.KindGauge:     ld(c("#3E7A1E"), c("#9ECE6A")),
			view.KindHistogram: ld(c("#966300"), c("#E0AF68")),
			view.KindSummary:   ld(c("#7847BD"), c("#BB9AF7")),
			view.KindInfo:      ld(c("#5A607F"), c("#A9B1D6")),
			view.KindTimestamp: ld(c("#167D6E"), c("#73DACA")),
			view.KindUsage:     ld(c("#3E7A1E"), c("#9ECE6A")),
			view.KindState:     ld(c("#387A1C"), c("#9ECE6A")),
		},
		sgr: map[color.Color]string{},
	}
	t.ramps = map[view.Kind][]string{}
	for k, kc := range t.kinds {
		// Fade from the background towards the full colour.
		start := lipgloss.Darken(kc, 0.55)
		if !dark {
			start = lipgloss.Lighten(kc, 0.55)
		}
		t.ramps[k] = t.prefixes(lipgloss.Blend1D(rampSteps, start, kc))
	}
	t.usage = t.prefixes(lipgloss.Blend1D(rampSteps, t.ok, t.warn, t.err))
	return t
}

func (t *theme) prefixes(cs []color.Color) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = t.fg(c)
	}
	return out
}

// fg returns the SGR sequence selecting foreground colour c.
func (t *theme) fg(c color.Color) string {
	if s, ok := t.sgr[c]; ok {
		return s
	}
	s := ansi.NewStyle().ForegroundColor(c).String()
	t.sgr[c] = s
	return s
}

const reset = "\x1b[m"

// paint colours s; empty strings stay empty so widths add up.
func (t *theme) paint(c color.Color, s string) string {
	if s == "" {
		return s
	}
	return t.fg(c) + s + reset
}

// bold colours s in bold.
func (t *theme) bold(c color.Color, s string) string {
	if s == "" {
		return s
	}
	return "\x1b[1m" + t.fg(c) + s + reset
}

// tone maps a presentation tone to a colour.
func (t *theme) tone(v view.Tone) color.Color {
	switch v {
	case view.ToneOK:
		return t.ok
	case view.ToneWarn:
		return t.warn
	case view.ToneErr:
		return t.err
	case view.ToneAccent:
		return t.accent
	}
	return t.value
}

// rampAt returns the gradient prefix for fraction f of kind k.
func (t *theme) rampAt(k view.Kind, f float64) string {
	return pick(t.ramps[k], f)
}

// usageAt returns the ok → err gradient prefix for a used fraction.
func (t *theme) usageAt(f float64) string {
	// Stay green until half full, then shift through amber to red.
	return pick(t.usage, (f-0.5)*2)
}

func pick(ramp []string, f float64) string {
	if len(ramp) == 0 {
		return ""
	}
	i := int(f * float64(len(ramp)-1))
	return ramp[clamp(i, len(ramp)-1)]
}
