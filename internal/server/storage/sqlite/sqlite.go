// Package sqlite implements storage.Repository on top of an embedded
// SQLite database via modernc.org/sqlite (pure Go, no cgo).
package sqlite

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
	"gophkeeper/internal/server/storage"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            TEXT         PRIMARY KEY,
    username      VARCHAR(64)  NOT NULL UNIQUE,
    email         VARCHAR(254) NOT NULL,
    password_hash VARCHAR(72)  NOT NULL,
    created_at    INTEGER      NOT NULL
);

CREATE TABLE IF NOT EXISTS items (
    id         TEXT          PRIMARY KEY,
    user_id    TEXT          NOT NULL,
    type       VARCHAR(16)   NOT NULL,
    title      VARCHAR(255)  NOT NULL,
    data       BLOB          NOT NULL,
    metadata   VARCHAR(1024) NOT NULL DEFAULT '',
    version    INTEGER       NOT NULL DEFAULT 1,
    updated_at INTEGER       NOT NULL,
    deleted_at INTEGER,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS idx_items_user_updated
    ON items(user_id, updated_at);
`

// Store is a SQLite-backed storage.Repository.
type Store struct {
	db *sql.DB
}

// Open opens (and if necessary creates) a SQLite database at dsn, applies
// the schema, and returns a ready-to-use Store.
//
// The special DSN ":memory:" creates a private in-memory database, which
// is primarily useful for tests.
//
// The underlying connection pool is capped at one connection for both
// in-memory and file-backed databases: SQLite's default DEFERRED
// transactions can race on the same row across concurrent connections
// and surface as SQLITE_BUSY, which is not a useful outcome for the
// caller. A single connection serializes all access at the pool level.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close implements storage.Repository.
func (s *Store) Close() error { return s.db.Close() }

// CreateUser implements storage.Repository.
//
// The INSERT is attempted directly; a uniqueness violation is translated
// into storage.ErrDuplicate. There is no pre-flight SELECT — a concurrent
// caller could win the race between the SELECT and the INSERT, and the
// resulting error would not be recognizable as a duplicate. The schema's
// UNIQUE constraint is the single source of truth.
func (s *Store) CreateUser(ctx context.Context, u *models.User) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users(id, username, email, password_hash, created_at)
		 VALUES(?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.Email, u.PasswordHash, u.CreatedAt.UnixNano())
	if err != nil {
		if isUniqueViolation(err) {
			return storage.ErrDuplicate
		}
		return fmt.Errorf("sqlite: insert user: %w", err)
	}
	return nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE-constraint
// violation. It prefers the structured error code exposed by the driver
// and falls back to the driver's message text for robustness.
func isUniqueViolation(err error) bool {
	const sqliteConstraint = 19 // SQLITE_CONSTRAINT
	var coder interface{ Code() int }
	if errors.As(err, &coder) {
		if coder.Code()&0xff == sqliteConstraint {
			return true
		}
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// GetUserByUsername implements storage.Repository.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, username, email, password_hash, created_at
		 FROM users WHERE username = ?`, username)
	var u models.User
	var createdNs int64
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &createdNs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("sqlite: get user: %w", err)
	}
	u.CreatedAt = time.Unix(0, createdNs).UTC()
	return &u, nil
}

