package web

import (
	"math"
	"strconv"
	"time"

	"github.com/igorrius/promtop/internal/expo"
	"github.com/igorrius/promtop/internal/store"
	"github.com/igorrius/promtop/internal/view"
)

// Grid cards show a few rows and a short history; details carry the rest.
const (
	cardRows   = 4
	cardPoints = 120
	maxDetail  = 200 // series listed in a detail response
)

// nums encodes floats compactly, with null for NaN and ±Inf (gaps).
type nums []float64

func (n nums) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 2+len(n)*6)
	b = append(b, '[')
	for i, v := range n {
		if i > 0 {
			b = append(b, ',')
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b = append(b, "null"...)
		} else {
			b = strconv.AppendFloat(b, v, 'g', 6, 64)
		}
	}
	return append(b, ']'), nil
}

// tail keeps the history a card plots.
func tail(vals []float64) nums {
	if len(vals) > cardPoints {
		vals = vals[len(vals)-cardPoints:]
	}
	return nums(vals)
}

// Format tells the page how to format a panel's raw values, mirroring
// view.FormatValue.
type Format struct {
	Unit      string  `json:"unit"` // bytes, seconds, ratio, percent, other, none
	Sym       string  `json:"sym,omitempty"`
	Scale     float64 `json:"scale"`
	Rate      bool    `json:"rate,omitempty"`
	Quantiled bool    `json:"quantiled,omitempty"`
}

func formatOf(p *view.Panel) Format {
	u := [...]string{"none", "bytes", "seconds", "ratio", "percent", "other"}[p.Unit]
	scale := p.Scale
	if scale == 0 {
		scale = 1
	}
	return Format{Unit: u, Sym: p.Sym, Scale: scale, Rate: p.Kind == view.KindRate, Quantiled: p.Quantiled()}
}

func tone(t view.Tone) string {
	return [...]string{"", "ok", "warn", "err", "accent"}[t]
}

// Frame is one dashboard update.
type Frame struct {
	Target   string    `json:"target,omitempty"`
	Source   string    `json:"source"`
	Version  string    `json:"version,omitempty"` // promtop's
	Interval float64   `json:"interval"`          // seconds
	At       int64     `json:"at"`                // unix ms of the latest scrape
	Took     string    `json:"took,omitempty"`
	Err      string    `json:"err,omitempty"`
	Metrics  int       `json:"metrics"`
	Series   int       `json:"series"`
	Hidden   int       `json:"hidden"`
	Scrapes  int       `json:"scrapes"`
	Names    []string  `json:"names,omitempty"` // for filter completion
	Sections []Section `json:"sections"`
}

// Section is a titled group of cards.
type Section struct {
	Title  string `json:"title"`
	Hidden int    `json:"hidden,omitempty"`
	Cards  []Card `json:"cards"`
}

// Card is one panel as the grid shows it. View picks the layout:
// single (value + chart), rows (label · value · sparkline), quantiles,
// usage, state, info or time.
type Card struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	View   string `json:"view"`
	Badge  string `json:"badge"`
	Head   string `json:"head,omitempty"`
	Tone   string `json:"tone,omitempty"`
	Format Format `json:"fmt"`

	Value  string   `json:"value,omitempty"`
	Sub    string   `json:"sub,omitempty"`
	Frac   *float64 `json:"frac,omitempty"`
	Spark  nums     `json:"spark,omitempty"`
	Rows   []Row    `json:"rows,omitempty"`
	More   int      `json:"more,omitempty"`
	Footer string   `json:"footer,omitempty"`
}

// Row is one line of a card.
type Row struct {
	Label string   `json:"label"`
	Value string   `json:"value,omitempty"`
	Sub   string   `json:"sub,omitempty"`
	Tone  string   `json:"tone,omitempty"`
	Frac  *float64 `json:"frac,omitempty"`
	Spark nums     `json:"spark,omitempty"`
	Up    *bool    `json:"up,omitempty"`
}

// frac returns f for JSON, or nil when it is not a number.
func frac(f float64) *float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}

func label(f *store.Family, s *store.Series) string {
	if l := view.RowLabel(f, s); l != "" {
		return l
	}
	return "{}"
}

// buildFrame renders the store as a frame.
func buildFrame(st *store.Store, secs []*view.Section, now time.Time) Frame {
	fr := Frame{Target: view.Target(st), Scrapes: st.Scrapes}
	for _, f := range st.Families {
		if l := len(f.Live()); l > 0 {
			fr.Metrics++
			fr.Series += l
			fr.Names = append(fr.Names, f.Name)
		}
	}
	for _, s := range secs {
		sec := Section{Title: s.Title, Hidden: s.Hidden, Cards: make([]Card, 0, len(s.Panels))}
		fr.Hidden += s.Hidden
		for _, p := range s.Panels {
			sec.Cards = append(sec.Cards, card(p, now))
		}
		fr.Sections = append(fr.Sections, sec)
	}
	return fr
}

