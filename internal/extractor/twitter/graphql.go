package twitter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hecker-01/go-gallery/internal/extractor"
	"github.com/hecker-01/go-gallery/internal/galleryerrs"
)

const maxRateLimitRetries = 3

// graphQLReqSeq is a per-process monotonic counter used to correlate
// request/response debug log lines.
var graphQLReqSeq atomic.Uint64

// ─── Query IDs ────────────────────────────────────────────────────────────────

// defaultQueryIDs maps GraphQL operation names to their query IDs.
// Twitter rotates these periodically; last verified 2025.
var defaultQueryIDs = map[string]string{
	"UserByScreenName":         "ck5KkZ8t5cOmoLssopN99Q",
	"UserTweets":               "E8Wq-_jFSaU7hxVcuOPR9g",
	"UserTweetsAndReplies":     "-O3QOHrVn1aOm_cF5wyTCQ",
	"UserMedia":                "jCRhbOzdgOHp6u9H4g2tEg",
	"TweetDetail":              "iFEr5AcP121Og4wx9Yqo3w",
	"SearchTimeline":           "4fpceYZ6-YQCx_JSl_Cn_A",
	"Bookmarks":                "pLtjrO4ubNh996M_Cubwsg",
	"Likes":                    "TGEKkJG_meudeaFcqaxM-Q",
	"HomeTimeline":             "DXmgQYmIft1oLP6vMkJixw",
	"ListLatestTweetsTimeline": "06JtmwM8k_1cthpFZITVVA",
}

// ─── Feature flags ────────────────────────────────────────────────────────────

// baseFeatures is the standard set of Twitter GraphQL feature flags used for
// timeline pagination endpoints (UserMedia, UserTweets, etc.).
var baseFeatures = map[string]bool{
	"rweb_video_screen_enabled": false,
	"payments_enabled":          false,
	"rweb_xchat_enabled":        false,
	"profile_label_improvements_pcf_label_in_post_enabled":                    true,
	"rweb_tipjar_consumption_enabled":                                         true,
	"verified_phone_label_enabled":                                            false,
	"creator_subscriptions_tweet_preview_api_enabled":                         true,
	"responsive_web_graphql_timeline_navigation_enabled":                      true,
	"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       false,
	"premium_content_api_read_enabled":                                        false,
	"communities_web_enable_tweet_community_results_fetch":                    true,
	"c9s_tweet_anatomy_moderator_badge_enabled":                               true,
	"responsive_web_grok_analyze_button_fetch_trends_enabled":                 false,
	"responsive_web_grok_analyze_post_followups_enabled":                      true,
	"responsive_web_jetfuel_frame":                                            true,
	"responsive_web_grok_share_attachment_enabled":                            true,
	"articles_preview_enabled":                                                true,
	"responsive_web_edit_tweet_api_enabled":                                   true,
	"graphql_is_translatable_rweb_tweet_is_translatable_enabled":              true,
	"view_counts_everywhere_api_enabled":                                      true,
	"longform_notetweets_consumption_enabled":                                 true,
	"responsive_web_twitter_article_tweet_consumption_enabled":                true,
	"tweet_awards_web_tipping_enabled":                                        false,
	"responsive_web_grok_show_grok_translated_post":                           false,
	"responsive_web_grok_analysis_button_from_backend":                        true,
	"creator_subscriptions_quote_tweet_preview_enabled":                       false,
	"freedom_of_speech_not_reach_fetch_enabled":                               true,
	"standardized_nudges_misinfo":                                             true,
	"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": true,
	"longform_notetweets_rich_text_read_enabled":                              true,
	"longform_notetweets_inline_media_enabled":                                true,
	"responsive_web_grok_image_annotation_enabled":                            true,
	"responsive_web_grok_imagine_annotation_enabled":                          true,
	"responsive_web_grok_community_note_auto_translation_is_enabled":          false,
	"responsive_web_enhance_cards_enabled":                                    false,
}

// userFeatures is the feature flag set used for user-lookup operations
// (UserByScreenName, UserByRestId). Different from baseFeatures.
var userFeatures = map[string]bool{
	"hidden_profile_subscriptions_enabled":                              true,
	"payments_enabled":                                                  false,
	"rweb_xchat_enabled":                                                false,
	"profile_label_improvements_pcf_label_in_post_enabled":              true,
	"rweb_tipjar_consumption_enabled":                                   true,
	"verified_phone_label_enabled":                                      false,
	"highlights_tweets_tab_ui_enabled":                                  true,
	"responsive_web_twitter_article_notes_tab_enabled":                  true,
	"subscriptions_feature_can_gift_premium":                            true,
	"creator_subscriptions_tweet_preview_api_enabled":                   true,
	"responsive_web_graphql_skip_user_profile_image_extensions_enabled": false,
	"responsive_web_graphql_timeline_navigation_enabled":                true,
	"subscriptions_verification_info_is_identity_verified_enabled":      true,
	"subscriptions_verification_info_verified_since_enabled":            true,
}

