// Package extractor defines the Extractor interface, the item types yielded
// by extractors, and the global registry used by gallery.Client.Extract.
//
// This package deliberately does NOT import the root gallery package so the
// Twitter (and future) sub-packages can implement this interface without
// creating an import cycle.
package extractor

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// ─── Item types ─────────────────────────────────────────────────────────────

// ItemKind discriminates the message variants.
type ItemKind uint8

const (
	KindDirectory ItemKind = iota
	KindMedia
	KindQueue
	KindSkipped // item was identified but permanently unavailable
)

// AuthorMeta holds the Twitter user fields embedded in every ItemMeta.
type AuthorMeta struct {
	ID         string
	Name       string
	ScreenName string
}

// ItemMeta is the full metadata record for a single extracted media item.
// It mirrors gallery.MediaInfo field-for-field; gallery.Client converts
// between the two at the boundary.
type ItemMeta struct {
	TweetID       string
	Author        AuthorMeta
	Date          time.Time
	Content       string
	MediaURL      string
	Extension     string
	Num           int
	Count         int
	FavoriteCount int
	RetweetCount  int
	ReplyCount    int
	QuoteCount    int
	Lang          string
	Hashtags      []string
	Mentions      []string
	IsRetweet     bool
	IsReply       bool
	IsQuote       bool
	Card          map[string]any
	Category      string
}

// Item is a single message produced by an Extractor. Exactly one of the
// variant fields is populated, determined by Kind.
type Item struct {
	Kind ItemKind

	// KindDirectory
	DirPath string

	// KindMedia
	URL  string
	Meta *ItemMeta

	// KindQueue
	QueueURL string

	// KindSkipped
	SkipReason  string // "tombstone" | "deleted" | "suspended" | "dmca" | …
	SkipTweetID string
}

// ─── Client params ───────────────────────────────────────────────────────────

// KVCache is the minimal cache interface that extractors use to persist
// short-lived values (guest tokens, query IDs). gallery.Cache satisfies it.
type KVCache interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

// ClientParams bundles the dependencies that an Extractor needs from the
// calling gallery.Client. Using a plain struct avoids importing the gallery
// package from within internal/extractor.
type ClientParams struct {
	HTTP        *http.Client
	Cookies     http.CookieJar
	Cache       KVCache
	Logger      *slog.Logger
	RateLimitCB func(endpoint string, resetAt time.Time)
	Concurrency int
}

// ─── Extractor interface ─────────────────────────────────────────────────────

// Extractor extracts media items from a class of URLs.
// Implementations must be safe for concurrent use: a single Extractor
// instance may have Items called from multiple goroutines.
type Extractor interface {
	// Name returns a stable dot-separated identifier, e.g. "twitter:user".
	Name() string
	// Category is the site label, e.g. "twitter".
	Category() string
	// Items starts extraction and returns a channel of Item values.
	// The channel is closed when extraction completes or ctx is cancelled.
	// Errors are surfaced by sending an Item with Kind == KindQueue and a
	// special QueueURL prefix (internal convention: "error:..."), OR via the
	// exported Err field. Callers should drain and close on ctx.Done().
	Items(ctx context.Context) <-chan Item
}

// Factory constructs an Extractor for a specific matched URL.
// rawURL is the exact URL that triggered the registration pattern.
type Factory func(rawURL string, params ClientParams) Extractor

// ─── Registry ────────────────────────────────────────────────────────────────

type registered struct {
	re      *regexp.Regexp
	factory Factory
}

var (
	mu       sync.RWMutex
	registry []registered
)

// Register adds an extractor pattern and factory to the global registry.
// It is safe to call from init() functions in sub-packages.
// Patterns are matched in registration order; the first match wins.
func Register(pattern string, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry = append(registry, registered{
		re:      regexp.MustCompile(pattern),
		factory: factory,
	})
}

// Dispatch scans the registry for the first pattern that matches rawURL and
// constructs the corresponding Extractor. Returns (nil, false) when no pattern
// matches.
func Dispatch(rawURL string, params ClientParams) (Extractor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	for _, r := range registry {
		if r.re.MatchString(rawURL) {
			return r.factory(rawURL, params), true
		}
	}
	return nil, false
}

// Registered returns the number of registered extractor patterns.
// Useful in tests to verify that init() side-effects ran.
func Registered() int {
	mu.RLock()
	defer mu.RUnlock()
	return len(registry)
}
