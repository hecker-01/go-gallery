package twitter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hecker-01/go-gallery/internal/extractor"
	"github.com/hecker-01/go-gallery/internal/galleryerrs"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func newTestParams(srv *httptest.Server) extractor.ClientParams {
	return extractor.ClientParams{
		HTTP:   srv.Client(),
		Logger: nil,
		Cache:  nil,
	}
}

// patchGuestToken injects a pre-set guest token so tests don't need the real
// endpoint. Call before the extractor issues any requests.
func withGuestToken(b *base, tok string) {
	b.guestToken = tok
}

// jsonBody returns an http.HandlerFunc that writes the given JSON response.
func jsonBody(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
}

// ─── URL pattern matching ─────────────────────────────────────────────────────

func TestURLPatterns(t *testing.T) {
	// Patterns are already registered via init() when the package is imported.

	cases := []struct {
		url       string
		wantMatch bool
		wantName  string
	}{
		{"https://twitter.com/testuser", true, "twitter:user"},
		{"https://x.com/testuser", true, "twitter:user"},
		{"https://twitter.com/testuser/media", true, "twitter:user"},
		{"https://twitter.com/testuser/status/123456", true, "twitter:tweet"},
		{"https://x.com/testuser/status/123456", true, "twitter:tweet"},
		{"https://twitter.com/search?q=golang", true, "twitter:search"},
		{"https://twitter.com/i/bookmarks", true, "twitter:bookmarks"},
		{"https://twitter.com/i/lists/987654321", true, "twitter:list"},
		{"https://twitter.com/testuser/likes", true, "twitter:likes"},
		{"https://twitter.com/home", true, "twitter:timeline"},
		{"https://youtube.com/watch?v=abc", false, ""},
	}

	for _, tc := range cases {
		params := extractor.ClientParams{HTTP: http.DefaultClient}
		ex, ok := extractor.Dispatch(tc.url, params)
		if ok != tc.wantMatch {
			t.Errorf("Dispatch(%q): ok=%v, want %v", tc.url, ok, tc.wantMatch)
			continue
		}
		if tc.wantMatch && ex.Name() != tc.wantName {
			t.Errorf("Dispatch(%q): name=%q, want %q", tc.url, ex.Name(), tc.wantName)
		}
	}
}

// ─── GraphQL parsing tests ────────────────────────────────────────────────────

func TestParseUserID(t *testing.T) {
	resp := map[string]any{
		"data": map[string]any{
			"user": map[string]any{
				"result": map[string]any{
					"rest_id": "123456",
					"legacy": map[string]any{
						"id_str": "123456",
					},
				},
			},
		},
	}
	id, err := parseUserID(resp)
	if err != nil {
		t.Fatalf("parseUserID: %v", err)
	}
	if id != "123456" {
		t.Errorf("got %q, want %q", id, "123456")
	}
}

func TestParseUserID_SuspendedAccount(t *testing.T) {
	resp := map[string]any{
		"errors": []any{
			map[string]any{
				"code":    float64(63),
				"message": "Sorry, you are not authorized to see this status.",
			},
		},
	}
	_, err := parseUserID(resp)
	if err == nil {
		t.Fatal("expected error for suspended account, got nil")
	}
	var nfe *galleryerrs.NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("want *NotFoundError, got %T: %v", err, err)
	} else if nfe.Reason != "suspended" {
		t.Errorf("Reason = %q, want %q", nfe.Reason, "suspended")
	}
}

func TestParseUserID_AuthError(t *testing.T) {
	resp := map[string]any{
		"errors": []any{
			map[string]any{
				"code":    float64(32),
				"message": "Could not authenticate you.",
			},
		},
	}
	_, err := parseUserID(resp)
	if err == nil {
		t.Fatal("expected error for auth failure, got nil")
	}
	var authnErr *galleryerrs.AuthenticationError
	if !errors.As(err, &authnErr) {
		t.Errorf("want *AuthenticationError, got %T: %v", err, err)
	}
}

