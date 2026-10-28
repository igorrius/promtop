package scrape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/igorrius/promtop/internal/expo"
)

// Handler scrapes a metrics http.Handler in-process: no network, no port.
type Handler struct {
	H         http.Handler
	Name      string // shown in the header line; "this process" when empty
	UserAgent string
	Timeout   time.Duration

	busy atomic.Bool // a call has not returned yet
}

// cancelGrace is how long a cancelled Scrape waits for the handler.
const cancelGrace = 100 * time.Millisecond

var _ Scraper = (*Handler)(nil)

// String describes the source for the header line.
func (h *Handler) String() string {
	if h.Name != "" {
		return h.Name
	}
	return "this process"
}

// Scrape calls the handler once and parses what it wrote.
func (h *Handler) Scrape(ctx context.Context) Result {
	start := time.Now()
	fams, err := h.fetch(ctx)
	return Result{Families: fams, At: start, Duration: time.Since(start), Err: err}
}

func (h *Handler) fetch(ctx context.Context) ([]*expo.Family, error) {
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/metrics", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if h.UserAgent != "" {
		req.Header.Set("User-Agent", h.UserAgent)
	}
	// The handler may ignore ctx, so it runs on its own goroutine and is
	// left behind if it does not answer in time. Only one such call at once.
	if !h.busy.CompareAndSwap(false, true) {
		return nil, errors.New("metrics handler still busy with an earlier scrape")
	}
	type answer struct {
		w   *recorder
		err error
	}
	ch := make(chan answer, 1)
	go func() {
		w := &recorder{header: http.Header{}}
		defer func() {
			a := answer{w: w}
			if v := recover(); v != nil {
				a = answer{err: fmt.Errorf("metrics handler panicked: %v", v)}
			}
			h.busy.Store(false)
			ch <- a
		}()
		h.H.ServeHTTP(w, req)
	}()
	var w *recorder
	select {
	case a := <-ch:
		if a.err != nil {
			return nil, a.err
		}
		w = a.w
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("metrics handler did not answer within %s", h.Timeout)
		}
		// Cancelled, e.g. the dashboard is closing: let a call in progress
		// finish so it does not outlive the caller, but not a hung one.
		select {
		case <-ch:
		case <-time.After(cancelGrace):
		}
		return nil, ctx.Err()
	}

	switch {
	case w.status() < 200 || w.status() > 299:
		return nil, fmt.Errorf("metrics handler returned %d %s", w.status(), http.StatusText(w.status()))
	case w.header.Get("Content-Encoding") != "":
		return nil, fmt.Errorf("unsupported Content-Encoding %q", w.header.Get("Content-Encoding"))
	case w.tooLarge:
		return nil, fmt.Errorf("metrics larger than %d MiB", maxBody>>20)
	}
	return expo.Parse(&w.body)
}

// recorder is the ResponseWriter handed to the metrics handler.
type recorder struct {
	header   http.Header
	code     int
	body     bytes.Buffer
	tooLarge bool
}

var errTooLarge = errors.New("metrics body too large")

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
}

func (r *recorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	if r.tooLarge || r.body.Len()+len(b) > maxBody {
		r.tooLarge = true
		return 0, errTooLarge
	}
	return r.body.Write(b)
}

func (r *recorder) status() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}
