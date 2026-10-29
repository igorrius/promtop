package version

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	vcs := []debug.BuildSetting{{Key: "vcs.revision", Value: "3b2a9d1f00c0ffee"}, {Key: "vcs.modified", Value: "true"}}
	for _, c := range []struct {
		name string
		bi   debug.BuildInfo
		want string
	}{
		{"go install @version", debug.BuildInfo{Main: debug.Module{Path: module, Version: "v0.3.0"}}, "0.3.0"},
		{"local build", debug.BuildInfo{Main: debug.Module{Path: module, Version: "(devel)"}, Settings: vcs}, "dev+3b2a9d1-dirty"},
		{"no vcs", debug.BuildInfo{Main: debug.Module{Path: module, Version: "(devel)"}}, "dev"},
		{"dependency", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/shop"}, Settings: vcs,
			Deps: []*debug.Module{{Path: module, Version: "v0.4.1"}},
		}, "0.4.1"},
		{"replaced dependency", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/shop"}, Settings: vcs,
			Deps: []*debug.Module{{Path: module, Version: "(devel)"}},
		}, "dev"},
		{"other program", debug.BuildInfo{Main: debug.Module{Path: "example.com/shop", Version: "v1.0.0"}}, "dev"},
	} {
		if got := fromBuildInfo(&c.bi); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := Get("v0.5.0"); got != "0.5.0" {
		t.Errorf("ldflags: %q", got)
	}
}
