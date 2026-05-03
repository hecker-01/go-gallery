package gallery

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"
)

// Formatter compiles a pattern string into a reusable path formatter.
//
// Pattern syntax:
//
//	Literal text is copied verbatim.
//
//	{key}                  - value from the keywords map
//	{key:layout}           - datetime value using Go's time.Format layout
//	{key!l}                - value forced to lower-case
//	{key!u}                - value forced to upper-case
//	{key!j}                - value JSON-encoded
//	{key/old/new}          - value with old replaced by new (first occurrence)
//	{key//old/new}         - value with all occurrences replaced
//	{key|sep}              - slice value joined with sep
//	{key?trueval:falseval} - if value is truthy use trueval else falseval
//
// Specifiers may be combined left-to-right, e.g. {author.screen_name!l/bad/good}.
// Unknown keys produce an empty string rather than an error so patterns are
// robust to missing optional fields.
type Formatter struct {
	tokens []fmtToken
}

// NewFormatter compiles pattern into a Formatter. Returns an error if the
// pattern contains unclosed braces.
func NewFormatter(pattern string) (*Formatter, error) {
	tokens, err := parsePattern(pattern)
	if err != nil {
		return nil, err
	}
	return &Formatter{tokens: tokens}, nil
}

// Format evaluates the pattern against kw and returns the resulting string.
// Path separators in individual variable values are replaced with underscores
// so that a single variable cannot inject extra directory levels.
func (f *Formatter) Format(kw map[string]any) string {
	var sb strings.Builder
	for _, tok := range f.tokens {
		switch tok.kind {
		case kindLiteral:
			sb.WriteString(tok.literal)
		case kindVar:
			sb.WriteString(evalVarToken(tok, kw))
		}
	}
	return path.Clean(sb.String())
}

// tokenKind distinguishes literal text from variable references.
type tokenKind int

const (
	kindLiteral tokenKind = iota
	kindVar
)

// fmtToken is one parsed segment of the pattern.
type fmtToken struct {
	kind    tokenKind
	literal string  // kindLiteral
	spec    varSpec // kindVar
}

// varSpec describes a parsed {...} block.
type varSpec struct {
	key string

	// conversion: "l" lowercase, "u" uppercase, "j" JSON
	conversion string

	// dateLayout: non-empty means the value is a time.Time formatted with this layout
	dateLayout string

	// replaceAll: when true all occurrences are replaced
	replaceAll bool
	replaceOld string
	replaceNew string

	// joinSep: non-empty means join a slice value with this separator
	joinSep string

	// conditional: evaluate key as bool and pick trueVal/falseVal
	conditional bool
	trueVal     string
	falseVal    string
}

// parsePattern tokenises pattern into a slice of fmtToken.
func parsePattern(pattern string) ([]fmtToken, error) {
	var tokens []fmtToken
	i := 0
	for i < len(pattern) {
		start := strings.IndexByte(pattern[i:], '{')
		if start == -1 {
			// No more variables - append the rest as a literal.
			tokens = append(tokens, fmtToken{kind: kindLiteral, literal: pattern[i:]})
			break
		}
		start += i
		if start > i {
			tokens = append(tokens, fmtToken{kind: kindLiteral, literal: pattern[i:start]})
		}
		end := strings.IndexByte(pattern[start:], '}')
		if end == -1 {
			return nil, fmt.Errorf("formatter: unclosed '{' at position %d in pattern %q", start, pattern)
		}
		end += start
		inner := pattern[start+1 : end]
		spec, err := parseVarSpec(inner)
		if err != nil {
			return nil, fmt.Errorf("formatter: %w", err)
		}
		tokens = append(tokens, fmtToken{kind: kindVar, spec: spec})
		i = end + 1
	}
	return tokens, nil
}

