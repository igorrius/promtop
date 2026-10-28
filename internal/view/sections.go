package view

import (
	"regexp"
	"sort"
	"strings"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
)

// Layout tuning. A section with more visible panels than maxSection is split
// by the next name token; a section with at least groupSection panels packs
// groupMembers or more single-series metrics of one kind and unit into a
// group panel.
const (
	maxSection   = 16
	minSection   = 4
	maxDepth     = 3
	groupSection = 6
	groupMembers = 3
)

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
// Metrics not listed keep their exposition order.
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

// Low-level Go runtime and handler internals, hidden until "show all".
var noise = regexp.MustCompile(`^(go_memstats_|go_sched_|go_gc_|go_cgo_|promhttp_|process_virtual_memory_max_bytes$)`)

// Families shown in the header instead of the grid.
var headerOnly = map[string]bool{"target_info": true}

// node is a section under construction: panels sharing the first depth
// tokens of their names.
type node struct {
	key    string // name prefix ("node_memory")
	depth  int    // number of prefix tokens
	panels []*Panel
	hidden []*Panel
}

// Build turns the store into dashboard sections.
func Build(st *store.Store, opt Options) []*Section {
	panels := make([]*Panel, 0, len(st.Families))
	limits := map[*store.Family]string{} // limit family → its usage panel
	for _, f := range st.Families {
		if len(f.Live()) == 0 || headerOnly[f.Name] {
			continue
		}
		p := Classify(f, st)
		if p == nil {
			continue
		}
		if p.Limit != nil {
			limits[p.Limit] = p.Name()
		}
		panels = append(panels, p)
	}

	// Top-level sections by the first name token, in order of appearance.
	var roots []*node
	byKey := map[string]*node{}
	for _, p := range panels {
		key := prefix(p.Name(), 1)
		n := byKey[key]
		if n == nil {
			n = &node{key: key, depth: 1}
			byKey[key] = n
			roots = append(roots, n)
		}
		if opt.Filter != nil && !opt.Filter.MatchString(p.Name()) {
			continue
		}
		// A limit shown inside a visible usage panel is redundant, filter or
		// not; noise and idle metrics stay visible when they were filtered for.
		usage, isLimit := limits[p.Family]
		merged := isLimit && (opt.Filter == nil || opt.Filter.MatchString(usage))
		if !opt.ShowAll && (merged || opt.Filter == nil && (p.Noise || idle(p))) {
			n.hidden = append(n.hidden, p)
			continue
		}
		n.panels = append(n.panels, p)
	}

	var out []*Section
	for _, r := range roots {
		for _, n := range split(r) {
			sec := &Section{Key: n.key, Title: title(n.key), Hidden: len(n.hidden)}
			for _, p := range n.panels {
				p.Title = trimPrefixTokens(p.Name(), n.depth)
			}
			sort.SliceStable(n.panels, func(i, j int) bool { return n.panels[i].Priority < n.panels[j].Priority })
			sec.Panels = n.panels
			if opt.Filter == nil || opt.GroupFiltered {
				sec.Panels = group(n, sec.Title)
			}
			if len(sec.Panels) > 0 || (sec.Hidden > 0 && opt.Filter == nil) {
				out = append(out, sec)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := tailSections[prefix(out[i].Key, 1)], tailSections[prefix(out[j].Key, 1)]
		return ti < tj // keep exposition order otherwise
	})
	return out
}

// prefix returns the first n tokens of name, or name itself when it is
// shorter.
func prefix(name string, n int) string {
	if i := prefixLen(name, n); i > 0 {
		return name[:i]
	}
	return name
}

// idle reports whether every series of a plotted family stayed at zero for
// the whole history: unused error counters, empty histograms, absent
// devices. Health flags and labels are never idle — a 0 there is news.
func idle(p *Panel) bool {
	switch p.Kind {
	case KindGauge, KindRate, KindHistogram, KindSummary:
	default:
		return false
	}
	for _, s := range p.Family.Live() {
		if !s.Idle() {
			return false
		}
	}
	return true
}

// split breaks crowded sections up by the next name token, judging size by
// the panels left after grouping. Panels of small subsections stay in the
// parent section, which comes first.
func split(n *node) []*node {
	if len(group(n, "")) <= maxSection || n.depth >= maxDepth || tailSections[n.key] > 0 {
		return []*node{n}
	}
	var kids []*node
	byKey := map[string]*node{}
	rest := &node{key: n.key, depth: n.depth}
	assign := func(p *Panel, hidden bool) {
		key := prefix(p.Name(), n.depth+1)
		k := byKey[key]
		if k == nil {
			k = &node{key: key, depth: n.depth + 1}
			byKey[key] = k
			kids = append(kids, k)
		}
		if hidden {
			k.hidden = append(k.hidden, p)
		} else {
			k.panels = append(k.panels, p)
		}
	}
	for _, p := range n.panels {
		assign(p, false)
	}
	for _, p := range n.hidden {
		assign(p, true)
	}
	var out []*node
	for _, k := range kids {
		if len(k.panels) < minSection {
			// Too small for its own section.
			rest.panels = append(rest.panels, k.panels...)
			rest.hidden = append(rest.hidden, k.hidden...)
			continue
		}
		out = append(out, split(k)...)
	}
	if len(out) == 0 {
		return []*node{n}
	}
	// Keep the parent's panels in exposition order.
	order := map[*Panel]int{}
	for i, p := range n.panels {
		order[p] = i
	}
	sort.SliceStable(rest.panels, func(i, j int) bool { return order[rest.panels[i]] < order[rest.panels[j]] })
	if len(rest.panels) > 0 || len(rest.hidden) > 0 {
		out = append([]*node{rest}, out...)
	}
	return out
}

// title renders a section key: the first token through sectionTitles, the
// rest joined with "·" ("node_memory" → "node · memory").
func title(key string) string {
	ts := tokens(key)
	if len(ts) == 0 {
		return key
	}
	if t, ok := sectionTitles[ts[0]]; ok {
		ts[0] = t
	}
	return strings.Join(ts, " · ")
}

// groupKey identifies panels that can share a group: same widget and unit.
type groupKey struct {
	kind  Kind
	unit  Unit
	sym   string
	scale float64
}

// group packs plain single-series gauges and rates of a crowded section into
// group panels, so a section with dozens of scalar metrics (node_memory_*,
// node_netstat_*) takes a few panels instead of a screenful. Each group sits
// where its first member was.
func group(n *node, secTitle string) []*Panel {
	if len(n.panels) < groupSection {
		return n.panels
	}
	members := map[groupKey][]*Panel{}
	for _, p := range n.panels {
		if k, ok := groupable(p); ok {
			members[k] = append(members[k], p)
		}
	}
	var out []*Panel
	placed := map[groupKey]bool{}
	for _, p := range n.panels {
		k, ok := groupable(p)
		if !ok || len(members[k]) < groupMembers {
			out = append(out, p)
			continue
		}
		if !placed[k] {
			placed[k] = true
			out = append(out, newGroup(n.key, secTitle, k, members[k]))
		}
	}
	return out
}

func groupable(p *Panel) (groupKey, bool) {
	if (p.Kind != KindGauge && p.Kind != KindRate) || p.Priority != 100 || len(p.Family.Live()) != 1 {
		return groupKey{}, false
	}
	return groupKey{p.Kind, p.Unit, p.Sym, p.scale()}, true
}

// newGroup builds a group panel over a synthetic family whose series are the
// members' single series, labelled with the member names.
func newGroup(key, secTitle string, k groupKey, members []*Panel) *Panel {
	typ := expo.Gauge
	if k.kind == KindRate {
		typ = expo.Counter
	}
	f := &store.Family{
		Name: key + "{" + groupWord(k) + "}",
		Type: typ,
		Help: "Single-series metrics of this section, grouped by unit. Use the filter to show them as separate panels.",
	}
	for _, m := range members {
		s := m.Family.Live()[0]
		f.Series = append(f.Series, &store.Series{
			Key:     m.Name(),
			Labels:  []expo.Label{{Name: "metric", Value: stripUnit(m.Title)}},
			Lines:   s.Lines,
			Raw:     s.Raw,
			Buckets: s.Buckets,
			Seen:    true,
		})
	}
	return &Panel{
		Family: f, Kind: k.kind, Unit: k.unit, Sym: k.sym, Scale: k.scale,
		Title: lastWord(secTitle) + " · " + groupWord(k), Group: true, Priority: 100,
	}
}

func lastWord(title string) string {
	if _, after, ok := strings.CutLast(title, " · "); ok {
		return after
	}
	return title
}

// groupWord names the shared unit of a group ("bytes", "seconds", "rates").
func groupWord(k groupKey) string {
	w := ""
	switch k.unit {
	case UnitBytes:
		w = "bytes"
	case UnitSeconds:
		w = "seconds"
	case UnitRatio, UnitPercent:
		w = "percent"
	case UnitOther:
		w = k.sym
	}
	if k.kind == KindRate {
		if w == "" {
			return "rates"
		}
		return w + "/s"
	}
	if w == "" {
		return "values"
	}
	return w
}
