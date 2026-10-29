package summary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

var t0 = time.Unix(1_800_000_000, 0)

// ingest feeds n scrapes 10s apart; gen returns the exposition of scrape i.
func ingest(t *testing.T, n int, gen func(i int) string) *store.Store {
	t.Helper()
	st := store.New(100)
	for i := range n {
		fams, err := expo.Parse(strings.NewReader(gen(i)))
		if err != nil {
			t.Fatal(err)
		}
		st.Ingest(fams, t0.Add(time.Duration(i)*10*time.Second))
	}
	return st
}

func build(st *store.Store, o Options) Doc {
	o.Interval = 10 * time.Second
	o.Now = t0.Add(time.Duration(st.Scrapes-1) * 10 * time.Second)
	o.From, o.To = t0, o.Now
	return Build(st, view.Build(st, view.Options{}), o)
}

func find(d Doc, name string) *Metric {
	for i := range d.Metrics {
		if d.Metrics[i].Name == name {
			return &d.Metrics[i]
		}
	}
	return nil
}

func notes(d Doc, metric string) []Note {
	var out []Note
	for _, n := range *d.Notable {
		if n.Metric == metric {
			out = append(out, n)
		}
	}
	return out
}

func TestTrends(t *testing.T) {
	// app_busy_total doubles its rate from 100/s to 200/s halfway; app_rare_total
	// counts a handful of events, so its "trend" is noise.
	busy, rare := 0.0, 0.0
	st := ingest(t, 9, func(i int) string {
		if i > 0 {
			busy += 1000
			if i >= 5 {
				busy += 1000
			}
			rare += float64(i % 2)
		}
		return fmt.Sprintf("# TYPE app_busy_total counter\napp_busy_total %g\n# TYPE app_rare_total counter\napp_rare_total %g\n", busy, rare)
	})
	d := build(st, Options{})

	n := notes(d, "app_busy_total")
	if len(n) != 1 || n[0].Reason != ReasonRising || n[0].Change == nil || *n[0].Change < 0.5 {
		t.Fatalf("busy notes = %+v", n)
	}
	if n := notes(d, "app_rare_total"); len(n) != 0 {
		t.Errorf("a few events should not make a trend: %+v", n)
	}
	m := find(d, "app_busy_total")
	if m == nil || m.Unit != "1/s" || m.Value != "rate" || len(m.Series) != 1 {
		t.Fatalf("metric = %+v", m)
	}
	s := m.Series[0]
	if *s.Last != 200 || *s.Min != 100 || float64(*s.Total) != busy || s.Trend != ReasonRising {
		t.Errorf("series = %+v", s)
	}
}

func TestGaugeTrends(t *testing.T) {
	// app_queue climbs steadily; app_jitter jumps around as much as it moves;
	// a Go heap climbs until the next GC.
	queue := []int{10, 11, 12, 13, 14, 15, 16, 17, 18}
	jitter := []int{400, 1600, 500, 1500, 450, 1650, 1700, 600, 1800}
	st := ingest(t, 9, func(i int) string {
		return fmt.Sprintf("# TYPE app_queue gauge\napp_queue %d\n# TYPE app_jitter gauge\napp_jitter %d\n"+
			"# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes %d\n", queue[i], jitter[i], 1e6*queue[i])
	})
	d := build(st, Options{})
	if n := notes(d, "app_queue"); len(n) != 1 || n[0].Reason != ReasonRising {
		t.Errorf("queue = %+v", n)
	}
	if n := notes(d, "app_jitter"); len(n) != 0 {
		t.Errorf("a sawtooth is not a trend: %+v", n)
	}
	if n := notes(d, "go_memstats_heap_alloc_bytes"); len(n) != 0 {
		t.Errorf("a growing heap is no news: %+v", n)
	}
	if s := find(d, "app_jitter").Series[0]; s.Trend != ReasonRising {
		t.Errorf("the series still shows its trend: %+v", s)
	}
}

