package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hecker-01/go-gallery/internal/extractor"
)

// ─── Query IDs ────────────────────────────────────────────────────────────────

// defaultQueryIDs maps GraphQL operation names to their query IDs.
// Twitter rotates these; they are correct as of late 2024.
// A future enhancement will scrape the live web bundle and update the cache.
var defaultQueryIDs = map[string]string{
	"UserByScreenName":         "qW5u-DAuXpMEG0zaGnZoIw",
	"UserTweets":               "V7H0Ap3_Hh2FyS75OCDO3Q",
	"UserTweetsAndReplies":     "9yyVn4nXbi_FDrVCFxlzhQ",
	"UserMedia":                "oMVVkQ5G-T8NCBzlRJ7UFBA",
	"TweetDetail":              "BoHLKeBvibdYDiJON1oqTg",
	"SearchTimeline":           "rKoqjfHnCEtQkBe6y1IYRQ",
	"Bookmarks":                "xLjCVLmkC35QaFHy9IbHWA",
	"Likes":                    "kgZtsNyE46T3JaEf2nF9vA",
	"HomeTimeline":             "zhX91JE87mWvfprhYBnMcA",
	"ListLatestTweetsTimeline": "BbGLL1ZfMibdFNWlk7a0Pw",
}

// ─── Feature flags ────────────────────────────────────────────────────────────

// baseFeatures is the standard set of Twitter GraphQL feature flags.
var baseFeatures = map[string]bool{
	"rweb_lists_timeline_redesign_enabled":                                    true,
	"responsive_web_graphql_exclude_directive_enabled":                        true,
	"verified_phone_label_enabled":                                            false,
	"creator_subscriptions_tweet_preview_api_enabled":                         true,
	"responsive_web_graphql_timeline_navigation_enabled":                      true,
	"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       false,
	"tweetypie_unmention_optimization_enabled":                                true,
	"responsive_web_edit_tweet_api_enabled":                                   true,
	"graphql_is_translatable_rweb_tweet_is_translatable_enabled":              true,
	"view_counts_everywhere_api_enabled":                                      true,
	"longform_notetweets_consumption_enabled":                                 true,
	"tweet_awards_web_tipping_enabled":                                        false,
	"freedom_of_speech_not_reach_fetch_enabled":                               true,
	"standardized_nudges_misinfo":                                             true,
	"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": false,
	"interactive_text_enabled":                                                true,
	"responsive_web_text_conversations_enabled":                               false,
	"longform_notetweets_rich_text_read_enabled":                              true,
	"longform_notetweets_inline_media_enabled":                                false,
	"responsive_web_enhance_cards_enabled":                                    false,
}

// featuresJSON returns a compact JSON representation of baseFeatures.
func featuresJSON() string {
	b, _ := json.Marshal(baseFeatures)
	return string(b)
}

// ─── GraphQL client ───────────────────────────────────────────────────────────

