package view

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/igorrius/promtop/internal/expo"
)

// Selector picks metrics by name — shell-style globs or a regex — and their
// series by label matchers, for one-shot renders.
type Selector struct {
	Globs  []string // temp_*, node_memory_Mem?*_bytes
	Regex  string   // case-insensitive, unanchored, like the live filter
	Labels []Matcher
}

// Matcher is a Prometheus-style label matcher: name=value, name!=value,
// name=~regex, name!~regex. Regexes are anchored; a missing label has the
// value "".
type Matcher struct {
	Name, Op, Value string
	re              *regexp.Regexp
}

// ParseMatcher parses a matcher such as mount=/data or sensor=~kitchen.*.
func ParseMatcher(s string) (Matcher, error) {
	for _, op := range []string{"!~", "=~", "!=", "="} {
		name, value, ok := strings.Cut(s, op)
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			break
		}
		m := Matcher{Name: name, Op: op, Value: strings.Trim(strings.TrimSpace(value), `"`)}
		if op == "=~" || op == "!~" {
			re, err := regexp.Compile("^(?:" + m.Value + ")$")
			if err != nil {
				return Matcher{}, fmt.Errorf("label matcher %q: %w", s, err)
			}
			m.re = re
		}
		return m, nil
	}
	return Matcher{}, fmt.Errorf("label matcher %q: want name=value, name!=value, name=~regex or name!~regex", s)
}

func (m Matcher) matches(ls []expo.Label) bool {
	v := ""
	for _, l := range ls {
		if l.Name == m.Name {
			v = l.Value
			break
		}
	}
	switch m.Op {
	case "=":
		return v == m.Value
	case "!=":
		return v != m.Value
	case "=~":
		return m.re.MatchString(v)
	}
	return !m.re.MatchString(v)
}

// Empty reports whether the selector selects everything.
func (s Selector) Empty() bool { return len(s.Globs) == 0 && s.Regex == "" && len(s.Labels) == 0 }

// NameFilter returns the regex the dashboard filters names with (nil when
// every name is selected): any glob, or the regex.
func (s Selector) NameFilter() (*regexp.Regexp, error) {
	var alts []string
	if len(s.Globs) > 0 {
		gs := make([]string, len(s.Globs))
		for i, g := range s.Globs {
			gs[i] = globRegex(g)
		}
		alts = append(alts, "^(?:"+strings.Join(gs, "|")+")$")
	}
	if s.Regex != "" {
		if _, err := regexp.Compile(s.Regex); err != nil {
			return nil, fmt.Errorf("filter %q: %w", s.Regex, err)
		}
		alts = append(alts, "(?i:"+s.Regex+")")
	}
	if len(alts) == 0 {
		return nil, nil
	}
	return regexp.Compile(strings.Join(alts, "|"))
}

// globRegex translates * and ? (and [...] classes) into a regex.
func globRegex(g string) string {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		switch c := g[i]; c {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		case '[':
			if j := strings.IndexByte(g[i:], ']'); j > 1 {
				class := g[i+1 : i+j]
				if class[0] == '!' {
					class = "^" + class[1:]
				}
				b.WriteString("[" + class + "]")
				i += j
				continue
			}
			b.WriteString(`\[`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// Apply drops the samples of selected families that fail the label
// matchers, and the families left without samples. Families the names do
// not select stay untouched: the dashboard hides them, but they can still
// serve as the limit of a usage panel.
func (s Selector) Apply(fams []*expo.Family) ([]*expo.Family, error) {
	if len(s.Labels) == 0 {
		return fams, nil
	}
	names, err := s.NameFilter()
	if err != nil {
		return nil, err
	}
	out := make([]*expo.Family, 0, len(fams))
	for _, f := range fams {
		if names != nil && !names.MatchString(f.Name) {
			out = append(out, f)
			continue
		}
		kept := *f
		kept.Samples = nil
	samples:
		for _, smp := range f.Samples {
			for _, m := range s.Labels {
				if !m.matches(smp.Labels) {
					continue samples
				}
			}
			kept.Samples = append(kept.Samples, smp)
		}
		if len(kept.Samples) > 0 {
			out = append(out, &kept)
		}
	}
	return out, nil
}
