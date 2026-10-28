package view

import "strings"

// isSep reports whether c separates tokens of a metric name: underscores in
// classic names, dots in OpenTelemetry-style UTF-8 names and colons in
// recording rules.
func isSep(c byte) bool { return c == '_' || c == '.' || c == ':' }

// tokens splits a metric name at separators, dropping empty tokens.
func tokens(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool { return r < 0x80 && isSep(byte(r)) })
}

// lastToken returns the token after the last separator.
func lastToken(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if isSep(name[i]) {
			return name[i+1:]
		}
	}
	return name
}

// prefixLen returns the byte length of the first n tokens of name (including
// the separators between them), or -1 when name has n tokens or fewer, so a
// prefix never swallows the whole name.
func prefixLen(name string, n int) int {
	seen := 0
	for i := range len(name) {
		if !isSep(name[i]) || i == 0 || isSep(name[i-1]) {
			continue
		}
		seen++
		if seen == n {
			if i+1 >= len(name) {
				return -1
			}
			return i
		}
	}
	return -1
}

// trimPrefixTokens drops the first n tokens and the separator after them.
func trimPrefixTokens(name string, n int) string {
	if n <= 0 {
		return name
	}
	if i := prefixLen(name, n); i > 0 {
		return name[i+1:]
	}
	return name
}

// stripUnit removes a trailing unit token ("MemFree_bytes" → "MemFree").
func stripUnit(s string) string {
	for _, suf := range nameSuffixes {
		s = strings.TrimSuffix(s, suf)
	}
	if t := lastToken(s); t != s && unitToken(t) {
		return s[:len(s)-len(t)-1]
	}
	return s
}
