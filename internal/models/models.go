// Package models defines the core domain entities shared between the
// GophKeeper client and the server.
package models

import "time"

// ItemType enumerates the kinds of secrets GophKeeper can store.
type ItemType string

const (
	// ItemTypeCredentials is a login/password pair, optionally with a URL.
	ItemTypeCredentials ItemType = "credentials"
	// ItemTypeText is a free-form text note.
	ItemTypeText ItemType = "text"
	// ItemTypeBinary is an arbitrary binary blob (a file).
	ItemTypeBinary ItemType = "binary"
	// ItemTypeCard is a bank card record.
	ItemTypeCard ItemType = "card"
)

// AllItemTypes lists every supported item type. Useful for validation
// and for iterating in tests.
var AllItemTypes = []ItemType{
	ItemTypeCredentials,
	ItemTypeText,
	ItemTypeBinary,
	ItemTypeCard,
}

// Valid reports whether t is one of the supported item types.
func (t ItemType) Valid() bool {
	for _, v := range AllItemTypes {
		if v == t {
			return true
		}
	}
	return false
}

// User is a registered GophKeeper account.
//
// PasswordHash is never serialized: it is an internal server-side
// implementation detail.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Item is a single secret owned by a user.
//
// Data holds the encrypted payload, produced by internal/crypto. The
// server never inspects it. Metadata is a free-form string the client
// may use for tags, URLs, notes, etc.
//
// Version is a monotonically increasing per-item counter used to detect
// concurrent updates during synchronisation.
//
// DeletedAt implements soft deletion: a nil value means the item is live,
// a non-nil value marks the time at which the item was removed. Soft
// deletion makes it possible to propagate deletions to other clients.
type Item struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Type      ItemType   `json:"type"`
	Title     string     `json:"title"`
	Data      []byte     `json:"data"`
	Metadata  string     `json:"metadata,omitempty"`
	Version   int64      `json:"version"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// IsDeleted reports whether the item has been soft-deleted.
func (i Item) IsDeleted() bool { return i.DeletedAt != nil }