func card(p *view.Panel, now time.Time) Card {
	head, t := p.Headline(now)
	c := Card{
		ID: p.Name(), Title: p.Title, Kind: p.Kind.String(), Badge: p.Badge(),
		Head: head, Tone: tone(t), Format: formatOf(p),
	}
	rows := p.Rows()
	if len(rows) == 0 {
		c.View, c.Sub = "single", "no data"
		return c
	}
	limit := func() []*store.Series {
		if len(rows) > cardRows {
			c.More = len(rows) - cardRows
			return rows[:cardRows]
		}
		return rows
	}
	f := p.Family
	switch {
	case p.Kind == view.KindInfo:
		c.View = "info"
		if len(rows) == 1 {
			for _, l := range view.VisibleLabels(f, rows[0]) {
				c.Rows = append(c.Rows, Row{Label: l.Name, Value: l.Value})
			}
			if len(c.Rows) == 0 {
				c.Rows = append(c.Rows, Row{Label: "value", Value: p.Format(rows[0].Raw)})
			}
			return c
		}
		for _, s := range limit() {
			c.Rows = append(c.Rows, Row{Label: view.LabelText(view.VisibleLabels(f, s), true)})
		}
	case p.Kind == view.KindTimestamp:
		c.View = "time"
		for _, s := range limit() {
			c.Rows = append(c.Rows, Row{
				Label: label(f, s), Value: p.Age(s, now),
				Sub: p.Time(s).Local().Format("2006-01-02 15:04:05 MST"),
			})
		}
	case p.Kind == view.KindState:
		c.View = "state"
		set := f.Type == expo.StateSet
		for _, s := range rows {
			r := Row{Label: label(f, s), Up: new(view.StateUp(s))}
			if set {
				r.Tone = "accent"
			}
			c.Rows = append(c.Rows, r)
		}
		if len(rows) == 1 && !set {
			c.Spark = tail(rows[0].Primary().Values())
		}
	case p.Kind == view.KindUsage:
		c.View = "usage"
		if len(rows) == 1 {
			s := rows[0]
			used, lim := p.UsageValues(s)
			c.Frac = frac(used / lim)
			if p.LimitInvert {
				c.Value = "free " + p.Format(s.Raw) + " of " + p.Format(lim)
			} else {
				c.Value = p.Format(used) + " / " + p.Format(lim)
			}
			c.Spark = tail(s.Primary().Values())
			return c
		}
		for _, s := range limit() {
			u := p.Usage(s)
			c.Rows = append(c.Rows, Row{Label: label(f, s), Value: view.Percent(u * 100), Frac: frac(u), Tone: tone(view.UsageTone(u))})
		}
	case len(rows) > 1:
		c.View = "rows"
		line := p.RowLine()
		for _, s := range limit() {
			r := view.Ring(s, line)
			c.Rows = append(c.Rows, Row{Label: label(f, s), Value: p.FormatLine(line, r.Last()), Spark: tail(r.Values())})
		}
	case p.Quantiled():
		c.View = "quantiles"
		s := rows[0]
		for _, n := range view.QuantileLines(s, 3) {
			r := s.Line(n)
			c.Rows = append(c.Rows, Row{Label: n, Value: p.FormatLine(n, r.Last()), Spark: tail(r.Values())})
		}
		c.Footer = "rate " + p.FormatLine("rate", s.Line("rate").Last())
		if avg := s.Line("avg"); avg != nil {
			c.Footer += " · avg " + p.Format(avg.Last())
		}
		c.Footer += " · n " + view.SI(s.Raw)
	default:
		c.View = "single"
		s := rows[0]
		vals := s.Primary().Values()
		c.Value = p.Format(s.Primary().Last())
		c.Spark = tail(vals)
		if p.Kind == view.KindRate {
			c.Sub = "total " + p.FormatTotal(s.Raw)
		} else if lo, hi, ok := bounds(vals); ok && lo != hi {
			c.Sub = "min " + p.Format(lo) + " · max " + p.Format(hi)
		}
	}
	return c
}

func bounds(vals []float64) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		lo, hi, ok = math.Min(lo, v), math.Max(hi, v), true
	}
	return lo, hi, ok
}

// Detail is the full data of one panel.
type Detail struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Describe string         `json:"describe"`
	Help     string         `json:"help,omitempty"`
	Kind     string         `json:"kind"`
	Format   Format         `json:"fmt"`
	Interval float64        `json:"interval"`
	At       int64          `json:"at"`
	Total    int            `json:"total"`
	Columns  []string       `json:"columns"`
	Series   []DetailSeries `json:"series"`
}

