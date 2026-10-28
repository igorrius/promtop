// Package history keeps raw scrapes in a file between runs: a cron job
// records a sample now and then, and a later render replays them into a
// store, so rates, quantiles and charts come out exactly as in a live
// session.
package history

import (
	"bufio"
	"compress/gzip"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
)

const magic = "promtop-history 1\n"

// Scrape is one recorded scrape.
type Scrape struct {
	At       time.Time
	Families []*expo.Family
}

// Load reads the scrapes in path, oldest first. A missing file is an empty
// history.
func Load(path string) ([]Scrape, error) {
	f, err := os.Open(path) //nolint:gosec // the user names the state file
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	head := make([]byte, len(magic))
	if _, err := io.ReadFull(r, head); err != nil || string(head) != magic {
		return nil, fmt.Errorf("%s is not a promtop state file", path)
	}
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []Scrape
	if err := gob.NewDecoder(zr).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// Save writes the newest keep scrapes to path, atomically: a reader never
// sees a half-written file.
func Save(path string, scrapes []Scrape, keep int) error {
	if keep > 0 && len(scrapes) > keep {
		scrapes = scrapes[len(scrapes)-keep:]
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".promtop-state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	w := bufio.NewWriter(tmp)
	if _, err := w.WriteString(magic); err != nil {
		return err
	}
	zw := gzip.NewWriter(w)
	if err := gob.NewEncoder(zw).Encode(scrapes); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Replay ingests scrapes into st in order.
func Replay(st *store.Store, scrapes []Scrape) {
	for _, s := range scrapes {
		st.Ingest(s.Families, s.At)
	}
}

// Interval is the typical gap between scrapes (the median), or 0.
func Interval(scrapes []Scrape) time.Duration {
	if len(scrapes) < 2 {
		return 0
	}
	gaps := make([]time.Duration, 0, len(scrapes)-1)
	for i := 1; i < len(scrapes); i++ {
		gaps = append(gaps, scrapes[i].At.Sub(scrapes[i-1].At))
	}
	slices.Sort(gaps)
	return gaps[len(gaps)/2]
}

// Append adds s to scrapes. A scrape that comes less than half the usual
// interval after the last one replaces it instead: a render right after a
// cron run would otherwise add a sample a few seconds after the previous
// one, and rates over a sliver of time are noise.
func Append(scrapes []Scrape, s Scrape) []Scrape {
	if iv := Interval(scrapes); iv > 0 && s.At.Sub(scrapes[len(scrapes)-1].At) < iv/2 {
		return append(scrapes[:len(scrapes)-1], s)
	}
	return append(scrapes, s)
}
