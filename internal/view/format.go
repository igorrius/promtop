package view

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Unit is the physical unit inferred from a metric name.
type Unit int

// Units promtop formats specially.
const (
	UnitNone Unit = iota
	UnitBytes
	UnitSeconds
	UnitRatio   // 0..1
	UnitPercent // 0..100
	UnitOther   // a named unit kept as a suffix (celsius, volts, ...)
)

// unitDef describes a unit suffix; scale converts a raw value into the base
// unit (milliseconds → seconds).
type unitDef struct {
	unit  Unit
	sym   string
	scale float64
}

// unitTokens maps the last token of a metric name (or an OpenMetrics UNIT)
// to its unit. Prometheus recommends base units, but exporters in the wild
// also use milliseconds, kilobytes and friends.
var unitTokens = map[string]unitDef{
	"bytes":        {UnitBytes, "", 1},
	"kilobytes":    {UnitBytes, "", 1e3},
	"megabytes":    {UnitBytes, "", 1e6},
	"gigabytes":    {UnitBytes, "", 1e9},
	"kibibytes":    {UnitBytes, "", 1 << 10},
	"mebibytes":    {UnitBytes, "", 1 << 20},
	"gibibytes":    {UnitBytes, "", 1 << 30},
	"seconds":      {UnitSeconds, "", 1},
	"milliseconds": {UnitSeconds, "", 1e-3},
	"ms":           {UnitSeconds, "", 1e-3},
	"microseconds": {UnitSeconds, "", 1e-6},
	"nanoseconds":  {UnitSeconds, "", 1e-9},
	"minutes":      {UnitSeconds, "", 60},
	"hours":        {UnitSeconds, "", 3600},
	"days":         {UnitSeconds, "", 86400},
	"ratio":        {UnitRatio, "", 1},
	"percent":      {UnitPercent, "", 1},
	"percentage":   {UnitPercent, "", 1},
	"celsius":      {UnitOther, "°C", 1},
	"kelvin":       {UnitOther, "K", 1},
	"volts":        {UnitOther, "V", 1},
	"amperes":      {UnitOther, "A", 1},
	"watts":        {UnitOther, "W", 1},
	"joules":       {UnitOther, "J", 1},
	"meters":       {UnitOther, "m", 1},
	"hertz":        {UnitOther, "Hz", 1},
	"grams":        {UnitOther, "g", 1},
	"bits":         {UnitOther, "b", 1},
	"pascals":      {UnitOther, "Pa", 1},
	"rpm":          {UnitOther, "rpm", 1},
}

// metaUnits maps OpenMetrics / UCUM unit annotations that differ from the
// name tokens above.
var metaUnits = map[string]string{"s": "seconds", "by": "bytes", "%": "percent", "1": "ratio", "cel": "celsius"}

var nameSuffixes = []string{"_total", "_count", "_sum", "_bucket", "_created"}

// UnitOf infers a unit from the metric name (Prometheus naming conventions),
// falling back to a declared OpenMetrics unit. It returns the unit, the symbol
// for UnitOther and the factor converting raw values into the unit.
func UnitOf(name, meta string) (Unit, string, float64) {
	n := name
	for _, suf := range nameSuffixes {
		n = strings.TrimSuffix(n, suf)
	}
	if d, ok := unitTokens[strings.ToLower(lastToken(n))]; ok {
		return d.unit, d.sym, d.scale
	}
	m := strings.ToLower(meta)
	if alias, ok := metaUnits[m]; ok {
		m = alias
	}
	if d, ok := unitTokens[m]; ok {
		return d.unit, d.sym, d.scale
	}
	return UnitNone, "", 1
}

// unitToken reports whether tok is a unit suffix.
func unitToken(tok string) bool {
	_, ok := unitTokens[strings.ToLower(tok)]
	return ok
}

// FormatValue renders v in the given unit. When rate is true the value is a
// per-second rate of a counter.
func FormatValue(v float64, u Unit, sym string, rate bool) string {
	if math.IsNaN(v) {
		return "–"
	}
	if math.IsInf(v, 0) {
		if v > 0 {
			return "+Inf"
		}
		return "-Inf"
	}
	switch u {
	case UnitBytes:
		s := IEC(v) + "B"
		if rate {
			s += "/s"
		}
		return s
	case UnitSeconds:
		if rate {
			// seconds per second == utilisation (e.g. CPU cores busy).
			return Percent(v * 100)
		}
		return Duration(v)
	case UnitRatio:
		return Percent(v * 100)
	case UnitPercent:
		return Percent(v)
	case UnitOther:
		s := SI(v) + sym
		if rate {
			s += "/s"
		}
		return s
	}
	s := SI(v)
	if rate {
		s += "/s"
	}
	return s
}

// Percent formats p (already multiplied by 100).
func Percent(p float64) string {
	switch a := math.Abs(p); {
	case a == 0:
		return "0%"
	case a < 10:
		return strconv.FormatFloat(p, 'f', 2, 64) + "%"
	case a < 100:
		return strconv.FormatFloat(p, 'f', 1, 64) + "%"
	}
	return strconv.FormatFloat(p, 'f', 0, 64) + "%"
}

// SI formats with k/M/G/T suffixes, three significant digits.
func SI(v float64) string {
	a := math.Abs(v)
	switch {
	case a == 0:
		return "0"
	case a >= 1e12:
		return sig(v/1e12) + "T"
	case a >= 1e9:
		return sig(v/1e9) + "G"
	case a >= 1e6:
		return sig(v/1e6) + "M"
	case a >= 1e4:
		return sig(v/1e3) + "k"
	case a >= 1 && v == math.Trunc(v):
		return strconv.FormatFloat(v, 'f', 0, 64)
	case a >= 0.001:
		return sig(v)
	}
	return strconv.FormatFloat(v, 'g', 3, 64)
}

// IEC formats a byte count with Ki/Mi/Gi suffixes (without the trailing B).
func IEC(v float64) string {
	a := math.Abs(v)
	units := []string{"", "Ki", "Mi", "Gi", "Ti", "Pi"}
	i := 0
	for a >= 1024 && i < len(units)-1 {
		a /= 1024
		v /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return sig(v) + units[i]
}

// sig renders three significant digits without trailing zeros noise.
func sig(v float64) string {
	a := math.Abs(v)
	var s string
	switch {
	case a >= 100:
		s = strconv.FormatFloat(v, 'f', 0, 64)
	case a >= 10:
		s = strconv.FormatFloat(v, 'f', 1, 64)
	default:
		s = strconv.FormatFloat(v, 'f', 2, 64)
	}
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// Duration formats seconds compactly: 820ns, 12.3µs, 4.5ms, 1.23s, 3m12s, 2h05m, 3d4h.
func Duration(sec float64) string {
	a := math.Abs(sec)
	neg := ""
	if sec < 0 {
		neg = "-"
	}
	switch {
	case a == 0:
		return "0s"
	case a < 1e-6:
		return neg + sig(a*1e9) + "ns"
	case a < 1e-3:
		return neg + sig(a*1e6) + "µs"
	case a < 1:
		return neg + sig(a*1e3) + "ms"
	case a < 60:
		return neg + sig(a) + "s"
	}
	return neg + Age(time.Duration(a*float64(time.Second)))
}

// Age formats a long duration: 3m12s, 2h05m, 3d04h.
func Age(d time.Duration) string {
	s := int64(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	case s < 86400:
		return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
	}
	return fmt.Sprintf("%dd%02dh", s/86400, (s%86400)/3600)
}
