// Command demo serves synthetic metrics of every type on :8080/metrics so you
// can try promtop without a real service:
//
//	go run ./examples/demo &
//	promtop 8080
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type histogram struct {
	bounds []float64
	counts []uint64
	sum    float64
	count  uint64
}

func newHistogram(bounds ...float64) *histogram {
	return &histogram{bounds: bounds, counts: make([]uint64, len(bounds))}
}

func (h *histogram) observe(v float64) {
	for i, b := range h.bounds {
		if v <= b {
			h.counts[i]++
		}
	}
	h.sum += v
	h.count++
}

type state struct {
	mu        sync.Mutex
	start     time.Time
	requests  map[[3]string]float64 // method, path, code
	latency   map[[2]string]*histogram
	inflight  float64
	queue     float64
	bytesOut  float64
	cpu       float64
	jobLat    []float64 // for a summary
	jobCount  uint64
	jobSum    float64
	cacheHits float64
	cacheMiss float64
	poolBusy  float64
	diskUsed  float64 // bytes written to /var/lib/demo since start
	spool     float64
}

var paths = []string{"/api/orders", "/api/users", "/api/search", "/healthz"}

func (s *state) tick(dt float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := time.Since(s.start).Seconds()
	load := 1 + 0.6*math.Sin(t/20) + 0.2*rand.Float64()
	for _, p := range paths {
		base := map[string]float64{"/api/orders": 40, "/api/users": 25, "/api/search": 15, "/healthz": 2}[p]
		n := base * load * dt
		for range int(n) + 1 {
			code := "200"
			switch r := rand.Float64(); {
			case r < 0.02*load:
				code = "500"
			case r < 0.06:
				code = "404"
			}
			method := "GET"
			if p == "/api/orders" && rand.Float64() < 0.3 {
				method = "POST"
			}
			s.requests[[3]string{method, p, code}]++
			key := [2]string{method, p}
			h := s.latency[key]
			if h == nil {
				h = newHistogram(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10)
				s.latency[key] = h
			}
			lat := rand.ExpFloat64() * 0.02 * load
			if p == "/api/search" {
				lat *= 6
			}
			h.observe(lat)
			s.bytesOut += 512 + rand.Float64()*4096
		}
	}
	s.inflight = math.Max(0, 8*load+rand.NormFloat64()*2)
	s.queue = math.Max(0, 30*math.Sin(t/45)+30+rand.NormFloat64()*3)
	s.cpu += dt * 0.35 * load
	for range 3 {
		v := rand.ExpFloat64() * 0.3
		s.jobLat = append(s.jobLat, v)
		if len(s.jobLat) > 500 {
			s.jobLat = s.jobLat[1:]
		}
		s.jobCount++
		s.jobSum += v
	}
	s.cacheHits += 90 * load * dt
	s.cacheMiss += 10 * load * dt * (1 + 0.5*math.Sin(t/10))
	s.poolBusy = math.Min(64, math.Max(1, s.poolBusy+(28*load-s.poolBusy)*0.3+4*rand.NormFloat64()))
	s.diskUsed += 3e6 * load * dt
	s.spool = math.Max(0, s.spool+(2e6*load-s.spool)*0.2+5e5*rand.NormFloat64())
}