// featuresJSON returns a compact JSON representation of baseFeatures.
func featuresJSON() string {
	b, _ := json.Marshal(baseFeatures)
	return string(b)
}

// userFeaturesJSON returns a compact JSON representation of userFeatures.
func userFeaturesJSON() string {
	b, _ := json.Marshal(userFeatures)
	return string(b)
}

// ─── GraphQL client ───────────────────────────────────────────────────────────

// graphQL performs a Twitter GraphQL GET request and decodes the JSON response.
// An optional fieldToggles map is appended as a "fieldToggles" query parameter.
func (b *base) graphQL(ctx context.Context, operation string, variables map[string]any, fieldToggles ...map[string]any) (map[string]any, error) {
	qid := b.queryID(ctx, operation)

	variablesJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, fmt.Errorf("twitter graphql %s: marshal variables: %w", operation, err)
	}

	endpoint := fmt.Sprintf("%s/i/api/graphql/%s/%s", b.endpointBase, qid, operation)
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("variables", string(variablesJSON))
	// UserByScreenName uses a different, smaller feature set.
	if operation == "UserByScreenName" {
		q.Set("features", userFeaturesJSON())
	} else {
		q.Set("features", featuresJSON())
	}
	if len(fieldToggles) > 0 && fieldToggles[0] != nil {
		if ft, err := json.Marshal(fieldToggles[0]); err == nil {
			q.Set("fieldToggles", string(ft))
		}
	}
	u.RawQuery = q.Encode()

	reqID := fmt.Sprintf("%04x", graphQLReqSeq.Add(1))

	var lastRateLimitErr error
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		if b.Params.RateLimits != nil {
			if wait := b.Params.RateLimits.Wait(operation, time.Now()); wait > 0 {
				if b.Params.Logger != nil {
					b.Params.Logger.Info(fmt.Sprintf("rate-limit window exhausted for %s; sleeping %s",
						operation, wait.Round(time.Second)))
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(wait + 500*time.Millisecond):
				}
			}
		}

		if b.Params.Logger != nil {
			b.Params.Logger.Debug(fmt.Sprintf("→ #%s %s (attempt %d/%d): %s",
				reqID, operation, attempt+1, maxRateLimitRetries+1, u.String()))
		}
		reqStart := time.Now()

		attemptCtx, attemptCancel := context.WithTimeout(ctx, 60*time.Second)
		resp, err := b.doGet(attemptCtx, u.String())
		elapsed := time.Since(reqStart)

		if err != nil {
			attemptCancel()
			if b.Params.Logger != nil {
				b.Params.Logger.Debug(fmt.Sprintf("← #%s %s: error after %s: %v",
					reqID, operation, elapsed.Round(time.Millisecond), err))
			}
			if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() == nil {
				// Per-attempt timeout (not parent cancellation): treat as transient.
				if b.Params.Logger != nil {
					b.Params.Logger.Warn(fmt.Sprintf("twitter %s request timed out (attempt %d/%d), retrying",
						operation, attempt+1, maxRateLimitRetries+1))
				}
				lastRateLimitErr = err
				continue
			}
			return nil, fmt.Errorf("twitter graphql %s: %w", operation, err)
		}

		if b.Params.Logger != nil {
			b.Params.Logger.Debug(fmt.Sprintf("← #%s %s: %d in %s",
				reqID, operation, resp.StatusCode, elapsed.Round(time.Millisecond)))
		}
		defer resp.Body.Close()

		if b.Params.RateLimits != nil {
			b.Params.RateLimits.Update(operation, resp)
			if s := b.Params.RateLimits.Status(operation); s.Limit > 0 && s.Remaining > 0 && s.Remaining <= 3 {
				if b.Params.Logger != nil {
					b.Params.Logger.Warn(fmt.Sprintf("twitter %s near rate limit: %d/%d remaining (resets %s)",
						operation, s.Remaining, s.Limit, s.ResetAt.UTC().Format(time.RFC3339)))
				}
			}
		}

		if resp.StatusCode == http.StatusUnauthorized {
			attemptCancel()
			return nil, &galleryerrs.AuthenticationError{}
		}
		if resp.StatusCode == http.StatusForbidden {
			attemptCancel()
			return nil, &galleryerrs.AuthorizationError{URL: operation}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			attemptCancel() // body not needed; release context before sleeping
			resetAt := parseRateLimitReset(resp)
			if b.Params.RateLimitCB != nil {
				b.Params.RateLimitCB(operation, resetAt)
			}
			lastRateLimitErr = &galleryerrs.RateLimitError{Endpoint: operation, ResetAt: resetAt}
			if attempt < maxRateLimitRetries {
				waitDur := time.Until(resetAt)
				if waitDur < 10*time.Second {
					waitDur = 10 * time.Second
				}
				if b.Params.Logger != nil {
					b.Params.Logger.Info(fmt.Sprintf("rate limited on %s; waiting %s (resets at %s)",
						operation, waitDur.Round(time.Second), resetAt.UTC().Format(time.RFC3339)))
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(waitDur):
				}
				continue
			}
			return nil, lastRateLimitErr
		}
		if resp.StatusCode != http.StatusOK {
			attemptCancel()
			return nil, galleryerrs.ClassifyHTTPStatus(resp.StatusCode, endpoint, nil)
		}

		var result map[string]any
		bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		attemptCancel() // body fully consumed; release context
		if err != nil {
			return nil, fmt.Errorf("twitter graphql %s: read body: %w", operation, err)
		}
		if err := json.Unmarshal(bodyBytes, &result); err != nil {
			return nil, fmt.Errorf("twitter graphql %s: decode: %w", operation, err)
		}
		// Debug: log the full API result
		if b.Params.Logger != nil {
			b.Params.Logger.Debug(fmt.Sprintf("graphql result for %s: %s", operation, string(bodyBytes)))
		}
		// Debug: log full response when data is unexpectedly empty.
		if data, ok := result["data"].(map[string]any); ok && len(data) == 0 {
			if b.Params.Logger != nil {
				b.Params.Logger.Debug(fmt.Sprintf("graphql empty data response for %s: %s", operation, string(bodyBytes)))
			}
		}
		return result, nil
	}
	return nil, lastRateLimitErr
}

