// Package summary turns a dashboard into a compact JSON document for agents
// and scripts: numbers in base units, labels as objects, statistics over the
// sampled window instead of raw points, and a ranked list of what deserves a
// look. The layout is versioned (Schema) and does not follow the web page.
package summary

import (
	"encoding/json"
	"io"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

// Schema names the document layout; it changes only incompatibly.
const Schema = "promtop.summary/v1"

// Options shape the document.
type Options struct {
	Source   string
	From, To time.Time // first and latest scrape of the window
	Interval time.Duration
	Now      time.Time
	Top      int  // series per metric, 0 for all
	List     bool // names, types and series counts only
	Notable  bool // only the notable list
	// Unfiltered holds the same scrapes before label matchers, when there
	// were any: an error share is of the metric's whole traffic.
	Unfiltered *store.Store
}

// Doc is the whole document.
type Doc struct {
	Schema  string   `json:"schema"`
	Source  string   `json:"source"`
	Target  string   `json:"target,omitempty"`
	Window  Window   `json:"window"`
	Totals  Totals   `json:"totals"`
	Notable *[]Note  `json:"notable,omitempty"` // absent with List
	Omitted int      `json:"notable_omitted,omitempty"`
	Metrics []Metric `json:"metrics,omitempty"`
}

// Window is the sampled time range.
type Window struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	Samples   int     `json:"samples"`
	IntervalS float64 `json:"interval_s"`
}

// Totals count what the target exposes and what the document shows.
type Totals struct {
	Metrics int `json:"metrics"` // families in the exposition
	Series  int `json:"series"`
	Shown   int `json:"shown"`  // metrics in this document
	Hidden  int `json:"hidden"` // idle, runtime noise or merged into a usage metric; -a shows them
}

// Note is one thing worth a look.
type Note struct {
	Metric string            `json:"metric"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  string            `json:"value,omitempty"` // what the numbers are: rate, p99, used_ratio...
	Reason string            `json:"reason"`
	Last   *Num              `json:"last,omitempty"`
	From   *Num              `json:"from,omitempty"`
	To     *Num              `json:"to,omitempty"`
	Change *Num              `json:"change,omitempty"`
	Share  *Num              `json:"share,omitempty"` // errors: of the metric's whole rate
	InS    *Num              `json:"in_s,omitempty"`
	Count  int               `json:"changes,omitempty"`
}

// Reasons, most urgent first.
const (
	ReasonDown        = "down"         // a health flag is 0
	ReasonNearLimit   = "near_limit"   // usage at 90% or more
	ReasonErrors      = "errors"       // 5xx responses are being counted, all series of a metric together
	ReasonExpiresSoon = "expires_soon" // a timestamp less than 7 days ahead
	ReasonFlapped     = "flapped"      // a health flag changed during the window
	ReasonChanged     = "changed"      // a timestamp moved: restart, deploy, reload
	ReasonRising      = "rising"
	ReasonFalling     = "falling"
)

var urgency = map[string]int{
	ReasonDown: 0, ReasonNearLimit: 1, ReasonErrors: 2, ReasonExpiresSoon: 3,
	ReasonFlapped: 4, ReasonChanged: 5, ReasonRising: 6, ReasonFalling: 6,
}

// Metric is one metric family.
type Metric struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"` // exposition type
	Kind    string            `json:"kind"` // how promtop reads it: gauge, rate, histogram, usage, state...
	Unit    string            `json:"unit,omitempty"`
	Value   string            `json:"value,omitempty"` // what last/avg/min/max of a series describe
	Help    string            `json:"help,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"` // shared by every series
	Limit   string            `json:"limit,omitempty"`  // usage: the metric holding the maximum
	Count   int               `json:"series_count"`
	Sum     *Stats            `json:"sum,omitempty"` // rates: all series added up
	Series  []Series          `json:"series,omitempty"`
	Omitted int               `json:"series_omitted,omitempty"`
}

// Stats describe one value over the window.
type Stats struct {
	Last   *Num    `json:"last,omitempty"`
	Avg    *Num    `json:"avg,omitempty"`
	Min    *Num    `json:"min,omitempty"`
	Max    *Num    `json:"max,omitempty"`
	Trend  string  `json:"trend,omitempty"`  // rising, falling or flat; needs 3 samples
	Change *Num    `json:"change,omitempty"` // relative, first third of the window to the last
	from   float64 // means of the first and the last third
	to     float64
	spread float64 // sum of both thirds' sample variances
	third  int     // samples in a third
}

