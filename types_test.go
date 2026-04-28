package gallery

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMessage_SealedTypeSwitch(t *testing.T) {
	msgs := []Message{
		Directory{Path: "/tmp"},
		Media{Info: &MediaInfo{TweetID: "123"}, URL: "https://pbs.twimg.com/foo.jpg"},
		Queue{URL: "https://twitter.com/user"},
	}
	for _, m := range msgs {
		switch m.(type) {
		case Directory, Media, Queue:
			// ok
		default:
			t.Errorf("unexpected Message type: %T", m)
		}
	}
}

func TestMediaInfo_Keywords(t *testing.T) {
	info := &MediaInfo{
		TweetID: "999",
		Author: AuthorInfo{
			ID:         "111",
			Name:       "Test User",
			ScreenName: "testuser",
		},
		Date:          time.Now(),
		Content:       "hello",
		Extension:     "jpg",
		Num:           2,
		Count:         5,
		FavoriteCount: 100,
		RetweetCount:  10,
		Lang:          "en",
		Hashtags:      []string{"go", "test"},
		Mentions:      []string{"someone"},
		Category:      "twitter",
	}
	kw := info.Keywords()

	require := map[string]any{
		"tweet_id":            "999",
		"author.screen_name":  "testuser",
		"author.name":         "Test User",
		"author.id":           "111",
		"extension":           "jpg",
		"num":                 2,
		"favorite_count":      100,
		"category":            "twitter",
	}
	for k, want := range require {
		got, ok := kw[k]
		if !ok {
			t.Errorf("Keywords() missing key %q", k)
			continue
		}
		switch w := want.(type) {
		case string:
			if g, ok := got.(string); !ok || g != w {
				t.Errorf("Keywords()[%q]: got %v (%T), want %v", k, got, got, w)
			}
		case int:
			if g, ok := got.(int); !ok || g != w {
				t.Errorf("Keywords()[%q]: got %v (%T), want %v", k, got, got, w)
			}
		}
	}
}

func TestMediaInfo_MarshalJSON(t *testing.T) {
	info := &MediaInfo{
		TweetID:  "12345",
		Author:   AuthorInfo{ScreenName: "user"},
		Date:     time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC),
		Category: "twitter",
	}
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if m["tweet_id"] != "12345" {
		t.Errorf("tweet_id: got %v", m["tweet_id"])
	}
	if m["category"] != "twitter" {
		t.Errorf("category: got %v", m["category"])
	}
	// Hashtags and mentions should be empty arrays, not null.
	if ht, ok := m["hashtags"].([]any); !ok || ht == nil {
		t.Errorf("hashtags should be empty array, got %v (%T)", m["hashtags"], m["hashtags"])
	}
}

func TestRange_Contains(t *testing.T) {
	tests := []struct {
		raw  string
		n    int
		want bool
	}{
		{"1-5", 1, true},
		{"1-5", 5, true},
		{"1-5", 6, false},
		{"7", 7, true},
		{"7", 8, false},
		{"1-5,7,10-20", 7, true},
		{"1-5,7,10-20", 8, false},
		{"1-5,7,10-20", 15, true},
		{"", 999, true}, // empty range means all
	}
	for _, tt := range tests {
		r, err := ParseRange(tt.raw)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", tt.raw, err)
		}
		got := r.Contains(tt.n)
		if got != tt.want {
			t.Errorf("Range(%q).Contains(%d) = %v, want %v", tt.raw, tt.n, got, tt.want)
		}
	}
}

func TestRange_InvalidParse(t *testing.T) {
	_, err := ParseRange("abc")
	if err == nil {
		t.Error("expected error for non-numeric range, got nil")
	}
}

func TestFilter_AllOf(t *testing.T) {
	alwaysTrue := filterFunc(func(*MediaInfo) bool { return true })
	alwaysFalse := filterFunc(func(*MediaInfo) bool { return false })
	info := &MediaInfo{}

	if !AllOf().Accept(info) {
		t.Error("AllOf() (empty) should accept everything")
	}
	if !AllOf(alwaysTrue, alwaysTrue).Accept(info) {
		t.Error("AllOf(true, true) should accept")
	}
	if AllOf(alwaysTrue, alwaysFalse).Accept(info) {
		t.Error("AllOf(true, false) should reject")
	}
}

func TestFilter_AnyOf(t *testing.T) {
	alwaysTrue := filterFunc(func(*MediaInfo) bool { return true })
	alwaysFalse := filterFunc(func(*MediaInfo) bool { return false })
	info := &MediaInfo{}

	if !AnyOf().Accept(info) {
		t.Error("AnyOf() (empty) should accept everything")
	}
	if !AnyOf(alwaysFalse, alwaysTrue).Accept(info) {
		t.Error("AnyOf(false, true) should accept")
	}
	if AnyOf(alwaysFalse, alwaysFalse).Accept(info) {
		t.Error("AnyOf(false, false) should reject")
	}
}

// filterFunc is a quick inline Filter implementation for tests.
type filterFunc func(*MediaInfo) bool

func (f filterFunc) Accept(info *MediaInfo) bool { return f(info) }
