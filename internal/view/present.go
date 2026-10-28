package view

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
)

// Presentation shared by every front end: what a panel says in its title,
// which line its rows plot and in which order the rows come.

// Tone is the emphasis of a headline or value.
type Tone int

// Tones, mapped to colours by each front end.
const (
	ToneValue Tone = iota
	ToneOK
	ToneWarn
	ToneErr
	ToneAccent
)

// UsageTone grades a used fraction: green, amber from 70%, red from 90%.
func UsageTone(f float64) Tone {
	switch {
	case f >= 0.9:
		return ToneErr
	case f >= 0.7:
		return ToneWarn
	}
	return ToneOK
}

// Quantiled reports whether the panel's series carry quantile lines.
func (p *Panel) Quantiled() bool { return p.Kind == KindHistogram || p.Kind == KindSummary }

// RowLine is the line plotted per row: a tail quantile for histograms and
// summaries, otherwise the primary line ("").
func (p *Panel) RowLine() string {
	if !p.Quantiled() {
		return ""
	}
	for _, s := range p.Family.Live() {
		if q := QuantileLines(s, 1); len(q) > 0 {
			return q[0]
		}
		if len(s.Lines) > 0 {
			return s.Lines[0].Name
		}
	}
	return "p99"
}

// Ring returns the history a row of s plots for line ("" = primary).
func Ring(s *store.Series, line string) *store.Ring {
	if line != "" {
		return s.Line(line)
	}
	return s.Primary()
}

// Rows orders the live series for display: state flags with problems first,
// groups of gauges in name order (they read like a table), everything else
// by the plotted value, largest first.
func (p *Panel) Rows() []*store.Series {
	out := p.Family.Live()
	switch {
	case p.Kind == KindState && p.Family.Type != expo.StateSet:
		sort.SliceStable(out, func(i, j int) bool { return !StateUp(out[i]) && StateUp(out[j]) })
	case p.Kind == KindUsage:
		u := func(s *store.Series) float64 {
			if v := p.Usage(s); !math.IsNaN(v) {
				return v
			}
			return -1
		}
		sort.SliceStable(out, func(i, j int) bool { return u(out[i]) > u(out[j]) })
	case p.Kind == KindTimestamp:
		sort.SliceStable(out, func(i, j int) bool { return out[i].Raw > out[j].Raw })
	case p.Kind == KindInfo || p.Kind == KindState || p.Group && p.Kind == KindGauge:
	default:
		line := p.RowLine()
		val := func(s *store.Series) float64 {
			if v := Ring(s, line).Last(); !math.IsNaN(v) {
				return v
			}
			return math.Inf(-1)
		}
		sort.SliceStable(out, func(i, j int) bool { return val(out[i]) > val(out[j]) })
	}
	return out
}

// Badge describes the widget in the panel's bottom border.
func (p *Panel) Badge() string {
	live := len(p.Family.Live())
	b := p.Kind.String()
	if p.Group {
		b = "group · " + b
	}
	if p.Quantiled() && live > 1 {
		b += " " + p.RowLine()
	}
	if live > 1 {
		noun := " series"
		if p.Group {
			noun = " metrics"
		}
		b += " · " + strconv.Itoa(live) + noun
	}
	return b
}

// UnitName names the panel's unit for titles (" · bytes").
func (p *Panel) UnitName() string {
	switch p.Unit {
	case UnitBytes:
		return " · bytes"
	case UnitSeconds:
		return " · seconds"
	case UnitRatio:
		return " · ratio"
	case UnitPercent:
		return " · percent"
	case UnitOther:
		return " · " + p.Sym
	}
	return ""
}

// Describe is the detail-view subtitle: type, widget and unit.
func (p *Panel) Describe() string {
	if p.Group {
		return "group of " + strconv.Itoa(len(p.Family.Live())) + " " + p.Kind.String() + " metrics" + p.UnitName()
	}
	kind := string(p.Family.Type)
	if w := p.Kind.String(); w != kind {
		kind += " · " + w
	}
	return kind + p.UnitName()
}

