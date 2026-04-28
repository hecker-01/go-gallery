package gallery

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Archive tracks downloaded media by key to prevent duplicate downloads.
// Inject an implementation via WithArchive; defaults to a no-op if unset.
type Archive interface {
	// Has reports whether key has been previously recorded.
	Has(ctx context.Context, key string) (bool, error)
	// Put records key as downloaded.
	Put(ctx context.Context, key string) error
	// Close flushes pending writes and releases resources.
	Close() error
}

// MemoryArchive is an in-process archive backed by a map.
// It is safe for concurrent use and suitable for testing or short-lived runs.
type MemoryArchive struct {
	mu   sync.RWMutex
	seen map[string]struct{}
}

// NewMemoryArchive returns an empty MemoryArchive.
func NewMemoryArchive() *MemoryArchive {
	return &MemoryArchive{seen: make(map[string]struct{})}
}

func (m *MemoryArchive) Has(_ context.Context, key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.seen[key]
	return ok, nil
}

func (m *MemoryArchive) Put(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[key] = struct{}{}
	return nil
}

func (m *MemoryArchive) Close() error { return nil }

// SQLiteArchive is a persistent archive backed by a modernc.org/sqlite database.
// It uses a single table with the archive key as the primary key so Has and Put
// are O(log n) and safe for concurrent access via a single *sql.DB.
type SQLiteArchive struct {
	db   *sql.DB
	has  *sql.Stmt
	put  *sql.Stmt
}

const archiveSchema = `
CREATE TABLE IF NOT EXISTS archive (
	key TEXT PRIMARY KEY,
	ts  INTEGER NOT NULL
);
`

// NewSQLiteArchive opens (or creates) the SQLite database at path and
// prepares statements. The caller must call Close when done.
func NewSQLiteArchive(path string) (*SQLiteArchive, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("archive: open %s: %w", path, err)
	}
	// One writer at a time; multiple readers fine.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(archiveSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("archive: create schema: %w", err)
	}

	has, err := db.Prepare(`SELECT 1 FROM archive WHERE key = ? LIMIT 1`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("archive: prepare has: %w", err)
	}
	put, err := db.Prepare(`INSERT OR IGNORE INTO archive(key, ts) VALUES(?, ?)`)
	if err != nil {
		has.Close()
		db.Close()
		return nil, fmt.Errorf("archive: prepare put: %w", err)
	}

	return &SQLiteArchive{db: db, has: has, put: put}, nil
}

func (a *SQLiteArchive) Has(ctx context.Context, key string) (bool, error) {
	var found int
	err := a.has.QueryRowContext(ctx, key).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("archive: has %q: %w", key, err)
	}
	return true, nil
}

func (a *SQLiteArchive) Put(ctx context.Context, key string) error {
	_, err := a.put.ExecContext(ctx, key, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("archive: put %q: %w", key, err)
	}
	return nil
}

func (a *SQLiteArchive) Close() error {
	a.has.Close()
	a.put.Close()
	return a.db.Close()
}
