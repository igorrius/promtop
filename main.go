// Command promtop shows a live terminal dashboard for a Prometheus /metrics
// endpoint, choosing a suitable widget for every metric automatically.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/igorrius/promtop/internal/scrape"
	"github.com/igorrius/promtop/internal/ui"
)

var version = "dev"

type headerFlag []string

func (h *headerFlag) String() string     { return strings.Join(*h, ", ") }
func (h *headerFlag) Set(v string) error { *h = append(*h, v); return nil }

const usage = `promtop — live terminal dashboard for a Prometheus /metrics endpoint

Usage:
  promtop [flags] [target]

Target (default localhost:8080):
  8080 | :8080             http://localhost:8080/metrics
  host:9100                http://host:9100/metrics
  https://host/path        used as is
  ./metrics.txt            read an exposition from a file

Flags:
`

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("promtop", flag.ContinueOnError)
	var (
		interval = fs.Duration("i", 2*time.Second, "scrape interval")
		history  = fs.Int("history", 300, "number of samples kept per series")
		once     = fs.Bool("once", false, "scrape twice, print one frame and exit")
		width    = fs.Int("width", 0, "render width for --once (default: terminal width or 120)")
		noColor  = fs.Bool("no-color", false, "disable colours (also NO_COLOR env)")
		all      = fs.Bool("a", false, "show all metrics, including low-level runtime noise")
		filter   = fs.String("f", "", "only show metrics matching this regex")
		timeout  = fs.Duration("timeout", 0, "scrape timeout (default: interval, at most 10s)")
		showVer  = fs.Bool("version", false, "print version and exit")
		headers  headerFlag
	)
	fs.Var(&headers, "H", `extra request header, repeatable ("Authorization: Bearer …")`)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}

	// Allow flags before and after the target.
	args, positional := os.Args[1:], []string(nil)
	for {
		if err := fs.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return 0
			}
			return 2
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
	if *showVer {
		fmt.Println("promtop", version)
		return 0
	}
	if len(positional) > 1 {
		fmt.Fprintln(os.Stderr, "promtop: expected a single target")
		return 2
	}
	target := ""
	if len(positional) == 1 {
		target = positional[0]
	}
	rawURL, file, err := scrape.NormalizeTarget(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "promtop:", err)
		return 2
	}
	if *interval < 100*time.Millisecond {
		*interval = 100 * time.Millisecond
	}
	to := *timeout
	if to <= 0 {
		to = min(*interval, 10*time.Second)
		if to < time.Second {
			to = time.Second
		}
	}
	hdr := http.Header{}
	for _, h := range headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			fmt.Fprintf(os.Stderr, "promtop: bad header %q, want \"Name: value\"\n", h)
			return 2
		}
		hdr.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	src := &scrape.Source{URL: rawURL, File: file, Header: hdr, Timeout: to, Version: version}

	color := !*noColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	if *once && !isTTY(os.Stdout) {
		color = false
	}
	app := ui.New(ui.Config{
		Source: src, Interval: *interval, History: *history, Color: color,
		ShowAll: *all, Filter: *filter, Width: *width,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *once {
		err = app.RunOnce(ctx)
	} else {
		err = app.Run(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "promtop:", err)
		return 1
	}
	return 0
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
