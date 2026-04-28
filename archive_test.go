package gallery

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestMemoryArchive_RoundTrip(t *testing.T) {
	a := NewMemoryArchive()
	ctx := context.Background()

	has, err := a.Has(ctx, "tweet_1")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("expected Has to return false for unseen key")
	}

	if err := a.Put(ctx, "tweet_1"); err != nil {
		t.Fatal(err)
	}

	has, err = a.Has(ctx, "tweet_1")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("expected Has to return true after Put")
	}

	// Different key should still be absent.
	has, _ = a.Has(ctx, "tweet_2")
	if has {
		t.Error("tweet_2 should not be in archive")
	}
}

func TestMemoryArchive_Close(t *testing.T) {
	a := NewMemoryArchive()
	if err := a.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

func TestMemoryArchive_Concurrent(t *testing.T) {
	a := NewMemoryArchive()
	ctx := context.Background()
	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			key := filepath.Join("tweet", string(rune('a'+i%26)))
			_ = a.Put(ctx, key)
			_, _ = a.Has(ctx, key)
		}(i)
	}
	wg.Wait()
}

func TestSQLiteArchive_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.sqlite3")
	a, err := NewSQLiteArchive(path)
	if err != nil {
		t.Fatalf("NewSQLiteArchive: %v", err)
	}
	defer a.Close()

	ctx := context.Background()
	has, err := a.Has(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("expected false for unseen key")
	}

	if err := a.Put(ctx, "key1"); err != nil {
		t.Fatal(err)
	}

	has, err = a.Has(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("expected true after Put")
	}

	// Idempotent Put (INSERT OR IGNORE).
	if err := a.Put(ctx, "key1"); err != nil {
		t.Errorf("second Put should not fail: %v", err)
	}
}

func TestSQLiteArchive_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.sqlite3")

	a, err := NewSQLiteArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Put(ctx, "persistent_key"); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	// Re-open and verify the key survived.
	a2, err := NewSQLiteArchive(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer a2.Close()
	has, err := a2.Has(ctx, "persistent_key")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("key should persist across re-open")
	}
}
