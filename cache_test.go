package gallery

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteCache_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite3")
	c, err := NewSQLiteCache(path)
	if err != nil {
		t.Fatalf("NewSQLiteCache: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	// Key not present yet.
	v, ok, err := c.Get(ctx, "guest_token")
	if err != nil {
		t.Fatal(err)
	}
	if ok || v != "" {
		t.Errorf("expected (empty, false), got (%q, %v)", v, ok)
	}

	// Store a value.
	if err := c.Set(ctx, "guest_token", "abc123", time.Hour); err != nil {
		t.Fatal(err)
	}

	v, ok, err = c.Get(ctx, "guest_token")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || v != "abc123" {
		t.Errorf("expected (abc123, true), got (%q, %v)", v, ok)
	}
}

func TestSQLiteCache_Expiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite3")
	c, err := NewSQLiteCache(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx := context.Background()

	// TTL of 1 nanosecond — effectively already expired.
	if err := c.Set(ctx, "short", "val", time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	// Sleep a tiny bit so the Unix second ticks (SQLite stores seconds).
	time.Sleep(1100 * time.Millisecond)

	_, ok, err := c.Get(ctx, "short")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expired key should not be returned")
	}
}

func TestSQLiteCache_Delete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite3")
	c, err := NewSQLiteCache(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx := context.Background()
	_ = c.Set(ctx, "del_key", "val", time.Hour)
	_ = c.Delete(ctx, "del_key")

	_, ok, _ := c.Get(ctx, "del_key")
	if ok {
		t.Error("deleted key should not be found")
	}
}

func TestSQLiteCache_Overwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite3")
	c, err := NewSQLiteCache(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx := context.Background()
	_ = c.Set(ctx, "k", "old", time.Hour)
	_ = c.Set(ctx, "k", "new", time.Hour)

	v, ok, _ := c.Get(ctx, "k")
	if !ok || v != "new" {
		t.Errorf("expected (new, true), got (%q, %v)", v, ok)
	}
}

func TestDefaultCachePath(t *testing.T) {
	p := DefaultCachePath()
	if p == "" {
		t.Error("DefaultCachePath should not return empty string")
	}
}
