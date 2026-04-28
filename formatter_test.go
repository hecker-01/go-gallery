package gallery

import (
	"testing"
	"time"
)

func TestFormatter_LiteralOnly(t *testing.T) {
	f, err := NewFormatter("hello/world")
	if err != nil {
		t.Fatal(err)
	}
	got := f.Format(map[string]any{})
	if got != "hello/world" {
		t.Errorf("got %q, want %q", got, "hello/world")
	}
}

func TestFormatter_SimpleVar(t *testing.T) {
	f, err := NewFormatter("{tweet_id}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"tweet_id": "1234567890"}
	got := f.Format(kw)
	if got != "1234567890" {
		t.Errorf("got %q, want %q", got, "1234567890")
	}
}

func TestFormatter_DotNotation(t *testing.T) {
	f, err := NewFormatter("{author.screen_name}")
	if err != nil {
		t.Fatal(err)
	}
	// Direct key with dot
	kw := map[string]any{"author.screen_name": "testuser"}
	got := f.Format(kw)
	if got != "testuser" {
		t.Errorf("got %q, want %q", got, "testuser")
	}
}

func TestFormatter_DefaultPattern(t *testing.T) {
	f, err := NewFormatter("{category}/{author.screen_name}/{tweet_id}_{num}.{extension}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{
		"category":            "twitter",
		"author.screen_name":  "testuser",
		"tweet_id":            "111",
		"num":                 1,
		"extension":           "jpg",
	}
	got := f.Format(kw)
	want := "twitter/testuser/111_1.jpg"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatter_ConversionLower(t *testing.T) {
	f, err := NewFormatter("{author.screen_name!l}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"author.screen_name": "TestUser"}
	got := f.Format(kw)
	if got != "testuser" {
		t.Errorf("got %q, want %q", got, "testuser")
	}
}

func TestFormatter_ConversionUpper(t *testing.T) {
	f, err := NewFormatter("{lang!u}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"lang": "en"}
	got := f.Format(kw)
	if got != "EN" {
		t.Errorf("got %q, want %q", got, "EN")
	}
}

func TestFormatter_ConversionJSON(t *testing.T) {
	f, err := NewFormatter("{num!j}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"num": 42}
	got := f.Format(kw)
	if got != "42" {
		t.Errorf("got %q, want %q", got, "42")
	}
}

func TestFormatter_DateLayout(t *testing.T) {
	f, err := NewFormatter("{date:2006-01-02}")
	if err != nil {
		t.Fatal(err)
	}
	d := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	kw := map[string]any{"date": d}
	got := f.Format(kw)
	if got != "2023-06-15" {
		t.Errorf("got %q, want %q", got, "2023-06-15")
	}
}

func TestFormatter_Replace(t *testing.T) {
	f, err := NewFormatter("{content/bad/good}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"content": "this is bad text"}
	got := f.Format(kw)
	if got != "this is good text" {
		t.Errorf("got %q, want %q", got, "this is good text")
	}
}

func TestFormatter_ReplaceAll(t *testing.T) {
	f, err := NewFormatter("{content// /}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"content": "a b c"}
	got := f.Format(kw)
	if got != "abc" {
		t.Errorf("got %q, want %q", got, "abc")
	}
}

func TestFormatter_Join(t *testing.T) {
	f, err := NewFormatter("{hashtags|,}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"hashtags": []string{"go", "twitter", "api"}}
	got := f.Format(kw)
	if got != "go,twitter,api" {
		t.Errorf("got %q, want %q", got, "go,twitter,api")
	}
}

func TestFormatter_ConditionalTrue(t *testing.T) {
	f, err := NewFormatter("{is_retweet?retweet:original}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"is_retweet": true}
	got := f.Format(kw)
	if got != "retweet" {
		t.Errorf("got %q, want %q", got, "retweet")
	}
}

func TestFormatter_ConditionalFalse(t *testing.T) {
	f, err := NewFormatter("{is_retweet?retweet:original}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"is_retweet": false}
	got := f.Format(kw)
	if got != "original" {
		t.Errorf("got %q, want %q", got, "original")
	}
}

func TestFormatter_MissingKey(t *testing.T) {
	f, err := NewFormatter("{nonexistent}")
	if err != nil {
		t.Fatal(err)
	}
	got := f.Format(map[string]any{})
	// Missing keys produce empty string (the formatter is robust).
	if got != "." {
		// filepath.Clean("") returns "." on all platforms
		t.Errorf("got %q, want \".\"", got)
	}
}

func TestFormatter_UnclosedBrace(t *testing.T) {
	_, err := NewFormatter("{unclosed")
	if err == nil {
		t.Error("expected error for unclosed brace, got nil")
	}
}

func TestFormatter_PathSanitization(t *testing.T) {
	f, err := NewFormatter("{content}")
	if err != nil {
		t.Fatal(err)
	}
	// A value containing a slash should not create extra path levels within
	// the variable expansion itself.
	kw := map[string]any{"content": "hello/world"}
	got := f.Format(kw)
	if got != "hello_world" {
		t.Errorf("got %q, want %q", got, "hello_world")
	}
}

func TestFormatter_IntValue(t *testing.T) {
	f, err := NewFormatter("{favorite_count}")
	if err != nil {
		t.Fatal(err)
	}
	kw := map[string]any{"favorite_count": 1000}
	got := f.Format(kw)
	if got != "1000" {
		t.Errorf("got %q, want %q", got, "1000")
	}
}

func BenchmarkFormatter_Default(b *testing.B) {
	f, _ := NewFormatter("{category}/{author.screen_name}/{tweet_id}_{num}.{extension}")
	kw := map[string]any{
		"category":           "twitter",
		"author.screen_name": "benchuser",
		"tweet_id":           "999000111",
		"num":                3,
		"extension":          "mp4",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = f.Format(kw)
	}
}