func (s *state) write(w *strings.Builder) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Fprintln(w, "# HELP demo_http_requests_total Total HTTP requests.")
	fmt.Fprintln(w, "# TYPE demo_http_requests_total counter")
	keys := make([][3]string, 0, len(s.requests))
	for k := range s.requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return strings.Join(keys[i][:], "") < strings.Join(keys[j][:], "") })
	for _, k := range keys {
		fmt.Fprintf(w, "demo_http_requests_total{code=%q,method=%q,path=%q} %g\n", k[2], k[0], k[1], s.requests[k])
	}

	fmt.Fprintln(w, "# HELP demo_http_request_duration_seconds Request latency.")
	fmt.Fprintln(w, "# TYPE demo_http_request_duration_seconds histogram")
	lk := make([][2]string, 0, len(s.latency))
	for k := range s.latency {
		lk = append(lk, k)
	}
	sort.Slice(lk, func(i, j int) bool { return lk[i][1]+lk[i][0] < lk[j][1]+lk[j][0] })
	for _, k := range lk {
		h := s.latency[k]
		for i, b := range h.bounds {
			fmt.Fprintf(w, "demo_http_request_duration_seconds_bucket{method=%q,path=%q,le=\"%g\"} %d\n", k[0], k[1], b, h.counts[i])
		}
		fmt.Fprintf(w, "demo_http_request_duration_seconds_bucket{method=%q,path=%q,le=\"+Inf\"} %d\n", k[0], k[1], h.count)
		fmt.Fprintf(w, "demo_http_request_duration_seconds_sum{method=%q,path=%q} %g\n", k[0], k[1], h.sum)
		fmt.Fprintf(w, "demo_http_request_duration_seconds_count{method=%q,path=%q} %d\n", k[0], k[1], h.count)
	}

	fmt.Fprintln(w, "# HELP demo_http_requests_in_flight Requests currently being served.")
	fmt.Fprintln(w, "# TYPE demo_http_requests_in_flight gauge")
	fmt.Fprintf(w, "demo_http_requests_in_flight %g\n", math.Round(s.inflight))
	fmt.Fprintln(w, "# HELP demo_http_response_size_bytes_total Bytes written to clients.")
	fmt.Fprintln(w, "# TYPE demo_http_response_size_bytes_total counter")
	fmt.Fprintf(w, "demo_http_response_size_bytes_total %g\n", s.bytesOut)

	fmt.Fprintln(w, "# HELP demo_queue_depth Jobs waiting in the queue.")
	fmt.Fprintln(w, "# TYPE demo_queue_depth gauge")
	fmt.Fprintf(w, "demo_queue_depth{queue=\"emails\"} %g\n", math.Round(s.queue))
	fmt.Fprintf(w, "demo_queue_depth{queue=\"reports\"} %g\n", math.Round(s.queue/3))
	fmt.Fprintln(w, "# HELP demo_job_duration_seconds Background job duration.")
	fmt.Fprintln(w, "# TYPE demo_job_duration_seconds summary")
	q := append([]float64(nil), s.jobLat...)
	sort.Float64s(q)
	for _, qq := range []float64{0.5, 0.9, 0.99} {
		v := math.NaN()
		if len(q) > 0 {
			v = q[int(qq*float64(len(q)-1))]
		}
		fmt.Fprintf(w, "demo_job_duration_seconds{quantile=\"%g\"} %g\n", qq, v)
	}
	fmt.Fprintf(w, "demo_job_duration_seconds_sum %g\n", s.jobSum)
	fmt.Fprintf(w, "demo_job_duration_seconds_count %d\n", s.jobCount)

	fmt.Fprintln(w, "# HELP demo_cache_requests_total Cache lookups by result.")
	fmt.Fprintln(w, "# TYPE demo_cache_requests_total counter")
	fmt.Fprintf(w, "demo_cache_requests_total{result=\"hit\"} %g\n", s.cacheHits)
	fmt.Fprintf(w, "demo_cache_requests_total{result=\"miss\"} %g\n", s.cacheMiss)
	// Health flags: kafka drops out for a few seconds every half minute.
	t := time.Since(s.start).Seconds()
	kafka := 1
	if math.Mod(t, 30) > 24 {
		kafka = 0
	}
	fmt.Fprintln(w, "# HELP demo_dependency_up Whether a downstream dependency answers its health check.")
	fmt.Fprintln(w, "# TYPE demo_dependency_up gauge")
	fmt.Fprintln(w, `demo_dependency_up{dependency="postgres"} 1`)
	fmt.Fprintln(w, `demo_dependency_up{dependency="redis"} 1`)
	fmt.Fprintf(w, "demo_dependency_up{dependency=\"kafka\"} %d\n", kafka)
	fmt.Fprintln(w, "# HELP demo_circuit_breaker Circuit breaker state towards the payment provider.")
	fmt.Fprintln(w, "# TYPE demo_circuit_breaker stateset")
	breaker := map[string]int{"closed": 1, "half_open": 0, "open": 0}
	if kafka == 0 {
		breaker = map[string]int{"closed": 0, "half_open": 0, "open": 1}
	}
	for _, st := range []string{"closed", "half_open", "open"} {
		fmt.Fprintf(w, "demo_circuit_breaker{demo_circuit_breaker=%q} %d\n", st, breaker[st])
	}

	// A connection pool and disks against their limits.
	fmt.Fprintln(w, "# HELP demo_db_pool_active_connections Connections in use.")
	fmt.Fprintln(w, "# TYPE demo_db_pool_active_connections gauge")
	fmt.Fprintf(w, "demo_db_pool_active_connections %g\n", math.Round(s.poolBusy))
	fmt.Fprintln(w, "# HELP demo_db_pool_max_connections Pool size.")
	fmt.Fprintln(w, "# TYPE demo_db_pool_max_connections gauge")
	fmt.Fprintln(w, "demo_db_pool_max_connections 64")
	disks := []struct {
		mount      string
		size, used float64
	}{{"/", 50e9, 31e9}, {"/var/lib/demo", 200e9, 172e9 + s.diskUsed}, {"/tmp", 8e9, 0.6e9 + s.spool*40}}
	fmt.Fprintln(w, "# HELP demo_disk_avail_bytes Free space available to the service.")
	fmt.Fprintln(w, "# TYPE demo_disk_avail_bytes gauge")
	for _, d := range disks {
		fmt.Fprintf(w, "demo_disk_avail_bytes{mount=%q} %g\n", d.mount, math.Max(0, d.size-d.used))
	}
	fmt.Fprintln(w, "# HELP demo_disk_size_bytes Size of the filesystem.")
	fmt.Fprintln(w, "# TYPE demo_disk_size_bytes gauge")
	for _, d := range disks {
		fmt.Fprintf(w, "demo_disk_size_bytes{mount=%q} %g\n", d.mount, d.size)
	}

	// Scalar memory gauges of one unit: promtop packs them into one panel.
	for _, m := range []struct {
		name string
		v    float64
	}{
		{"cache_memory", 180e6 + 20e6*math.Sin(t/15)},
		{"session_store", 42e6 + 1e5*t},
		{"upload_spool", s.spool},
		{"template_cache", 6.5e6},
	} {
		fmt.Fprintf(w, "# HELP demo_%s_bytes Memory held by the %s.\n", m.name, strings.ReplaceAll(m.name, "_", " "))
		fmt.Fprintf(w, "# TYPE demo_%s_bytes gauge\n", m.name)
		fmt.Fprintf(w, "demo_%s_bytes %g\n", m.name, math.Round(m.v))
	}

	fmt.Fprintln(w, "# HELP demo_last_deploy_timestamp_seconds When the running version was deployed.")
	fmt.Fprintln(w, "# TYPE demo_last_deploy_timestamp_seconds gauge")
	fmt.Fprintf(w, "demo_last_deploy_timestamp_seconds %d\n", s.start.Add(-26*time.Hour-40*time.Minute).Unix())
	fmt.Fprintln(w, "# HELP demo_tls_cert_expiry_timestamp_seconds When the serving certificate expires.")
	fmt.Fprintln(w, "# TYPE demo_tls_cert_expiry_timestamp_seconds gauge")
	fmt.Fprintf(w, "demo_tls_cert_expiry_timestamp_seconds %d\n", s.start.Add(23*24*time.Hour+5*time.Hour).Unix())

	fmt.Fprintln(w, "# HELP demo_build_info Build information.")
	fmt.Fprintln(w, "# TYPE demo_build_info gauge")
	fmt.Fprintf(w, "demo_build_info{version=\"1.4.2\",revision=\"a1b2c3d\",goversion=%q} 1\n", runtime.Version())

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintln(w, "# HELP go_goroutines Number of goroutines that currently exist.")
	fmt.Fprintln(w, "# TYPE go_goroutines gauge")
	fmt.Fprintf(w, "go_goroutines %d\n", runtime.NumGoroutine()+int(s.inflight))
	fmt.Fprintln(w, "# HELP go_memstats_heap_alloc_bytes Heap bytes allocated and in use.")
	fmt.Fprintln(w, "# TYPE go_memstats_heap_alloc_bytes gauge")
	fmt.Fprintf(w, "go_memstats_heap_alloc_bytes %d\n", ms.HeapAlloc)
	fmt.Fprintln(w, "# HELP go_memstats_alloc_bytes_total Total bytes allocated.")
	fmt.Fprintln(w, "# TYPE go_memstats_alloc_bytes_total counter")
	fmt.Fprintf(w, "go_memstats_alloc_bytes_total %d\n", ms.TotalAlloc)
	fmt.Fprintln(w, "# HELP go_memstats_sys_bytes Bytes obtained from the OS.")
	fmt.Fprintln(w, "# TYPE go_memstats_sys_bytes gauge")
	fmt.Fprintf(w, "go_memstats_sys_bytes %d\n", ms.Sys)
	fmt.Fprintln(w, "# HELP go_gc_duration_seconds GC pause durations.")
	fmt.Fprintln(w, "# TYPE go_gc_duration_seconds summary")
	pauses := make([]float64, 0, 256)
	for i := range int(min(ms.NumGC, 256)) {
		pauses = append(pauses, float64(ms.PauseNs[i])/1e9)
	}
	sort.Float64s(pauses)
	for _, qq := range []float64{0, 0.25, 0.5, 0.75, 1} {
		v := 0.0
		if len(pauses) > 0 {
			v = pauses[int(qq*float64(len(pauses)-1))]
		}
		fmt.Fprintf(w, "go_gc_duration_seconds{quantile=\"%g\"} %g\n", qq, v)
	}
	fmt.Fprintf(w, "go_gc_duration_seconds_sum %g\n", float64(ms.PauseTotalNs)/1e9)
	fmt.Fprintf(w, "go_gc_duration_seconds_count %d\n", ms.NumGC)

	fmt.Fprintln(w, "# HELP process_cpu_seconds_total Total user and system CPU time spent in seconds.")
	fmt.Fprintln(w, "# TYPE process_cpu_seconds_total counter")
	fmt.Fprintf(w, "process_cpu_seconds_total %g\n", s.cpu)
	fmt.Fprintln(w, "# HELP process_resident_memory_bytes Resident memory size in bytes.")
	fmt.Fprintln(w, "# TYPE process_resident_memory_bytes gauge")
	fmt.Fprintf(w, "process_resident_memory_bytes %d\n", ms.Sys+uint64(20e6))
	fmt.Fprintln(w, "# HELP process_open_fds Number of open file descriptors.")
	fmt.Fprintln(w, "# TYPE process_open_fds gauge")
	fmt.Fprintf(w, "process_open_fds %g\n", 40+math.Round(s.inflight*3))
	fmt.Fprintln(w, "# HELP process_max_fds Maximum number of open file descriptors.")
	fmt.Fprintln(w, "# TYPE process_max_fds gauge")
	fmt.Fprintln(w, "process_max_fds 1024")
	fmt.Fprintln(w, "# HELP process_start_time_seconds Start time of the process since unix epoch in seconds.")
	fmt.Fprintln(w, "# TYPE process_start_time_seconds gauge")
	fmt.Fprintf(w, "process_start_time_seconds %g\n", float64(s.start.Add(-3*time.Hour-17*time.Minute).UnixNano())/1e9)
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()
	s := &state{start: time.Now(), requests: map[[3]string]float64{}, latency: map[[2]string]*histogram{}}
	go func() {
		last := time.Now()
		for now := range time.Tick(250 * time.Millisecond) {
			s.tick(now.Sub(last).Seconds())
			last = now
		}
	}()
	http.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		var b strings.Builder
		s.write(&b)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	})
	log.Printf("demo metrics on http://localhost%s/metrics", *addr)
	srv := &http.Server{Addr: *addr, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
