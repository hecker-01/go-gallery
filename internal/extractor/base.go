package extractor

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"
)

// BaseExtractor provides the shared HTTP helpers (retried GET, pagination)
// that concrete extractor types embed.
type BaseExtractor struct {
	RawURL string
	Params ClientParams
}

// NewBase constructs a BaseExtractor.
func NewBase(rawURL string, params ClientParams) BaseExtractor {
	return BaseExtractor{RawURL: rawURL, Params: params}
}

// Get performs an HTTP GET with up to maxRetries retries on transient errors.
func (b *BaseExtractor) Get(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	return retryGet(ctx, b.Params.HTTP, url, headers, 4)
}

// retryGet issues a GET with exponential-jittered backoff on transient errors.
// Only 5xx responses and network-level errors trigger a retry.
func retryGet(ctx context.Context, client *http.Client, url string, headers map[string]string, maxRetries int) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := jitteredBackoff(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			if isContextErr(err) {
				return nil, err
			}
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = &httpErr{code: resp.StatusCode, url: url}
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

// jitteredBackoff returns 1s·2^(n-1) + uniform random jitter up to 50% of
// the base delay, capped at 60 s.
func jitteredBackoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	if base > 60*time.Second {
		base = 60 * time.Second
	}
	jitter := time.Duration(rand.Int63n(int64(base/2) + 1))
	return base + jitter
}

type httpErr struct {
	code int
	url  string
}

func (e *httpErr) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.code, e.url)
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// ─── Paginator ───────────────────────────────────────────────────────────────

// Paginate drives cursor-based pagination. fetchPage receives the current
// cursor (empty string for the first call) and returns the page items, the
// next cursor, and an error. When nextCursor is empty the loop ends.
// The returned channel is closed when pagination finishes or ctx is cancelled.
func Paginate[T any](
	ctx context.Context,
	fetchPage func(ctx context.Context, cursor string) ([]T, string, error),
) <-chan T {
	out := make(chan T)
	go func() {
		defer close(out)
		cursor := ""
		for {
			items, next, err := fetchPage(ctx, cursor)
			if err != nil {
				return
			}
			for _, item := range items {
				select {
				case out <- item:
				case <-ctx.Done():
					return
				}
			}
			if next == "" {
				return
			}
			cursor = next
		}
	}()
	return out
}
