package dash

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const fast = 10 * time.Millisecond

func metrics(calls *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls != nil {
			calls.Add(1)
		}
		_, _ = io.WriteString(w, "# TYPE app_up gauge\napp_up 1\n")
	})
}

func get(h http.Handler, path, remote string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr, r.Host = remote, "localhost:8080"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestServe(t *testing.T) {
	// The first scrape is slow, as on a loaded CI runner: the stream starts
	// with an empty frame and the data follows.
	var slow sync.Once
	d := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slow.Do(func() { time.Sleep(100 * time.Millisecond) })
		metrics(nil).ServeHTTP(w, r)
	}), Interval(fast), Title("orders"))
	defer func() { _ = d.Close() }()
	mux := http.NewServeMux()
	mux.Handle("/webmetrics/", d)
	srv := httptest.NewServer(mux) // a loopback client with a loopback Host
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/webmetrics/api/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("events: %d", res.StatusCode)
	}
	// Read frames until one carries the metrics; the stream ends at the deadline.
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(nil, 1<<20)
	var last string
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		last = line
		var fr struct {
			Source  string `json:"source"`
			Scrapes int    `json:"scrapes"`
		}
		if err := json.Unmarshal([]byte(line), &fr); err != nil {
			t.Fatal(err)
		}
		if fr.Source != "orders" {
			t.Fatalf("source %q", fr.Source)
		}
		if fr.Scrapes > 0 && strings.Contains(line, `"app_up"`) {
			return
		}
	}
	t.Errorf("no frame with app_up before the deadline; last: %s", last)
}

func TestDefaultDenies(t *testing.T) {
	d := New(metrics(nil))
	defer func() { _ = d.Close() }()
	rec := get(d, "/api/config", "172.17.0.1:1")
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "dash.Allow") {
		t.Errorf("docker bridge: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(d, "/api/config", "127.0.0.1:1"); rec.Code != http.StatusOK {
		t.Errorf("localhost: %d", rec.Code)
	}
}

func TestAllowAnyone(t *testing.T) {
	d := New(metrics(nil), Allow(Anyone))
	defer func() { _ = d.Close() }()
	if rec := get(d, "/api/config", "172.17.0.1:1"); rec.Code != http.StatusOK {
		t.Errorf("Anyone: %d", rec.Code)
	}
}

func TestAllowNilKeepsLocalhost(t *testing.T) {
	d := New(metrics(nil), Allow(nil))
	defer func() { _ = d.Close() }()
	if rec := get(d, "/api/config", "172.17.0.1:1"); rec.Code != http.StatusForbidden {
		t.Errorf("Allow(nil): %d", rec.Code)
	}
}

func TestUserAgent(t *testing.T) {
	got := make(chan string, 1)
	d := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case got <- r.Header.Get("User-Agent"):
		default:
		}
		_, _ = io.WriteString(w, "up 1\n")
	}), Interval(fast))
	defer func() { _ = d.Close() }()
	select {
	case ua := <-got:
		if !strings.HasPrefix(ua, "promtop-dash/") {
			t.Errorf("User-Agent %q", ua)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("metrics handler never called")
	}
}

func TestFilterConfig(t *testing.T) {
	d := New(metrics(nil), Filter("disk"))
	defer func() { _ = d.Close() }()
	rec := get(d, "/api/config", "127.0.0.1:1")
	if !strings.Contains(rec.Body.String(), `"filter":"disk"`) {
		t.Errorf("config: %s", rec.Body.String())
	}
}

func TestClose(t *testing.T) {
	var calls atomic.Int64
	d := New(metrics(&calls), Interval(fast))
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if rec := get(d, "/", "127.0.0.1:1"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("after Close: %d", rec.Code)
	}
	n := calls.Load()
	time.Sleep(3 * fast)
	if m := calls.Load(); m != n {
		t.Errorf("still scraping after Close: %d → %d", n, m)
	}
}

func TestCloseWithHungHandler(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	d := New(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }), Interval(time.Hour))
	closed := make(chan struct{})
	go func() { _ = d.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waits for a hung metrics handler")
	}
}

// Closing the dashboard on shutdown ends its event streams, so
// http.Server.Shutdown does not wait for them.
func TestCloseOnShutdown(t *testing.T) {
	d := New(metrics(nil), Interval(fast))
	mux := http.NewServeMux()
	mux.Handle("/webmetrics/", d)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.RegisterOnShutdown(func() { _ = d.Close() })
	srv.Start()
	res, err := http.Get(srv.URL + "/webmetrics/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if _, err := bufio.NewReader(res.Body).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown with an open dashboard: %v", err)
	}
}
