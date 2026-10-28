package expo

import (
	"math"
	"os"
	"strings"
	"testing"
)

func TestParseBasics(t *testing.T) {
	in := `# HELP http_requests_total Total requests.\nSecond line
# TYPE http_requests_total counter
http_requests_total{method="GET",code="200"} 1027 1395066363000
http_requests_total{code="500",method="POST"} 3
# TYPE rpc_duration_seconds histogram
rpc_duration_seconds_bucket{le="0.1"} 5
rpc_duration_seconds_bucket{le="+Inf"} 7
rpc_duration_seconds_sum 1.5
rpc_duration_seconds_count 7
# TYPE gc summary
gc{quantile="0.5"} NaN
gc_sum 0
gc_count 0
weird{path="a\"b\\c\nd"} -Inf
no_type 42
`
	fams, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(fams) != 5 {
		t.Fatalf("families = %d, want 5", len(fams))
	}
	c := fams[0]
	if c.Name != "http_requests_total" || c.Type != Counter || len(c.Samples) != 2 {
		t.Fatalf("counter = %+v", c)
	}
	if !strings.Contains(c.Help, "Total requests.") {
		t.Errorf("help = %q", c.Help)
	}
	if c.Samples[1].Labels[0].Name != "code" || c.Samples[1].Labels[1].Name != "method" {
		t.Errorf("labels not sorted: %+v", c.Samples[1].Labels)
	}
	h := fams[1]
	if h.Type != Histogram || len(h.Samples) != 4 {
		t.Fatalf("histogram = %+v", h)
	}
	if !math.IsInf(h.Samples[1].Value, 0) && h.Samples[1].Label("le") != "+Inf" {
		t.Errorf("le label = %q", h.Samples[1].Label("le"))
	}
	if s := fams[2]; s.Type != Summary || !math.IsNaN(s.Samples[0].Value) {
		t.Errorf("summary = %+v", s)
	}
	w := fams[3]
	if got := w.Samples[0].Label("path"); got != "a\"b\\c\nd" {
		t.Errorf("escaped label = %q", got)
	}
	if !math.IsInf(w.Samples[0].Value, -1) {
		t.Errorf("value = %v", w.Samples[0].Value)
	}
	if u := fams[4]; u.Type != Untyped || u.Samples[0].Value != 42 {
		t.Errorf("untyped = %+v", u)
	}
}

func TestParseOpenMetricsCounter(t *testing.T) {
	// OpenMetrics declares the counter without the _total suffix.
	in := `# TYPE jobs counter
jobs_total 12
jobs_created 1.7e9
# EOF
`
	fams, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(fams) != 1 || fams[0].Type != Counter || len(fams[0].Samples) != 2 {
		t.Fatalf("got %+v", fams)
	}
}

func TestParseOTelFixture(t *testing.T) {
	f, err := os.Open("testdata/otel_go_runtime.prom")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	fams, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*Family{}
	for _, f := range fams {
		byName[f.Name] = f
	}
	if len(fams) != 9 {
		t.Errorf("families = %d, want 9", len(fams))
	}
	h := byName["go_schedule_duration_seconds"]
	if h == nil || h.Type != Histogram || len(h.Samples) != 164 {
		t.Fatalf("schedule histogram: %+v", h)
	}
	if byName["go_memory_used_bytes"] == nil || len(byName["go_memory_used_bytes"].Samples) != 2 {
		t.Errorf("memory_used_bytes missing or wrong")
	}
	if ti := byName["target_info"]; ti == nil || ti.Samples[0].Label("service_name") != "pape" {
		t.Errorf("target_info: %+v", ti)
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{`m{a="b" 1`, `m{a=b} 1`, `m`, `m abc`} {
		if _, err := Parse(strings.NewReader(in)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", in)
		}
	}
}