func TestErrorShareUnfiltered(t *testing.T) {
	all := ingest(t, 3, func(i int) string {
		return fmt.Sprintf("# TYPE req_total counter\nreq_total{code=\"200\"} %d\nreq_total{code=\"500\"} %d\n", 300*i, 100*i)
	})
	only5xx := ingest(t, 3, func(i int) string {
		return fmt.Sprintf("# TYPE req_total counter\nreq_total{code=\"500\"} %d\n", 100*i)
	})
	d := build(only5xx, Options{Unfiltered: all})
	if n := notes(d, "req_total"); len(n) != 1 || *n[0].Share != 0.25 || *n[0].Last != 10 {
		t.Errorf("share = %+v", n)
	}
}

func TestErrorsAndStates(t *testing.T) {
	st := ingest(t, 4, func(i int) string {
		up := 1
		if i == 2 {
			up = 0 // a flap
		}
		return fmt.Sprintf(`# TYPE http_requests_total counter
http_requests_total{code="200",path="/a"} %d
http_requests_total{code="503",path="/a"} %d
# TYPE dep_up gauge
dep_up{dep="db"} 0
dep_up{dep="cache"} %d
# TYPE breaker stateset
breaker{breaker="closed"} %d
breaker{breaker="open"} %d
`, 900*i, 100*i, up, up, 1-up)
	})
	d := build(st, Options{})

	n := notes(d, "http_requests_total")
	if len(n) != 1 || n[0].Reason != ReasonErrors || *n[0].Last != 10 || math.Abs(float64(*n[0].Share)-0.1) > 1e-9 {
		t.Fatalf("errors = %+v", n)
	}
	got := map[string]string{}
	for _, n := range notes(d, "dep_up") {
		got[n.Labels["dep"]] = n.Reason
	}
	if got["db"] != ReasonDown || got["cache"] != ReasonFlapped {
		t.Errorf("states = %v", got)
	}
	if n := notes(d, "breaker"); len(n) != 1 || n[0].Reason != ReasonFlapped || n[0].Labels["breaker"] != "closed" || n[0].Count != 2 {
		t.Errorf("stateset = %+v", n)
	}
	if (*d.Notable)[0].Reason != ReasonDown {
		t.Errorf("down should come first: %+v", *d.Notable)
	}
	m := find(d, "dep_up")
	for _, s := range m.Series {
		if s.Up == nil || *s.Up != (s.Labels["dep"] == "cache") {
			t.Errorf("state series = %+v", s)
		}
	}
}

func TestUsageAndUnits(t *testing.T) {
	st := ingest(t, 3, func(i int) string {
		return fmt.Sprintf(`# TYPE fs_avail_bytes gauge
fs_avail_bytes{mount="/data"} %d
fs_avail_bytes{mount="/"} 600
# TYPE fs_size_bytes gauge
fs_size_bytes{mount="/data"} 1000
fs_size_bytes{mount="/"} 1000
# TYPE job_wait_milliseconds gauge
job_wait_milliseconds{instance="a",queue="x"} 250
job_wait_milliseconds{instance="a",queue="y"} 100
`, 50-10*i)
	})
	d := build(st, Options{})

	n := notes(d, "fs_avail_bytes")
	if len(n) != 1 || n[0].Reason != ReasonNearLimit || n[0].Labels["mount"] != "/data" || *n[0].Last != 0.97 {
		t.Fatalf("usage notes = %+v", n)
	}
	m := find(d, "fs_avail_bytes")
	if m.Kind != "usage" || m.Limit != "fs_size_bytes" || m.Value != "used_ratio" {
		t.Fatalf("usage metric = %+v", m)
	}
	if s := m.Series[0]; s.Labels["mount"] != "/data" || *s.Used != 970 || *s.Limit != 1000 || *s.Last != 0.97 {
		t.Errorf("fullest mount first: %+v", s)
	}
	if find(d, "fs_size_bytes") != nil {
		t.Error("the limit is part of the usage metric")
	}

	w := find(d, "job_wait_milliseconds")
	if w.Unit != "seconds" || *w.Series[0].Last != 0.25 {
		t.Errorf("milliseconds should be converted: %+v", w)
	}
	if w.Labels["instance"] != "a" || len(w.Series[0].Labels) != 1 {
		t.Errorf("shared labels belong to the metric: %+v", w)
	}
}

