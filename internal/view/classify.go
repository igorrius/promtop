// Package view decides how each metric family should be visualised and groups
// families into dashboard sections.
package view

import (
	"regexp"
	"sort"
	"strings"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
)

// Kind is the widget chosen for a family.
type Kind int

const (
	KindGauge     Kind = iota // current value + history
	KindRate                  // counter shown as per-second rate
	KindHistogram             // quantiles from buckets
	KindSummary               // client-side quantiles
	KindInfo                  // label table (build info, target info)
	KindTimestamp             // unix time shown as age / uptime
	KindUsage                 // gauge against a limit (open fds / max fds)
)

func (k Kind) String() string {
	return [...]string{"gauge", "rate", "histogram", "summary", "info", "time", "usage"}[k]
}

// Panel is one widget on the dashboard.
type Panel struct {
	Family   *store.Family
	Kind     Kind
	Unit     Unit
	Sym      string
	Title    string        // name without the section prefix
	Limit    *store.Family // KindUsage: the family holding the maximum
	Priority int
	Noise    bool
}

// Name is the full metric name.
func (p *Panel) Name() string { return p.Family.Name }

// Format renders a value of this panel's primary unit.
func (p *Panel) Format(v float64) string {
	return FormatValue(v, p.Unit, p.Sym, p.Kind == KindRate)
}

// FormatLine renders a value of the named derived line.
func (p *Panel) FormatLine(line string, v float64) string {
	if line == "rate" && (p.Kind == KindHistogram || p.Kind == KindSummary) {
		return FormatValue(v, UnitNone, "", true)
	}
	return p.Format(v)
}

// Section is a titled group of panels.
type Section struct {
	Key    string
	Title  string
	Panels []*Panel
	Hidden int // panels hidden as noise
}

// Options control which panels are produced.
type Options struct {
	ShowAll bool
	Filter  *regexp.Regexp
}

var sectionTitles = map[string]string{
	"go":       "Go runtime",
	"process":  "Process",
	"promhttp": "Metrics handler",
	"http":     "HTTP",
	"grpc":     "gRPC",
	"db":       "Database",
	"rpc":      "RPC",
}

// Infrastructure sections go after application ones, in this order.
var tailSections = map[string]int{"go": 1, "process": 2, "promhttp": 3, "otel": 4}

// Priority of well-known metrics within their section (lower first). Covers
// both client_golang collectors and OpenTelemetry runtime semantic conventions.
var priority = map[string]int{
	"go_goroutines":                   1,
	"go_goroutine_count":              1,
	"go_threads":                      2,
	"go_memstats_heap_alloc_bytes":    3,
	"go_memory_used_bytes":            3,
	"go_memstats_heap_inuse_bytes":    4,
	"go_memory_gc_goal_bytes":         5,
	"go_memstats_alloc_bytes_total":   6,
	"go_memory_allocated_bytes_total": 6,
	"go_memory_allocations_total":     7,
	"go_memstats_mallocs_total":       7,
	"go_gc_duration_seconds":          8,
	"go_schedule_duration_seconds":    9,
	"go_memstats_next_gc_bytes":       10,
	"go_memstats_stack_inuse_bytes":   11,
	"go_sched_gomaxprocs_threads":     12,
	"go_processor_limit":              12,
	"go_gc_gogc_percent":              13,
	"go_config_gogc_percent":          13,
	"go_gc_gomemlimit_bytes":          14,
	"go_memory_limit_bytes":           14,
	"go_info":                         15,

	"process_cpu_seconds_total":            1,
	"process_resident_memory_bytes":        2,
	"process_open_fds":                     3,
	"process_start_time_seconds":           4,
	"process_virtual_memory_bytes":         5,
	"process_network_receive_bytes_total":  6,
	"process_network_transmit_bytes_total": 7,
}

// Families that are merged into another panel or rarely useful.
var noise = regexp.MustCompile(`^(go_memstats_|go_sched_|go_gc_|go_cgo_|promhttp_|process_virtual_memory_max_bytes$|process_max_fds$)`)

// Families shown in the header instead of the grid.
var headerOnly = map[string]bool{"target_info": true}