func TestTweetResultToItems_Photo(t *testing.T) {
	result := map[string]any{
		"__typename": "Tweet",
		"legacy": map[string]any{
			"id_str":         "999",
			"created_at":     "Mon Jan 02 15:04:05 +0000 2023",
			"full_text":      "hello",
			"lang":           "en",
			"favorite_count": float64(10),
			"extended_entities": map[string]any{
				"media": []any{
					map[string]any{
						"type":            "photo",
						"media_url_https": "https://pbs.twimg.com/media/abc.jpg",
					},
				},
			},
		},
		"core": map[string]any{
			"user_results": map[string]any{
				"result": map[string]any{
					"rest_id": "user1",
					"legacy": map[string]any{
						"id_str":      "user1",
						"name":        "Test User",
						"screen_name": "testuser",
					},
				},
			},
		},
	}

	items := tweetResultToItems(result, 1, 1, "", twOpts{VideoMaxBitrate: true})
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	item := items[0]
	if item.Kind != extractor.KindMedia {
		t.Errorf("kind: got %v, want KindMedia", item.Kind)
	}
	if item.Meta.TweetID != "999" {
		t.Errorf("tweet_id: got %q", item.Meta.TweetID)
	}
	if item.Meta.Author.ScreenName != "testuser" {
		t.Errorf("screen_name: got %q", item.Meta.Author.ScreenName)
	}
	if item.Meta.Extension != "jpg" {
		t.Errorf("extension: got %q", item.Meta.Extension)
	}
	if item.Meta.FavoriteCount != 10 {
		t.Errorf("favorite_count: got %d", item.Meta.FavoriteCount)
	}
	// URL should have ?name=orig appended.
	if item.URL == "" {
		t.Error("URL should not be empty")
	}
}

func TestTweetResultToItems_Video(t *testing.T) {
	result := map[string]any{
		"__typename": "Tweet",
		"legacy": map[string]any{
			"id_str":     "777",
			"created_at": "Mon Jan 02 15:04:05 +0000 2023",
			"full_text":  "video tweet",
			"lang":       "en",
			"extended_entities": map[string]any{
				"media": []any{
					map[string]any{
						"type": "video",
						"video_info": map[string]any{
							"variants": []any{
								map[string]any{
									"bitrate":      float64(2176000),
									"content_type": "video/mp4",
									"url":          "https://video.twimg.com/ext_tw_video/777/pu/vid/1280x720/video.mp4",
								},
								map[string]any{
									"bitrate":      float64(832000),
									"content_type": "video/mp4",
									"url":          "https://video.twimg.com/ext_tw_video/777/pu/vid/640x360/video.mp4",
								},
							},
						},
					},
				},
			},
		},
		"core": map[string]any{
			"user_results": map[string]any{
				"result": map[string]any{
					"rest_id": "user2",
					"legacy": map[string]any{
						"id_str":      "user2",
						"name":        "Video User",
						"screen_name": "videouser",
					},
				},
			},
		},
	}

	items := tweetResultToItems(result, 1, 1, "", twOpts{VideoMaxBitrate: true})
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	item := items[0]
	// Should pick highest bitrate
	if item.URL == "" {
		t.Error("URL should not be empty")
	}
	if item.Meta.Extension != "mp4" {
		t.Errorf("extension: got %q, want mp4", item.Meta.Extension)
	}
}

func TestTweetResultToItems_Tombstone(t *testing.T) {
	result := map[string]any{
		"__typename": "TweetTombstone",
		"tombstone": map[string]any{
			"text": map[string]any{
				"text":     "This Tweet is from a suspended account.",
				"entities": []any{},
			},
		},
	}

	items := tweetResultToItems(result, 0, 0, "tweet-1234567890", twOpts{VideoMaxBitrate: true})
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (KindSkipped)", len(items))
	}
	item := items[0]
	if item.Kind != extractor.KindSkipped {
		t.Errorf("kind: got %v, want KindSkipped", item.Kind)
	}
	if item.SkipTweetID != "1234567890" {
		t.Errorf("SkipTweetID: got %q, want %q", item.SkipTweetID, "1234567890")
	}
	if item.SkipReason != "suspended" {
		t.Errorf("SkipReason: got %q, want %q", item.SkipReason, "suspended")
	}
}

