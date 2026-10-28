package store

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/igorrius/promtop/internal/expo"
)

func parse(t *testing.T, s string) []*expo.Family {
	t.Helper()
	f, err := expo.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCounterRate(t *testing.T) {
	st := New(10)
	t0 := time.Unix(1000, 0)
	st.Ingest(parse(t, "# TYPE c counter\nc 100\n"), t0)
	st.Ingest(parse(t, "# TYPE c counter\nc 120\n"), t0.Add(2*time.Second))
	s := st.Get("c").Series[0]
	if v := s.Primary().Last(); !near(v, 10) {
		t.Errorf("rate = %v, want 10", v)
	}
	// Reset: counter goes down, rate uses the new value.
	st.Ingest(parse(t, "# TYPE c counter\nc 4\n"), t0.Add(4*time.Second))
	if v := s.Primary().Last(); !near(v, 2) {
		t.Errorf("rate after reset = %v, want 2", v)
	}
}

func TestBucketQuantile(t *testing.T) {
	b := []Bucket{{0.1, 10}, {0.2, 20}, {0.4, 40}, {math.Inf(1), 40}}
	cases := map[float64]float64{0.5: 0.2, 0.25: 0.1, 0.75: 0.3, 1: 0.4}
	for q, want := range cases {
		if got := BucketQuantile(q, b); !near(got, want) {
			t.Errorf("q%v = %v, want %v", q, got, want)
		}
	}
	// Everything in +Inf: falls back to the highest finite bound.
	if got := BucketQuantile(0.5, []Bucket{{1, 0}, {math.Inf(1), 5}}); got != 1 {
		t.Errorf("inf bucket = %v", got)
	}
	if got := BucketQuantile(0.5, []Bucket{{1, 0}, {math.Inf(1), 0}}); !math.IsNaN(got) {
		t.Errorf("empty = %v, want NaN", got)
	}
}

func TestHistogramWindow(t *testing.T) {
	st := New(10)
	t0 := time.Unix(1000, 0)
	h := func(a, b, inf, sum float64) string {
		return "# TYPE h histogram\n" +
			"h_bucket{le=\"1\"} " + ftoa(a) + "\n" +
			"h_bucket{le=\"2\"} " + ftoa(b) + "\n" +
			"h_bucket{le=\"+Inf\"} " + ftoa(inf) + "\n" +
			"h_sum " + ftoa(sum) + "\nh_count " + ftoa(inf) + "\n"
	}
	st.Ingest(parse(t, h(100, 100, 100, 50)), t0)
	// 10 new observations, all between 1 and 2.
	st.Ingest(parse(t, h(100, 110, 110, 65)), t0.Add(time.Second))
	s := st.Get("h").Series[0]
	if v := s.Line("p50").Last(); !near(v, 1.5) {
		t.Errorf("p50 = %v, want 1.5 (window only)", v)
	}
	if v := s.Line("rate").Last(); !near(v, 10) {
		t.Errorf("rate = %v, want 10", v)
	}
	if v := s.Line("avg").Last(); !near(v, 1.5) {
		t.Errorf("avg = %v, want 1.5", v)
	}
	if len(s.Buckets) != 3 || s.Buckets[1].Count != 10 {
		t.Errorf("buckets = %+v", s.Buckets)
	}
	// No new observations: quantiles become gaps, rate is zero.
	st.Ingest(parse(t, h(100, 110, 110, 65)), t0.Add(2*time.Second))
	if v := s.Line("p50").Last(); !math.IsNaN(v) {
		t.Errorf("idle p50 = %v, want NaN", v)
	}
	if v := s.Line("rate").Last(); v != 0 {
		t.Errorf("idle rate = %v", v)
	}
}

func TestSummaryAndCommonLabels(t *testing.T) {
	st := New(10)
	in := `# TYPE s summary
s{job="a",quantile="0.5"} 1
s{job="a",quantile="0.99"} 3
s{job="a",quantile="1"} 9
s_sum{job="a"} 10
s_count{job="a"} 5
# TYPE g gauge
g{scope="x",kind="a"} 1
g{scope="x",kind="b"} 2
`
	st.Ingest(parse(t, in), time.Unix(1, 0))
	s := st.Get("s").Series[0]
	if s.Lines[0].Name != "p99" {
		t.Errorf("primary summary line = %s, want p99", s.Lines[0].Name)
	}
	if v := s.Line("avg").Last(); !near(v, 2) {
		t.Errorf("avg = %v", v)
	}
	g := st.Get("g")
	if len(g.CommonLabels) != 1 || g.CommonLabels[0].Name != "scope" {
		t.Errorf("common labels = %+v", g.CommonLabels)
	}
}

func TestRingBounded(t *testing.T) {
	r := NewRing(3)
	for i := range 10 {
		r.Push(float64(i))
	}
	if v := r.Values(); len(v) != 3 || v[0] != 7 || v[2] != 9 {
		t.Errorf("ring = %v", v)
	}
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// Families follow the latest exposition order, even when a family shows up
// only in a later scrape.
func TestFamilyOrder(t *testing.T) {
	st := New(5)
	st.Ingest(parse(t, "# TYPE b gauge\nb 1\n"), time.Unix(1000, 0))
	st.Ingest(parse(t, "# TYPE a gauge\na 1\n# TYPE b gauge\nb 1\n"), time.Unix(1002, 0))
	if got := st.Families[0].Name + st.Families[1].Name; got != "ab" {
		t.Errorf("order = %s, want ab", got)
	}
}