// parseVarSpec parses the content between { and }, which may carry specifiers.
func parseVarSpec(inner string) (varSpec, error) {
	spec := varSpec{}

	// Conditional: key?trueval:falseval
	if qi := strings.IndexByte(inner, '?'); qi >= 0 {
		spec.key = inner[:qi]
		rest := inner[qi+1:]
		ci := strings.IndexByte(rest, ':')
		if ci < 0 {
			spec.trueVal = rest
		} else {
			spec.trueVal = rest[:ci]
			spec.falseVal = rest[ci+1:]
		}
		spec.conditional = true
		return spec, nil
	}

	// Join: key|sep
	if pi := strings.IndexByte(inner, '|'); pi >= 0 {
		spec.key = inner[:pi]
		spec.joinSep = inner[pi+1:]
		return spec, nil
	}

	// Replace: key/old/new or key//old/new (all occurrences)
	if si := strings.IndexByte(inner, '/'); si >= 0 {
		spec.key = inner[:si]
		rest := inner[si+1:]
		if strings.HasPrefix(rest, "/") {
			spec.replaceAll = true
			rest = rest[1:]
		}
		parts := strings.SplitN(rest, "/", 2)
		spec.replaceOld = parts[0]
		if len(parts) == 2 {
			spec.replaceNew = parts[1]
		}
		return spec, nil
	}

	// Date layout: key:layout
	if ci := strings.IndexByte(inner, ':'); ci >= 0 {
		spec.key = inner[:ci]
		spec.dateLayout = inner[ci+1:]
		return spec, nil
	}

	// Conversion: key!l  key!u  key!j
	if ei := strings.IndexByte(inner, '!'); ei >= 0 {
		spec.key = inner[:ei]
		spec.conversion = strings.ToLower(inner[ei+1:])
		return spec, nil
	}

	// Plain key
	spec.key = inner
	return spec, nil
}

// evalVarToken resolves a kindVar token against kw and returns the string value.
func evalVarToken(tok fmtToken, kw map[string]any) string {
	spec := tok.spec

	if spec.conditional {
		v := lookupKey(kw, spec.key)
		if isTruthy(v) {
			return spec.trueVal
		}
		return spec.falseVal
	}

	raw := lookupKey(kw, spec.key)

	// Join specifier for slice values.
	if spec.joinSep != "" {
		return joinValue(raw, spec.joinSep)
	}

	// Replace specifier.
	if spec.replaceOld != "" {
		s := valueToString(raw, "")
		if spec.replaceAll {
			s = strings.ReplaceAll(s, spec.replaceOld, spec.replaceNew)
		} else {
			s = strings.Replace(s, spec.replaceOld, spec.replaceNew, 1)
		}
		return sanitizePath(s)
	}

	// Date layout.
	if spec.dateLayout != "" {
		if t, ok := raw.(time.Time); ok {
			return t.UTC().Format(spec.dateLayout)
		}
		// If the value is a string, try to parse it as RFC3339.
		if s, ok := raw.(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t.UTC().Format(spec.dateLayout)
			}
		}
		return ""
	}

	// Conversion.
	switch spec.conversion {
	case "l":
		return strings.ToLower(valueToString(raw, ""))
	case "u":
		return strings.ToUpper(valueToString(raw, ""))
	case "j":
		b, _ := json.Marshal(raw)
		return string(b)
	}

	// Plain value.
	s := valueToString(raw, "")
	return sanitizePath(s)
}

// lookupKey retrieves a value from kw supporting dot-notation (e.g. "author.screen_name").
func lookupKey(kw map[string]any, key string) any {
	if v, ok := kw[key]; ok {
		return v
	}
	// Dot-notation fallback: try nested maps.
	parts := strings.SplitN(key, ".", 2)
	if len(parts) == 2 {
		if sub, ok := kw[parts[0]]; ok {
			if m, ok := sub.(map[string]any); ok {
				return lookupKey(m, parts[1])
			}
		}
	}
	return nil
}

// valueToString converts an arbitrary value to its string representation.
// def is returned when v is nil or an unsupported type.
func valueToString(v any, def string) string {
	if v == nil {
		return def
	}
	switch t := v.(type) {
	case string:
		return t
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case time.Time:
		return t.UTC().Format("2006-01-02T15-04-05")
	case []string:
		return strings.Join(t, ",")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// joinValue joins a slice value using sep, or falls back to valueToString.
func joinValue(v any, sep string) string {
	switch t := v.(type) {
	case []string:
		return strings.Join(t, sep)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = valueToString(e, "")
		}
		return strings.Join(parts, sep)
	default:
		return valueToString(v, "")
	}
}

// isTruthy mirrors a broad notion of "false" so conditionals work on bools,
// empty strings, zero ints, nil, and empty slices.
func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case []string:
		return len(t) > 0
	case []any:
		return len(t) > 0
	default:
		return true
	}
}

// sanitizePath replaces path separator characters in a single value so it
// cannot accidentally create extra directory levels.
func sanitizePath(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		if !unicode.IsPrint(r) {
			return '_'
		}
		return r
	}, s)
}
