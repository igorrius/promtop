package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
)

func scrape(t *testing.T, at int64, v string) Scrape {
	t.Helper()
	fams, err := expo.Parse(strings.NewReader("# TYPE c counter\nc " + v + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return Scrape{At: time.Unix(at, 0), Families: fams}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state")
	if got, err := Load(path); err != nil || got != nil {
		t.Fatalf("missing file: %v %v", got, err)
	}
	var ss []Scrape
	for i := range 5 {
		ss = append(ss, scrape(t, int64(1000+60*i), "1"+strings.Repeat("0", i)))
	}
	if err := Save(path, ss, 3); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || !got[0].At.Equal(ss[2].At) || got[2].Families[0].Samples[0].Value != 10000 {
		t.Fatalf("loaded %d scrapes, first at %v", len(got), got[0].At)
	}
	if iv := Interval(got); iv != time.Minute {
		t.Errorf("interval = %v", iv)
	}
	st := store.New(10)
	Replay(st, got)
	if r := st.Get("c").Series[0].Primary().Last(); r != 9000.0/60 {
		t.Errorf("rate after replay = %v", r)
	}

	bad := filepath.Join(t.TempDir(), "bad")
	if err := os.WriteFile(bad, []byte("not a state file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Error("loading a foreign file should fail")
	}
}

// A scrape right after the previous one replaces it.
func TestAppendCoalesces(t *testing.T) {
	var ss []Scrape
	for _, at := range []int64{0, 300, 600} {
		ss = Append(ss, scrape(t, at, "1"))
	}
	ss = Append(ss, scrape(t, 610, "2"))
	if len(ss) != 3 || ss[2].At.Unix() != 610 {
		t.Fatalf("after a close scrape: %d scrapes, last at %d", len(ss), ss[len(ss)-1].At.Unix())
	}
	ss = Append(ss, scrape(t, 900, "3"))
	if len(ss) != 4 {
		t.Errorf("a regular scrape should be appended: %d", len(ss))
	}
}