// UpsertItem implements storage.Repository.
func (s *Store) UpsertItem(ctx context.Context, it *models.Item) (*models.Item, error) {
	if it.Data == nil {
		it.Data = []byte{}
	}
	if it.Type == "" {
		it.Type = models.ItemTypeText
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		curVersion int64
		curUpdated int64
	)
	err = tx.QueryRowContext(ctx,
		`SELECT version, updated_at FROM items WHERE id = ?`, it.ID).
		Scan(&curVersion, &curUpdated)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		it.Version = 1
		_, err = tx.ExecContext(ctx,
			`INSERT INTO items(id, user_id, type, title, data, metadata,
			                   version, updated_at, deleted_at)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			it.ID, it.UserID, it.Type, it.Title, it.Data, it.Metadata,
			it.Version, it.UpdatedAt.UnixNano(), nullTime(it.DeletedAt))
		if err != nil {
			return nil, fmt.Errorf("sqlite: insert item: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("sqlite: lookup item: %w", err)
	default:
		storedUpdated := time.Unix(0, curUpdated).UTC()
		if !it.UpdatedAt.After(storedUpdated) {
			_ = tx.Rollback()
			return s.GetItem(ctx, it.UserID, it.ID)
		}
		it.Version = curVersion + 1
		_, err = tx.ExecContext(ctx,
			`UPDATE items SET type = ?, title = ?, data = ?, metadata = ?,
			                  version = ?, updated_at = ?, deleted_at = ?
			 WHERE id = ?`,
			it.Type, it.Title, it.Data, it.Metadata,
			it.Version, it.UpdatedAt.UnixNano(), nullTime(it.DeletedAt), it.ID)
		if err != nil {
			return nil, fmt.Errorf("sqlite: update item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: commit: %w", err)
	}
	return it, nil
}

// GetItem implements storage.Repository.
func (s *Store) GetItem(ctx context.Context, userID, itemID string) (*models.Item, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, type, title, data, metadata,
		        version, updated_at, deleted_at
		 FROM items WHERE id = ? AND user_id = ?`, itemID, userID)
	it, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("sqlite: get item: %w", err)
	}
	return it, nil
}

// ListItems implements storage.Repository.
func (s *Store) ListItems(ctx context.Context, userID string) ([]models.Item, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, type, title, data, metadata,
		        version, updated_at, deleted_at
		 FROM items WHERE user_id = ? AND deleted_at IS NULL
		 ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list items: %w", err)
	}
	defer rows.Close()
	return collectItems(rows)
}

// SearchItems implements storage.Repository.
//
// The query is matched as a substring of the metadata column using
// SQLite's LIKE (which is case-insensitive for ASCII). Wildcards in the
// user-supplied string are treated as literals via ESCAPE.
func (s *Store) SearchItems(ctx context.Context, userID, query string) ([]models.Item, error) {
	pattern := "%" + escapeLike(query) + "%"
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, type, title, data, metadata,
		        version, updated_at, deleted_at
		 FROM items
		 WHERE user_id = ? AND deleted_at IS NULL
		   AND metadata LIKE ? ESCAPE '\'
		 ORDER BY updated_at DESC`, userID, pattern)
	if err != nil {
		return nil, fmt.Errorf("sqlite: search items: %w", err)
	}
	defer rows.Close()
	return collectItems(rows)
}

// escapeLike escapes the special characters %, _ and \ in s so that
// they are matched literally by a LIKE ... ESCAPE '\' query.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ItemsSince implements storage.Repository.
func (s *Store) ItemsSince(ctx context.Context, userID string, since time.Time) ([]models.Item, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, type, title, data, metadata,
		        version, updated_at, deleted_at
		 FROM items WHERE user_id = ? AND updated_at > ?
		 ORDER BY updated_at ASC`, userID, since.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("sqlite: items since: %w", err)
	}
	defer rows.Close()
	return collectItems(rows)
}

func scanItem(row interface{ Scan(...any) error }) (*models.Item, error) {
	var (
		it        models.Item
		updatedNs int64
		deletedNs sql.NullInt64
	)
	if err := row.Scan(&it.ID, &it.UserID, &it.Type, &it.Title, &it.Data,
		&it.Metadata, &it.Version, &updatedNs, &deletedNs); err != nil {
		return nil, err
	}
	it.UpdatedAt = time.Unix(0, updatedNs).UTC()
	if deletedNs.Valid {
		t := time.Unix(0, deletedNs.Int64).UTC()
		it.DeletedAt = &t
	}
	return &it, nil
}

func collectItems(rows *sql.Rows) ([]models.Item, error) {
	var out []models.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan item: %w", err)
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}
