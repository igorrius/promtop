// Package expo parses the Prometheus text exposition format (and the subset of
// OpenMetrics that Go client libraries emit) into metric families.
package expo

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Type is a metric family type as declared by a "# TYPE" line.
type Type string

// Metric types of the text format and OpenMetrics.
const (
	Counter        Type = "counter"
	Gauge          Type = "gauge"
	Histogram      Type = "histogram"
	GaugeHistogram Type = "gaugehistogram"
	Summary        Type = "summary"
	Info           Type = "info"
	StateSet       Type = "stateset"
	Untyped        Type = "untyped"
)

// Label is a single name/value pair.
type Label struct {
	Name  string
	Value string
}

// Sample is one line of the exposition: a full sample name (which may carry a
// _bucket/_sum/_count/_total suffix), its labels and value.
type Sample struct {
	Name   string
	Labels []Label // sorted by name
	Value  float64
}

// Label returns the value of the named label, or "".
func (s Sample) Label(name string) string {
	for _, l := range s.Labels {
		if l.Name == name {
			return l.Value
		}
	}
	return ""
}

// Family groups the samples that belong to one metric.
type Family struct {
	Name    string
	Type    Type
	Help    string
	Unit    string
	Samples []Sample
}

var suffixes = []string{"_bucket", "_sum", "_count", "_total", "_created", "_gcount", "_gsum", "_info"}

// Parse reads an exposition and returns its families in order of first
// appearance.
func Parse(r io.Reader) ([]*Family, error) {
	var (
		fams  []*Family
		byKey = map[string]*Family{}
	)
	get := func(name string) *Family {
		if f, ok := byKey[name]; ok {
			return f
		}
		f := &Family{Name: name, Type: Untyped}
		byKey[name] = f
		fams = append(fams, f)
		return f
	}
	// resolve finds the family a sample belongs to, honouring TYPE
	// declarations for the base name of suffixed samples.
	resolve := func(sample string) *Family {
		if f, ok := byKey[sample]; ok && (f.Type != Untyped || f.Help != "" || len(f.Samples) > 0) {
			return f
		}
		for _, suf := range suffixes {
			base, ok := strings.CutSuffix(sample, suf)
			if !ok {
				continue
			}
			if f, ok := byKey[base]; ok && f.Type != Untyped {
				return f
			}
		}
		return get(sample)
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			parseComment(line, get)
			continue
		}
		s, err := parseSample(line)
		if err != nil {
			return fams, fmt.Errorf("line %d: %w", lineNo, err)
		}
		f := resolve(s.Name)
		f.Samples = append(f.Samples, s)
	}
	if err := sc.Err(); err != nil {
		return fams, err
	}
	// Drop families that only had metadata.
	out := fams[:0]
	for _, f := range fams {
		if len(f.Samples) > 0 {
			out = append(out, f)
		}
	}
	return out, nil
}

func parseComment(line string, get func(string) *Family) {
	fields := strings.SplitN(strings.TrimSpace(line[1:]), " ", 3)
	if len(fields) < 3 {
		return
	}
	name := unquoteName(fields[1])
	switch fields[0] {
	case "HELP":
		get(name).Help = unescapeHelp(fields[2])
	case "TYPE":
		t := Type(strings.ToLower(strings.TrimSpace(fields[2])))
		switch t {
		case Counter, Gauge, Histogram, GaugeHistogram, Summary, Info, StateSet, Untyped:
		case "unknown":
			t = Untyped
		default:
			t = Untyped
		}
		get(name).Type = t
	case "UNIT":
		get(name).Unit = strings.TrimSpace(fields[2])
	}
}

