// Package store keeps a short history of scraped metrics and derives the
// values worth plotting: rates for counters, quantiles for histograms, etc.
package store

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/expo"
)

// Ring is a bounded history of float values (NaN marks a gap).
type Ring struct {
	vals []float64
	max  int
}

// NewRing returns a ring keeping the last n values.
func NewRing(n int) *Ring { return &Ring{max: n} }

// Push appends v, dropping the oldest value when the ring is full.
func (r *Ring) Push(v float64) {
	r.vals = append(r.vals, v)
	if len(r.vals) > r.max {
		copy(r.vals, r.vals[len(r.vals)-r.max:])
		r.vals = r.vals[:r.max]
	}
}

// Values returns the history, oldest first.
func (r *Ring) Values() []float64 { return r.vals }

// Last returns the newest value or NaN.
func (r *Ring) Last() float64 {
	if r == nil || len(r.vals) == 0 {
		return math.NaN()
	}
	return r.vals[len(r.vals)-1]
}

// Line is a named derived history inside a series ("rate", "p99", ...).
type Line struct {
	Name string
	Ring *Ring
}

// Bucket is one cumulative histogram bucket.
type Bucket struct {
	LE    float64
	Count float64
}

// Series is one label combination of a family.
type Series struct {
	Key    string
	Labels []expo.Label // identifying labels (le / quantile removed)
	Lines  []Line       // first line is the primary one

	Raw     float64  // latest raw value: counter total, gauge value, histogram/summary count
	Buckets []Bucket // histogram: per-bucket observations during the last interval (non-cumulative)
	Seen    bool     // present in the latest scrape

	prevRaw     float64
	prevSum     float64
	prevBuckets []Bucket
	havePrev    bool
}

// Line returns the named line or nil.
func (s *Series) Line(name string) *Ring {
	for _, l := range s.Lines {
		if l.Name == name {
			return l.Ring
		}
	}
	return nil
}

// Primary returns the primary line.
func (s *Series) Primary() *Ring {
	if len(s.Lines) == 0 {
		return nil
	}
	return s.Lines[0].Ring
}

func (s *Series) ensure(name string, n int) *Ring {
	if r := s.Line(name); r != nil {
		return r
	}
	r := NewRing(n)
	s.Lines = append(s.Lines, Line{Name: name, Ring: r})
	return r
}

// Family is the stored state of one metric family.
type Family struct {
	Name   string
	Type   expo.Type
	Help   string
	Unit   string    // OpenMetrics # UNIT, if declared
	Series []*Series // stable order of first appearance
	byKey  map[string]*Series
	order  int // position in the latest scrape that had it
	// CommonLabels are label pairs identical in every series of the family;
	// they are hidden from per-series display.
	CommonLabels []expo.Label
}

// Store holds all families.
type Store struct {
	MaxPoints int
	Families  []*Family
	byName    map[string]*Family
	lastAt    time.Time
	Scrapes   int
}

// New returns an empty store keeping maxPoints values per line.
func New(maxPoints int) *Store {
	if maxPoints < 2 {
		maxPoints = 2
	}
	return &Store{MaxPoints: maxPoints, byName: map[string]*Family{}}
}

// Get returns a family by name.
func (st *Store) Get(name string) *Family { return st.byName[name] }

// Ingest adds one scrape taken at time at.
func (st *Store) Ingest(fams []*expo.Family, at time.Time) {
	dt := 0.0
	if !st.lastAt.IsZero() {
		dt = at.Sub(st.lastAt).Seconds()
	}
	st.lastAt = at
	st.Scrapes++
	for _, f := range st.Families {
		for _, s := range f.Series {
			s.Seen = false
		}
	}
	for i, ef := range fams {
		f := st.byName[ef.Name]
		if f == nil {
			f = &Family{Name: ef.Name, byKey: map[string]*Series{}}
			st.byName[ef.Name] = f
			st.Families = append(st.Families, f)
		}
		f.Type, f.Help, f.Unit, f.order = ef.Type, ef.Help, ef.Unit, i
		switch ef.Type {
		case expo.Histogram, expo.GaugeHistogram:
			st.ingestHistogram(f, ef, dt)
		case expo.Summary:
			st.ingestSummary(f, ef, dt)
		case expo.Counter:
			st.ingestCounter(f, ef, dt)
		default:
			st.ingestGauge(f, ef)
		}
		f.CommonLabels = commonLabels(f)
	}
	// Follow the exposition order: client libraries create labelled metrics
	// lazily, so families can first appear after the ones exposed below them.
	sort.SliceStable(st.Families, func(i, j int) bool { return st.Families[i].order < st.Families[j].order })
}

func (f *Family) series(labels []expo.Label, exclude ...string) *Series {
	key := expo.LabelKey(labels, exclude...)
	s := f.byKey[key]
	if s == nil {
		s = &Series{Key: key, Labels: expo.Without(labels, exclude...)}
		f.byKey[key] = s
		f.Series = append(f.Series, s)
	}
	return s
}