// DetailSeries is one series with every derived line.
type DetailSeries struct {
	Label   string          `json:"label"`
	Labels  string          `json:"labels"`
	Lines   []string        `json:"lines"`
	Values  map[string]nums `json:"values"`
	Cells   []string        `json:"cells"` // formatted, one per column
	Tone    string          `json:"tone,omitempty"`
	Frac    *float64        `json:"frac,omitempty"`
	Up      *bool           `json:"up,omitempty"`
	Buckets []Bucket        `json:"buckets,omitempty"`
}

// Bucket is a histogram bucket's observations in the last interval.
type Bucket struct {
	LE    string  `json:"le"`
	Count float64 `json:"count"`
}

func detail(p *view.Panel, interval time.Duration, at time.Time, now time.Time) Detail {
	rows := p.Rows()
	d := Detail{
		ID: p.Name(), Name: p.Family.Name, Describe: p.Describe(), Help: p.Family.Help,
		Kind: p.Kind.String(), Format: formatOf(p), Interval: interval.Seconds(),
		At: at.UnixMilli(), Total: len(rows),
	}
	if len(rows) > maxDetail {
		rows = rows[:maxDetail]
	}
	switch p.Kind {
	case view.KindGauge, view.KindRate:
		d.Columns = []string{"now", "min", "max"}
	case view.KindUsage:
		d.Columns = []string{"used", "limit", "%"}
	case view.KindState:
		d.Columns = []string{"state"}
	case view.KindTimestamp:
		d.Columns = []string{"age", "time"}
	case view.KindInfo:
		d.Columns = []string{"value"}
	default:
		if len(rows) > 0 {
			for _, l := range rows[0].Lines {
				d.Columns = append(d.Columns, l.Name)
			}
		}
	}
	for _, s := range rows {
		ds := DetailSeries{
			Label: label(p.Family, s), Labels: view.LabelText(view.VisibleLabels(p.Family, s), true),
			Values: map[string]nums{},
		}
		for _, l := range s.Lines {
			ds.Lines = append(ds.Lines, l.Name)
			ds.Values[l.Name] = nums(l.Ring.Values())
		}
		switch p.Kind {
		case view.KindGauge, view.KindRate:
			lo, hi, ok := bounds(s.Primary().Values())
			ds.Cells = []string{p.Format(s.Primary().Last()), "–", "–"}
			if ok {
				ds.Cells[1], ds.Cells[2] = p.Format(lo), p.Format(hi)
			}
		case view.KindUsage:
			used, lim := p.UsageValues(s)
			u := p.Usage(s)
			ds.Cells = []string{p.Format(used), p.Format(lim), view.Percent(u * 100)}
			ds.Frac, ds.Tone = frac(u), tone(view.UsageTone(u))
		case view.KindState:
			up := view.StateUp(s)
			ds.Up = &up
			switch {
			case p.Family.Type == expo.StateSet && up:
				ds.Cells = []string{"on"}
			case p.Family.Type == expo.StateSet:
				ds.Cells = []string{"off"}
			case up:
				ds.Cells = []string{"up"}
			default:
				ds.Cells = []string{"down"}
			}
		case view.KindTimestamp:
			ds.Cells = []string{p.Age(s, now), p.Time(s).Local().Format("2006-01-02 15:04:05")}
		case view.KindInfo:
			ds.Cells = []string{view.SI(s.Raw)}
		default:
			for _, c := range d.Columns {
				ds.Cells = append(ds.Cells, p.FormatLine(c, s.Line(c).Last()))
			}
		}
		if p.Kind == view.KindHistogram {
			for _, b := range s.Buckets {
				le := "≤ " + p.Format(b.LE)
				if math.IsInf(b.LE, 1) {
					le = "+Inf"
				}
				ds.Buckets = append(ds.Buckets, Bucket{LE: le, Count: b.Count})
			}
		}
		d.Series = append(d.Series, ds)
	}
	return d
}

// Document is a frame plus the full details of chosen panels: what
// promtop render -format json writes for bots and pipelines.
type Document struct {
	Frame
	Details []Detail `json:"details,omitempty"`
}

// Snapshot builds a document from a store: the cards of secs and, for
// each panel in details, all of its series.
func Snapshot(st *store.Store, secs []*view.Section, details []*view.Panel, interval time.Duration, at, now time.Time) Document {
	doc := Document{Frame: buildFrame(st, secs, now)}
	doc.Interval = interval.Seconds()
	doc.At = at.UnixMilli()
	doc.Names = nil // the page's filter completion; noise in a document
	for _, p := range details {
		doc.Details = append(doc.Details, detail(p, interval, at, now))
	}
	return doc
}
