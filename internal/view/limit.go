package view

import (
	"strings"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
)

// Words that mark the "used" side of a usage/limit pair and the words their
// limits use. A "free" side is shown inverted: used = limit − free.
var (
	usedWords  = []string{"open", "used", "allocated", "inuse", "current", "active"}
	limitWords = []string{"max", "maximum", "limit", "capacity", "size", "total"}
	freeWords  = []string{"free", "avail", "available"}
	freeLimits = []string{"size", "total", "capacity"}
	// CamelCase words used by /proc-style names (MemFree → MemTotal).
	camelFree = []string{"Free", "Available", "Avail"}
)

type limitCand struct {
	name   string
	invert bool
}

// limitCandidates lists sibling names that may hold the maximum for name.
func limitCandidates(name string) []limitCand {
	toks := strings.Split(name, "_")
	n := len(toks)
	if n < 2 {
		return nil
	}
	suffix := []string{}
	if unitToken(toks[n-1]) {
		suffix, toks = toks[n-1:], toks[:n-1]
	}
	join := func(body []string) string { return strings.Join(append(append([]string{}, body...), suffix...), "_") }
	replace := func(i int, w string) string {
		b := append([]string{}, toks...)
		b[i] = matchCase(b[i], w)
		return join(b)
	}
	var out []limitCand
	add := func(n string, inv bool) {
		if n != name {
			out = append(out, limitCand{n, inv})
		}
	}
	// Later tokens are more specific; the namespace (first token) never pairs.
	for i := len(toks) - 1; i >= 1; i-- {
		t := strings.ToLower(toks[i])
		for _, u := range usedWords {
			if t == u {
				for _, l := range limitWords {
					add(replace(i, l), false)
				}
			}
		}
		for _, f := range freeWords {
			if t == f {
				for _, l := range freeLimits {
					add(replace(i, l), true)
				}
			}
		}
		for _, c := range camelFree {
			if strings.HasSuffix(toks[i], c) && len(toks[i]) > len(c) {
				stem := toks[i][:len(toks[i])-len(c)]
				for _, l := range []string{"Total", "Size"} {
					b := append([]string{}, toks...)
					b[i] = stem + l
					add(join(b), true)
				}
			}
		}
	}
	// Appended limits: nf_conntrack_entries → nf_conntrack_entries_limit,
	// process_virtual_memory_bytes → process_virtual_memory_max_bytes.
	for _, l := range []string{"max", "limit", "maximum", "capacity"} {
		add(join(append(append([]string{}, toks...), l)), false)
		add(name+"_"+l, false)
	}
	return out
}

// matchCase capitalises w like tok ("Free" → "Total").
func matchCase(tok, w string) string {
	if tok != "" && tok[0] >= 'A' && tok[0] <= 'Z' {
		return strings.ToUpper(w[:1]) + w[1:]
	}
	return w
}

// limitFor finds a sibling gauge family holding the maximum for every series
// of f, such as process_max_fds for process_open_fds or
// node_filesystem_size_bytes for node_filesystem_avail_bytes.
func limitFor(f *store.Family, st *store.Store) (*store.Family, bool) {
	live := f.Live()
	if len(live) == 0 {
		return nil, false
	}
	for _, c := range limitCandidates(f.Name) {
		lf := st.Get(c.name)
		if lf == nil || (lf.Type != expo.Gauge && lf.Type != expo.Untyped) {
			continue
		}
		ok := true
		for _, s := range live {
			ls := limitSeries(lf, s)
			// A limit of MaxInt64-ish means "unlimited" (e.g. GOMEMLIMIT unset);
			// a value above the limit means the names only look related.
			if ls == nil || ls.Raw <= 0 || ls.Raw >= 1e18 || s.Raw < 0 || s.Raw > ls.Raw*1.0001 {
				ok = false
				break
			}
		}
		if ok {
			return lf, c.invert
		}
	}
	return nil, false
}

// limitSeries returns the series of the limit family matching s by labels;
// a single-series limit applies to every series.
func limitSeries(lim *store.Family, s *store.Series) *store.Series {
	if ls := lim.Lookup(s.Key); ls != nil && ls.Seen {
		return ls
	}
	if live := lim.Live(); len(live) == 1 && len(live[0].Labels) == 0 {
		return live[0]
	}
	return nil
}
