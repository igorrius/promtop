package scrape

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// A metrics handler that never returns must not hang the scraper: Scrape
// gives up at the timeout, and while the call is stuck later scrapes fail
// at once instead of piling up more stuck calls.
func TestHandlerHung(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := &Handler{H: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }), Timeout: 20 * time.Millisecond}

	start := time.Now()
	res := h.Scrape(t.Context())
	if res.Err == nil || !strings.Contains(res.Err.Error(), "did not answer") || time.Since(start) > time.Second {
		t.Fatalf("hung handler: %v after %s", res.Err, time.Since(start))
	}
	if res := h.Scrape(t.Context()); res.Err == nil || !strings.Contains(res.Err.Error(), "busy") {
		t.Errorf("second scrape while stuck: %v", res.Err)
	}
}
