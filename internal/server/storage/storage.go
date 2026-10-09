// Package storage declares the persistence contract used by the
// GophKeeper server.
//
// Concrete implementations live in subpackages (currently: sqlite).
// The interface is deliberately free of SQL- or SQLite-specific types,
// so any relational or key/value backend can be plugged in.
package storage

import (
	"context"
	"errors"
	"time"

	"gophkeeper/internal/models"
)

// Sentinel errors returned by implementations of Repository.
var (
	// ErrNotFound indicates that the requested record does not exist.
	ErrNotFound = errors.New("storage: not found")
	// ErrDuplicate indicates a uniqueness violation (e.g. a taken username).
	ErrDuplicate = errors.New("storage: duplicate")
)

// Repository is the persistence layer for GophKeeper.
//
// All methods are safe for concurrent use. Implementations must return
// ErrNotFound (not a driver-specific error) when a lookup misses.
type Repository interface {
	// CreateUser inserts a new user. Returns ErrDuplicate if the username
	// is already taken.
	CreateUser(ctx context.Context, u *models.User) error

	// GetUserByUsername returns the user with the given username, or
	// ErrNotFound.
	GetUserByUsername(ctx context.Context, username string) (*models.User, error)

	// UpsertItem inserts or updates it according to last-write-wins on
	// Item.UpdatedAt. Returns the canonical stored item.
	UpsertItem(ctx context.Context, it *models.Item) (*models.Item, error)

	// GetItem returns the item with the given ID belonging to userID,
	// or ErrNotFound.
	GetItem(ctx context.Context, userID, itemID string) (*models.Item, error)

	// ListItems returns all non-deleted items belonging to userID.
	ListItems(ctx context.Context, userID string) ([]models.Item, error)

	// ItemsSince returns all items (including soft-deleted ones) whose
	// UpdatedAt is strictly after since. Used by the sync pull endpoint.
	ItemsSince(ctx context.Context, userID string, since time.Time) ([]models.Item, error)

	// Close releases any resources held by the repository.
	Close() error
}