// queryIDCacheVersion is bumped whenever defaultQueryIDs changes, to
// invalidate any stale entries from a previous binary version.
const queryIDCacheVersion = "v2"

// queryID returns the query ID for the named operation, checking the cache
// first, then the baked-in default map.
func (b *base) queryID(ctx context.Context, operation string) string {
	cacheKey := "twitter:qid:" + queryIDCacheVersion + ":" + operation
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
// corresponding time plus a 10-second buffer for clock skew.
// Falls back to 15 minutes from now.
func parseRateLimitReset(resp *http.Response) time.Time {
	if resp == nil {
		return time.Now().Add(15 * time.Minute)
	}
	v := resp.Header.Get("x-rate-limit-reset")
	if v != "" {
		var unix int64
		fmt.Sscanf(v, "%d", &unix)
		if unix > 0 {
			return time.Unix(unix, 0).Add(10 * time.Second)
		}
	}
	return time.Now().Add(15 * time.Minute)
}

// ─── Response parsers ─────────────────────────────────────────────────────────

// parseUserID extracts the numeric user ID from a UserByScreenName response,
// mapping known Twitter API error codes to typed errors.
func parseUserID(resp map[string]any) (string, error) {
	// Check for top-level API errors first, mapping known codes to typed errors.
	if errs, ok := resp["errors"].([]any); ok && len(errs) > 0 {
		if first, ok := errs[0].(map[string]any); ok {
			code := int(intOrZero(first["code"]))
			msg, _ := first["message"].(string)
			switch code {
			case 50, 63: // User not found / suspended
				return "", &galleryerrs.NotFoundError{Reason: "suspended", URL: msg}
			case 144: // No status with that ID
				return "", &galleryerrs.NotFoundError{Reason: "deleted", URL: msg}
			case 32: // Could not authenticate
				return "", &galleryerrs.AuthenticationError{}
			case 220: // Your credentials do not allow access
				return "", &galleryerrs.AuthorizationError{URL: msg}
			case 88: // Rate limit exceeded
				return "", &galleryerrs.RateLimitError{Endpoint: "UserByScreenName"}
			default:
				if msg != "" {
					return "", fmt.Errorf("UserByScreenName API error (code %d): %s", code, msg)
				}
			}
		}
	}
	// Log available keys under "data" for debug if lookup fails.
	user, err := dig(resp, "data", "user", "result")
	if err != nil {
		if data, ok := resp["data"].(map[string]any); ok {
			keys := make([]string, 0, len(data))
			for k := range data {
				keys = append(keys, k)
			}
			return "", fmt.Errorf("UserByScreenName: unexpected data keys %v (expected 'user')", keys)
		}
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
	items := tweetResultToItems(res, 1, 1, "")
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
	entryID, _ := entry["entryId"].(string)
	content, ok := entry["content"].(map[string]any)
	if !ok {
		return nil
	}
	contentType, _ := content["entryType"].(string)
	switch contentType {
	case "TimelineTimelineItem":
		ic, _ := content["itemContent"].(map[string]any)
		return itemContentToItems(ic, entryID)
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
			out = append(out, itemContentToItems(ic3, entryID)...)
		}
		return out
	}
	return nil
}

func itemContentToItems(ic map[string]any, entryID string) []extractor.Item {
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
	return tweetResultToItems(result, 0, 0, entryID)
}

// tweetResultToItems converts a raw tweet result map to extractor Items.
// entryID is the timeline entryId (e.g. "tweet-1234567890") used to populate
// SkipTweetID when a tombstone is encountered; pass "" for direct-tweet lookups.
func tweetResultToItems(result any, num, count int, entryID string) []extractor.Item {
	r, ok := result.(map[string]any)
	if !ok {
		return nil
	}
	// Handle tweet tombstone — emit a KindSkipped item instead of silently dropping.
	typename, _ := r["__typename"].(string)
	if typename == "TweetTombstone" {
		reason := "tombstone"
		// Extract tombstone text for a more specific reason string.
		if ts, _ := r["tombstone"].(map[string]any); ts != nil {
			if textObj, _ := ts["text"].(map[string]any); textObj != nil {
				if txt, _ := textObj["text"].(string); txt != "" {
					lower := strings.ToLower(txt)
					switch {
					case strings.Contains(lower, "suspended"):
						reason = "suspended"
					case strings.Contains(lower, "violat"):
						reason = "policy-violation"
					case strings.Contains(lower, "unavailable"):
						reason = "tombstone"
					}
				}
			}
		}
		// Best-effort tweet ID: strip the "tweet-" prefix from the entry ID.
		tweetID := strings.TrimPrefix(entryID, "tweet-")
		return []extractor.Item{{
			Kind:        extractor.KindSkipped,
			SkipReason:  reason,
			SkipTweetID: tweetID,
		}}
	}
	// TweetUnavailable: DMCA takedown, suspended author, protected status, etc.
	// These arrive without a "legacy" object; emit Skipped instead of dropping.
	if typename == "TweetUnavailable" {
		reason := "unavailable"
		if r2, _ := r["reason"].(string); r2 != "" {
			reason = strings.ToLower(r2)
		}
		return []extractor.Item{{
			Kind:        extractor.KindSkipped,
			SkipReason:  reason,
			SkipTweetID: strings.TrimPrefix(entryID, "tweet-"),
		}}
	}
	// Unwrap TweetWithVisibilityResults
	if typename == "TweetWithVisibilityResults" {
		inner, _ := r["tweet"].(map[string]any)
		if inner != nil {
			return tweetResultToItems(inner, num, count, entryID)
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

	// Author — try multiple paths used by different API response shapes:
	// 1. Classic: r["core"]["user_results"]["result"]["legacy"]
	// 2. New 2025: r["core"]["user_results"]["result"]["core"] for screen_name/name
	// 3. New:     r["author"]["core"]["screen_name"] (gallery-dl 2025 format)
	var authorMeta extractor.AuthorMeta
	if core, _ := r["core"].(map[string]any); core != nil {
		if userRes, _ := core["user_results"].(map[string]any); userRes != nil {
			if userResult, _ := userRes["result"].(map[string]any); userResult != nil {
				// Try legacy first (classic format)
				if ul, _ := userResult["legacy"].(map[string]any); ul != nil {
					authorMeta.ID, _ = ul["id_str"].(string)
					authorMeta.Name, _ = ul["name"].(string)
					authorMeta.ScreenName, _ = ul["screen_name"].(string)
				}
				// New 2025 format: screen_name/name in a nested "core" object
				if authorMeta.ScreenName == "" {
					if uc, _ := userResult["core"].(map[string]any); uc != nil {
						authorMeta.ScreenName, _ = uc["screen_name"].(string)
						authorMeta.Name, _ = uc["name"].(string)
					}
				}
				if authorMeta.ID == "" {
					authorMeta.ID, _ = userResult["rest_id"].(string)
				}
			}
		}
	}
	// Fallback: new "author" key format (Twitter/X API circa 2025)
	if authorMeta.ScreenName == "" {
		if author, _ := r["author"].(map[string]any); author != nil {
			if authorCore, _ := author["core"].(map[string]any); authorCore != nil {
				authorMeta.ScreenName, _ = authorCore["screen_name"].(string)
				authorMeta.Name, _ = authorCore["name"].(string)
			}
			if authorMeta.ID == "" {
				authorMeta.ID, _ = author["rest_id"].(string)
			}
			// Also try legacy inside author
			if authorMeta.ScreenName == "" {
				if ul, _ := author["legacy"].(map[string]any); ul != nil {
					authorMeta.ID, _ = ul["id_str"].(string)
					authorMeta.Name, _ = ul["name"].(string)
					authorMeta.ScreenName, _ = ul["screen_name"].(string)
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
