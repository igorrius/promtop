package view

import (
	"strings"
	"testing"

	"github.com/igorrius/promtop/internal/expo"
)

func TestNameFilter(t *testing.T) {
	sel := Selector{Globs: []string{"temp_*", "node_load?", "fan_[ab]_rpm"}, Regex: "BATTERY"}
	re, err := sel.NameFilter()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"temp_kitchen_celsius":     true,
		"room_temp_celsius":        false, // globs are anchored
		"node_load1":               true,
		"node_load15":              false,
		"fan_a_rpm":                true,
		"fan_c_rpm":                false,
		"inverter_battery_percent": true, // the regex is not anchored, any case
		"temp_hall.sensor":         true, // * spans dots too
	} {
		if got := re.MatchString(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	if re, _ := (Selector{}).NameFilter(); re != nil {
		t.Error("an empty selector selects everything")
	}
	if _, err := (Selector{Regex: "("}).NameFilter(); err == nil {
		t.Error("a broken regex should fail")
	}
}

func TestMatchers(t *testing.T) {
	in := `# TYPE fs_avail_bytes gauge
fs_avail_bytes{mount="/"} 1
fs_avail_bytes{mount="/data"} 2
fs_avail_bytes{mount="/boot"} 3
# TYPE up gauge
up 1
`
	fams, err := expo.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	count := func(sel Selector) (fs, up int) {
		t.Helper()
		out, err := sel.Apply(fams)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range out {
			if f.Name == "fs_avail_bytes" {
				fs = len(f.Samples)
			} else {
				up = len(f.Samples)
			}
		}
		return fs, up
	}
	m := func(s string) Matcher {
		t.Helper()
		x, err := ParseMatcher(s)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	cases := []struct {
		sel    Selector
		fs, up int
	}{
		{Selector{Labels: []Matcher{m("mount=/data")}}, 1, 0}, // up has no mount label
		{Selector{Labels: []Matcher{m("mount!=/")}}, 2, 1},
		{Selector{Labels: []Matcher{m(`mount=~"/(data|boot)"`)}}, 2, 0},
		{Selector{Labels: []Matcher{m("mount!~/b.*")}}, 2, 1},
		// Families the names do not select keep all their samples.
		{Selector{Globs: []string{"fs_*"}, Labels: []Matcher{m("mount=/data")}}, 1, 1},
	}
	for i, c := range cases {
		if fs, up := count(c.sel); fs != c.fs || up != c.up {
			t.Errorf("case %d: fs=%d up=%d, want %d %d", i, fs, up, c.fs, c.up)
		}
	}
	for _, bad := range []string{"mount", "=x", "mount=~("} {
		if _, err := ParseMatcher(bad); err == nil {
			t.Errorf("ParseMatcher(%q) should fail", bad)
		}
	}
}