func (st *Store) ingestGauge(f *Family, ef *expo.Family) {
	for _, smp := range ef.Samples {
		s := f.series(smp.Labels)
		s.ensure("value", st.MaxPoints).Push(smp.Value)
		s.Raw, s.Seen = smp.Value, true
	}
}

func (st *Store) ingestCounter(f *Family, ef *expo.Family, dt float64) {
	for _, smp := range ef.Samples {
		if strings.HasSuffix(smp.Name, "_created") {
			continue
		}
		s := f.series(smp.Labels)
		r := s.ensure("rate", st.MaxPoints)
		if s.havePrev && dt > 0 {
			d := smp.Value - s.prevRaw
			if d < 0 { // counter reset
				d = smp.Value
			}
			r.Push(d / dt)
		} else {
			r.Push(math.NaN())
		}
		s.prevRaw, s.havePrev = smp.Value, true
		s.Raw, s.Seen = smp.Value, true
	}
}

// HistQuantiles are the quantiles derived from histograms.
var HistQuantiles = []float64{0.5, 0.9, 0.99}

// QuantileName names a quantile line: p50, p99, p999, min, max.
func QuantileName(q float64) string {
	switch q {
	case 0:
		return "min"
	case 1:
		return "max"
	}
	s := strconv.FormatFloat(q*100, 'f', -1, 64)
	return "p" + strings.ReplaceAll(s, ".", "")
}

type histAcc struct {
	labels  []expo.Label
	buckets []Bucket
	sum     float64
	count   float64
	hasCnt  bool
}

func (st *Store) ingestHistogram(f *Family, ef *expo.Family, dt float64) {
	accs := map[string]*histAcc{}
	var order []string
	for _, smp := range ef.Samples {
		key := expo.LabelKey(smp.Labels, "le")
		a := accs[key]
		if a == nil {
			a = &histAcc{labels: smp.Labels}
			accs[key] = a
			order = append(order, key)
		}
		switch {
		case strings.HasSuffix(smp.Name, "_bucket"):
			le, err := strconv.ParseFloat(smp.Label("le"), 64)
			if err != nil {
				continue
			}
			a.buckets = append(a.buckets, Bucket{LE: le, Count: smp.Value})
		case strings.HasSuffix(smp.Name, "_sum"), strings.HasSuffix(smp.Name, "_gsum"):
			a.sum = smp.Value
		case strings.HasSuffix(smp.Name, "_count"), strings.HasSuffix(smp.Name, "_gcount"):
			a.count, a.hasCnt = smp.Value, true
		}
	}
	for _, key := range order {
		a := accs[key]
		sort.Slice(a.buckets, func(i, j int) bool { return a.buckets[i].LE < a.buckets[j].LE })
		if !a.hasCnt && len(a.buckets) > 0 {
			a.count = a.buckets[len(a.buckets)-1].Count
		}
		s := f.series(a.labels, "le")
		lines := make([]*Ring, len(HistQuantiles))
		for i, q := range HistQuantiles {
			lines[i] = s.ensure(QuantileName(q), st.MaxPoints)
		}
		rate := s.ensure("rate", st.MaxPoints)
		avg := s.ensure("avg", st.MaxPoints)

		var window []Bucket // observations in the last interval, cumulative
		reset := s.havePrev && a.count < s.prevRaw
		if s.havePrev && dt > 0 && !reset && sameBounds(a.buckets, s.prevBuckets) {
			window = make([]Bucket, len(a.buckets))
			for i, b := range a.buckets {
				window[i] = Bucket{LE: b.LE, Count: b.Count - s.prevBuckets[i].Count}
			}
			dc := a.count - s.prevRaw
			rate.Push(dc / dt)
			if dc > 0 {
				avg.Push((a.sum - s.prevSum) / dc)
			} else {
				avg.Push(math.NaN())
			}
		} else {
			// First scrape (or reset): use lifetime distribution so the panel
			// has something to show immediately.
			window = a.buckets
			if s.havePrev && reset && dt > 0 {
				rate.Push(a.count / dt)
			} else {
				rate.Push(math.NaN())
			}
			if a.count > 0 {
				avg.Push(a.sum / a.count)
			} else {
				avg.Push(math.NaN())
			}
		}
		for i, q := range HistQuantiles {
			lines[i].Push(BucketQuantile(q, window))
		}
		s.Buckets = decumulate(window)
		s.prevBuckets = a.buckets
		s.prevRaw, s.prevSum, s.havePrev = a.count, a.sum, true
		s.Raw, s.Seen = a.count, true
	}
}

func sameBounds(a, b []Bucket) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].LE != b[i].LE {
			return false
		}
	}
	return true
}

func decumulate(cum []Bucket) []Bucket {
	out := make([]Bucket, len(cum))
	prev := 0.0
	for i, b := range cum {
		out[i] = Bucket{LE: b.LE, Count: b.Count - prev}
		prev = b.Count
	}
	return out
}

