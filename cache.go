package gallery

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Cache stores short-lived session data: guest tokens, GraphQL query IDs,
// user-ID lookups, and similar values that are expensive to re-fetch.
type Cache interface {
	// Get retrieves the value for key. Returns ("", false, nil) when not found.
	Get(ctx context.Context, key string) (string, bool, error)
	// Set stores value under key with the given TTL.
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// Delete removes key from the cache.
	Delete(ctx context.Context, key string) error
	// Close releases resources.
	Close() error
}

// DefaultCachePath returns the platform-appropriate path for the cache
// database, honouring XDG_CACHE_HOME on Linux/macOS and the equivalent on
// Windows.  Falls back to ~/.cache/go-gallery/cache.sqlite3.
func DefaultCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".cache")
	}
	return filepath.Join(dir, "go-gallery", "cache.sqlite3")
}

// SQLiteCache is a persistent cache backed by modernc.org/sqlite.
type SQLiteCache struct {
	db    *sql.DB
	get   *sql.Stmt
	set   *sql.Stmt
	del   *sql.Stmt
	purge *sql.Stmt
}

const cacheSchema = `
CREATE TABLE IF NOT EXISTS cache (
	key       TEXT PRIMARY KEY,
	value     TEXT NOT NULL,
	expires   INTEGER NOT NULL
);
`

// NewSQLiteCache opens (or creates) the cache database at path.
// The caller must call Close when done.
func NewSQLiteCache(path string) (*SQLiteCache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("cache: mkdir %s: %w", filepath.Dir(path), err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("cache: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(cacheSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache: create schema: %w", err)
	}

	get, err := db.Prepare(
		`SELECT value FROM cache WHERE key = ? AND expires > ? LIMIT 1`,
	)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("cache: prepare get: %w", err)
	}
	set, err := db.Prepare(
		`INSERT OR REPLACE INTO cache(key, value, expires) VALUES(?, ?, ?)`,
	)
	if err != nil {
		get.Close()
		db.Close()
		return nil, fmt.Errorf("cache: prepare set: %w", err)
	}
	del, err := db.Prepare(`DELETE FROM cache WHERE key = ?`)
	if err != nil {
		get.Close()
		set.Close()
		db.Close()
		return nil, fmt.Errorf("cache: prepare del: %w", err)
	}
	purge, err := db.Prepare(`DELETE FROM cache WHERE expires <= ?`)
	if err != nil {
		get.Close()
		set.Close()
		del.Close()
		db.Close()
		return nil, fmt.Errorf("cache: prepare purge: %w", err)
	}

	c := &SQLiteCache{db: db, get: get, set: set, del: del, purge: purge}
	// Eagerly evict expired entries.
	_, _ = c.purge.Exec(time.Now().Unix())
	return c, nil
}

func (c *SQLiteCache) Get(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := c.get.QueryRowContext(ctx, key, time.Now().Unix()).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("cache: get %q: %w", key, err)
	}
	return value, true, nil
}

func (c *SQLiteCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	expires := time.Now().Add(ttl).Unix()
	_, err := c.set.ExecContext(ctx, key, value, expires)
	if err != nil {
		return fmt.Errorf("cache: set %q: %w", key, err)
	}
	return nil
}

func (c *SQLiteCache) Delete(ctx context.Context, key string) error {
	_, err := c.del.ExecContext(ctx, key)
	if err != nil {
		return fmt.Errorf("cache: delete %q: %w", key, err)
	}
	return nil
}

func (c *SQLiteCache) Close() error {
	c.get.Close()
	c.set.Close()
	c.del.Close()
	c.purge.Close()
	return c.db.Close()
}
