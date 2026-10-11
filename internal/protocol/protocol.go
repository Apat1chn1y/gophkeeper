// Package protocol defines the JSON wire format exchanged between the
// GophKeeper client and server over HTTP.
//
// The types here are intentionally decoupled from internal/models so that
// the on-the-wire shape can evolve without changing the domain.
package protocol

import "time"

// RegisterRequest is the body of POST /api/v1/auth/register.
type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RegisterResponse is returned on successful registration.
type RegisterResponse struct {
	UserID string `json:"user_id"`
}

// LoginRequest is the body of POST /api/v1/auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse carries the issued JWT and the caller's user ID.
type LoginResponse struct {
	Token  string `json:"token"`
	UserID string `json:"user_id"`
}

// ItemDTO is the wire representation of a single stored item.
//
// Data carries the encrypted payload as-is; the server treats it as opaque.
type ItemDTO struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Data      []byte     `json:"data"`
	Metadata  string     `json:"metadata,omitempty"`
	Version   int64      `json:"version"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// PushRequest is the body of POST /api/v1/sync/push.
type PushRequest struct {
	Items []ItemDTO `json:"items"`
}

// PushResponse returns the server's canonical version of every pushed
// item, in the same order as the request.
type PushResponse struct {
	Items []ItemDTO `json:"items"`
}

// PullRequest is the body of POST /api/v1/sync/pull. The server returns
// all items whose UpdatedAt is strictly greater than Since.
type PullRequest struct {
	Since time.Time `json:"since"`
}

// PullResponse carries the delta since the requested timestamp.
type PullResponse struct {
	Items []ItemDTO `json:"items"`
	Now   time.Time `json:"now"`
}

// ErrorResponse is returned for any non-2xx HTTP response.
type ErrorResponse struct {
	Error string `json:"error"`
}
