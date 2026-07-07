package twitter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// Twitter rotates GraphQL query IDs with web-client deploys, which turns the
// baked-in defaultQueryIDs stale and makes every request to that operation
// 404. The current IDs are embedded in the web client's JS bundles, so when a
// 404 is seen the client scrapes them from there once and retries.

// bundleURLRe finds client-web JS bundle URLs in the x.com homepage HTML.
// Query IDs live in the main bundle plus a handful of split chunks.
var bundleURLRe = regexp.MustCompile(`https://abs\.twimg\.com/responsive-web/client-web(?:-legacy)?/(?:main|api)[.\w~-]*\.js`)

// queryIDRe matches `queryId:"...",operationName:"..."` pairs inside a bundle.
var queryIDRe = regexp.MustCompile(`queryId:"([^"]+)",operationName:"([^"]+)"`)

// maxBundleBytes caps how much of a JS bundle is read (they run 1-5 MB).
const maxBundleBytes = 16 << 20

// fetchedQueryIDTTL is the cache lifetime for scraped query IDs.
const fetchedQueryIDTTL = 24 * time.Hour

// qidRefreshCooldown prevents repeated scrapes when a refresh does not fix
// the 404 (e.g. the operation moved to a bundle we do not fetch).
const qidRefreshCooldown = 15 * time.Minute

// qidFetch is process-wide shared state so concurrent extractors trigger at
// most one scrape per cooldown window.
var qidFetch struct {
	mu  sync.Mutex
	at  time.Time
	ids map[string]string
}

// refreshedQueryID scrapes the current query IDs from the x.com JS bundles
// and returns the ID for operation, or ("", false) when unavailable. Results
// are shared process-wide and persisted to the KV cache, where queryID picks
// them up in preference to the baked-in defaults.
func (b *base) refreshedQueryID(ctx context.Context, operation string) (string, bool) {
	qidFetch.mu.Lock()
	defer qidFetch.mu.Unlock()

	if time.Since(qidFetch.at) >= qidRefreshCooldown {
		qidFetch.at = time.Now()
		ids, err := fetchQueryIDs(ctx, b.Params.HTTP)
		if err != nil {
			if b.Params.Logger != nil {
				b.Params.Logger.Warn(fmt.Sprintf("twitter: could not refresh query IDs: %v", err))
			}
		} else {
			if b.Params.Logger != nil {
				b.Params.Logger.Info(fmt.Sprintf("twitter: refreshed %d query IDs from web client", len(ids)))
			}
			qidFetch.ids = ids
			if b.Params.Cache != nil {
				for op, id := range ids {
					_ = b.Params.Cache.Set(ctx, qidCacheKey(op), id, fetchedQueryIDTTL)
				}
			}
		}
	}

	id, ok := qidFetch.ids[operation]
	return id, ok && id != ""
}

// fetchQueryIDs downloads the x.com homepage, locates the client-web JS
// bundles, and extracts all queryId/operationName pairs from them.
func fetchQueryIDs(ctx context.Context, client *http.Client) (map[string]string, error) {
	page, err := fetchTextResource(ctx, client, "https://x.com/", 4<<20)
	if err != nil {
		return nil, fmt.Errorf("fetch homepage: %w", err)
	}

	bundles := bundleURLRe.FindAllString(page, -1)
	if len(bundles) == 0 {
		return nil, fmt.Errorf("no client-web bundle URLs found in homepage")
	}

	ids := make(map[string]string)
	seen := make(map[string]bool)
	for _, bundleURL := range bundles {
		if seen[bundleURL] {
			continue
		}
		seen[bundleURL] = true
		js, err := fetchTextResource(ctx, client, bundleURL, maxBundleBytes)
		if err != nil {
			continue // best effort; other bundles may still yield IDs
		}
		for _, m := range queryIDRe.FindAllStringSubmatch(js, -1) {
			ids[m[2]] = m[1]
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no query IDs found in %d bundle(s)", len(seen))
	}
	return ids, nil
}

// fetchTextResource GETs url with a browser User-Agent and returns up to
// maxBytes of the body as a string.
func fetchTextResource(ctx context.Context, client *http.Client, url string, maxBytes int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