// BucketQuantile estimates quantile q from cumulative buckets the way
// Prometheus' histogram_quantile does (linear interpolation within a bucket).
// It returns NaN when there are no observations.
func BucketQuantile(q float64, buckets []Bucket) float64 {
	if len(buckets) == 0 || math.IsNaN(q) {
		return math.NaN()
	}
	total := buckets[len(buckets)-1].Count
	if total <= 0 {
		return math.NaN()
	}
	if q <= 0 {
		q = 0
	}
	if q >= 1 {
		q = 1
	}
	rank := q * total
	i := sort.Search(len(buckets), func(i int) bool { return buckets[i].Count >= rank })
	if i >= len(buckets) {
		i = len(buckets) - 1
	}
	if math.IsInf(buckets[i].LE, 1) {
		if i == 0 {
			return math.NaN()
		}
		return buckets[i-1].LE
	}
	lower, below := 0.0, 0.0
	if i > 0 {
		lower, below = buckets[i-1].LE, buckets[i-1].Count
	} else if buckets[0].LE <= 0 {
		return buckets[0].LE
	}
	inBucket := buckets[i].Count - below
	if inBucket <= 0 {
		return buckets[i].LE
	}
	return lower + (buckets[i].LE-lower)*((rank-below)/inBucket)
}

func (st *Store) ingestSummary(f *Family, ef *expo.Family, dt float64) {
	type acc struct {
		labels []expo.Label
		qs     map[float64]float64
		sum    float64
		count  float64
	}
	accs := map[string]*acc{}
	var order []string
	for _, smp := range ef.Samples {
		key := expo.LabelKey(smp.Labels, "quantile")
		a := accs[key]
		if a == nil {
			a = &acc{labels: smp.Labels, qs: map[float64]float64{}}
			accs[key] = a
			order = append(order, key)
		}
		switch {
		case strings.HasSuffix(smp.Name, "_sum"):
			a.sum = smp.Value
		case strings.HasSuffix(smp.Name, "_count"):
			a.count = smp.Value
		default:
			if q, err := strconv.ParseFloat(smp.Label("quantile"), 64); err == nil {
				a.qs[q] = smp.Value
			}
		}
	}
	for _, key := range order {
		a := accs[key]
		s := f.series(a.labels, "quantile")
		qs := make([]float64, 0, len(a.qs))
		for q := range a.qs {
			qs = append(qs, q)
		}
		sort.Float64s(qs)
		// Prefer the interesting quantiles as the primary line.
		pref := pickSummaryOrder(qs)
		for _, q := range pref {
			s.ensure(QuantileName(q), st.MaxPoints).Push(a.qs[q])
		}
		rate := s.ensure("rate", st.MaxPoints)
		avg := s.ensure("avg", st.MaxPoints)
		if s.havePrev && dt > 0 && a.count >= s.prevRaw {
			dc := a.count - s.prevRaw
			rate.Push(dc / dt)
			if dc > 0 {
				avg.Push((a.sum - s.prevSum) / dc)
			} else {
				avg.Push(math.NaN())
			}
		} else {
			rate.Push(math.NaN())
			if a.count > 0 {
				avg.Push(a.sum / a.count)
			} else {
				avg.Push(math.NaN())
			}
		}
		s.prevRaw, s.prevSum, s.havePrev = a.count, a.sum, true
		s.Raw, s.Seen = a.count, true
	}
}

// pickSummaryOrder puts the highest non-max quantile first (p99 rather than
// max), then the rest descending.
func pickSummaryOrder(qs []float64) []float64 {
	out := make([]float64, 0, len(qs))
	for _, q := range slices.Backward(qs) {
		out = append(out, q)
	}
	if len(out) > 1 && out[0] == 1 {
		out = append(out[1:], out[0])
	}
	return out
}

func commonLabels(f *Family) []expo.Label {
	var live []*Series
	for _, s := range f.Series {
		if s.Seen {
			live = append(live, s)
		}
	}
	if len(live) < 2 {
		return nil
	}
	var out []expo.Label
	for _, l := range live[0].Labels {
		same := true
		for _, s := range live[1:] {
			found := false
			for _, o := range s.Labels {
				if o.Name == l.Name {
					found = o.Value == l.Value
					break
				}
			}
			if !found {
				same = false
				break
			}
		}
		if same {
			out = append(out, l)
		}
	}
	return out
}

// Lookup returns the series with the given key (see expo.LabelKey) or nil.
func (f *Family) Lookup(key string) *Series {
	if f == nil {
		return nil
	}
	return f.byKey[key]
}

// Idle reports whether the series never had a non-zero value in its history.
func (s *Series) Idle() bool {
	if s.Raw != 0 {
		return false
	}
	for _, l := range s.Lines {
		for _, v := range l.Ring.Values() {
			if v != 0 && !math.IsNaN(v) {
				return false
			}
		}
	}
	return true
}

// Live returns the series present in the latest scrape.
func (f *Family) Live() []*Series {
	out := make([]*Series, 0, len(f.Series))
	for _, s := range f.Series {
		if s.Seen {
			out = append(out, s)
		}
	}
	return out
}
