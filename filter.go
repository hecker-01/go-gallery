package gallery

import (
	"fmt"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// Filter decides whether a Media item should be processed.
// Implementations must be safe for concurrent use.
type Filter interface {
	Accept(info *MediaInfo) bool
}

// AllOf composes filters with logical AND (short-circuit).
// An empty list accepts everything.
func AllOf(filters ...Filter) Filter { return allFilter(filters) }

// AnyOf composes filters with logical OR (short-circuit).
// An empty list accepts everything.
func AnyOf(filters ...Filter) Filter { return anyFilter(filters) }

type allFilter []Filter

func (af allFilter) Accept(info *MediaInfo) bool {
	for _, f := range af {
		if !f.Accept(info) {
			return false
		}
	}
	return true
}

type anyFilter []Filter

func (af anyFilter) Accept(info *MediaInfo) bool {
	if len(af) == 0 {
		return true
	}
	for _, f := range af {
		if f.Accept(info) {
			return true
		}
	}
	return false
}

// ─── RangeFilter ─────────────────────────────────────────────────────────────

// RangeFilter selects items by 1-based index using a comma-separated range
// expression like "1-5,7,10-20".
type RangeFilter struct {
	r Range
}

// NewRangeFilter parses s and returns a RangeFilter or an error.
func NewRangeFilter(s string) (RangeFilter, error) {
	r, err := ParseRange(s)
	if err != nil {
		return RangeFilter{}, err
	}
	return RangeFilter{r: r}, nil
}

func (f RangeFilter) Accept(info *MediaInfo) bool {
	return f.r.Contains(info.Num)
}

// ─── DateFilter ──────────────────────────────────────────────────────────────

// DateFilter accepts items whose date falls within [After, Before].
// A zero time means "no bound".
type DateFilter struct {
	After  time.Time
	Before time.Time
}

func (f DateFilter) Accept(info *MediaInfo) bool {
	if !f.After.IsZero() && info.Date.Before(f.After) {
		return false
	}
	if !f.Before.IsZero() && info.Date.After(f.Before) {
		return false
	}
	return true
}

// ─── ContentTypeFilter ───────────────────────────────────────────────────────

// ContentTypeFilter matches items whose Extension is in the allowed set.
// Extension comparison is case-insensitive.
type ContentTypeFilter struct {
	allowed map[string]struct{}
}

// NewContentTypeFilter returns a filter that accepts items with any of the
// given extensions (without leading dot, e.g. "jpg", "mp4").
func NewContentTypeFilter(extensions ...string) ContentTypeFilter {
	m := make(map[string]struct{}, len(extensions))
	for _, ext := range extensions {
		m[strings.ToLower(strings.TrimPrefix(ext, "."))] = struct{}{}
	}
	return ContentTypeFilter{allowed: m}
}

func (f ContentTypeFilter) Accept(info *MediaInfo) bool {
	ext := strings.ToLower(strings.TrimPrefix(info.Extension, "."))
	_, ok := f.allowed[ext]
	return ok
}

// ─── Bool filters ─────────────────────────────────────────────────────────────

type boolFilter struct {
	fn func(*MediaInfo) bool
}

func (f boolFilter) Accept(info *MediaInfo) bool { return f.fn(info) }

// IncludeRetweets returns a Filter that accepts retweets.
func IncludeRetweets() Filter { return boolFilter{fn: func(m *MediaInfo) bool { return true }} }

// ExcludeRetweets returns a Filter that rejects retweets.
func ExcludeRetweets() Filter { return boolFilter{fn: func(m *MediaInfo) bool { return !m.IsRetweet }} }

// IncludeReplies returns a Filter that accepts replies.
func IncludeReplies() Filter { return boolFilter{fn: func(m *MediaInfo) bool { return true }} }

// ExcludeReplies returns a Filter that rejects replies.
func ExcludeReplies() Filter { return boolFilter{fn: func(m *MediaInfo) bool { return !m.IsReply }} }

// IncludeQuotes returns a Filter that accepts quote tweets.
func IncludeQuotes() Filter { return boolFilter{fn: func(m *MediaInfo) bool { return true }} }

// ExcludeQuotes returns a Filter that rejects quote tweets.
func ExcludeQuotes() Filter { return boolFilter{fn: func(m *MediaInfo) bool { return !m.IsQuote }} }

// ImagesOnly returns a filter that accepts only image media (jpg, jpeg, png, gif, webp).
func ImagesOnly() Filter {
	return NewContentTypeFilter("jpg", "jpeg", "png", "gif", "webp")
}

// VideosOnly returns a filter that accepts only video media (mp4, mov, avi, webm).
func VideosOnly() Filter {
	return NewContentTypeFilter("mp4", "mov", "avi", "webm", "m4v")
}

// MinFaves returns a filter that accepts items with at least n favorites.
func MinFaves(n int) Filter {
	return boolFilter{fn: func(m *MediaInfo) bool { return m.FavoriteCount >= n }}
}

// ─── ExprFilter ──────────────────────────────────────────────────────────────

// ExprFilter evaluates a boolean expression against MediaInfo.Keywords().
// The expression is compiled once at construction time for efficiency.
//
// Example expressions:
//   - "favorite_count >= 100"
//   - "extension == \"jpg\" && !is_retweet"
//   - "len(hashtags) > 0"
type ExprFilter struct {
	program *vm.Program
}

// exprEnv is the environment type used for expression compilation.
// Fields mirror MediaInfo.Keywords() map keys.
type exprEnv map[string]any

// NewExprFilter compiles src into an expression filter.
// Returns an error if the expression is syntactically invalid or does not
// evaluate to a boolean.
func NewExprFilter(src string) (ExprFilter, error) {
	// Build a sample environment for type-checking.
	env := exprEnv{
		"tweet_id":           "",
		"author.name":        "",
		"author.id":          "",
		"author.screen_name": "",
		"date":               time.Time{},
		"content":            "",
		"media_url":          "",
		"extension":          "",
		"num":                0,
		"count":              0,
		"favorite_count":     0,
		"retweet_count":      0,
		"reply_count":        0,
		"quote_count":        0,
		"lang":               "",
		"hashtags":           []string{},
		"mentions":           []string{},
		"is_retweet":         false,
		"is_reply":           false,
		"is_quote":           false,
		"category":           "",
	}
	program, err := expr.Compile(src, expr.Env(env), expr.AsBool())
	if err != nil {
		return ExprFilter{}, fmt.Errorf("ExprFilter compile: %w", err)
	}
	return ExprFilter{program: program}, nil
}

func (f ExprFilter) Accept(info *MediaInfo) bool {
	if info == nil || f.program == nil {
		return true
	}
	kw := info.Keywords()
	result, err := expr.Run(f.program, exprEnv(kw))
	if err != nil {
		return false
	}
	b, _ := result.(bool)
	return b
}

