package twitter

import (
	"context"
	"fmt"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

// TwitterBookmarksExtractor handles:
//   - https://twitter.com/i/bookmarks
//   - https://x.com/i/bookmarks
//
// Requires authenticated cookies (auth_token + ct0).
type TwitterBookmarksExtractor struct {
	base
}

func newBookmarksExtractor(rawURL string, params extractor.ClientParams) extractor.Extractor {
	return &TwitterBookmarksExtractor{base: newBase(rawURL, params)}
}

func (e *TwitterBookmarksExtractor) Name() string     { return "twitter:bookmarks" }
func (e *TwitterBookmarksExtractor) Category() string { return "twitter" }

func (e *TwitterBookmarksExtractor) Items(ctx context.Context) <-chan extractor.Item {
	out := make(chan extractor.Item)
	go func() {
		defer close(out)

		for item := range extractor.Paginate(ctx, func(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
			return e.fetchBookmarksPage(ctx, cursor)
		}, func(err error) {
			if e.Params.Logger != nil {
				e.Params.Logger.Error(fmt.Sprintf("fetch bookmarks page failed: %v", err))
			}
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

func (e *TwitterBookmarksExtractor) fetchBookmarksPage(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
	vars := map[string]any{
		"count":                  20,
		"includePromotedContent": false,
	}
	if cursor != "" {
		vars["cursor"] = cursor
	}

	resp, err := e.graphQL(ctx, "Bookmarks", vars)
	if err != nil {
		return nil, "", err
	}
	return parseBookmarks(resp)
}