func (st Stats) avgOr0() float64 {
	if st.Avg == nil {
		return 0
	}
	return float64(*st.Avg)
}

// Series is one label combination.
type Series struct {
	Labels map[string]string `json:"labels,omitempty"`
	Stats
	Total     *Num           `json:"total,omitempty"` // counter: lifetime value
	Rate      *Num           `json:"rate,omitempty"`  // histogram, summary: observations per second
	Mean      *Num           `json:"mean,omitempty"`  // histogram, summary: average observation
	Quantiles map[string]Num `json:"quantiles,omitempty"`
	Used      *Num           `json:"used,omitempty"`
	Limit     *Num           `json:"limit,omitempty"`
	Up        *bool          `json:"up,omitempty"`     // health flag
	Active    *bool          `json:"active,omitempty"` // stateset member
	Changes   int            `json:"changes,omitempty"`
	Time      string         `json:"time,omitempty"`  // timestamp metrics
	AgeS      *Num           `json:"age_s,omitempty"` // a past timestamp: seconds ago
	InS       *Num           `json:"in_s,omitempty"`  // a future one: seconds ahead
}

// Num is a float written with six significant digits.
type Num float64

// MarshalJSON writes the number compactly.
func (n Num) MarshalJSON() ([]byte, error) {
	return strconv.AppendFloat(nil, float64(n), 'g', 6, 64), nil
}

func num(v float64) *Num {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	n := Num(v)
	return &n
}

// Write encodes the document for the store and its dashboard sections.
func Write(w io.Writer, st *store.Store, secs []*view.Section, o Options) error {
	// Compact: the reader is a program or a model paying per token.
	return json.NewEncoder(w).Encode(Build(st, secs, o))
}

// Build makes the document.
func Build(st *store.Store, secs []*view.Section, o Options) Doc {
	d := Doc{
		Schema: Schema, Source: o.Source, Target: view.Target(st),
		Window: Window{
			From: stamp(o.From), To: stamp(o.To), Samples: min(st.Scrapes, st.MaxPoints),
			IntervalS: o.Interval.Seconds(),
		},
	}
	for _, f := range st.Families {
		if n := len(f.Live()); n > 0 {
			d.Totals.Metrics++
			d.Totals.Series += n
		}
	}
	var notes []Note
	for _, sec := range secs {
		d.Totals.Hidden += sec.Hidden
		for _, p := range expand(st, sec.Panels) {
			d.Totals.Shown++
			if !o.List {
				notes = append(notes, notable(p, o)...)
			}
			if !o.Notable {
				d.Metrics = append(d.Metrics, metric(p, o))
			}
		}
	}
	sort.SliceStable(notes, func(i, j int) bool {
		a, b := notes[i], notes[j]
		if urgency[a.Reason] != urgency[b.Reason] {
			return urgency[a.Reason] < urgency[b.Reason]
		}
		return magnitude(a) > magnitude(b)
	})
	if len(notes) > maxNotes {
		d.Omitted = len(notes) - maxNotes
		notes = notes[:maxNotes]
	}
	if !o.List {
		notes = append([]Note{}, notes...)
		d.Notable = &notes
	}
	return d
}

const maxNotes = 20

