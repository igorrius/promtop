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

// runtimeExposition is a Go and a JVM service over a window: allocations at
// alloc(i) per second, a GC per 100k of them whose pauses last pause(i)
// seconds, a resident memory that keeps climbing and a steady share of 5xx.
func runtimeExposition(alloc, pause func(i int) float64) func(i int) string {
	var mallocs, frees, allocBytes, gcs, gcSum float64
	return func(i int) string {
		if i > 0 {
			r := alloc(i) * 10 // per scrape, 10s apart
			mallocs += r
			frees += 0.97 * r
			allocBytes += 64 * r
			gcs += r / 1e5
			gcSum += r / 1e5 * pause(i)
		}
		p := pause(i)
		return fmt.Sprintf(`# TYPE go_memstats_mallocs_total counter
go_memstats_mallocs_total %g
# TYPE go_memstats_frees_total counter
go_memstats_frees_total %g
# TYPE go_memstats_alloc_bytes_total counter
go_memstats_alloc_bytes_total %g
# TYPE go_memory_allocations_total counter
go_memory_allocations_total %g
# TYPE go_memory_allocated_bytes_total counter
go_memory_allocated_bytes_total %g
# TYPE go_gc_cycles_automatic_gc_cycles_total counter
go_gc_cycles_automatic_gc_cycles_total %g
# TYPE go_gc_duration_seconds summary
go_gc_duration_seconds{quantile="0"} %g
go_gc_duration_seconds{quantile="0.5"} %g
go_gc_duration_seconds{quantile="1"} %g
go_gc_duration_seconds_sum %g
go_gc_duration_seconds_count %g
# TYPE jvm_memory_pool_allocated_bytes_total counter
jvm_memory_pool_allocated_bytes_total{pool="G1 Eden Space"} %g
# TYPE jvm_gc_memory_allocated_bytes_total counter
jvm_gc_memory_allocated_bytes_total %g
# TYPE jvm_gc_memory_promoted_bytes_total counter
jvm_gc_memory_promoted_bytes_total %g
# TYPE jvm_gc_collection_seconds summary
jvm_gc_collection_seconds_sum{gc="G1 Young Generation"} %g
jvm_gc_collection_seconds_count{gc="G1 Young Generation"} %g
# TYPE jvm_gc_pause_seconds summary
jvm_gc_pause_seconds_sum{action="end of minor GC"} %g
jvm_gc_pause_seconds_count{action="end of minor GC"} %g
# TYPE jvm_gc_pause_seconds_max gauge
jvm_gc_pause_seconds_max{action="end of minor GC"} %g
# TYPE process_resident_memory_bytes gauge
process_resident_memory_bytes %d
# TYPE http_requests_total counter
http_requests_total{code="200"} %d
http_requests_total{code="500"} %d
`, mallocs, frees, allocBytes, mallocs, allocBytes, gcs,
			p/4, p/2, p, gcSum, gcs,
			allocBytes, allocBytes, allocBytes/50,
			gcSum, gcs, gcSum, gcs, p,
			100e6+i*20e6, 900*i, 100*i)
	}
}

func TestRuntimeChurn(t *testing.T) {
	// A burst of work (renders of /metrics, a batch) changes the allocation
	// rate tenfold. How much the runtime allocates, frees, promotes and
	// collects follows the work, in bursts; that is no news, and a leak shows
	// in the resident memory instead. A JVM summary without quantiles plots
	// its collections per second, so it is such a count too. How long a GC
	// pauses is another matter: pauses are short by design whatever the
	// allocation rate, so pauses that grow keep their notes.
	steady := func(int) float64 { return 1e6 }
	burst := func(i int) float64 {
		if i >= 5 {
			return 1e5
		}
		return 1e6
	}
	quiet := func(i int) float64 {
		if i >= 5 {
			return 1e6
		}
		return 1e5
	}
	shortPauses := func(i int) float64 { return 100e-6 + float64(i%2)*5e-6 }
	longerPauses := func(i int) float64 {
		if i >= 5 {
			return 10e-3
		}
		return 1e-3
	}
	churn := []string{
		"go_memstats_mallocs_total", "go_memstats_frees_total", "go_memstats_alloc_bytes_total",
		"go_memory_allocations_total", "go_memory_allocated_bytes_total", "go_gc_cycles_automatic_gc_cycles_total",
		"jvm_memory_pool_allocated_bytes_total", "jvm_gc_memory_allocated_bytes_total",
		"jvm_gc_memory_promoted_bytes_total", "jvm_gc_collection_seconds", "jvm_gc_pause_seconds",
	}
	pauses := []string{"go_gc_duration_seconds", "jvm_gc_pause_seconds_max"}
	for _, tc := range []struct {
		name         string
		alloc, pause func(int) float64
		trend        string // of the counts
		pausesNoted  bool
	}{
		{"allocations fall", burst, shortPauses, ReasonFalling, false},
		{"allocations rise", quiet, shortPauses, ReasonRising, false},
		{"pauses grow", steady, longerPauses, "flat", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := ingest(t, 9, runtimeExposition(tc.alloc, tc.pause))
			o := Options{Interval: 10 * time.Second, From: t0, To: t0.Add(80 * time.Second), Now: t0.Add(80 * time.Second)}
			d := Build(st, view.Build(st, view.Options{ShowAll: true}), o) // some are hidden as noise
			for _, name := range churn {
				if n := notes(d, name); len(n) != 0 {
					t.Errorf("%s follows the work, no news: %+v", name, n)
				}
				m := find(d, name)
				if m == nil || len(m.Series) != 1 || m.Series[0].Trend != tc.trend {
					t.Errorf("%s stays in the summary with its trend %s: %+v", name, tc.trend, m)
				}
			}
			for _, name := range pauses {
				n := notes(d, name)
				if tc.pausesNoted && (len(n) != 1 || n[0].Reason != ReasonRising) {
					t.Errorf("%s: pauses ten times longer are news: %+v", name, n)
				}
				if !tc.pausesNoted && len(n) != 0 {
					t.Errorf("%s: more GCs of the same short pause are no news: %+v", name, n)
				}
			}
			if n := notes(d, "process_resident_memory_bytes"); len(n) != 1 || n[0].Reason != ReasonRising {
				t.Errorf("a growing RSS is news: %+v", n)
			}
			if n := notes(d, "http_requests_total"); len(n) != 1 || n[0].Reason != ReasonErrors {
				t.Errorf("5xx are news: %+v", n)
			}
		})
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
