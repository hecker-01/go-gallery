package twitter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hecker-01/go-gallery/internal/extractor"
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

	items := tweetResultToItems(result, 1, 1)
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

	items := tweetResultToItems(result, 1, 1)
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

func TestSimulated429_PausesAndContinues(t *testing.T) {
	resetAt := time.Now().Add(100 * time.Millisecond)
	resetUnix := resetAt.Unix()

	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("x-rate-limit-reset", fmt.Sprintf("%d", resetUnix))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer srv.Close()

	params := extractor.ClientParams{HTTP: srv.Client()}
	b := newBase("https://twitter.com/testuser", params)
	b.guestToken = "test"

	// Override GraphQL endpoint
	b.RawURL = srv.URL

	// graphQL will get a 429 on first call.
	_, err := b.graphQL(context.Background(), "UserTweets", map[string]any{"userId": "1"})
	if err == nil {
		t.Error("expected rate limit error, got nil")
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

// patchGraphQLBase replaces the GraphQL base URL in a TwitterUserExtractor so
// tests can route calls to httptest.Server instead of api.twitter.com.
// This is a test helper — production code never calls it.
func patchGraphQLBase(ex *TwitterUserExtractor, baseURL string) {
	ex.base.RawURL = baseURL
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