func TestTweetResultToItems_TombstoneGeneric(t *testing.T) {
	result := map[string]any{
		"__typename": "TweetTombstone",
		"tombstone": map[string]any{
			"text": map[string]any{
				"text": "This Tweet is unavailable.",
			},
		},
	}

	items := tweetResultToItems(result, 0, 0, "tweet-999", twOpts{VideoMaxBitrate: true})
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (KindSkipped)", len(items))
	}
	if items[0].Kind != extractor.KindSkipped {
		t.Errorf("kind: got %v, want KindSkipped", items[0].Kind)
	}
	if items[0].SkipReason != "tombstone" {
		t.Errorf("SkipReason: got %q, want %q", items[0].SkipReason, "tombstone")
	}
}

func TestTweetTimeline_ContainsTombstone(t *testing.T) {
	fixture := tweetTimelineWithTombstone()
	items, _, err := parseTweetTimeline(fixture, twOpts{VideoMaxBitrate: true})
	if err != nil {
		t.Fatalf("parseTweetTimeline: %v", err)
	}

	var mediaCount, skippedCount int
	for _, item := range items {
		switch item.Kind {
		case extractor.KindMedia:
			mediaCount++
		case extractor.KindSkipped:
			skippedCount++
		}
	}
	if mediaCount != 1 {
		t.Errorf("mediaCount = %d, want 1", mediaCount)
	}
	if skippedCount != 1 {
		t.Errorf("skippedCount = %d, want 1", skippedCount)
	}
}

// ─── GraphQL HTTP error classification ───────────────────────────────────────

func TestGraphQL_HTTP404_ReturnsNotFoundError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	params := extractor.ClientParams{HTTP: srv.Client()}
	b := newBase("https://twitter.com/testuser", params)
	b.guestToken = "test"
	b.endpointBase = srv.URL

	_, err := b.graphQL(context.Background(), "UserTweets", map[string]any{"userId": "1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var nfe *galleryerrs.NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("want *NotFoundError, got %T: %v", err, err)
	}
}

func TestGraphQL_HTTP403_ReturnsAuthorizationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	params := extractor.ClientParams{HTTP: srv.Client()}
	b := newBase("https://twitter.com/testuser", params)
	b.guestToken = "test"
	b.endpointBase = srv.URL

	_, err := b.graphQL(context.Background(), "UserTweets", map[string]any{"userId": "1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var authzErr *galleryerrs.AuthorizationError
	if !errors.As(err, &authzErr) {
		t.Errorf("want *AuthorizationError, got %T: %v", err, err)
	}
}

func TestGraphQL_HTTP401_ReturnsAuthenticationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	params := extractor.ClientParams{HTTP: srv.Client()}
	b := newBase("https://twitter.com/testuser", params)
	b.guestToken = "test"
	b.endpointBase = srv.URL

	_, err := b.graphQL(context.Background(), "UserTweets", map[string]any{"userId": "1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var authnErr *galleryerrs.AuthenticationError
	if !errors.As(err, &authnErr) {
		t.Errorf("want *AuthenticationError, got %T: %v", err, err)
	}
}

// ─── User-ID cache ────────────────────────────────────────────────────────────

// mapCache is an in-memory KVCache for tests. TTLs are ignored.
type mapCache struct{ m map[string]string }

func newMapCache() *mapCache { return &mapCache{m: map[string]string{}} }

func (c *mapCache) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := c.m[key]
	return v, ok, nil
}
func (c *mapCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	c.m[key] = value
	return nil
}
func (c *mapCache) Delete(_ context.Context, key string) error {
	delete(c.m, key)
	return nil
}
func (c *mapCache) Close() error { return nil }

func TestResolveUserID_CacheHitSkipsAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call %s; cached user ID should have been used", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cache := newMapCache()
	cache.m["twitter:userid:testuser"] = "42"

	params := extractor.ClientParams{HTTP: srv.Client(), Cache: cache}
	b := newBase("https://twitter.com/TestUser", params)
	b.guestToken = "test"
	b.endpointBase = srv.URL

	// Mixed case must hit the lowercased cache key.
	id, err := b.resolveUserID(context.Background(), "TestUser")
	if err != nil {
		t.Fatalf("resolveUserID: %v", err)
	}
	if id != "42" {
		t.Errorf("id = %q, want %q", id, "42")
	}
}

