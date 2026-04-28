// Package twitter implements extractors for Twitter/X URLs.
// Each concrete extractor type is registered in init() so that importing this
// package (via a blank import from the gallery package) is sufficient to make
// them available.
package twitter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

// ─── Constants ────────────────────────────────────────────────────────────────

// publicBearerToken is the permanent bearer token used by the Twitter web
// client. It is widely documented and used by tools like gallery-dl.
const publicBearerToken = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I45S0571UAg%3DekV6BuODpif9TSUIvnGl8mD4ZEhCanYnHHmMITITnXU"

const guestTokenURL = "https://api.twitter.com/1.1/guest/activate.json"
const guestTokenCacheKey = "twitter:guest_token"
const guestTokenTTL = 3 * time.Hour

// userAgent mimics a real browser to avoid trivial bot detection.
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// ─── Base Twitter extractor ───────────────────────────────────────────────────

// base is the shared foundation for all Twitter extractors.
// It manages guest-token acquisition and adds the required auth headers.
type base struct {
	extractor.BaseExtractor
	guestToken string // lazily populated
}

func newBase(rawURL string, params extractor.ClientParams) base {
	return base{BaseExtractor: extractor.NewBase(rawURL, params)}
}

// ensureGuestToken fetches a guest token if one is not yet cached.
func (b *base) ensureGuestToken(ctx context.Context) error {
	if b.guestToken != "" {
		return nil
	}

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

// fetchGuestToken activates a new guest token via the Twitter 1.1 API.
func (b *base) fetchGuestToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, guestTokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+publicBearerToken)
	req.Header.Set("User-Agent", userAgent)

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
		"Authorization":              "Bearer " + publicBearerToken,
		"User-Agent":                 userAgent,
		"Content-Type":               "application/json",
		"x-twitter-client-language":  "en",
		"x-twitter-active-user":      "yes",
	}
	if b.guestToken != "" {
		h["x-guest-token"] = b.guestToken
	}

	// Check for auth cookies from the jar.
	if b.Params.Cookies != nil {
		apiURL, _ := url.Parse("https://api.twitter.com/")
		for _, ck := range b.Params.Cookies.Cookies(apiURL) {
			if ck.Name == "ct0" {
				h["x-csrf-token"] = ck.Value
				h["x-twitter-auth-type"] = "OAuth2Session"
			}
		}
	}
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

// doPost performs an authenticated POST with a JSON body.
func (b *base) doPost(ctx context.Context, rawURL string, body any) (*http.Response, error) {
	if err := b.ensureGuestToken(ctx); err != nil {
		return nil, err
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("twitter: marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	for k, v := range b.authHeaders() {
		req.Header.Set(k, v)
	}

	resp, err := b.Params.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// readJSON decodes the JSON body of resp into v.
func readJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// extractCt0 reads the ct0 cookie from the jar for twitter.com / x.com.
func (b *base) extractCt0() string {
	if b.Params.Cookies == nil {
		return ""
	}
	for _, domain := range []string{"https://twitter.com/", "https://x.com/"} {
		u, _ := url.Parse(domain)
		for _, ck := range b.Params.Cookies.Cookies(u) {
			if ck.Name == "ct0" {
				return ck.Value
			}
		}
	}
	return ""
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// joinStrings converts []any to a comma-separated string (used for hashtags/mentions).
func stringsFromAny(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

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

// bestVideoVariant picks the variant with the highest bitrate.
func bestVideoVariant(variants []any) string {
	var bestURL string
	var bestBitrate float64
	for _, vv := range variants {
		v, ok := vv.(map[string]any)
		if !ok {
			continue
		}
		bitrate, _ := v["bitrate"].(float64)
		u, _ := v["url"].(string)
		if u != "" && (bestURL == "" || bitrate > bestBitrate) {
			bestURL = u
			bestBitrate = bitrate
		}
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
