package view

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		v    float64
		u    Unit
		rate bool
		want string
	}{
		{1536, UnitBytes, false, "1.5KiB"},
		{4.49239762e+08, UnitBytes, false, "428MiB"},
		{2048, UnitBytes, true, "2KiB/s"},
		{0.000127, UnitSeconds, false, "127µs"},
		{0.25, UnitSeconds, false, "250ms"},
		{3, UnitSeconds, false, "3s"},
		{0.453, UnitSeconds, true, "45.3%"},
		{0.0712, UnitRatio, false, "7.12%"},
		{365, UnitNone, false, "365"},
		{15440144, UnitNone, false, "15.4M"},
		{12.5, UnitNone, true, "12.5/s"},
		{math.NaN(), UnitNone, false, "–"},
	}
	for _, c := range cases {
		if got := FormatValue(c.v, c.u, "", c.rate); got != c.want {
			t.Errorf("FormatValue(%v, %v, %v) = %q, want %q", c.v, c.u, c.rate, got, c.want)
		}
	}
	if got := Duration(3*3600 + 17*60); got != "3h17m" {
		t.Errorf("Duration = %q", got)
	}
}

func TestUnitOf(t *testing.T) {
	cases := map[string]Unit{
		"go_memory_allocated_bytes_total": UnitBytes,
		"http_request_duration_seconds":   UnitSeconds,
		"cache_hit_ratio":                 UnitRatio,
		"go_config_gogc_percent":          UnitPercent,
		"go_goroutine_count":              UnitNone,
	}
	for n, want := range cases {
		if got, _ := UnitOf(n); got != want {
			t.Errorf("UnitOf(%s) = %v, want %v", n, got, want)
		}
	}
	if u, sym := UnitOf("node_hwmon_temp_celsius"); u != UnitOther || sym != "°C" {
		t.Errorf("celsius = %v %q", u, sym)
	}
}

func load(t *testing.T, path string) *store.Store {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fams, err := expo.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(10)
	st.Ingest(fams, time.Now())
	return st
}

func TestClassifyOTelFixture(t *testing.T) {
	st := load(t, "../expo/testdata/otel_go_runtime.prom")
	want := map[string]Kind{
		"go_goroutine_count":              KindGauge,
		"go_memory_allocated_bytes_total": KindRate,
		"go_schedule_duration_seconds":    KindHistogram,
		"go_memory_used_bytes":            KindGauge,
	}
	for name, k := range want {
		p := Classify(st.Get(name), st)
		if p.Kind != k {
			t.Errorf("%s: kind %v, want %v", name, p.Kind, k)
		}
	}
	if got := Target(st); got != "pape" {
		t.Errorf("Target = %q, want pape", got)
	}
	secs := Build(st, Options{})
	if len(secs) != 1 || secs[0].Title != "Go runtime" {
		t.Fatalf("sections = %+v", secs)
	}
	if first := secs[0].Panels[0].Name(); first != "go_goroutine_count" {
		t.Errorf("first panel = %s, want go_goroutine_count", first)
	}
	for _, p := range secs[0].Panels {
		if p.Name() == "target_info" {
			t.Error("target_info should be shown in the header only")
		}
	}
	// otel_scope_* labels are hidden.
	f := st.Get("go_memory_used_bytes")
	for _, s := range f.Live() {
		for _, l := range VisibleLabels(f, s) {
			if strings.HasPrefix(l.Name, "otel_scope_") {
				t.Errorf("visible otel label %s", l.Name)
			}
		}
	}
}

func TestClassifySpecialGauges(t *testing.T) {
	in := `# TYPE process_open_fds gauge
process_open_fds 40
# TYPE process_max_fds gauge
process_max_fds 1024
# TYPE process_start_time_seconds gauge
process_start_time_seconds 1.7e9
# TYPE app_build_info gauge
app_build_info{version="1.0"} 1
# TYPE jobs_total untyped
jobs_total 5
`
	fams, err := expo.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(5)
	st.Ingest(fams, time.Now())
	want := map[string]Kind{
		"process_open_fds":           KindUsage,
		"process_start_time_seconds": KindTimestamp,
		"app_build_info":             KindInfo,
		"jobs_total":                 KindRate,
	}
	for name, k := range want {
		if got := Classify(st.Get(name), st).Kind; got != k {
			t.Errorf("%s: %v, want %v", name, got, k)
		}
	}
	secs := Build(st, Options{})
	for _, s := range secs {
		for _, p := range s.Panels {
			if p.Name() == "process_max_fds" {
				t.Error("process_max_fds should be hidden (merged into open_fds)")
			}
		}
	}
}
