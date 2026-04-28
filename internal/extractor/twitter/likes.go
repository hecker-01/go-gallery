package twitter

import (
	"context"
	"fmt"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

// TwitterLikesExtractor handles:
//   - https://twitter.com/{username}/likes
//   - https://x.com/{username}/likes
type TwitterLikesExtractor struct {
	base
	screenName string
}

func newLikesExtractor(rawURL string, params extractor.ClientParams) extractor.Extractor {
	screenName, _ := parseUserURL(rawURL)
	return &TwitterLikesExtractor{
		base:       newBase(rawURL, params),
		screenName: screenName,
	}
}

func (e *TwitterLikesExtractor) Name() string     { return "twitter:likes" }
func (e *TwitterLikesExtractor) Category() string { return "twitter" }

func (e *TwitterLikesExtractor) Items(ctx context.Context) <-chan extractor.Item {
	out := make(chan extractor.Item)
	go func() {
		defer close(out)

		userID, err := e.resolveUserID(ctx, e.screenName)
		if err != nil {
			if e.Params.Logger != nil {
				e.Params.Logger.Error("resolve user for likes", "screen_name", e.screenName, "error", err)
			}
			return
		}

		for item := range extractor.Paginate(ctx, func(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
			return e.fetchLikesPage(ctx, userID, cursor)
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

func (e *TwitterLikesExtractor) resolveUserID(ctx context.Context, screenName string) (string, error) {
	resp, err := e.graphQL(ctx, "UserByScreenName", map[string]any{
		"screen_name":              screenName,
		"withSafetyModeUserFields": true,
	})
	if err != nil {
		return "", fmt.Errorf("resolve user %q: %w", screenName, err)
	}
	return parseUserID(resp)
}

func (e *TwitterLikesExtractor) fetchLikesPage(ctx context.Context, userID, cursor string) ([]extractor.Item, string, error) {
	vars := map[string]any{
		"userId":                 userID,
		"count":                  20,
		"includePromotedContent": false,
	}
	if cursor != "" {
		vars["cursor"] = cursor
	}

	resp, err := e.graphQL(ctx, "Likes", vars)
	if err != nil {
		return nil, "", err
	}
	return parseLikes(resp)
}
