package view

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
)

func panels(secs []*Section) map[string]*Panel {
	out := map[string]*Panel{}
	for _, s := range secs {
		for _, p := range s.Panels {
			out[p.Name()] = p
			if p.Group {
				for _, m := range p.Family.Series {
					out[m.Key] = p
				}
			}
		}
	}
	return out
}

func TestClassifyExporters(t *testing.T) {
	st := load(t, "testdata/exporters.prom")
	kind := map[string]Kind{
		"node_memory_MemAvailable_bytes":     KindUsage,
		"node_filesystem_avail_bytes":        KindUsage,
		"node_nf_conntrack_entries":          KindUsage,
		"node_filefd_allocated":              KindUsage,
		"node_load1":                         KindGauge,
		"node_boot_time_seconds":             KindTimestamp,
		"node_network_up":                    KindState,
		"probe_success":                      KindState,
		"probe_http_status_code":             KindGauge,
		"probe_ssl_earliest_cert_expiry":     KindTimestamp,
		"kube_pod_status_ready":              KindState,
		"kube_deployment_spec_replicas":      KindGauge,
		"kube_pod_labels":                    KindInfo,
		"jvm_gc_pause_milliseconds":          KindSummary,
		"app_last_success_timestamp_ms":      KindTimestamp,
		"app_queue_wait":                     KindGauge,
		"app_feature":                        KindState,
		"http.server.active_requests":        KindGauge,
		"node_network_carrier_changes_total": KindRate,
	}
	for name, want := range kind {
		f := st.Get(name)
		if f == nil {
			t.Errorf("%s: not parsed", name)
			continue
		}
		if got := Classify(f, st).Kind; got != want {
			t.Errorf("%s: kind %v, want %v", name, got, want)
		}
	}

	mem := Classify(st.Get("node_memory_MemAvailable_bytes"), st)
	if mem.Limit == nil || mem.Limit.Name != "node_memory_MemTotal_bytes" || !mem.LimitInvert {
		t.Fatalf("MemAvailable limit = %+v", mem.Limit)
	}
	if u := mem.Usage(st.Get("node_memory_MemAvailable_bytes").Live()[0]); math.Abs(u-0.75) > 1e-9 {
		t.Errorf("memory usage = %v, want 0.75", u)
	}
	fs := Classify(st.Get("node_filesystem_avail_bytes"), st)
	for _, s := range fs.Family.Live() {
		want := map[string]float64{"/": 0.8, "/data": 0.1}[s.Labels[1].Value]
		if u := fs.Usage(s); math.Abs(u-want) > 1e-9 {
			t.Errorf("filesystem %v usage = %v, want %v", s.Labels, u, want)
		}
	}
	if p := Classify(st.Get("node_memory_HugePages_Free"), st); p.Kind == KindUsage {
		t.Error("a zero limit must not pair")
	}

	// Units from name suffixes and OpenMetrics UNIT.
	if p := Classify(st.Get("jvm_gc_pause_milliseconds"), st); p.Format(1500.0/30) != "50ms" {
		t.Errorf("milliseconds: %s", p.Format(50))
	}
	if p := Classify(st.Get("app_queue_wait"), st); p.Unit != UnitSeconds || p.Format(0.25) != "250ms" {
		t.Errorf("UNIT seconds: %v %s", p.Unit, p.Format(0.25))
	}
	ts := Classify(st.Get("app_last_success_timestamp_ms"), st)
	if got := ts.Family.Live()[0].Raw * ts.Scale; got != 1.7e9 {
		t.Errorf("ms timestamp = %v seconds", got)
	}
}

func TestBuildExporters(t *testing.T) {
	st := load(t, "testdata/exporters.prom")
	ps := panels(Build(st, Options{}))
	for _, hidden := range []string{
		"node_memory_MemTotal_bytes",         // merged into MemAvailable
		"node_filesystem_size_bytes",         // merged into avail
		"node_nf_conntrack_entries_limit",    // merged into entries
		"node_network_carrier_changes_total", // idle
		"node_memory_HugePages_Free",         // idle
	} {
		if ps[hidden] != nil {
			t.Errorf("%s should be hidden", hidden)
		}
	}
	for _, shown := range []string{"node_network_up", "probe_success", "kube_pod_status_ready", "http.server.active_requests"} {
		if ps[shown] == nil {
			t.Errorf("%s should be shown", shown)
		}
	}
	all := panels(Build(st, Options{ShowAll: true}))
	if all["node_memory_MemTotal_bytes"] == nil || all["node_network_carrier_changes_total"] == nil {
		t.Error("show-all should include limits and idle metrics")
	}

	var titles []string
	for _, s := range Build(st, Options{}) {
		titles = append(titles, s.Title)
	}
	if got := strings.Join(titles, ","); !strings.Contains(got, "HTTP") {
		t.Errorf("dotted OTel name should get its own section: %s", got)
	}
}

