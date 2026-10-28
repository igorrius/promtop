// Package view decides how each metric family should be visualised and groups
// families into dashboard sections.
//
// Everything here works from the exposition alone — metric types, name
// conventions and the values themselves — so arbitrary exporters get a
// sensible dashboard. The few lists of well-known metric names only refine the
// order of Go runtime and process metrics.
package view

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
)

// Kind is the widget chosen for a family.
type Kind int

// Widgets, by the kind of value they show.
const (
	KindGauge     Kind = iota // current value + history
	KindRate                  // counter shown as per-second rate
	KindHistogram             // quantiles from buckets
	KindSummary               // client-side quantiles
	KindInfo                  // label table (build info, target info)
	KindTimestamp             // unix time shown as age / uptime
	KindUsage                 // gauge against a limit (open fds / max fds)
	KindState                 // 0/1 health flags (up, probe_success) and statesets
)

func (k Kind) String() string {
	return [...]string{"gauge", "rate", "histogram", "summary", "info", "time", "usage", "state"}[k]
}

// Panel is one widget on the dashboard.
type Panel struct {
	Family *store.Family
	Kind   Kind
	Unit   Unit
	Sym    string
	Scale  float64 // raw value × Scale is in Unit
	Title  string  // name without the section prefix

	// KindUsage: the family holding the maximum, matched per series by labels.
	// With LimitInvert the panel's value is free space and usage is
	// limit − value (node_filesystem_avail_bytes vs node_filesystem_size_bytes).
	Limit       *store.Family
	LimitInvert bool

	// Group is set for panels that pack several single-series families of a
	// crowded section into rows; Family is then synthetic and each series is
	// one member family.
	Group bool

	Priority int
	Noise    bool
}

// Name is the full metric name (a synthetic name for groups).
func (p *Panel) Name() string { return p.Family.Name }

// Format renders a raw value of this panel's primary unit.
func (p *Panel) Format(v float64) string {
	return FormatValue(v*p.scale(), p.Unit, p.Sym, p.Kind == KindRate)
}

// FormatTotal renders a counter's lifetime total (not a rate).
func (p *Panel) FormatTotal(v float64) string {
	return FormatValue(v*p.scale(), p.Unit, p.Sym, false)
}

// FormatLine renders a value of the named derived line.
func (p *Panel) FormatLine(line string, v float64) string {
	if line == "rate" && (p.Kind == KindHistogram || p.Kind == KindSummary) {
		return FormatValue(v, UnitNone, "", true)
	}
	return p.Format(v)
}

func (p *Panel) scale() float64 {
	if p.Scale == 0 {
		return 1
	}
	return p.Scale
}

// Usage returns the used fraction of series s against its limit, or NaN.
func (p *Panel) Usage(s *store.Series) float64 {
	used, lim := p.UsageValues(s)
	if math.IsNaN(used) || lim <= 0 {
		return math.NaN()
	}
	return used / lim
}

// UsageValues returns the used amount and the limit for series s (raw
// units), or NaN when s has no matching limit.
func (p *Panel) UsageValues(s *store.Series) (used, limit float64) {
	if p.Limit == nil {
		return math.NaN(), 0
	}
	ls := limitSeries(p.Limit, s)
	if ls == nil || ls.Raw <= 0 {
		return math.NaN(), 0
	}
	if p.LimitInvert {
		return ls.Raw - s.Raw, ls.Raw
	}
	return s.Raw, ls.Raw
}

// Section is a titled group of panels.
type Section struct {
	Key    string
	Title  string
	Panels []*Panel
	Hidden int // panels hidden as noise, idle or merged into another panel
}

// Options control which panels are produced.
type Options struct {
	ShowAll bool
	Filter  *regexp.Regexp
	// GroupFiltered keeps grouping crowded sections under a filter. The live
	// views list filtered metrics one by one; a snapshot of node_memory_*
	// would otherwise be dozens of panels tall.
	GroupFiltered bool
}

// Classify picks the widget for a family. It returns nil for families that
// are rendered as part of another panel.
func Classify(f *store.Family, st *store.Store) *Panel {
	u, sym, scale := UnitOf(f.Name, f.Unit)
	p := &Panel{Family: f, Unit: u, Sym: sym, Scale: scale, Priority: 100, Title: f.Name}
	if pr, ok := priority[f.Name]; ok {
		p.Priority = pr
	}
	p.Noise = noise.MatchString(f.Name) && p.Priority == 100

	switch f.Type {
	case expo.Counter:
		p.Kind = KindRate
	case expo.Histogram, expo.GaugeHistogram:
		p.Kind = KindHistogram
	case expo.Summary:
		p.Kind = KindSummary
	case expo.Info:
		p.Kind = KindInfo
	case expo.StateSet:
		p.Kind = KindState
	default:
		p.Kind = guessGaugeKind(f, p)
		if f.Type == expo.Untyped && p.Kind == KindGauge && strings.HasSuffix(f.Name, "_total") {
			p.Kind = KindRate
		}
	}
	if p.Kind == KindGauge && st != nil {
		if lim, inv := limitFor(f, st); lim != nil {
			p.Kind, p.Limit, p.LimitInvert = KindUsage, lim, inv
		}
	}
	return p
}

