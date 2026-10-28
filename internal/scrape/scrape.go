// Package scrape fetches a /metrics endpoint (or reads a file) and parses it.
package scrape

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/expo"
)

const maxBody = 64 << 20

// Result is the outcome of one scrape.
type Result struct {
	Families []*expo.Family
	At       time.Time
	Duration time.Duration
	Err      error
}

// Source is a metrics endpoint or a local file.
type Source struct {
	URL     string
	File    string
	Header  http.Header
	Timeout time.Duration
	Client  *http.Client
	Version string
}

// String describes the source for the header line.
func (s *Source) String() string {
	if s.File != "" {
		return s.File
	}
	return s.URL
}

// Static reports whether the source never changes (a file).
func (s *Source) Static() bool { return s.File != "" }

// NormalizeTarget turns user input into a URL or file path:
//
//	8080              → http://localhost:8080/metrics
//	:8080             → http://localhost:8080/metrics
//	host:9100         → http://host:9100/metrics
//	https://host/x    → unchanged
//	./metrics.txt     → read from the file
func NormalizeTarget(in string) (rawURL, file string, err error) {
	in = strings.TrimSpace(in)
	if in == "" {
		in = "localhost:8080"
	}
	if strings.HasPrefix(in, "file://") {
		return "", strings.TrimPrefix(in, "file://"), nil
	}
	if !strings.Contains(in, "://") {
		if st, err := os.Stat(in); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(in)
			return "", abs, nil
		}
	}
	if isDigits(in) {
		in = "localhost:" + in
	}
	if strings.HasPrefix(in, ":") {
		in = "localhost" + in
	}
	if !strings.Contains(in, "://") {
		in = "http://" + in
	}
	u, err := url.Parse(in)
	if err != nil {
		return "", "", fmt.Errorf("invalid target %q: %w", in, err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("invalid target %q: missing host", in)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/metrics"
	}
	return u.String(), "", nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Scrape performs one fetch and parse.
func (s *Source) Scrape(ctx context.Context) Result {
	start := time.Now()
	fams, err := s.fetch(ctx)
	return Result{Families: fams, At: start, Duration: time.Since(start), Err: err}
}

func (s *Source) fetch(ctx context.Context) ([]*expo.Family, error) {
	if s.File != "" {
		data, err := os.ReadFile(s.File)
		if err != nil {
			return nil, err
		}
		return expo.Parse(bytes.NewReader(data))
	}
	if s.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range s.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// Ask for the classic text format; it is what every client library speaks.
	req.Header.Set("Accept", "text/plain;version=0.0.4;q=1,*/*;q=0.1")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "promtop/"+s.Version)
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "protobuf") {
		return nil, fmt.Errorf("unsupported content type %q", ct)
	}
	return expo.Parse(io.LimitReader(resp.Body, maxBody))
}
