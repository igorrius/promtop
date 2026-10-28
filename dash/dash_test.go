package dash

import (
	"bufio"
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
	d := New(metrics(nil), Interval(fast), Title("orders"))
	defer func() { _ = d.Close() }()
	mux := http.NewServeMux()
	mux.Handle("/webmetrics/", d)
	srv := httptest.NewServer(mux) // a loopback client with a loopback Host
	defer srv.Close()

	res, err := http.Get(srv.URL + "/webmetrics/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("events: %d", res.StatusCode)
	}
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var fr struct {
		Source string   `json:"source"`
		Names  []string `json:"names"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &fr); err != nil {
		t.Fatal(err)
	}
	if fr.Source != "orders" || !strings.Contains(line, `"app_up"`) {
		t.Errorf("frame: %s", line)
	}
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
	var mu sync.Mutex
	var ua string
	d := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ua = r.Header.Get("User-Agent")
		mu.Unlock()
		_, _ = io.WriteString(w, "up 1\n")
	}), Interval(fast))
	_ = d.Close() // New has scraped at least once by the time Close returns
	mu.Lock()
	defer mu.Unlock()
	if !strings.HasPrefix(ua, "promtop-dash/") {
		t.Errorf("User-Agent %q", ua)
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
