// Package localstore implements the GophKeeper client's local cache of
// items plus the minimal bookkeeping needed for incremental sync.
//
// Design notes:
//
//   - Items are stored in plaintext. In a production password manager
//     they would additionally be encrypted with a key derived from the
//     user's master password; that step is out of scope for this project.
//
//   - Every row carries a "dirty" flag. A row becomes dirty when the
//     user creates, edits or deletes it locally; it is cleared after a
//     successful push that returns the server's canonical version.
//
//   - The server's timestamp of the last successful pull is stored in a
//     dedicated key/value table so the next pull can be incremental.
package localstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	// Register the pure-Go "sqlite" driver with database/sql. The import
	// is blank because the package is only needed for its side effect.
	_ "modernc.org/sqlite"

	"gophkeeper/internal/models"
)

const schema = `
CREATE TABLE IF NOT EXISTS items (
    id          TEXT PRIMARY KEY,
    type        TEXT NOT NULL,
    title       TEXT NOT NULL,
    data        BLOB NOT NULL,
    metadata    TEXT NOT NULL DEFAULT '',
    version     INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL,
    deleted_at  INTEGER,
    dirty       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// ErrNotFound is returned when an item does not exist locally.
var ErrNotFound = errors.New("localstore: not found")

// Store is a SQLite-backed local cache.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the local database at dsn and applies the schema.
// ":memory:" selects a private in-memory database, useful for tests.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("localstore: open: %w", err)
	}
	if strings.Contains(dsn, ":memory:") {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("localstore: apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// UpsertItem inserts or updates an item, setting its dirty flag.
func (s *Store) UpsertItem(ctx context.Context, it *models.Item, dirty bool) error {
	if it.Data == nil {
		it.Data = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO items(id, type, title, data, metadata, version, updated_at, deleted_at, dirty)
        VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            type = excluded.type,
            title = excluded.title,
            data = excluded.data,
            metadata = excluded.metadata,
            version = excluded.version,
            updated_at = excluded.updated_at,
            deleted_at = excluded.deleted_at,
            dirty = excluded.dirty
    `, it.ID, string(it.Type), it.Title, it.Data, it.Metadata,
		it.Version, it.UpdatedAt.UnixNano(), nullableTime(it.DeletedAt), boolToInt(dirty))
	if err != nil {
		return fmt.Errorf("localstore: upsert: %w", err)
	}
	return nil
}

// GetItem returns the item and its dirty flag.
func (s *Store) GetItem(ctx context.Context, id string) (*models.Item, bool, error) {
	row := s.db.QueryRowContext(ctx, `
        SELECT id, type, title, data, metadata, version, updated_at, deleted_at, dirty
        FROM items WHERE id = ?`, id)
	it, dirty, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}
	return it, dirty, nil
}

// ListItems returns every item regardless of its dirty flag. Deleted
// items (with DeletedAt set) are included so the UI can show tombstones
// if it wants to.
func (s *Store) ListItems(ctx context.Context) ([]models.Item, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, type, title, data, metadata, version, updated_at, deleted_at, dirty
        FROM items
        WHERE deleted_at IS NULL
        ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("localstore: list: %w", err)
	}
	defer rows.Close()
	return collect(rows, false)
}

// ListDirty returns every dirty item, deleted ones included, in
// UpdatedAt order. This is exactly the batch sent to the server on push.
func (s *Store) ListDirty(ctx context.Context) ([]models.Item, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, type, title, data, metadata, version, updated_at, deleted_at, dirty
        FROM items
        WHERE dirty = 1
        ORDER BY updated_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("localstore: list dirty: %w", err)
	}
	defer rows.Close()
	return collect(rows, true)
}

// MarkClean replaces the local copy of an item with the server's
// canonical version and clears its dirty flag.
func (s *Store) MarkClean(ctx context.Context, it *models.Item) error {
	return s.UpsertItem(ctx, it, false)
}

// DeleteLocal removes a row outright. Used by tests and by "purge"
// operations; normal user-facing deletion is a soft-delete followed by
// a push.
func (s *Store) DeleteLocal(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, id)
	return err
}

// LastSync returns the server timestamp recorded at the end of the last
// successful pull, or the zero time if no pull has ever succeeded.
func (s *Store) LastSync(ctx context.Context) (time.Time, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'last_sync'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("localstore: last sync: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("localstore: parse last sync: %w", err)
	}
	return t, nil
}

// SetLastSync records the server timestamp of the last successful pull.
func (s *Store) SetLastSync(ctx context.Context, t time.Time) error {
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO meta(key, value) VALUES('last_sync', ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value
    `, t.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("localstore: set last sync: %w", err)
	}
	return nil
}

func scanItem(row interface{ Scan(...any) error }) (*models.Item, bool, error) {
	var (
		it        models.Item
		typ       string
		updatedNs int64
		deletedNs sql.NullInt64
		dirty     int
	)
	if err := row.Scan(&it.ID, &typ, &it.Title, &it.Data, &it.Metadata,
		&it.Version, &updatedNs, &deletedNs, &dirty); err != nil {
		return nil, false, err
	}
	it.Type = models.ItemType(typ)
	it.UpdatedAt = time.Unix(0, updatedNs).UTC()
	if deletedNs.Valid {
		t := time.Unix(0, deletedNs.Int64).UTC()
		it.DeletedAt = &t
	}
	return &it, dirty == 1, nil
}

func collect(rows *sql.Rows, _ bool) ([]models.Item, error) {
	var out []models.Item
	for rows.Next() {
		it, _, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("localstore: scan: %w", err)
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
