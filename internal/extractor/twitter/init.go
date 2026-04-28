// Package twitter registers extractors for Twitter/X URLs.
// Import this package with a blank import to activate all Twitter extractors:
//
//	import _ "github.com/hecker-01/go-gallery/internal/extractor/twitter"
package twitter

import "github.com/hecker-01/go-gallery/internal/extractor"

// URL patterns for Twitter/X. More specific patterns must appear before more
// general ones because Dispatch returns the first match.
const (
	// /i/bookmarks
	patBookmarks = `(?i)https?://(?:www\.)?(?:twitter|x)\.com/i/bookmarks\b`
	// /i/lists/{id}
	patList = `(?i)https?://(?:www\.)?(?:twitter|x)\.com/i/lists/\d+`
	// /home (home timeline)
	patTimeline = `(?i)https?://(?:www\.)?(?:twitter|x)\.com/home\b`
	// /search?q=...
	patSearch = `(?i)https?://(?:www\.)?(?:twitter|x)\.com/search\b`
	// /{username}/status/{id}
	patTweet = `(?i)https?://(?:www\.)?(?:twitter|x)\.com/[A-Za-z0-9_]+/status/\d+`
	// /{username}/likes
	patLikes = `(?i)https?://(?:www\.)?(?:twitter|x)\.com/[A-Za-z0-9_]+/likes\b`
	// /{username} and /{username}/media
	patUser = `(?i)https?://(?:www\.)?(?:twitter|x)\.com/[A-Za-z0-9_]+(?:/media)?/?$`
)

func init() {
	extractor.Register(patBookmarks, newBookmarksExtractor)
	extractor.Register(patList, newListExtractor)
	extractor.Register(patTimeline, newTimelineExtractor)
	extractor.Register(patSearch, newSearchExtractor)
	extractor.Register(patTweet, newTweetExtractor)
	extractor.Register(patLikes, newLikesExtractor)
	extractor.Register(patUser, newUserExtractor)
}