// Headline is the summary value in the panel's title.
func (p *Panel) Headline(now time.Time) (string, Tone) {
	live := p.Family.Live()
	if len(live) == 0 {
		return "", ToneValue
	}
	switch p.Kind {
	case KindInfo:
		return "", ToneValue
	case KindTimestamp:
		if len(live) == 1 {
			return p.Age(live[0], now), ToneValue
		}
		return "", ToneValue
	case KindState:
		if p.Family.Type == expo.StateSet {
			var on []string
			for _, s := range live {
				if s.Raw == 1 {
					on = append(on, RowLabel(p.Family, s))
				}
			}
			return strings.Join(on, ", "), ToneAccent
		}
		up := 0
		for _, s := range live {
			if StateUp(s) {
				up++
			}
		}
		tone := ToneOK
		if up < len(live) {
			tone = ToneErr
		}
		if len(live) == 1 {
			if up == 1 {
				return "up", tone
			}
			return "down", tone
		}
		return strconv.Itoa(up) + "/" + strconv.Itoa(len(live)) + " up", tone
	case KindRate:
		if p.Group {
			return "", ToneValue // different counters do not add up
		}
		sum, ok := 0.0, false
		for _, s := range live {
			if v := s.Primary().Last(); !math.IsNaN(v) {
				sum, ok = sum+v, true
			}
		}
		switch {
		case !ok:
			return "", ToneValue
		case len(live) > 1:
			return "Σ " + p.Format(sum), ToneValue
		}
		return p.Format(sum), ToneValue
	case KindHistogram, KindSummary:
		if len(live) > 1 {
			return "", ToneValue
		}
		l := p.RowLine()
		if v := live[0].Line(l).Last(); !math.IsNaN(v) {
			return l + " " + p.FormatLine(l, v), ToneValue
		}
		return "", ToneValue
	case KindUsage:
		worst := math.NaN()
		for _, s := range live {
			if u := p.Usage(s); !math.IsNaN(u) && (math.IsNaN(worst) || u > worst) {
				worst = u
			}
		}
		if math.IsNaN(worst) {
			return "", ToneValue
		}
		txt := Percent(worst * 100)
		if len(live) > 1 {
			txt = "max " + txt
		}
		return txt, UsageTone(worst)
	}
	if len(live) == 1 {
		return p.Format(live[0].Raw), ToneValue
	}
	return "", ToneValue
}

// Time converts a timestamp series' value to a time.
func (p *Panel) Time(s *store.Series) time.Time {
	return time.Unix(0, int64(s.Raw*p.scale()*1e9))
}

// Age renders a timestamp relative to now: "up 3d04h", "5m ago", "in 2h".
func (p *Panel) Age(s *store.Series, now time.Time) string {
	d := now.Sub(p.Time(s))
	name := strings.ToLower(p.Name())
	switch {
	case strings.Contains(name, "start") || strings.Contains(name, "boot"):
		return "up " + Age(d)
	case d >= 0:
		return Age(d) + " ago"
	}
	return "in " + Age(-d)
}

// QuantileLines returns up to n quantile line names of s (not rate/avg),
// preferring a tail quantile, the median and the maximum, highest first.
func QuantileLines(s *store.Series, n int) []string {
	var all []string
	for _, l := range s.Lines {
		if l.Name != "rate" && l.Name != "avg" {
			all = append(all, l.Name)
		}
	}
	var out []string
	add := func(name string) {
		if len(out) < n && slices.Contains(all, name) && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	for _, tail := range []string{"p999", "p99", "p95", "p90", "p75"} {
		if slices.Contains(all, tail) {
			add(tail)
			break
		}
	}
	add("p90")
	add("p50")
	add("max")
	for _, a := range all {
		add(a)
	}
	rank := func(name string) float64 {
		switch name {
		case "max":
			return 2
		case "min":
			return -1
		}
		// p5 = 5%, p50 = 50%, p999 = 99.9%
		d := strings.TrimPrefix(name, "p")
		if len(d) > 2 {
			d = d[:2] + "." + d[2:]
		}
		v, _ := strconv.ParseFloat(d, 64)
		return v / 100
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) > rank(out[j]) })
	return out
}
