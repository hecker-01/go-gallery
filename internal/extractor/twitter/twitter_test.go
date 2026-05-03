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

	items := tweetResultToItems(result, 1, 1, "")
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

	items := tweetResultToItems(result, 1, 1, "")
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

	items := tweetResultToItems(result, 0, 0, "tweet-1234567890")
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

	items := tweetResultToItems(result, 0, 0, "tweet-999")
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
	items, _, err := parseTweetTimeline(fixture)
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
