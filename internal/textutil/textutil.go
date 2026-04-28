// Package textutil provides string helpers used across the library.
package textutil

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Truncate returns s truncated to at most maxRunes UTF-8 runes, appending
// suffix (e.g. "…") if truncation occurred. maxRunes must be > 0.
func Truncate(s, suffix string, maxRunes int) string {
	if maxRunes <= 0 {
		return suffix
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	// Shorten by suffix rune count first.
	suffixLen := utf8.RuneCountInString(suffix)
	keep := maxRunes - suffixLen
	if keep <= 0 {
		return suffix
	}
	var n int
	for i := range s {
		if n >= keep {
			return s[:i] + suffix
		}
		n++
	}
	return s + suffix
}

// Slugify converts s to a lowercase ASCII slug, replacing runs of
// non-alphanumeric characters with sep.
func Slugify(s, sep string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	prevSep := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
			prevSep = false
		} else if !prevSep {
			sb.WriteString(sep)
			prevSep = true
		}
	}
	result := sb.String()
	result = strings.TrimPrefix(result, sep)
	result = strings.TrimSuffix(result, sep)
	return result
}

// SanitizeFilename replaces characters that are invalid in filenames on
// Windows or Unix with repl. repl is typically "_".
func SanitizeFilename(name, repl string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			// Write the first rune of repl, or '_' if repl is empty.
			if len(repl) == 0 {
				return '_'
			}
			r, _ = utf8.DecodeRuneInString(repl)
			return r
		}
		if !unicode.IsPrint(r) {
			if len(repl) == 0 {
				return '_'
			}
			r, _ = utf8.DecodeRuneInString(repl)
			return r
		}
		return r
	}, name)
}

// CollapseSpaces replaces runs of whitespace with a single space and trims.
func CollapseSpaces(s string) string {
	var sb strings.Builder
	prevSpace := false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsSpace(r) {
			if !prevSpace {
				sb.WriteByte(' ')
			}
			prevSpace = true
		} else {
			sb.WriteRune(r)
			prevSpace = false
		}
	}
	return sb.String()
}

// TruncateWords returns s truncated to at most n words, appending suffix if
// truncation occurred.
func TruncateWords(s, suffix string, n int) string {
	words := strings.Fields(s)
	if len(words) <= n {
		return s
	}
	return strings.Join(words[:n], " ") + suffix
}

// ContainsFold reports whether substr is a case-insensitive substring of s.
func ContainsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