func magnitude(n Note) float64 {
	if n.Change == nil {
		return math.Inf(1) // appeared from zero
	}
	return math.Abs(float64(*n.Change))
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// expand replaces group panels, which pack single-series metrics of a
// crowded section for the screen, by their members: an agent wants names.
func expand(st *store.Store, panels []*view.Panel) []*view.Panel {
	var out []*view.Panel
	for _, p := range panels {
		if !p.Group {
			out = append(out, p)
			continue
		}
		for _, s := range p.Family.Series {
			if f := st.Get(s.Key); f != nil {
				if m := view.Classify(f, st); m != nil {
					out = append(out, m)
				}
			}
		}
	}
	return out
}

func metric(p *view.Panel, o Options) Metric {
	f := p.Family
	live := p.Rows()
	m := Metric{
		Name: f.Name, Type: string(f.Type), Kind: p.Kind.String(), Unit: unit(p),
		Value: valueName(p), Help: f.Help, Labels: labelMap(f.CommonLabels), Count: len(live),
	}
	if p.Limit != nil {
		m.Limit = p.Limit.Name
	}
	if o.List {
		return m
	}
	if p.Kind == view.KindRate && len(live) > 1 {
		s := stats(sumRings(live, scaleOf(p)))
		m.Sum = &s
	}
	if o.Top > 0 && len(live) > o.Top {
		m.Omitted = len(live) - o.Top
		live = live[:o.Top]
	}
	for _, s := range live {
		m.Series = append(m.Series, series(p, s, o.Now))
	}
	return m
}

func series(p *view.Panel, s *store.Series, now time.Time) Series {
	out := Series{Labels: labelMap(view.VisibleLabels(p.Family, s))}
	k := scaleOf(p)
	switch p.Kind {
	case view.KindInfo:
		return out
	case view.KindTimestamp:
		t := p.Time(s)
		out.Time = stamp(t)
		if d := now.Sub(t).Seconds(); d >= 0 {
			out.AgeS = num(d)
		} else {
			out.InS = num(-d)
		}
		return out
	case view.KindState:
		on := view.StateUp(s)
		if p.Family.Type == expo.StateSet {
			out.Active = &on
		} else {
			out.Up = &on
		}
		out.Changes = transitions(values(s.Primary()))
		return out
	case view.KindUsage:
		used, lim := p.UsageValues(s)
		out.Used, out.Limit = num(used*k), num(lim*k)
	case view.KindHistogram, view.KindSummary:
		out.Rate = num(s.Line("rate").Last())
		out.Mean = num(s.Line("avg").Last() * k)
		out.Quantiles = map[string]Num{}
		for _, l := range s.Lines {
			if l.Name == "rate" || l.Name == "avg" {
				continue
			}
			if v := num(l.Ring.Last() * k); v != nil {
				out.Quantiles[l.Name] = *v
			}
		}
	case view.KindRate:
		out.Total = num(s.Raw * k)
	}
	out.Stats = stats(history(p, s))
	return out
}

// history is the value of s that the panel plots, in base units, oldest
// first: the rate, a tail quantile, the used fraction or the gauge value.
func history(p *view.Panel, s *store.Series) []float64 {
	var vals []float64
	switch p.Kind {
	case view.KindUsage:
		return usageRing(p, s)
	case view.KindHistogram, view.KindSummary:
		vals = slices.Clone(values(view.Ring(s, p.RowLine())))
		if p.Kind == view.KindHistogram {
			// Before the first rate a histogram's quantiles describe its whole
			// lifetime, not the interval; they would fake a trend.
			rate := values(s.Line("rate"))
			for i := range vals {
				if j := len(rate) - len(vals) + i; j < 0 || math.IsNaN(rate[j]) {
					vals[i] = math.NaN()
				}
			}
		}
	default:
		vals = slices.Clone(values(s.Primary()))
	}
	if k := scaleOf(p); k != 1 {
		for i := range vals {
			vals[i] *= k
		}
	}
	return vals
}

func scaleOf(p *view.Panel) float64 {
	if p.Scale == 0 {
		return 1
	}
	return p.Scale
}

// notable lists the series of p that deserve a look.
func notable(p *view.Panel, o Options) []Note {
	if p.Kind == view.KindState && p.Family.Type == expo.StateSet {
		return stateSetNotes(p)
	}
	var out []Note
	for _, s := range p.Family.Live() {
		note := func(reason string) Note {
			return Note{Metric: p.Name(), Labels: labelMap(view.VisibleLabels(p.Family, s)), Value: valueName(p), Reason: reason}
		}
		switch p.Kind {
		case view.KindInfo:
			continue
		case view.KindState:
			n := transitions(values(s.Primary()))
			switch {
			case !view.StateUp(s):
				nt := note(ReasonDown)
				nt.Count = n
				out = append(out, nt)
			case n > 0:
				nt := note(ReasonFlapped)
				nt.Count = n
				out = append(out, nt)
			}
			continue
		case view.KindTimestamp:
			if d := p.Time(s).Sub(o.Now); d > 0 && d < 7*24*time.Hour && expiry(p.Name()) {
				nt := note(ReasonExpiresSoon)
				nt.InS = num(d.Seconds())
				out = append(out, nt)
			} else if transitions(values(s.Primary())) > 0 {
				out = append(out, note(ReasonChanged))
			}
			continue
		}

		st := stats(history(p, s))
		switch {
		case p.Kind == view.KindUsage && p.Usage(s) >= nearLimit:
			nt := note(ReasonNearLimit)
			nt.Last = num(p.Usage(s))
			out = append(out, nt)
		case p.Kind == view.KindRate && serverError(s):
			// Counted once per metric below.
		case !gcHeap.MatchString(p.Name()) && moved(p, s, st, o.Interval):
			nt := note(ReasonRising)
			if st.Trend == ReasonFalling {
				nt.Reason = ReasonFalling
			}
			nt.Last, nt.From, nt.To, nt.Change = st.Last, num(st.from), num(st.to), st.Change
			out = append(out, nt)
		}
	}
	if p.Kind == view.KindRate {
		f := p.Family
		if o.Unfiltered != nil && o.Unfiltered.Get(f.Name) != nil {
			f = o.Unfiltered.Get(f.Name)
		}
		if errs, all := errorRate(f.Live()); errs > 0 {
			out = append(out, Note{
				Metric: p.Name(), Value: "rate", Reason: ReasonErrors,
				Last: num(errs * scaleOf(p)), Share: num(errs / all),
			})
		}
	}
	return out
}

// stateSetNotes reports a stateset that switched during the window, once,
// labelled with the state it is in now.
func stateSetNotes(p *view.Panel) []Note {
	n, active := 0, map[string]string(nil)
	for _, s := range p.Family.Live() {
		n = max(n, transitions(values(s.Primary())))
		if view.StateUp(s) {
			active = labelMap(view.VisibleLabels(p.Family, s))
		}
	}
	if n == 0 {
		return nil
	}
	return []Note{{Metric: p.Name(), Labels: active, Reason: ReasonFlapped, Count: n}}
}

// errorRate adds up the latest rates of 5xx series and of all series.
func errorRate(live []*store.Series) (errs, all float64) {
	for _, s := range live {
		v := s.Primary().Last()
		if math.IsNaN(v) {
			continue
		}
		all += v
		if serverError(s) {
			errs += v
		}
	}
	return errs, all
}

// gcHeap matches garbage-collected heap gauges: they climb until the next
// collection, often for longer than the window, so a rise is no news. A
// leak shows in the resident memory, which keeps its trend notes.
var gcHeap = regexp.MustCompile(`(?i)heap|^go_memstats_alloc_bytes$|^go_memory_used|^jvm_memory_(bytes_)?used`)

const (
	nearLimit = 0.9
	bigChange = 0.5 // relative change between the window's thirds that is notable
	minEvents = 20  // observations per third before a quantile's trend means anything
)

// moved reports whether the trend of st is big enough to mention, and more
// than the noise of counting few events.
func moved(p *view.Panel, s *store.Series, st Stats, interval time.Duration) bool {
	if st.Trend != ReasonRising && st.Trend != ReasonFalling {
		return false
	}
	if st.Change != nil && math.Abs(float64(*st.Change)) < bigChange {
		return false
	}
	span := float64(st.third) * interval.Seconds() // seconds in a third of the window
	if p.Kind == view.KindRate && p.Unit == view.UnitNone {
		// Event counts are Poisson: a third with c events varies by about √c.
		c1, c2 := st.from*span, st.to*span
		return math.Abs(c2-c1) > 3*math.Sqrt(c1+c2)
	}
	if (p.Kind == view.KindHistogram || p.Kind == view.KindSummary) &&
		stats(values(s.Line("rate"))).avgOr0()*span < minEvents {
		return false
	}
	// Anything else must move more than it wobbles within the thirds (a
	// Welch test at 3σ), so a GC heap's sawtooth is not a trend. That needs
	// two samples per third.
	return st.third >= 2 && math.Abs(st.to-st.from) > 3*math.Sqrt(st.spread/float64(st.third))
}

// expiry reports whether a timestamp metric names a deadline (a certificate's
// notAfter) rather than a past event.
func expiry(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "expir") || strings.Contains(n, "not_after") || strings.Contains(n, "notafter") || strings.Contains(n, "deadline")
}

