package cli

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/igorrius/promtop/internal/scrape"
	"github.com/igorrius/promtop/internal/view"
)

// listFlag collects a repeatable flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ", ") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// sourceFlags are the flags every command shares: where to scrape and how.
type sourceFlags struct {
	interval *time.Duration
	timeout  *time.Duration
	headers  listFlag
}

func addSourceFlags(fs *flag.FlagSet, interval time.Duration) *sourceFlags {
	sf := &sourceFlags{
		interval: fs.Duration("i", interval, "scrape interval"),
		timeout:  fs.Duration("timeout", 0, "scrape timeout (default: interval, at most 10s)"),
	}
	fs.Var(&sf.headers, "H", `extra request header, repeatable ("Authorization: Bearer …")`)
	return sf
}

// source builds the scrape source for target.
func (sf *sourceFlags) source(target string) (*scrape.Source, error) {
	rawURL, file, err := scrape.NormalizeTarget(target)
	if err != nil {
		return nil, err
	}
	if *sf.interval < 100*time.Millisecond {
		*sf.interval = 100 * time.Millisecond
	}
	to := *sf.timeout
	if to <= 0 {
		to = max(min(*sf.interval, 10*time.Second), time.Second)
	}
	hdr := http.Header{}
	for _, h := range sf.headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("bad header %q, want \"Name: value\"", h)
		}
		hdr.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	return &scrape.Source{URL: rawURL, File: file, Header: hdr, Timeout: to, Version: version}, nil
}

// selectFlags pick metrics and series.
type selectFlags struct {
	globs  listFlag
	regex  *string
	labels listFlag
}

func addSelectFlags(fs *flag.FlagSet) *selectFlags {
	sel := &selectFlags{regex: fs.String("f", "", "only metrics matching this regex (case-insensitive)")}
	fs.Var(&sel.globs, "m", "only metrics matching this glob, repeatable (temp_*, node_memory_*)")
	fs.Var(&sel.labels, "l", "only series matching this label matcher, repeatable (mount=/data, sensor=~kitchen.*, dev!=lo)")
	return sel
}

func (sel *selectFlags) selector() (view.Selector, error) {
	s := view.Selector{Globs: sel.globs, Regex: *sel.regex}
	for _, l := range sel.labels {
		m, err := view.ParseMatcher(l)
		if err != nil {
			return s, err
		}
		s.Labels = append(s.Labels, m)
	}
	return s, nil
}

// parseArgs parses flags that may come before and after the single
// positional target. It returns the target ("" when absent) and an exit
// code ≥ 0 when the command should stop.
func parseArgs(fs *flag.FlagSet, args []string) (string, int) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return "", 0
			}
			return "", 2
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
	if len(positional) > 1 {
		fmt.Fprintln(os.Stderr, "promtop: expected a single target")
		return "", 2
	}
	if len(positional) == 1 {
		return positional[0], -1
	}
	return "", -1
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "promtop:", err)
	return 1
}

func usageErr(err error) int {
	fmt.Fprintln(os.Stderr, "promtop:", err)
	return 2
}
