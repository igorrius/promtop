// Package web serves the promtop dashboard to a browser: the same metric
// classification as the terminal UI, streamed as JSON over server-sent events
// to an embedded page.
package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/igorrius/promtop/internal/scrape"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

//go:embed assets
var assets embed.FS

// Config configures the web dashboard.
type Config struct {
	Source   scrape.Scraper
	Interval time.Duration
	History  int
	ShowAll  bool   // initial state of the "all metrics" toggle
	Filter   string // initial filter
	Addr     string // listen address; 127.0.0.1:0 picks a free local port
	Open     bool   // open the page in the default browser
	Out      io.Writer
	Denied   string // body of a 403; default "promtop only answers to localhost"
}

// Server scrapes the target and serves the dashboard.
type Server struct {
	cfg Config

	mu      sync.Mutex
	st      *store.Store
	lastErr error
	lastDur time.Duration
	lastAt  time.Time
	seq     int
	subs    map[chan struct{}]struct{}

	cacheSeq int
	cache    map[string][]byte // frame JSON by options, for the current seq

	start sync.Once
	stop  sync.Once
	done  chan struct{} // closed by Close: ends polling and event streams
	wg    sync.WaitGroup
}

// New returns a server that has not scraped yet.
func New(cfg Config) *Server {
	if cfg.Denied == "" {
		cfg.Denied = "promtop only answers to localhost"
	}
	return &Server{
		cfg: cfg, st: store.New(cfg.History), subs: map[chan struct{}]struct{}{},
		cache: map[string][]byte{}, done: make(chan struct{}),
	}
}

// Start begins scraping in the background; calls after the first do nothing.
func (s *Server) Start() {
	s.start.Do(func() {
		s.wg.Go(s.scrapeLoop)
	})
}

// Close stops scraping, ends open event streams and waits for the scrape
// loop to return. It is safe to call more than once, and without Start.
func (s *Server) Close() {
	s.stop.Do(func() { close(s.done) })
	s.wg.Wait()
}

// Run listens, opens the browser if asked and serves until ctx is done.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	addr := ln.Addr().(*net.TCPAddr)
	host := addr.IP.String()
	if addr.IP.IsUnspecified() {
		host = "localhost"
	}
	page := fmt.Sprintf("http://%s/", net.JoinHostPort(host, strconv.Itoa(addr.Port)))
	if q := s.query(); q != "" {
		page += "#" + q
	}
	_, _ = fmt.Fprintf(s.cfg.Out, "promtop: dashboard for %s at %s (Ctrl+C to stop)\n", s.cfg.Source, page)
	if !addr.IP.IsLoopback() {
		_, _ = fmt.Fprintf(s.cfg.Out, "promtop: listening on %s — anyone who can reach it sees these metrics\n", addr)
	}

	var allow func(*http.Request) bool
	if addr.IP.IsLoopback() {
		allow = HostOnly
	}
	s.Start()
	srv := &http.Server{Handler: s.Handler(allow), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		s.Close()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if s.cfg.Open {
		if err := openBrowser(page); err != nil {
			_, _ = fmt.Fprintf(s.cfg.Out, "promtop: could not open a browser (%v); open %s yourself\n", err, page)
		}
	}
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// query encodes the initial options for the page's URL fragment.
func (s *Server) query() string {
	var q []string
	if s.cfg.ShowAll {
		q = append(q, "all=1")
	}
	if s.cfg.Filter != "" {
		q = append(q, "filter="+url.QueryEscape(s.cfg.Filter))
	}
	return strings.Join(q, "&")
}

func (s *Server) scrapeLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		s.ingest(s.cfg.Source.Scrape(ctx))
		select {
		case <-s.done:
			return
		case <-t.C:
		}
	}
}

func (s *Server) ingest(r scrape.Result) {
	s.mu.Lock()
	s.lastDur, s.lastAt, s.lastErr = r.Duration, r.At, r.Err
	if r.Err == nil {
		s.st.Ingest(r.Families, r.At)
	}
	s.seq++
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default: // the client still has an update pending
		}
	}
	s.mu.Unlock()
}