var statusLabels = []string{"code", "status", "status_code", "http_status_code", "http_response_status_code"}

func serverError(s *store.Series) bool {
	for _, l := range s.Labels {
		for _, n := range statusLabels {
			if l.Name == n && len(l.Value) == 3 && l.Value[0] == '5' {
				return true
			}
		}
	}
	return false
}

// stats describes vals, ignoring gaps.
func stats(vals []float64) Stats {
	var xs []float64
	for _, v := range vals {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			xs = append(xs, v)
		}
	}
	if len(xs) == 0 {
		return Stats{}
	}
	lo, hi, sum := xs[0], xs[0], 0.0
	for _, v := range xs {
		lo, hi, sum = min(lo, v), max(hi, v), sum+v
	}
	st := Stats{Last: num(xs[len(xs)-1]), Avg: num(sum / float64(len(xs))), Min: num(lo), Max: num(hi)}
	if len(xs) < 3 {
		return st
	}
	third := max(1, len(xs)/3)
	st.third = third
	st.from, st.to = mean(xs[:third]), mean(xs[len(xs)-third:])
	st.spread = variance(xs[:third]) + variance(xs[len(xs)-third:])
	switch st.from {
	case st.to:
		st.Trend = "flat"
	case 0:
		st.Trend = ReasonRising
		if st.to < 0 {
			st.Trend = ReasonFalling
		}
	default:
		c := (st.to - st.from) / math.Abs(st.from)
		st.Change = num(c)
		switch {
		case c >= 0.1:
			st.Trend = ReasonRising
		case c <= -0.1:
			st.Trend = ReasonFalling
		default:
			st.Trend = "flat"
		}
	}
	return st
}

