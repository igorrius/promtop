package cli

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/igorrius/promtop/internal/history"
)

const fixture = "../expo/testdata/otel_go_runtime.prom"

func TestRenderCommand(t *testing.T) {
	dir := t.TempDir()
	grid := filepath.Join(dir, "grid.png")
	if code := runRender([]string{"-o", grid, "-title", "Test", fixture}); code != 0 {
		t.Fatalf("render grid: exit %d", code)
	}
	f, err := os.Open(grid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := png.Decode(f); err != nil {
		t.Fatalf("grid.png: %v", err)
	}
	// One metric selected: a detail view, as SVG chosen by extension.
	if code := runRender([]string{"-m", "go_goroutine_count", "-o", filepath.Join(dir, "one.svg"), fixture}); code != 0 {
		t.Fatalf("render detail: exit %d", code)
	}
	for _, args := range [][]string{
		{"-format", "gif", fixture},
		{"-offline", fixture},
		{"-view", "pie", "-o", filepath.Join(dir, "x.png"), fixture},
		{"-l", "broken", fixture},
	} {
		if code := runRender(args); code != 2 {
			t.Errorf("render %v: exit %d, want 2", args, code)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	if code := Main([]string{"version"}, "v1.2.3"); code != 0 || version != "1.2.3" {
		t.Errorf("exit %d, version %q", code, version)
	}
}

func TestRecordCommand(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if code := runRecord([]string{fixture}); code != 2 {
		t.Errorf("record without -state: exit %d", code)
	}
	for range 2 {
		if code := runRecord([]string{"-state", state, "-m", "go_*", fixture}); code != 0 {
			t.Fatalf("record: exit %d", code)
		}
	}
	ss, err := history.Load(state)
	if err != nil || len(ss) == 0 {
		t.Fatalf("state: %d scrapes, %v", len(ss), err)
	}
	for _, f := range ss[0].Families {
		if f.Name == "target_info" {
			t.Error("record should keep only the selected metrics")
		}
	}
	out := filepath.Join(t.TempDir(), "s.txt")
	if code := runRender([]string{"-state", state, "-offline", "-o", out, fixture}); code != 0 {
		t.Fatalf("offline render: exit %d", code)
	}
}
