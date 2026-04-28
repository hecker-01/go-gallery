package twitter

import (
	"context"
	"regexp"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

var listIDRe = regexp.MustCompile(`/i/lists/(\d+)`)

// TwitterListExtractor handles:
//   - https://twitter.com/i/lists/{id}
//   - https://x.com/i/lists/{id}
type TwitterListExtractor struct {
	base
	listID string
}

func newListExtractor(rawURL string, params extractor.ClientParams) extractor.Extractor {
	listID := ""
	if m := listIDRe.FindStringSubmatch(rawURL); len(m) > 1 {
		listID = m[1]
	}
	return &TwitterListExtractor{
		base:   newBase(rawURL, params),
		listID: listID,
	}
}

func (e *TwitterListExtractor) Name() string     { return "twitter:list" }
func (e *TwitterListExtractor) Category() string { return "twitter" }

func (e *TwitterListExtractor) Items(ctx context.Context) <-chan extractor.Item {
	out := make(chan extractor.Item)
	go func() {
		defer close(out)

		for item := range extractor.Paginate(ctx, func(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
			return e.fetchListPage(ctx, cursor)
		}, func(err error) {
			if e.Params.Logger != nil {
				e.Params.Logger.Error("fetch list page failed", "list_id", e.listID, "error", err)
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

func (e *TwitterListExtractor) fetchListPage(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
	vars := map[string]any{
		"listId": e.listID,
		"count":  20,
	}
	if cursor != "" {
		vars["cursor"] = cursor
	}

	resp, err := e.graphQL(ctx, "ListLatestTweetsTimeline", vars)
	if err != nil {
		return nil, "", err
	}
	return parseListTimeline(resp)
}
