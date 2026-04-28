package twitter

import (
	"context"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

// TwitterTimelineExtractor handles the home timeline:
//   - https://twitter.com/home
//   - https://x.com/home
//
// Requires authenticated cookies.
type TwitterTimelineExtractor struct {
	base
}

func newTimelineExtractor(rawURL string, params extractor.ClientParams) extractor.Extractor {
	return &TwitterTimelineExtractor{base: newBase(rawURL, params)}
}

func (e *TwitterTimelineExtractor) Name() string     { return "twitter:timeline" }
func (e *TwitterTimelineExtractor) Category() string { return "twitter" }

func (e *TwitterTimelineExtractor) Items(ctx context.Context) <-chan extractor.Item {
	out := make(chan extractor.Item)
	go func() {
		defer close(out)

		for item := range extractor.Paginate(ctx, func(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
			return e.fetchTimelinePage(ctx, cursor)
		}, func(err error) {
			if e.Params.Logger != nil {
				e.Params.Logger.Error("fetch home timeline page failed", "error", err)
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

func (e *TwitterTimelineExtractor) fetchTimelinePage(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
	vars := map[string]any{
		"count":                  20,
		"includePromotedContent": false,
		"latestControlAvailable": true,
		"requestContext":         "launch",
		"withCommunity":          true,
	}
	if cursor != "" {
		vars["cursor"] = cursor
	}

	resp, err := e.graphQL(ctx, "HomeTimeline", vars)
	if err != nil {
		return nil, "", err
	}
	return parseHomeTimeline(resp)
}