// variance is the sample variance of xs, 0 for fewer than two values.
func variance(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m, s := mean(xs), 0.0
	for _, v := range xs {
		s += (v - m) * (v - m)
	}
	return s / float64(len(xs)-1)
}

func mean(xs []float64) float64 {
	s := 0.0
	for _, v := range xs {
		s += v
	}
	return s / float64(len(xs))
}

// transitions counts value changes, ignoring gaps.
func transitions(vals []float64) int {
	n, prev, have := 0, 0.0, false
	for _, v := range vals {
		if math.IsNaN(v) {
			continue
		}
		if have && v != prev {
			n++
		}
		prev, have = v, true
	}
	return n
}

// sumRings adds the series' primary histories, aligned at the newest value.
func sumRings(ss []*store.Series, k float64) []float64 {
	n := 0
	for _, s := range ss {
		n = max(n, len(values(s.Primary())))
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
	}
	for _, s := range ss {
		vals := values(s.Primary())
		for i, v := range vals {
			j := n - len(vals) + i
			if math.IsNaN(v) {
				continue
			}
			if math.IsNaN(out[j]) {
				out[j] = 0
			}
			out[j] += v * k
		}
	}
	return out
}

// usageRing is the used fraction of s over the window, against the latest
// limit: limits rarely move, and their history is not kept per usage row.
func usageRing(p *view.Panel, s *store.Series) []float64 {
	_, lim := p.UsageValues(s)
	if lim <= 0 {
		return nil
	}
	vals := values(s.Primary())
	out := make([]float64, len(vals))
	for i, v := range vals {
		if p.LimitInvert {
			v = lim - v
		}
		out[i] = v / lim
	}
	return out
}

// values is the history of r, nil for a missing line.
func values(r *store.Ring) []float64 {
	if r == nil {
		return nil
	}
	return r.Values()
}

func valueName(p *view.Panel) string {
	switch p.Kind {
	case view.KindRate:
		return "rate"
	case view.KindHistogram, view.KindSummary:
		return p.RowLine()
	case view.KindUsage:
		return "used_ratio"
	case view.KindGauge:
		return "value"
	}
	return ""
}

func unit(p *view.Panel) string {
	var u string
	switch p.Unit {
	case view.UnitBytes:
		u = "bytes"
	case view.UnitSeconds:
		u = "seconds"
	case view.UnitRatio:
		u = "ratio"
	case view.UnitPercent:
		u = "percent"
	case view.UnitOther:
		u = p.Sym
	}
	if p.Kind == view.KindRate {
		if u == "" {
			return "1/s"
		}
		return u + "/s"
	}
	return u
}

func labelMap(ls []expo.Label) map[string]string {
	if len(ls) == 0 {
		return nil
	}
	m := make(map[string]string, len(ls))
	for _, l := range ls {
		m[l.Name] = l.Value
	}
	return m
}
