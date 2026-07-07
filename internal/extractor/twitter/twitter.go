// Package twitter implements extractors for Twitter/X URLs.
// Each concrete extractor type is registered in init() so that importing this
// package (via a blank import from the gallery package) is sufficient to make
// them available.
package twitter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

// guestWarnOnce ensures the guest-mode rate-limit warning is logged at most
// once per process, regardless of how many extractors are constructed.
var guestWarnOnce sync.Once

// ─── Constants ────────────────────────────────────────────────────────────────

// publicBearerToken is the permanent bearer token used by the Twitter web
// client. It is widely documented and used by tools like gallery-dl.
const publicBearerToken = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"

const guestTokenURL = "https://api.x.com/1.1/guest/activate.json"
const guestTokenCacheKey = "twitter:guest_token"
const guestTokenTTL = 3 * time.Hour

// userIDCacheTTL is how long screen-name → user-ID lookups stay cached.
// User IDs are immutable, but a handle can be freed and re-registered by a
// different account, so the mapping is not cached forever.
const userIDCacheTTL = 90 * 24 * time.Hour

// userAgent mimics a real browser to avoid trivial bot detection.
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// ─── Base Twitter extractor ───────────────────────────────────────────────────

// base is the shared foundation for all Twitter extractors.
// It manages guest-token acquisition and adds the required auth headers.
type base struct {
	extractor.BaseExtractor
	guestToken   string // lazily populated
	csrfToken    string // always set; from ct0 cookie or randomly generated
	endpointBase string // GraphQL API base, default "https://x.com"
}

func newBase(rawURL string, params extractor.ClientParams) base {
	if params.Logger != nil {
		params.Logger = params.Logger.With("source", "twitter")
	}
	return base{
		BaseExtractor: extractor.NewBase(rawURL, params),
		csrfToken:     randomCSRFToken(),
		endpointBase:  "https://x.com",
	}
}

// userAgent returns the configured User-Agent override, or the built-in
// browser default.
func (b *base) userAgent() string {
	if b.Params.Twitter.UserAgent != "" {
		return b.Params.Twitter.UserAgent
	}
	return userAgent
}

// randomCSRFToken generates a random 32-char hex CSRF token for guest-mode
// requests. Twitter requires x-csrf-token on all GraphQL calls, even
// unauthenticated ones; gallery-dl does the same.
func randomCSRFToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ensureGuestToken fetches a guest token if one is not yet cached.
// It is a no-op when authenticated cookies (auth_token) are already present.
func (b *base) ensureGuestToken(ctx context.Context) error {
	if b.guestToken != "" {
		return nil
	}

	// If the cookie jar already has an auth_token for x.com/twitter.com,
	// we are in authenticated mode and do not need a guest token.
	if b.hasAuthToken() {
		return nil
	}

	// Config-supplied override.
	if b.Params.Twitter.GuestToken != "" {
		b.guestToken = b.Params.Twitter.GuestToken
		return nil
	}

	guestWarnOnce.Do(func() {
		if b.Params.Logger != nil {
			b.Params.Logger.Warn("twitter: running in guest mode - Twitter rate limits are very low. Use --cookies-from-browser firefox or --cookies-from-file to authenticate.")
		}
	})

	// Try the KV cache first.
	if b.Params.Cache != nil {
		if v, ok, err := b.Params.Cache.Get(ctx, guestTokenCacheKey); err == nil && ok {
			b.guestToken = v
			return nil
		}
	}

	tok, err := b.fetchGuestToken(ctx)
	if err != nil {
		return err
	}
	b.guestToken = tok

	if b.Params.Cache != nil {
		_ = b.Params.Cache.Set(ctx, guestTokenCacheKey, tok, guestTokenTTL)
	}
	return nil
}

// resolveUserID translates a screen name into the numeric user ID via the
// UserByScreenName endpoint. Successful lookups are cached (see
// userIDCacheTTL): the endpoint has a tight rate limit (150/15min), so a
// cache hit saves one call per user URL.
func (b *base) resolveUserID(ctx context.Context, screenName string) (string, error) {
	cacheKey := "twitter:userid:" + strings.ToLower(screenName)
	if b.Params.Cache != nil {
		if v, ok, err := b.Params.Cache.Get(ctx, cacheKey); err == nil && ok && v != "" {
			return v, nil
		}
	}

	resp, err := b.graphQL(ctx, "UserByScreenName", map[string]any{
		"screen_name":           screenName,
		"withGrokTranslatedBio": false,
	}, map[string]any{"withAuxiliaryUserLabels": true})
	if err != nil {
		return "", fmt.Errorf("resolve user %q: %w", screenName, err)
	}
	id, err := parseUserID(resp)
	if err != nil {
		return "", err
	}
	if b.Params.Cache != nil {
		_ = b.Params.Cache.Set(ctx, cacheKey, id, userIDCacheTTL)
	}
	return id, nil
}

