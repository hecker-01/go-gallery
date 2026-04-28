package twitter

import (
	"context"
	"net/url"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

// TwitterSearchExtractor handles:
//   - https://twitter.com/search?q=...
//   - https://x.com/search?q=...
type TwitterSearchExtractor struct {
	base
	query string
}

func newSearchExtractor(rawURL string, params extractor.ClientParams) extractor.Extractor {
	query := ""
	if u, err := url.Parse(rawURL); err == nil {
		query = u.Query().Get("q")
	}
	return &TwitterSearchExtractor{
		base:  newBase(rawURL, params),
		query: query,
	}
}

func (e *TwitterSearchExtractor) Name() string     { return "twitter:search" }
func (e *TwitterSearchExtractor) Category() string { return "twitter" }

func (e *TwitterSearchExtractor) Items(ctx context.Context) <-chan extractor.Item {
	out := make(chan extractor.Item)
	go func() {
		defer close(out)

		for item := range extractor.Paginate(ctx, func(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
			return e.fetchSearchPage(ctx, cursor)
		}) {
			select {
			case out <- item:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (e *TwitterSearchExtractor) fetchSearchPage(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
	vars := map[string]any{
		"rawQuery":    e.query,
		"count":       20,
		"querySource": "typed_query",
		"product":     "Media",
	}
	if cursor != "" {
		vars["cursor"] = cursor
	}

	resp, err := e.graphQL(ctx, "SearchTimeline", vars)
	if err != nil {
		return nil, "", err
	}
	return parseSearchTimeline(resp)
}