// graphQL performs a Twitter GraphQL GET request and decodes the JSON response.
func (b *base) graphQL(ctx context.Context, operation string, variables map[string]any) (map[string]any, error) {
	qid := b.queryID(ctx, operation)

	variablesJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, fmt.Errorf("twitter graphql %s: marshal variables: %w", operation, err)
	}

	endpoint := fmt.Sprintf("https://twitter.com/i/api/graphql/%s/%s", qid, operation)
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("variables", string(variablesJSON))
	q.Set("features", featuresJSON())
	u.RawQuery = q.Encode()

	resp, err := b.doGet(ctx, u.String())
	if err != nil {
		return nil, fmt.Errorf("twitter graphql %s: %w", operation, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, &authErr{msg: "authentication required for " + operation}
	}
	if resp.StatusCode == 403 {
		return nil, &authErr{msg: "not authorized for " + operation}
	}
	if resp.StatusCode == 429 {
		resetAt := parseRateLimitReset(resp)
		if b.Params.RateLimitCB != nil {
			b.Params.RateLimitCB(operation, resetAt)
		}
		return nil, &rateLimitErr{endpoint: operation, resetAt: resetAt}
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("twitter graphql %s: HTTP %d", operation, resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("twitter graphql %s: decode: %w", operation, err)
	}
	return result, nil
}

// queryID returns the query ID for the named operation, checking the cache
// first, then the baked-in default map.
func (b *base) queryID(ctx context.Context, operation string) string {
	cacheKey := "twitter:qid:" + operation
	if b.Params.Cache != nil {
		if v, ok, err := b.Params.Cache.Get(ctx, cacheKey); err == nil && ok {
			return v
		}
	}
	if id, ok := defaultQueryIDs[operation]; ok {
		if b.Params.Cache != nil {
			_ = b.Params.Cache.Set(ctx, cacheKey, id, 24*time.Hour)
		}
		return id
	}
	return operation // fallback: use the operation name itself (will fail, but won't panic)
}

// parseRateLimitReset reads the x-rate-limit-reset header and returns the
// corresponding time. Falls back to 15 minutes from now.
func parseRateLimitReset(resp *http.Response) time.Time {
	if resp == nil {
		return time.Now().Add(15 * time.Minute)
	}
	v := resp.Header.Get("x-rate-limit-reset")
	if v != "" {
		var unix int64
		fmt.Sscanf(v, "%d", &unix)
		if unix > 0 {
			return time.Unix(unix, 0)
		}
	}
	return time.Now().Add(15 * time.Minute)
}

// ─── Typed errors ────────────────────────────────────────────────────────────

type authErr struct{ msg string }

func (e *authErr) Error() string { return e.msg }

type rateLimitErr struct {
	endpoint string
	resetAt  time.Time
}

func (e *rateLimitErr) Error() string {
	return fmt.Sprintf("rate limit on %s, resets at %s", e.endpoint, e.resetAt.UTC().Format(time.RFC3339))
}

// ─── Response parsers ─────────────────────────────────────────────────────────

// parseUserID extracts the numeric user ID from a UserByScreenName response.
func parseUserID(resp map[string]any) (string, error) {
	user, err := dig(resp, "data", "user", "result")
	if err != nil {
		return "", fmt.Errorf("UserByScreenName: %w", err)
	}
	legacy, err := dig(user, "legacy")
	if err != nil {
		return "", fmt.Errorf("UserByScreenName: %w", err)
	}
	id, ok := legacy.(map[string]any)["id_str"].(string)
	if !ok || id == "" {
		// Try rest_id
		m, _ := user.(map[string]any)
		id, _ = m["rest_id"].(string)
	}
	if id == "" {
		return "", fmt.Errorf("UserByScreenName: could not find user ID")
	}
	return id, nil
}

// parseTweetTimeline parses a UserTweets / UserMedia response and returns
// (items, nextCursor, error).
func parseTweetTimeline(resp map[string]any) ([]extractor.Item, string, error) {
	instructions, err := findTimelineInstructions(resp)
	if err != nil {
		return nil, "", err
	}
	return extractTimelineItems(instructions)
}

// parseSearchTimeline parses a SearchTimeline response.
func parseSearchTimeline(resp map[string]any) ([]extractor.Item, string, error) {
	instructions, err := digArr(resp, "data", "search_by_raw_query", "search_timeline", "timeline", "instructions")
	if err != nil {
		return nil, "", err
	}
	return extractTimelineItems(instructions)
}

// parseBookmarks parses a Bookmarks response.
func parseBookmarks(resp map[string]any) ([]extractor.Item, string, error) {
	instructions, err := digArr(resp, "data", "bookmark_timeline_v2", "timeline", "instructions")
	if err != nil {
		return nil, "", err
	}
	return extractTimelineItems(instructions)
}

// parseLikes parses a Likes response.
func parseLikes(resp map[string]any) ([]extractor.Item, string, error) {
	instructions, err := findTimelineInstructions(resp)
	if err != nil {
		return nil, "", err
	}
	return extractTimelineItems(instructions)
}

// parseHomeTimeline parses a HomeTimeline response.
func parseHomeTimeline(resp map[string]any) ([]extractor.Item, string, error) {
	instructions, err := digArr(resp, "data", "home", "home_timeline_urt", "instructions")
	if err != nil {
		return nil, "", err
	}
	return extractTimelineItems(instructions)
}

// parseListTimeline parses a ListLatestTweetsTimeline response.
func parseListTimeline(resp map[string]any) ([]extractor.Item, string, error) {
	instructions, err := findTimelineInstructions(resp)
	if err != nil {
		return nil, "", err
	}
	return extractTimelineItems(instructions)
}

// parseTweetDetail extracts items from a TweetDetail response.
func parseTweetDetail(resp map[string]any) ([]extractor.Item, error) {
	res, err := dig(resp, "data", "tweetResult", "result")
	if err != nil {
		return nil, err
	}
	items := tweetResultToItems(res, 1, 1)
	return items, nil
}

// ─── Timeline parsing internals ───────────────────────────────────────────────

func findTimelineInstructions(resp map[string]any) ([]any, error) {
	// Several endpoints nest their timeline differently; try common paths.
	paths := [][]string{
		{"data", "user", "result", "timeline_v2", "timeline", "instructions"},
		{"data", "user", "result", "timeline", "timeline", "instructions"},
		{"data", "list", "tweets_timeline", "timeline", "instructions"},
	}
	for _, path := range paths {
		if arr, err := digArr(resp, path...); err == nil {
			return arr, nil
		}
	}
	return nil, fmt.Errorf("could not locate timeline instructions in response")
}

func extractTimelineItems(instructions []any) ([]extractor.Item, string, error) {
	var items []extractor.Item
	var nextCursor string

	for _, instrAny := range instructions {
		instr, ok := instrAny.(map[string]any)
		if !ok {
			continue
		}
		switch instr["type"] {
		case "TimelineAddEntries":
			entries, _ := instr["entries"].([]any)
			for _, entryAny := range entries {
				entry, ok := entryAny.(map[string]any)
				if !ok {
					continue
				}
				entryID, _ := entry["entryId"].(string)
				if strings.HasPrefix(entryID, "cursor-bottom") || strings.HasSuffix(entryID, "-cursor-bottom") {
					if c := extractCursorFromEntry(entry); c != "" {
						nextCursor = c
					}
					continue
				}
				if strings.HasPrefix(entryID, "cursor-") {
					continue
				}
				newItems := entryToItems(entry)
				items = append(items, newItems...)
			}
		case "TimelineReplaceEntry":
			entry, _ := instr["entry"].(map[string]any)
			if entry != nil {
				entryID, _ := entry["entryId"].(string)
				if strings.Contains(entryID, "cursor-bottom") {
					if c := extractCursorFromEntry(entry); c != "" {
						nextCursor = c
					}
				}
			}
		}
	}
	return items, nextCursor, nil
}

func extractCursorFromEntry(entry map[string]any) string {
	content, _ := entry["content"].(map[string]any)
	if content == nil {
		return ""
	}
	if v, _ := content["value"].(string); v != "" {
		return v
	}
	// itemContent path
	ic, _ := content["itemContent"].(map[string]any)
	if ic != nil {
		if v, _ := ic["value"].(string); v != "" {
			return v
		}
	}
	return ""
}

func entryToItems(entry map[string]any) []extractor.Item {
	content, ok := entry["content"].(map[string]any)
	if !ok {
		return nil
	}
	contentType, _ := content["entryType"].(string)
	switch contentType {
	case "TimelineTimelineItem":
		ic, _ := content["itemContent"].(map[string]any)
		return itemContentToItems(ic)
	case "TimelineTimelineModule":
		items2, _ := content["items"].([]any)
		var out []extractor.Item
		for _, it := range items2 {
			itMap, _ := it.(map[string]any)
			if itMap == nil {
				continue
			}
			ic2, _ := itMap["item"].(map[string]any)
			if ic2 == nil {
				continue
			}
			ic3, _ := ic2["itemContent"].(map[string]any)
			out = append(out, itemContentToItems(ic3)...)
		}
		return out
	}
	return nil
}

func itemContentToItems(ic map[string]any) []extractor.Item {
	if ic == nil {
		return nil
	}
	itemType, _ := ic["itemType"].(string)
	if itemType != "TimelineTweet" {
		return nil
	}
	tweetResult, _ := ic["tweet_results"].(map[string]any)
	if tweetResult == nil {
		return nil
	}
	result, _ := tweetResult["result"].(map[string]any)
	return tweetResultToItems(result, 0, 0)
}

func tweetResultToItems(result any, num, count int) []extractor.Item {
	r, ok := result.(map[string]any)
	if !ok {
		return nil
	}
	// Handle tweet tombstone / protected tweet
	typename, _ := r["__typename"].(string)
	if typename == "TweetTombstone" {
		return nil
	}
	// Unwrap TweetWithVisibilityResults
	if typename == "TweetWithVisibilityResults" {
		inner, _ := r["tweet"].(map[string]any)
		if inner != nil {
			return tweetResultToItems(inner, num, count)
		}
	}

	legacy, _ := r["legacy"].(map[string]any)
	if legacy == nil {
		return nil
	}

	// Retweet check
	if rt, ok := legacy["retweeted_status_id_str"].(string); ok && rt != "" {
		return nil // skip retweets at this level; caller can decide
	}

	tweetID, _ := legacy["id_str"].(string)
	createdAt, _ := legacy["created_at"].(string)
	fullText, _ := legacy["full_text"].(string)
	lang, _ := legacy["lang"].(string)
	favoriteCount := intOrZero(legacy["favorite_count"])
	retweetCount := intOrZero(legacy["retweet_count"])
	replyCount := intOrZero(legacy["reply_count"])
	quoteCount := intOrZero(legacy["quote_count"])
	isReply := strOrEmpty(legacy["in_reply_to_status_id_str"]) != ""
	isQuote := boolOrFalse(legacy["is_quote_status"])

	// Author
	var authorMeta extractor.AuthorMeta
	if core, _ := r["core"].(map[string]any); core != nil {
		if userRes, _ := core["user_results"].(map[string]any); userRes != nil {
			if userResult, _ := userRes["result"].(map[string]any); userResult != nil {
				if ul, _ := userResult["legacy"].(map[string]any); ul != nil {
					authorMeta.ID, _ = ul["id_str"].(string)
					authorMeta.Name, _ = ul["name"].(string)
					authorMeta.ScreenName, _ = ul["screen_name"].(string)
				}
				if authorMeta.ID == "" {
					authorMeta.ID, _ = userResult["rest_id"].(string)
				}
			}
		}
	}

	// Hashtags and mentions
	var hashtags, mentions []string
	if entities, _ := legacy["entities"].(map[string]any); entities != nil {
		for _, htAny := range arrOrNil(entities["hashtags"]) {
			ht, _ := htAny.(map[string]any)
			if ht != nil {
				if text, _ := ht["text"].(string); text != "" {
					hashtags = append(hashtags, text)
				}
			}
		}
		for _, umAny := range arrOrNil(entities["user_mentions"]) {
			um, _ := umAny.(map[string]any)
			if um != nil {
				if sn, _ := um["screen_name"].(string); sn != "" {
					mentions = append(mentions, sn)
				}
			}
		}
	}

	date := parseTwitterDate(createdAt)

	// Extract media
	extEntities, _ := legacy["extended_entities"].(map[string]any)
	mediaArr := arrOrNil(nil)
	if extEntities != nil {
		mediaArr = arrOrNil(extEntities["media"])
	}
	if len(mediaArr) == 0 {
		entities2, _ := legacy["entities"].(map[string]any)
		if entities2 != nil {
			mediaArr = arrOrNil(entities2["media"])
		}
	}

	if len(mediaArr) == 0 {
		return nil // tweet has no media
	}

	total := len(mediaArr)
	if count != 0 {
		total = count
	}

	var items []extractor.Item
	for i, mAny := range mediaArr {
		m, ok := mAny.(map[string]any)
		if !ok {
			continue
		}
		mediaType, _ := m["type"].(string)

		var mediaURL, ext string
		switch mediaType {
		case "photo":
			mediaURL = imageOrig(strOrEmpty(m["media_url_https"]))
			ext = extensionFromURL(mediaURL)
		case "video", "animated_gif":
			videoInfo, _ := m["video_info"].(map[string]any)
			variants := arrOrNil(nil)
			if videoInfo != nil {
				variants = arrOrNil(videoInfo["variants"])
			}
			mediaURL = bestVideoVariant(variants)
			if mediaURL == "" {
				continue
			}
			// Strip query params for extension detection
			ext = extensionFromURL(strings.Split(mediaURL, "?")[0])
			if ext == "" || ext == "m3u8" {
				ext = "mp4"
			}
		default:
			continue
		}

		itemNum := num
		if num == 0 {
			itemNum = i + 1
		}

		items = append(items, extractor.Item{
			Kind: extractor.KindMedia,
			URL:  mediaURL,
			Meta: &extractor.ItemMeta{
				TweetID:       tweetID,
				Author:        authorMeta,
				Date:          date,
				Content:       fullText,
				MediaURL:      mediaURL,
				Extension:     ext,
				Num:           itemNum,
				Count:         total,
				FavoriteCount: favoriteCount,
				RetweetCount:  retweetCount,
				ReplyCount:    replyCount,
				QuoteCount:    quoteCount,
				Lang:          lang,
				Hashtags:      hashtags,
				Mentions:      mentions,
				IsReply:       isReply,
				IsQuote:       isQuote,
				Category:      "twitter",
			},
		})
	}
	return items
}

// ─── Utility ─────────────────────────────────────────────────────────────────

// dig navigates a nested map by successive string keys.
func dig(m any, keys ...string) (any, error) {
	cur := m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("dig: expected map at key %q, got %T", k, cur)
		}
		v, ok := mm[k]
		if !ok {
			return nil, fmt.Errorf("dig: key %q not found", k)
		}
		cur = v
	}
	return cur, nil
}

// digArr is like dig but asserts the final value is []any.
func digArr(m any, keys ...string) ([]any, error) {
	v, err := dig(m, keys...)
	if err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("digArr: expected []any at %v, got %T", keys, v)
	}
	return arr, nil
}

// arrOrNil coerces v to []any, returning nil if the assertion fails.
func arrOrNil(v any) []any {
	arr, _ := v.([]any)
	return arr
}
