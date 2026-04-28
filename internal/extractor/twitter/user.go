package twitter

import (
	"context"
	"fmt"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

// TwitterUserExtractor handles:
//   - https://twitter.com/{username}
//   - https://twitter.com/{username}/media
//   - https://x.com/{username}
//   - https://x.com/{username}/media
type TwitterUserExtractor struct {
	base
	screenName string
	mediaOnly  bool
}

func newUserExtractor(rawURL string, params extractor.ClientParams) extractor.Extractor {
	screenName, mediaOnly := parseUserURL(rawURL)
	return &TwitterUserExtractor{
		base:       newBase(rawURL, params),
		screenName: screenName,
		mediaOnly:  mediaOnly,
	}
}

func (e *TwitterUserExtractor) Name() string     { return "twitter:user" }
func (e *TwitterUserExtractor) Category() string { return "twitter" }

func (e *TwitterUserExtractor) Items(ctx context.Context) <-chan extractor.Item {
	out := make(chan extractor.Item)
	go func() {
		defer close(out)

		// Resolve user ID from screen name.
		userID, err := e.resolveUserID(ctx, e.screenName)
		if err != nil {
			return
		}

		// Emit a directory item first.
		select {
		case out <- extractor.Item{Kind: extractor.KindDirectory, DirPath: e.screenName}:
		case <-ctx.Done():
			return
		}

		operation := "UserTweets"
		if e.mediaOnly {
			operation = "UserMedia"
		}

		for item := range extractor.Paginate(ctx, func(ctx context.Context, cursor string) ([]extractor.Item, string, error) {
			return e.fetchUserPage(ctx, userID, operation, cursor)
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

func (e *TwitterUserExtractor) resolveUserID(ctx context.Context, screenName string) (string, error) {
	resp, err := e.graphQL(ctx, "UserByScreenName", map[string]any{
		"screen_name":                screenName,
		"withSafetyModeUserFields":   true,
	})
	if err != nil {
		return "", fmt.Errorf("resolve user %q: %w", screenName, err)
	}
	return parseUserID(resp)
}

func (e *TwitterUserExtractor) fetchUserPage(ctx context.Context, userID, operation, cursor string) ([]extractor.Item, string, error) {
	vars := map[string]any{
		"userId":                 userID,
		"count":                  20,
		"includePromotedContent": false,
		"withQuickPromoteEligibilityTweetFields": true,
		"withVoice":              true,
		"withV2Timeline":         true,
	}
	if cursor != "" {
		vars["cursor"] = cursor
	}

	resp, err := e.graphQL(ctx, operation, vars)
	if err != nil {
		return nil, "", err
	}
	return parseTweetTimeline(resp)
}

// parseUserURL extracts screenName and mediaOnly from a twitter.com user URL.
func parseUserURL(rawURL string) (screenName string, mediaOnly bool) {
	// Strip scheme and host.
	for _, prefix := range []string{
		"https://twitter.com/", "http://twitter.com/",
		"https://x.com/", "http://x.com/",
		"https://www.twitter.com/", "https://www.x.com/",
	} {
		if len(rawURL) > len(prefix) && rawURL[:len(prefix)] == prefix {
			rawURL = rawURL[len(prefix):]
			break
		}
	}
	// Remove query/fragment.
	for _, sep := range []byte{'?', '#'} {
		if idx := indexOf(rawURL, sep); idx >= 0 {
			rawURL = rawURL[:idx]
		}
	}
	// Trim trailing slash.
	for len(rawURL) > 0 && rawURL[len(rawURL)-1] == '/' {
		rawURL = rawURL[:len(rawURL)-1]
	}

	parts := splitPath(rawURL)
	if len(parts) == 0 {
		return "", false
	}
	screenName = parts[0]
	if len(parts) > 1 && parts[1] == "media" {
		mediaOnly = true
	}
	return
}

func indexOf(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func splitPath(s string) []string {
	var parts []string
	cur := ""
	for _, c := range s {
		if c == '/' {
			if cur != "" {
				parts = append(parts, cur)
				cur = ""
			}
		} else {
			cur += string(c)
		}
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	return parts
}
