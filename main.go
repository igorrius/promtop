// Command promtop shows a live dashboard for a Prometheus /metrics endpoint
// — in the terminal or the browser — and draws one-off snapshots of it,
// choosing a suitable widget for every metric automatically.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/igorrius/promtop/internal/ui"
	webui "github.com/igorrius/promtop/internal/web"
)

var version = "dev"

const usage = `promtop — live dashboard for a Prometheus /metrics endpoint

Usage:
  promtop [flags] [target]          live dashboard in the terminal
  promtop -web [flags] [target]     the same in the browser
  promtop render [flags] [target]   draw once into PNG, SVG, JSON or text
  promtop record -state f [target]  add a scrape to a history file (for cron)

Run "promtop render -h" or "promtop record -h" for their flags.

Target (default localhost:8080):
  8080 | :8080             http://localhost:8080/metrics
  host:9100                http://host:9100/metrics
  https://host/path        used as is
  ./metrics.txt            read an exposition from a file

Flags:
`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "render":
			os.Exit(runRender(os.Args[2:]))
		case "record":
			os.Exit(runRecord(os.Args[2:]))
		}
	}
	os.Exit(runLive(os.Args[1:]))
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func runLive(args []string) int {
	fs := flag.NewFlagSet("promtop", flag.ContinueOnError)
	sf := addSourceFlags(fs, 2*time.Second)
	var (
		history = fs.Int("history", 300, "number of samples kept per series")
		once    = fs.Bool("once", false, "scrape twice, print one frame and exit")
		width   = fs.Int("width", 0, "render width for --once (default: terminal width or 120)")
		noColor = fs.Bool("no-color", false, "disable colours (also NO_COLOR env)")
		all     = fs.Bool("a", false, "show all metrics, including low-level runtime noise")
		filter  = fs.String("f", "", "only show metrics matching this regex")
		showVer = fs.Bool("version", false, "print version and exit")
		web     = fs.Bool("web", false, "serve the dashboard over HTTP and open it in the browser instead of the terminal UI")
		webAddr = fs.String("web-addr", "127.0.0.1:0", "listen address for -web (default: a free port on localhost)")
	)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), usage)
		fs.PrintDefaults()
	}
	target, code := parseArgs(fs, args)
	if code >= 0 {
		return code
	}
	if *showVer {
		fmt.Println("promtop", version)
		return 0
	}
	src, err := sf.source(target)
	if err != nil {
		return usageErr(err)
	}
	if *web && *once {
		return usageErr(errors.New("-web and -once do not combine"))
	}
	ctx, stop := signalContext()
	defer stop()
	if *web {
		srv := webui.New(webui.Config{
			Source: src, Interval: *sf.interval, History: *history, ShowAll: *all, Filter: *filter,
			Addr: *webAddr, Open: true, Out: os.Stderr,
		})
		if err := srv.Run(ctx); err != nil {
			return fail(err)
		}
		return 0
	}

	color := !*noColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	if *once && !isTTY(os.Stdout) {
		color = false
	}
	app := ui.New(ui.Config{
		Source: src, Interval: *sf.interval, History: *history, Color: color,
		ShowAll: *all, Filter: *filter, Width: *width,
	})
	if *once {
		err = app.RunOnce(ctx)
	} else {
		err = app.Run(ctx)
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