func TestHistogramIgnoresLifetime(t *testing.T) {
	// A slow past (every lifetime observation above 1s) and a fast present: the
	// first scrape's lifetime quantiles must not read as a falling trend.
	st := ingest(t, 6, func(i int) string {
		fast := 1000 * i
		return fmt.Sprintf(`# TYPE rpc_seconds histogram
rpc_seconds_bucket{le="0.1"} %d
rpc_seconds_bucket{le="1"} %d
rpc_seconds_bucket{le="10"} %d
rpc_seconds_bucket{le="+Inf"} %d
rpc_seconds_sum 1
rpc_seconds_count %d
`, fast, fast, fast+5000, fast+5000, fast+5000)
	})
	d := build(st, Options{})
	if n := notes(d, "rpc_seconds"); len(n) != 0 {
		t.Errorf("notes = %+v", n)
	}
	s := find(d, "rpc_seconds").Series[0]
	if s.Trend != "flat" || *s.Max > 0.1 || *s.Rate != 100 || s.Quantiles["p50"] > 0.1 {
		t.Errorf("series = %+v", s)
	}
}

func TestTimestamps(t *testing.T) {
	st := ingest(t, 3, func(i int) string {
		return fmt.Sprintf(`# TYPE tls_cert_expiry_timestamp_seconds gauge
tls_cert_expiry_timestamp_seconds %d
# TYPE process_start_time_seconds gauge
process_start_time_seconds %d
`, t0.Add(48*time.Hour).Unix(), t0.Unix()+int64(10*(i/2)))
	})
	d := build(st, Options{})
	if n := notes(d, "tls_cert_expiry_timestamp_seconds"); len(n) != 1 || n[0].Reason != ReasonExpiresSoon || *n[0].InS != 48*3600-20 {
		t.Errorf("expiry = %+v", n)
	}
	if n := notes(d, "process_start_time_seconds"); len(n) != 1 || n[0].Reason != ReasonChanged {
		t.Errorf("restart = %+v", n)
	}
	s := find(d, "process_start_time_seconds").Series[0]
	if s.Time != "2027-01-15T08:00:10Z" || *s.AgeS != 10 {
		t.Errorf("time = %+v", s)
	}
}

func TestShape(t *testing.T) {
	gen := func(i int) string {
		var b strings.Builder
		b.WriteString("# TYPE req_total counter\n")
		for p := range 8 {
			fmt.Fprintf(&b, "req_total{path=\"/%d\"} %d\n", p, p*i)
		}
		// Enough single-series gauges for the dashboard to group them.
		for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
			fmt.Fprintf(&b, "# TYPE mem_%s_bytes gauge\nmem_%s_bytes 1\n", n, n)
		}
		return b.String()
	}
	st := ingest(t, 2, gen)

	d := build(st, Options{Top: 3})
	m := find(d, "req_total")
	if len(m.Series) != 3 || m.Omitted != 5 || m.Count != 8 || m.Series[0].Labels["path"] != "/7" {
		t.Errorf("top = %+v", m)
	}
	if find(d, "mem_a_bytes") == nil || d.Totals.Shown != 7 {
		t.Errorf("groups should be expanded into metrics: %+v", d.Totals)
	}

	var buf bytes.Buffer
	if err := Write(&buf, st, view.Build(st, view.Options{}), Options{List: true}); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["schema"] != Schema || raw["notable"] != nil {
		t.Errorf("list doc = %s", buf.String())
	}
	if strings.Contains(buf.String(), `"series":[`) || strings.Count(buf.String(), "\n") != 1 {
		t.Errorf("a list has no series and is one line: %s", buf.String())
	}

	buf.Reset()
	if err := Write(&buf, st, view.Build(st, view.Options{}), Options{Notable: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"notable":[]`) || strings.Contains(buf.String(), `"metrics":[`) {
		t.Errorf("notable doc = %s", buf.String())
	}
}

func TestNum(t *testing.T) {
	b, _ := json.Marshal(struct {
		A *Num `json:"a,omitempty"`
		B *Num `json:"b,omitempty"`
	}{num(math.NaN()), num(1.0 / 3)})
	if string(b) != `{"b":0.333333}` {
		t.Errorf("got %s", b)
	}
}