// Name tokens that mark a 0/1 gauge as a health flag.
var stateWords = map[string]bool{
	"up": true, "success": true, "ready": true, "healthy": true, "health": true,
	"ok": true, "alive": true, "running": true, "connected": true, "online": true,
	"enabled": true, "leader": true,
}

// Name tokens that mark a unix-time gauge.
var timeWords = map[string]bool{
	"time": true, "timestamp": true, "last": true, "epoch": true, "unixtime": true,
	"seen": true, "created": true, "boot": true, "start": true, "modified": true,
	"mtime": true, "expiry": true, "expiration": true, "expires": true, "notafter": true,
}

func guessGaugeKind(f *store.Family, p *Panel) Kind {
	n := f.Name
	live := f.Live()
	low := strings.ToLower(n)
	if strings.HasSuffix(low, "_info") || strings.HasSuffix(low, "_labels") ||
		strings.HasSuffix(low, "_annotations") || allOnesWithLabels(live) && strings.HasSuffix(low, "_version") {
		return KindInfo
	}
	toks := tokens(strings.ToLower(stripUnit(n)))
	if p.Unit == UnitSeconds || p.Unit == UnitNone {
		named := false
		for _, t := range toks {
			if timeWords[t] {
				named = true
				break
			}
		}
		// A unix time between 2001 and 2286; smaller values are durations.
		if named && len(live) > 0 {
			ok := true
			for _, s := range live {
				v := s.Raw * p.scale()
				if p.Unit == UnitNone && v > 1e12 {
					v /= 1e3 // milliseconds since the epoch
				}
				if v < 1e9 || v > 1e10 {
					ok = false
					break
				}
			}
			if ok {
				if p.Unit == UnitNone && live[0].Raw > 1e12 {
					p.Scale = 1e-3
				}
				p.Unit = UnitSeconds
				return KindTimestamp
			}
		}
	}
	if p.Unit == UnitNone && len(toks) > 0 && (n == "up" || stateWords[toks[len(toks)-1]]) && zeroOne(live) {
		return KindState
	}
	return KindGauge
}

func zeroOne(live []*store.Series) bool {
	if len(live) == 0 {
		return false
	}
	for _, s := range live {
		if s.Raw != 0 && s.Raw != 1 {
			return false
		}
	}
	return true
}

func allOnesWithLabels(live []*store.Series) bool {
	if len(live) == 0 {
		return false
	}
	for _, s := range live {
		if s.Raw != 1 || len(s.Labels) == 0 {
			return false
		}
	}
	return true
}

// StateUp reports whether a state series is in the "good" state: 1 for
// health flags, or the active member of a stateset.
func StateUp(s *store.Series) bool { return s.Raw == 1 }

// VisibleLabels returns the labels worth printing for a series: OTel scope
// labels and labels shared by every series of the family are dropped.
func VisibleLabels(f *store.Family, s *store.Series) []expo.Label {
	out := make([]expo.Label, 0, len(s.Labels))
outer:
	for _, l := range s.Labels {
		if strings.HasPrefix(l.Name, "otel_scope_") {
			continue
		}
		for _, c := range f.CommonLabels {
			if c.Name == l.Name && c.Value == l.Value {
				continue outer
			}
		}
		out = append(out, l)
	}
	return out
}

// RowLabel renders a series' visible label values, most distinguishing label
// first (the one with the most distinct values across the family).
func RowLabel(f *store.Family, s *store.Series) string {
	ls := VisibleLabels(f, s)
	if len(ls) < 2 {
		return LabelText(ls, false)
	}
	distinct := map[string]map[string]bool{}
	for _, o := range f.Live() {
		for _, l := range o.Labels {
			if distinct[l.Name] == nil {
				distinct[l.Name] = map[string]bool{}
			}
			distinct[l.Name][l.Value] = true
		}
	}
	sorted := append([]expo.Label(nil), ls...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(distinct[sorted[i].Name]) > len(distinct[sorted[j].Name])
	})
	return LabelText(sorted, false)
}

// LabelText renders labels compactly as values joined by "·" (or name=value
// when verbose).
func LabelText(ls []expo.Label, verbose bool) string {
	parts := make([]string, 0, len(ls))
	for _, l := range ls {
		if verbose {
			parts = append(parts, l.Name+"="+l.Value)
		} else {
			parts = append(parts, l.Value)
		}
	}
	return strings.Join(parts, " · ")
}

// Target extracts a short service description from target_info / go_info
// style families for the header.
func Target(st *store.Store) string {
	if f := st.Get("target_info"); f != nil {
		for _, s := range f.Live() {
			var name, ns, ver string
			for _, l := range s.Labels {
				switch l.Name {
				case "service_name", "job":
					name = l.Value
				case "service_namespace":
					ns = l.Value
				case "service_version":
					ver = l.Value
				}
			}
			if ns != "" && name != "" {
				name = ns + "/" + name
			}
			if ver != "" {
				name += " " + ver
			}
			if name != "" {
				return name
			}
		}
	}
	return ""
}
