package twitter

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

var tweetURLRe = regexp.MustCompile(`/status/(\d+)`)

// TwitterTweetExtractor handles individual tweet URLs:
//   - https://twitter.com/{username}/status/{id}
//   - https://x.com/{username}/status/{id}
type TwitterTweetExtractor struct {
	base
	tweetID string
}

func newTweetExtractor(rawURL string, params extractor.ClientParams) extractor.Extractor {
	tweetID := ""
	if m := tweetURLRe.FindStringSubmatch(rawURL); len(m) > 1 {
		tweetID = m[1]
	}
	return &TwitterTweetExtractor{
		base:    newBase(rawURL, params),
		tweetID: tweetID,
	}
}

func (e *TwitterTweetExtractor) Name() string     { return "twitter:tweet" }
func (e *TwitterTweetExtractor) Category() string { return "twitter" }

func (e *TwitterTweetExtractor) Items(ctx context.Context) <-chan extractor.Item {
	out := make(chan extractor.Item)
	go func() {
		defer close(out)

		resp, err := e.graphQL(ctx, "TweetDetail", map[string]any{
			"focalTweetId":                           e.tweetID,
			"referrer":                               "tweet",
			"count":                                  20,
			"includePromotedContent":                 true,
			"withCommunity":                          true,
			"withQuickPromoteEligibilityTweetFields": true,
			"withBirdwatchNotes":                     true,
			"withVoice":                              true,
			"withV2Timeline":                         true,
		})
		if err != nil {
			if e.Params.Logger != nil {
				e.Params.Logger.Error("TweetDetail failed", "tweet_id", e.tweetID, "error", err)
			}
			return
		}

		items, err := parseTweetDetail(resp)
		if err != nil {
			if e.Params.Logger != nil {
				e.Params.Logger.Error("parseTweetDetail failed", "tweet_id", e.tweetID, "error", err)
			}
			return
		}

		for _, item := range items {
			select {
			case out <- item:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// resolveTweetAuthor returns the screen name from a tweet URL.
func resolveTweetAuthor(rawURL string) string {
	for _, prefix := range []string{
		"https://twitter.com/", "http://twitter.com/",
		"https://x.com/", "http://x.com/",
		"https://www.twitter.com/", "https://www.x.com/",
	} {
		if len(rawURL) > len(prefix) && rawURL[:len(prefix)] == prefix {
			path := rawURL[len(prefix):]
			parts := splitPath(path)
			if len(parts) > 0 {
				return parts[0]
			}
		}
	}
	return ""
}

// unused but demonstrates discoverability
var _ = fmt.Sprintf