// Build turns the store into dashboard sections.
func Build(st *store.Store, opt Options) []*Section {
	byKey := map[string]*Section{}
	var sections []*Section
	for _, f := range st.Families {
		if len(f.Live()) == 0 || headerOnly[f.Name] {
			continue
		}
		p := Classify(f, st)
		if p == nil {
			continue
		}
		key := sectionKey(f.Name)
		sec := byKey[key]
		if sec == nil {
			title := sectionTitles[key]
			if title == "" {
				title = key
			}
			sec = &Section{Key: key, Title: title}
			byKey[key] = sec
			sections = append(sections, sec)
		}
		if opt.Filter != nil && !opt.Filter.MatchString(f.Name) {
			continue
		}
		if p.Noise && !opt.ShowAll && opt.Filter == nil {
			sec.Hidden++
			continue
		}
		sec.Panels = append(sec.Panels, p)
	}
	out := sections[:0]
	for _, s := range sections {
		sort.SliceStable(s.Panels, func(i, j int) bool { return s.Panels[i].Priority < s.Panels[j].Priority })
		if len(s.Panels) > 0 || (s.Hidden > 0 && opt.Filter == nil) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := tailSections[out[i].Key], tailSections[out[j].Key]
		if ti != tj {
			return ti < tj
		}
		return false // keep exposition order for application sections
	})
	return out
}

func sectionKey(name string) string {
	if i := strings.IndexByte(name, '_'); i > 0 {
		return name[:i]
	}
	return name
}

// Classify picks the widget for a family. It returns nil for families that
// are rendered as part of another panel.
func Classify(f *store.Family, st *store.Store) *Panel {
	u, sym := UnitOf(f.Name)
	p := &Panel{Family: f, Unit: u, Sym: sym, Priority: 100}
	if pr, ok := priority[f.Name]; ok {
		p.Priority = pr
	}
	p.Noise = noise.MatchString(f.Name) && p.Priority == 100
	p.Title = f.Name
	if k := sectionKey(f.Name); k != f.Name {
		p.Title = strings.TrimPrefix(f.Name, k+"_")
	}

	switch f.Type {
	case expo.Counter:
		p.Kind = KindRate
	case expo.Histogram, expo.GaugeHistogram:
		p.Kind = KindHistogram
	case expo.Summary:
		p.Kind = KindSummary
	case expo.Info, expo.StateSet:
		p.Kind = KindInfo
	default:
		p.Kind = guessGaugeKind(f)
		if f.Type == expo.Untyped && strings.HasSuffix(f.Name, "_total") {
			p.Kind = KindRate
		}
	}
	if p.Kind == KindGauge && st != nil && len(f.Live()) == 1 {
		if lim := limitFor(f.Name, st); lim != nil {
			p.Kind, p.Limit = KindUsage, lim
		}
	}
	return p
}

func guessGaugeKind(f *store.Family) Kind {
	n := f.Name
	if strings.HasSuffix(n, "_info") {
		return KindInfo
	}
	if strings.HasSuffix(n, "_seconds") && (strings.Contains(n, "timestamp") ||
		strings.HasSuffix(n, "start_time_seconds") || strings.HasSuffix(n, "_time_seconds")) {
		// Values far in the past would be durations, not timestamps.
		for _, s := range f.Live() {
			if s.Raw > 1e9 {
				return KindTimestamp
			}
		}
	}
	// Info-style gauges: every series is a constant 1 and carries labels.
	live := f.Live()
	if len(live) > 0 && strings.HasSuffix(n, "_build_info") {
		return KindInfo
	}
	return KindGauge
}

// limitFor finds a sibling family that is the maximum for this gauge, such as
// process_max_fds for process_open_fds.
func limitFor(name string, st *store.Store) *store.Family {
	var cands []string
	if strings.Contains(name, "_open_") {
		cands = append(cands, strings.Replace(name, "_open_", "_max_", 1))
	}
	if strings.Contains(name, "_used_") {
		cands = append(cands, strings.Replace(name, "_used_", "_limit_", 1),
			strings.Replace(name, "_used_", "_max_", 1))
	}
	cands = append(cands, name+"_max", name+"_limit")
	for _, c := range cands {
		f := st.Get(c)
		if f == nil {
			continue
		}
		live := f.Live()
		// A limit of MaxInt64-ish means "unlimited" (e.g. GOMEMLIMIT unset).
		if len(live) == 1 && live[0].Raw > 0 && live[0].Raw < 1e18 {
			return f
		}
	}
	return nil
}

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
