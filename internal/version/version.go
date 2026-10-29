// Package version names the running promtop build.
package version

import (
	"runtime/debug"
	"strings"
)

const module = "github.com/igorrius/promtop"

// Get returns the version to show: the one a release build sets with
// -ldflags, else the module version ("go install …@v0.3.0", or promtop
// built into another program as a dependency), else the commit of a
// local build ("dev+3b2a9d1", "-dirty" with uncommitted changes), else
// "dev". A leading "v" is dropped, as in release builds.
func Get(ldflags string) string {
	if ldflags != "" && ldflags != "dev" {
		return strings.TrimPrefix(ldflags, "v")
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return fromBuildInfo(bi)
}

func fromBuildInfo(bi *debug.BuildInfo) string {
	mod := bi.Main
	for _, d := range bi.Deps {
		if d.Path == module {
			mod = *d
		}
	}
	if mod.Path != module {
		return "dev"
	}
	if v := mod.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	if mod != bi.Main {
		return "dev" // a replaced dependency: the VCS stamp is the host program's
	}
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	v := "dev+" + rev[:min(7, len(rev))]
	if dirty {
		v += "-dirty"
	}
	return v
}
