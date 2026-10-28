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
		for i := 0; i < int(n)+1; i++ {
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
	for i := 0; i < 3; i++ {
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
	for i := 0; i < int(min(ms.NumGC, 256)); i++ {
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
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		s.write(&b)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	})
	log.Printf("demo metrics on http://localhost%s/metrics", *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