// big returns a store with n single-series gauges per subsystem of one
// namespace, plus a few loners.
func big(t *testing.T, subsystems []string, n int) *store.Store {
	t.Helper()
	var b strings.Builder
	for _, sub := range subsystems {
		for i := range n {
			fmt.Fprintf(&b, "# TYPE host_%s_metric%d_bytes gauge\nhost_%s_metric%d_bytes %d\n", sub, i, sub, i, i+1)
		}
	}
	b.WriteString("# TYPE host_uptime_seconds gauge\nhost_uptime_seconds 5\n")
	b.WriteString("# TYPE host_load1 gauge\nhost_load1 0.3\n")
	fams, err := expo.Parse(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(5)
	st.Ingest(fams, time.Now())
	return st
}

func TestBuildSplitsAndGroups(t *testing.T) {
	// 40 gauges over two subsystems with a different unit each: grouping alone
	// keeps the section small, so it is not split.
	st := big(t, []string{"disk", "mem"}, 20)
	secs := Build(st, Options{})
	if len(secs) != 1 || secs[0].Title != "host" {
		t.Fatalf("sections = %v", sectionTitlesOf(secs))
	}
	var groups int
	for _, p := range secs[0].Panels {
		if p.Group {
			groups++
			if n := len(p.Family.Live()); n != 40 {
				t.Errorf("group has %d members, want 40", n)
			}
			if lbl := RowLabel(p.Family, p.Family.Live()[0]); lbl != "disk_metric0" {
				t.Errorf("member label = %q", lbl)
			}
		}
	}
	if groups != 1 {
		t.Errorf("groups = %d, want 1", groups)
	}

	// Many multi-series metrics cannot be grouped: the namespace is split by
	// subsystem, loners stay in the parent section, which comes first.
	var b strings.Builder
	for _, sub := range []string{"cpu", "disk", "net"} {
		for i := range 8 {
			fmt.Fprintf(&b, "host_%s_m%d{dev=\"a\"} 1\nhost_%s_m%d{dev=\"b\"} 2\n", sub, i, sub, i)
		}
	}
	b.WriteString("host_load1 1\n")
	fams, err := expo.Parse(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	st = store.New(5)
	st.Ingest(fams, time.Now())
	got := strings.Join(sectionTitlesOf(Build(st, Options{})), ",")
	if got != "host,host · cpu,host · disk,host · net" {
		t.Errorf("sections = %s", got)
	}
}

func sectionTitlesOf(secs []*Section) []string {
	var out []string
	for _, s := range secs {
		out = append(out, s.Title)
	}
	return out
}

func TestTokens(t *testing.T) {
	if got := strings.Join(tokens("http.server.request_duration:rate5m"), "|"); got != "http|server|request|duration|rate5m" {
		t.Errorf("tokens = %s", got)
	}
	if got := prefix("node_memory_MemFree_bytes", 2); got != "node_memory" {
		t.Errorf("prefix = %s", got)
	}
	if got := prefix("node_load1", 2); got != "node_load1" {
		t.Errorf("short prefix = %s", got)
	}
	if got := trimPrefixTokens("node_memory_MemFree_bytes", 2); got != "MemFree_bytes" {
		t.Errorf("trim = %s", got)
	}
	if got := stripUnit("MemFree_bytes"); got != "MemFree" {
		t.Errorf("stripUnit = %s", got)
	}
}

// A filter that selects both sides of a usage pair shows the usage panel
// only; one that selects just the limit shows the limit.
func TestFilterHidesMergedLimit(t *testing.T) {
	st := load(t, "testdata/exporters.prom")
	ps := panels(Build(st, Options{Filter: regexp.MustCompile("filesystem")}))
	if ps["node_filesystem_avail_bytes"] == nil || ps["node_filesystem_size_bytes"] != nil {
		t.Errorf("filesystem filter shows %v", keys(ps))
	}
	ps = panels(Build(st, Options{Filter: regexp.MustCompile("size")}))
	if ps["node_filesystem_size_bytes"] == nil {
		t.Errorf("size filter shows %v", keys(ps))
	}
}

func keys(m map[string]*Panel) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