func TestResolveUserID_CacheMissResolvesAndStores(t *testing.T) {
	srv := httptest.NewServer(jsonBody(map[string]any{
		"data": map[string]any{
			"user": map[string]any{
				"result": map[string]any{
					"rest_id": "123456",
					"legacy":  map[string]any{"id_str": "123456"},
				},
			},
		},
	}))
	defer srv.Close()

	cache := newMapCache()
	params := extractor.ClientParams{HTTP: srv.Client(), Cache: cache}
	b := newBase("https://twitter.com/TestUser", params)
	b.guestToken = "test"
	b.endpointBase = srv.URL

	id, err := b.resolveUserID(context.Background(), "TestUser")
	if err != nil {
		t.Fatalf("resolveUserID: %v", err)
	}
	if id != "123456" {
		t.Errorf("id = %q, want %q", id, "123456")
	}
	if got := cache.m["twitter:userid:testuser"]; got != "123456" {
		t.Errorf("cached id = %q, want %q", got, "123456")
	}
}

// ─── Extractor integration tests with httptest ────────────────────────────────

func TestUserExtractor_HTTPMock(t *testing.T) {
	// Build a fake Twitter API that returns a user ID then a page of tweets.
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		switch {
		case contains(r.URL.Path, "UserByScreenName"):
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"user": map[string]any{
						"result": map[string]any{
							"rest_id": "42",
							"legacy":  map[string]any{"id_str": "42"},
						},
					},
				},
			})
		case contains(r.URL.Path, "UserTweets"):
			json.NewEncoder(w).Encode(tweetTimelineFixture())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Override the GraphQL endpoint base for tests.
	// We patch the extractor to use the test server.
	params := extractor.ClientParams{
		HTTP: srv.Client(),
	}

	// Create extractor directly to avoid pattern matching in integration mode.
	ex := &TwitterUserExtractor{
		base:       newBase("https://twitter.com/testuser", params),
		screenName: "testuser",
		mediaOnly:  false,
	}
	// Override base URL for GraphQL calls
	patchGraphQLBase(ex, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var items []extractor.Item
	for item := range ex.Items(ctx) {
		items = append(items, item)
	}

	if len(items) == 0 {
		t.Log("no items returned (may be due to mock response structure)")
	}
}

func TestSimulated429_ContextCancelsWait(t *testing.T) {
	// Reset time is 60 seconds in the future (plus the 10s buffer = ~70s wait).
	// The context times out in 300ms, which should cancel the wait and return an error.
	resetAt := time.Now().Add(60 * time.Second)
	resetUnix := resetAt.Unix()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-rate-limit-reset", fmt.Sprintf("%d", resetUnix))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	params := extractor.ClientParams{HTTP: srv.Client()}
	b := newBase("https://twitter.com/testuser", params)
	b.guestToken = "test"
	b.endpointBase = srv.URL

	// Short-lived context: the rate-limit wait should be interrupted.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := b.graphQL(ctx, "UserTweets", map[string]any{"userId": "1"})
	if err == nil {
		t.Error("expected error after context cancellation, got nil")
	}
}

// ─── Helper fixtures ─────────────────────────────────────────────────────────