// Handler returns the HTTP routes. Requests that allow rejects get a 403;
// a nil allow lets everyone in.
func (s *Server) Handler(allow func(*http.Request) bool) http.Handler {
	mux := http.HandlerFunc(s.route)
	if allow == nil {
		return mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allow(r) {
			http.Error(w, s.cfg.Denied, http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

var static, _ = fs.Sub(assets, "assets")

// route dispatches on the end of the path, not the whole of it, so the
// dashboard works wherever it is mounted, with or without http.StripPrefix.
// The page only uses relative URLs.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := r.URL.Path
	if p == "" {
		// The whole mount point was stripped (chi-style mounting): the
		// original path tells the page from the mount point without its slash.
		p = "/" + originalBase(r)
		if p == "/" {
			http.NotFound(w, r)
			return
		}
	} else if !strings.HasPrefix(p, "/") {
		p = "/" + p // http.StripPrefix with the mount point's trailing slash
	}
	base := path.Base(p)
	switch {
	case strings.HasSuffix(p, "/api/events"):
		s.events(w, r)
	case strings.HasSuffix(p, "/api/panel"):
		s.panel(w, r)
	case strings.HasSuffix(p, "/api/config"):
		s.config(w)
	case strings.HasSuffix(p, "/"):
		http.ServeFileFS(w, r, static, "index.html")
	case strings.HasSuffix(p, "/api"):
		http.NotFound(w, r)
	case isAsset(base):
		http.ServeFileFS(w, r, static, base)
	case !strings.Contains(base, "."):
		// The mount point without its slash: relative URLs need the slash.
		// A relative Location stays right behind StripPrefix and proxies.
		w.Header().Set("Location", "./"+url.PathEscape(base)+"/")
		w.WriteHeader(http.StatusMovedPermanently)
	default:
		http.NotFound(w, r)
	}
}

// originalBase is the last segment of the path as the client sent it, with
// the trailing slash kept: "webmetrics" or "webmetrics/" for /webmetrics/.
func originalBase(r *http.Request) string {
	u, err := url.ParseRequestURI(r.RequestURI)
	if err != nil || u.Path == "" || u.Path == "/" {
		return ""
	}
	if strings.HasSuffix(u.Path, "/") {
		return path.Base(u.Path) + "/"
	}
	return path.Base(u.Path)
}

func isAsset(name string) bool {
	st, err := fs.Stat(static, name)
	return err == nil && !st.IsDir() && name != "index.html"
}

// config tells the page its initial options when its URL has no fragment.
func (s *Server) config(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		All    bool   `json:"all"`
		Filter string `json:"filter"`
	}{s.cfg.ShowAll, s.cfg.Filter})
}

// HostOnly admits requests that name a loopback host, which stops
// DNS-rebinding pages from reading the metrics.
func HostOnly(r *http.Request) bool { return LoopbackHost(r.Host) }

// LoopbackHost reports whether hostport names localhost or a loopback IP.
func LoopbackHost(hostport string) bool {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

type options struct {
	all    bool
	filter *regexp.Regexp
	key    string
}

func parseOptions(r *http.Request) options {
	q := r.URL.Query()
	o := options{all: q.Get("all") == "1"}
	if f := q.Get("filter"); f != "" {
		re, err := regexp.Compile("(?i)" + f)
		if err != nil {
			re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(f))
		}
		o.filter = re
	}
	o.key = q.Get("all") + "\x00" + q.Get("filter")
	return o
}

func (s *Server) sections(o options) []*view.Section {
	return view.Build(s.st, view.Options{ShowAll: o.all, Filter: o.filter})
}

// frame returns the current frame as JSON; callers hold s.mu.
func (s *Server) frame(o options) []byte {
	if s.cacheSeq != s.seq {
		s.cacheSeq, s.cache = s.seq, map[string][]byte{}
	}
	if b, ok := s.cache[o.key]; ok {
		return b
	}
	fr := buildFrame(s.st, s.sections(o), time.Now())
	fr.Source = s.cfg.Source.String()
	fr.Interval = s.cfg.Interval.Seconds()
	fr.At = s.lastAt.UnixMilli()
	fr.Took = s.lastDur.Round(time.Millisecond).String()
	if s.lastErr != nil {
		fr.Err = s.lastErr.Error()
	}
	b, err := json.Marshal(fr)
	if err != nil {
		b = []byte(`{"err":"encoding failed"}`)
	}
	s.cache[o.key] = b
	return b
}

// lockedFrame is frame under the lock.
func (s *Server) lockedFrame(o options) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame(o)
}

// events streams a frame after every scrape.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	o := parseOptions(r)
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	send := func() bool {
		b := s.lockedFrame(o)
		var buf bytes.Buffer
		buf.WriteString("data: ")
		buf.Write(b)
		buf.WriteString("\n\n")
		if _, err := w.Write(buf.Bytes()); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send() {
		return
	}
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			return
		case <-ch:
			if !send() {
				return
			}
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// panel returns the full data of one panel, found by name in the current
// layout (groups only exist there).
func (s *Server) panel(w http.ResponseWriter, r *http.Request) {
	o := parseOptions(r)
	id := r.URL.Query().Get("id")
	s.mu.Lock()
	var p *view.Panel
	for _, sec := range s.sections(o) {
		for _, c := range sec.Panels {
			if c.Name() == id {
				p = c
			}
		}
	}
	if p == nil {
		if f := s.st.Get(id); f != nil {
			p = view.Classify(f, s.st)
		}
	}
	// Encode under the lock: the detail's series share memory with the
	// store, which the next scrape writes into.
	var body []byte
	if p != nil {
		body, _ = json.Marshal(detail(p, s.cfg.Interval, s.lastAt, time.Now()))
	}
	s.mu.Unlock()
	if p == nil {
		http.Error(w, "no such metric", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(append(body, '\n'))
}

// openBrowser opens url with $BROWSER when set (BROWSER=true turns it off),
// otherwise with the platform's default handler.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch b := os.Getenv("BROWSER"); {
	case b != "":
		cmd = exec.Command(b, url) //nolint:gosec // the user's own browser setting; url is ours
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", url) //nolint:gosec // url is the dashboard's own address
	case runtime.GOOS == "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec // as above
	default:
		cmd = exec.Command("xdg-open", url) //nolint:gosec // as above
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