func unquoteName(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

func unescapeHelp(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func parseSample(line string) (Sample, error) {
	var s Sample
	i := 0
	// Metric name (absent when it is given as a quoted string inside braces).
	for i < len(line) && line[i] != '{' && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	s.Name = line[:i]
	if i < len(line) && line[i] == '{' {
		end, err := parseLabels(line, i+1, &s)
		if err != nil {
			return s, err
		}
		i = end
	}
	if s.Name == "" {
		return s, errors.New("missing metric name")
	}
	rest := strings.Fields(line[i:])
	if len(rest) == 0 {
		return s, fmt.Errorf("missing value for %s", s.Name)
	}
	v, err := parseFloat(rest[0])
	if err != nil {
		return s, fmt.Errorf("bad value %q for %s", rest[0], s.Name)
	}
	s.Value = v
	sort.Slice(s.Labels, func(a, b int) bool { return s.Labels[a].Name < s.Labels[b].Name })
	return s, nil
}

// parseLabels parses `name="value",...}` starting at pos and returns the index
// just past the closing brace.
func parseLabels(line string, pos int, s *Sample) (int, error) {
	i := pos
	for {
		for i < len(line) && (line[i] == ' ' || line[i] == ',') {
			i++
		}
		if i >= len(line) {
			return i, errors.New("unterminated label set")
		}
		if line[i] == '}' {
			return i + 1, nil
		}
		// Label name, possibly quoted (UTF-8 names), or a bare quoted metric name.
		var name string
		if line[i] == '"' {
			str, n, err := readQuoted(line, i)
			if err != nil {
				return i, err
			}
			i = n
			for i < len(line) && line[i] == ' ' {
				i++
			}
			if i < len(line) && line[i] != '=' {
				if s.Name == "" {
					s.Name = str
				}
				continue
			}
			name = str
		} else {
			start := i
			for i < len(line) && line[i] != '=' && line[i] != ' ' {
				i++
			}
			name = line[start:i]
		}
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) || line[i] != '=' {
			return i, fmt.Errorf("expected '=' after label %q", name)
		}
		i++
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= len(line) || line[i] != '"' {
			return i, fmt.Errorf("expected quoted value for label %q", name)
		}
		val, n, err := readQuoted(line, i)
		if err != nil {
			return i, err
		}
		i = n
		s.Labels = append(s.Labels, Label{Name: name, Value: val})
	}
}

// readQuoted reads a double-quoted string with \\, \" and \n escapes starting
// at line[pos] == '"' and returns it with the index after the closing quote.
func readQuoted(line string, pos int) (string, int, error) {
	var b strings.Builder
	for i := pos + 1; i < len(line); i++ {
		c := line[i]
		switch c {
		case '\\':
			if i+1 >= len(line) {
				return "", i, errors.New("dangling escape")
			}
			i++
			switch line[i] {
			case 'n':
				b.WriteByte('\n')
			default:
				b.WriteByte(line[i])
			}
		case '"':
			return b.String(), i + 1, nil
		default:
			b.WriteByte(c)
		}
	}
	return "", len(line), errors.New("unterminated string")
}

func parseFloat(s string) (float64, error) {
	switch s {
	case "+Inf", "Inf", "+inf", "inf":
		s = "+Inf"
	case "-Inf", "-inf":
		s = "-Inf"
	case "NaN", "nan":
		s = "NaN"
	}
	return strconv.ParseFloat(s, 64)
}

// LabelKey renders labels (excluding the given names) as a stable key such as
// `code="200",method="GET"`.
func LabelKey(ls []Label, exclude ...string) string {
	var b strings.Builder
	first := true
outer:
	for _, l := range ls {
		for _, e := range exclude {
			if l.Name == e {
				continue outer
			}
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(l.Name)
		b.WriteString(`="`)
		b.WriteString(l.Value)
		b.WriteByte('"')
	}
	return b.String()
}

// Without returns a copy of ls without the named labels.
func Without(ls []Label, exclude ...string) []Label {
	out := make([]Label, 0, len(ls))
outer:
	for _, l := range ls {
		for _, e := range exclude {
			if l.Name == e {
				continue outer
			}
		}
		out = append(out, l)
	}
	return out
}