// hasAuthToken reports whether the cookie jar contains an auth_token cookie
// for x.com or twitter.com (indicating an authenticated session).
func (b *base) hasAuthToken() bool {
	if b.Params.Cookies == nil {
		return false
	}
	for _, domain := range []string{"https://x.com/", "https://twitter.com/"} {
		u, _ := url.Parse(domain)
		for _, ck := range b.Params.Cookies.Cookies(u) {
			if ck.Name == "auth_token" && ck.Value != "" {
				return true
			}
		}
	}
	return false
}

// fetchGuestToken activates a new guest token via the Twitter 1.1 API.
func (b *base) fetchGuestToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, guestTokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+publicBearerToken)
	req.Header.Set("User-Agent", b.userAgent())

	resp, err := b.Params.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("twitter: guest token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("twitter: guest token HTTP %d", resp.StatusCode)
	}

	var result struct {
		GuestToken string `json:"guest_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("twitter: guest token decode: %w", err)
	}
	if result.GuestToken == "" {
		return "", fmt.Errorf("twitter: empty guest_token in response")
	}
	return result.GuestToken, nil
}

// authHeaders returns the common authentication headers for Twitter API
// requests. If the cookie jar contains ct0/auth_token, authenticated headers
// are added; otherwise guest-token mode is used.
func (b *base) authHeaders() map[string]string {
	h := map[string]string{
		"Accept":                    "*/*",
		"Authorization":             "Bearer " + publicBearerToken,
		"Content-Type":              "application/json",
		"Referer":                   "https://x.com/",
		"Sec-Fetch-Dest":            "empty",
		"Sec-Fetch-Mode":            "cors",
		"Sec-Fetch-Site":            "same-origin",
		"User-Agent":                b.userAgent(),
		"x-twitter-active-user":     "yes",
		"x-twitter-client-language": "en",
	}
	if b.guestToken != "" {
		h["x-guest-token"] = b.guestToken
	}

	// Start with the session CSRF token (random for guest mode).
	csrfToken := b.csrfToken

	// Override with ct0 cookie when authenticated.
	if b.Params.Cookies != nil {
		for _, domain := range []string{"https://x.com/", "https://twitter.com/"} {
			u, _ := url.Parse(domain)
			for _, ck := range b.Params.Cookies.Cookies(u) {
				switch ck.Name {
				case "ct0":
					csrfToken = ck.Value
					h["x-twitter-auth-type"] = "OAuth2Session"
				}
			}
		}
	}

	// Always send x-csrf-token - Twitter silently returns {"data":{}}
	// when this header is missing, even for unauthenticated GraphQL calls.
	h["x-csrf-token"] = csrfToken
	return h
}

// doGet performs an authenticated GET, ensuring a guest token is available.
func (b *base) doGet(ctx context.Context, rawURL string) (*http.Response, error) {
	if err := b.ensureGuestToken(ctx); err != nil {
		return nil, err
	}
	headers := b.authHeaders()
	return b.Get(ctx, rawURL, headers)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// strOrEmpty returns the string value of v or "".
func strOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

// intOrZero returns the int value of v or 0.
func intOrZero(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

// boolOrFalse returns the bool value of v or false.
func boolOrFalse(v any) bool {
	b, _ := v.(bool)
	return b
}

// parseTwitterDate parses a Twitter date string ("Mon Jan 02 15:04:05 +0000 2006").
func parseTwitterDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse("Mon Jan 02 15:04:05 +0000 2006", s)
	return t
}

// bestVideoVariant picks a video variant URL. maxBitrate selects the highest
// bitrate; false selects the lowest. Variants without a bitrate (HLS .m3u8
// playlists) are used only when no direct variant exists.
func bestVideoVariant(variants []any, maxBitrate bool) string {
	var bestURL, playlistURL string
	bestBitrate := -1.0
	for _, vv := range variants {
		v, ok := vv.(map[string]any)
		if !ok {
			continue
		}
		u, _ := v["url"].(string)
		if u == "" {
			continue
		}
		bitrate, _ := v["bitrate"].(float64)
		if bitrate == 0 {
			if playlistURL == "" {
				playlistURL = u
			}
			continue
		}
		if bestBitrate < 0 ||
			(maxBitrate && bitrate > bestBitrate) ||
			(!maxBitrate && bitrate < bestBitrate) {
			bestURL, bestBitrate = u, bitrate
		}
	}
	if bestURL == "" {
		return playlistURL
	}
	return bestURL
}

// imageOrig rewrites a Twitter image URL to request the original resolution.
func imageOrig(rawURL string) string {
	if strings.Contains(rawURL, "pbs.twimg.com/media/") {
		u, err := url.Parse(rawURL)
		if err != nil {
			return rawURL
		}
		q := u.Query()
		q.Set("name", "orig")
		u.RawQuery = q.Encode()
		return u.String()
	}
	return rawURL
}

// extensionFromURL guesses the file extension from a URL.
func extensionFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "jpg"
	}
	path := u.Path
	// Remove query string already handled via url.Parse
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return strings.ToLower(path[idx+1:])
	}
	return "jpg"
}