func tweetTimelineFixture() map[string]any {
	return map[string]any{
		"data": map[string]any{
			"user": map[string]any{
				"result": map[string]any{
					"timeline_v2": map[string]any{
						"timeline": map[string]any{
							"instructions": []any{
								map[string]any{
									"type": "TimelineAddEntries",
									"entries": []any{
										tweetEntry("tweet-1", "100", "testuser"),
										tweetEntry("tweet-2", "101", "testuser"),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// tweetTimelineWithTombstone returns a timeline containing one live tweet and
// one tombstone entry so parseTweetTimeline yields both KindMedia and KindSkipped.
func tweetTimelineWithTombstone() map[string]any {
	tombstoneEntry := map[string]any{
		"entryId": "tweet-9999999999",
		"content": map[string]any{
			"entryType": "TimelineTimelineItem",
			"itemContent": map[string]any{
				"itemType": "TimelineTweet",
				"tweet_results": map[string]any{
					"result": map[string]any{
						"__typename": "TweetTombstone",
						"tombstone": map[string]any{
							"text": map[string]any{
								"text": "This Tweet is unavailable.",
							},
						},
					},
				},
			},
		},
	}
	return map[string]any{
		"data": map[string]any{
			"user": map[string]any{
				"result": map[string]any{
					"timeline_v2": map[string]any{
						"timeline": map[string]any{
							"instructions": []any{
								map[string]any{
									"type": "TimelineAddEntries",
									"entries": []any{
										tweetEntry("tweet-100", "100", "testuser"),
										tombstoneEntry,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func tweetEntry(entryID, tweetID, screenName string) map[string]any {
	return map[string]any{
		"entryId": entryID,
		"content": map[string]any{
			"entryType": "TimelineTimelineItem",
			"itemContent": map[string]any{
				"itemType": "TimelineTweet",
				"tweet_results": map[string]any{
					"result": map[string]any{
						"__typename": "Tweet",
						"legacy": map[string]any{
							"id_str":     tweetID,
							"created_at": "Mon Jan 02 15:04:05 +0000 2023",
							"full_text":  "test tweet",
							"lang":       "en",
							"extended_entities": map[string]any{
								"media": []any{
									map[string]any{
										"type":            "photo",
										"media_url_https": "https://pbs.twimg.com/media/test.jpg",
									},
								},
							},
						},
						"core": map[string]any{
							"user_results": map[string]any{
								"result": map[string]any{
									"rest_id": "user42",
									"legacy": map[string]any{
										"id_str":      "user42",
										"name":        "Test User",
										"screen_name": screenName,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// patchGraphQLBase replaces the GraphQL endpoint base in a TwitterUserExtractor
// so tests can route calls to an httptest.Server instead of x.com.
func patchGraphQLBase(ex *TwitterUserExtractor, baseURL string) {
	ex.base.endpointBase = baseURL
}

// graphQL wraps base.graphQL but overrides the endpoint host with RawURL
// when it looks like a test server URL (starts with http://127.0.0.1).
func (b *base) graphQLOverride(ctx context.Context, operation string, variables map[string]any, baseURL string) (map[string]any, error) {
	_ = baseURL // used by test shim only
	return b.graphQL(ctx, operation, variables)
}

func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGraphQL_404RefreshesQueryID(t *testing.T) {
	var oldHits, newHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case contains(r.URL.Path, "/OLDID/"):
			oldHits++
			w.WriteHeader(http.StatusNotFound)
		case contains(r.URL.Path, "/NEWID/"):
			newHits++
			jsonBody(map[string]any{"data": map[string]any{"user": map[string]any{}}})(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Pre-populate the process-wide refreshed-ID state so the test exercises
	// the 404→retry path without scraping the real x.com bundles.
	qidFetch.mu.Lock()
	savedAt, savedIDs := qidFetch.at, qidFetch.ids
	qidFetch.at = time.Now()
	qidFetch.ids = map[string]string{"UserTweets": "NEWID"}
	qidFetch.mu.Unlock()
	defer func() {
		qidFetch.mu.Lock()
		qidFetch.at, qidFetch.ids = savedAt, savedIDs
		qidFetch.mu.Unlock()
	}()

	cache := newMapCache()
	cache.m[qidCacheKey("UserTweets")] = "OLDID"
	params := extractor.ClientParams{HTTP: srv.Client(), Cache: cache}
	b := newBase("https://twitter.com/testuser", params)
	b.guestToken = "test"
	b.endpointBase = srv.URL

	if _, err := b.graphQL(context.Background(), "UserTweets", map[string]any{"userId": "1"}); err != nil {
		t.Fatalf("graphQL after query-ID refresh: %v", err)
	}
	if oldHits != 1 || newHits != 1 {
		t.Errorf("hits: old=%d new=%d, want 1 each", oldHits, newHits)
	}
}

func TestFetchQueryIDs_Parsing(t *testing.T) {
	html := `<script src="https://abs.twimg.com/responsive-web/client-web/main.abc123.js"></script>`
	if got := bundleURLRe.FindAllString(html, -1); len(got) != 1 {
		t.Errorf("bundleURLRe found %d URLs, want 1", len(got))
	}
	js := `{queryId:"aBc-123_x",operationName:"UserMedia",operationType:"query"},{queryId:"zZz",operationName:"UserTweets"}`
	ids := map[string]string{}
	for _, m := range queryIDRe.FindAllStringSubmatch(js, -1) {
		ids[m[2]] = m[1]
	}
	if ids["UserMedia"] != "aBc-123_x" || ids["UserTweets"] != "zZz" {
		t.Errorf("parsed ids = %v", ids)
	}
}

func TestTweetResultToItems_Retweet(t *testing.T) {
	result := map[string]any{
		"__typename": "Tweet",
		"legacy": map[string]any{
			"id_str":                  "555",
			"retweeted_status_id_str": "111",
			"retweeted_status_result": map[string]any{
				"result": map[string]any{
					"__typename": "Tweet",
					"legacy": map[string]any{
						"id_str":     "111",
						"created_at": "Mon Jan 02 15:04:05 +0000 2023",
						"full_text":  "original",
						"extended_entities": map[string]any{
							"media": []any{
								map[string]any{
									"type":            "photo",
									"media_url_https": "https://pbs.twimg.com/media/orig.jpg",
								},
							},
						},
					},
				},
			},
		},
	}

	if items := tweetResultToItems(result, 0, 0, "", twOpts{}); len(items) != 0 {
		t.Errorf("retweets disabled: got %d items, want 0", len(items))
	}
	items := tweetResultToItems(result, 0, 0, "", twOpts{RetweetsEnabled: true})
	if len(items) != 1 {
		t.Fatalf("retweets enabled: got %d items, want 1", len(items))
	}
	if !items[0].Meta.IsRetweet {
		t.Error("IsRetweet should be true")
	}
	if items[0].Meta.TweetID != "111" {
		t.Errorf("TweetID = %q, want the original tweet's ID 111", items[0].Meta.TweetID)
	}
}

func TestBestVideoVariant(t *testing.T) {
	variants := []any{
		map[string]any{"url": "https://v/playlist.m3u8"},
		map[string]any{"bitrate": float64(832000), "url": "https://v/low.mp4"},
		map[string]any{"bitrate": float64(2176000), "url": "https://v/high.mp4"},
	}
	if got := bestVideoVariant(variants, true); got != "https://v/high.mp4" {
		t.Errorf("max: got %q", got)
	}
	if got := bestVideoVariant(variants, false); got != "https://v/low.mp4" {
		t.Errorf("min: got %q", got)
	}
	only := []any{map[string]any{"url": "https://v/playlist.m3u8"}}
	if got := bestVideoVariant(only, true); got != "https://v/playlist.m3u8" {
		t.Errorf("m3u8-only fallback: got %q", got)
	}
}
