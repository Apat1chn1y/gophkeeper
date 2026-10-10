// Package service implements the business logic of the GophKeeper server.
//
// The service sits between the HTTP layer and the storage layer. It owns
// all domain rules: input validation, password hashing, session issuing,
// conflict resolution on push, and — most importantly — transparent
// encryption of item payloads at rest.
//
// The service never touches SQL directly. It depends only on the
// storage.Repository interface, so any backend can be swapped in.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"gophkeeper/internal/crypto"
	"gophkeeper/internal/models"
	"gophkeeper/internal/protocol"
	"gophkeeper/internal/server/auth"
	"gophkeeper/internal/server/storage"
)

// Sentinel errors returned by the service and mapped to HTTP statuses
// by the api layer.
var (
	// ErrInvalidInput indicates that a caller-supplied value failed
	// validation (empty username, unknown item type, etc.).
	ErrInvalidInput = errors.New("service: invalid input")
	// ErrInvalidCredentials indicates a failed login. It is returned for
	// both an unknown user and a wrong password, deliberately, to avoid
	// leaking which usernames exist.
	ErrInvalidCredentials = errors.New("service: invalid credentials")
)

// Service is the GophKeeper business-logic facade.
//
// A single Service is safe for concurrent use.
type Service struct {
	repo   storage.Repository
	enc    *crypto.Encryptor
	secret []byte
	ttl    time.Duration
}

// New returns a Service backed by repo, using enc for payload encryption
// and secret/ttl for issuing session tokens.
func New(repo storage.Repository, enc *crypto.Encryptor, secret []byte, ttl time.Duration) *Service {
	return &Service{repo: repo, enc: enc, secret: secret, ttl: ttl}
}

// Register creates a new user and returns its ID.
//
// The password is hashed with bcrypt before being handed to storage;
// the plaintext is never persisted.
func (s *Service) Register(ctx context.Context, username, email, password string) (string, error) {
	if username == "" || password == "" {
		return "", fmt.Errorf("%w: username and password are required", ErrInvalidInput)
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return "", err
	}
	u := &models.User{
		ID:           uuid.NewString(),
		Username:     username,
		Email:        email,
		PasswordHash: hash,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.repo.CreateUser(ctx, u); err != nil {
		return "", err
	}
	return u.ID, nil
}

// Login verifies the given credentials and returns a signed session
// token together with the caller's user ID.
func (s *Service) Login(ctx context.Context, username, password string) (token, userID string, err error) {
	u, err := s.repo.GetUserByUsername(ctx, username)
	if err != nil {
		// Do not distinguish "unknown user" from "wrong password".
		return "", "", ErrInvalidCredentials
	}
	if !crypto.VerifyPassword(u.PasswordHash, password) {
		return "", "", ErrInvalidCredentials
	}
	tok, err := auth.Sign(u.ID, s.secret, s.ttl)
	if err != nil {
		return "", "", err
	}
	return tok, u.ID, nil
}

// Push stores the given items on behalf of userID and returns the
// server's canonical view of each one.
//
// The server treats client-supplied UpdatedAt as authoritative and applies
// a last-write-wins rule: if the stored copy is at least as fresh, it is
// returned unchanged and the client is expected to reconcile locally.
//
// If an incoming item has an empty ID, a new one is generated.
func (s *Service) Push(ctx context.Context, userID string, items []protocol.ItemDTO) ([]protocol.ItemDTO, error) {
	out := make([]protocol.ItemDTO, 0, len(items))
	for _, dto := range items {
		if !models.ItemType(dto.Type).Valid() {
			return nil, fmt.Errorf("%w: unknown item type %q", ErrInvalidInput, dto.Type)
		}
		if dto.ID == "" {
			dto.ID = uuid.NewString()
		}
		if dto.UpdatedAt.IsZero() {
			dto.UpdatedAt = time.Now().UTC()
		}

		blob, err := s.seal(dto.Data)
		if err != nil {
			return nil, err
		}
		m := &models.Item{
			ID:        dto.ID,
			UserID:    userID,
			Type:      models.ItemType(dto.Type),
			Title:     dto.Title,
			Data:      blob,
			Metadata:  dto.Metadata,
			UpdatedAt: dto.UpdatedAt,
			DeletedAt: dto.DeletedAt,
		}
		saved, err := s.repo.UpsertItem(ctx, m)
		if err != nil {
			return nil, err
		}
		canonical, err := s.toDTO(saved)
		if err != nil {
			return nil, err
		}
		out = append(out, canonical)
	}
	return out, nil
}

// Pull returns every item (including tombstones) owned by userID whose
// UpdatedAt is strictly after since, together with the server's current
// timestamp which the client should pass as the next `since`.
func (s *Service) Pull(ctx context.Context, userID string, since time.Time) ([]protocol.ItemDTO, time.Time, error) {
	items, err := s.repo.ItemsSince(ctx, userID, since)
	if err != nil {
		return nil, time.Time{}, err
	}
	out := make([]protocol.ItemDTO, 0, len(items))
	for i := range items {
		dto, err := s.toDTO(&items[i])
		if err != nil {
			return nil, time.Time{}, err
		}
		out = append(out, dto)
	}
	return out, time.Now().UTC(), nil
}

// ListItems returns every live (non-deleted) item owned by userID.
func (s *Service) ListItems(ctx context.Context, userID string) ([]protocol.ItemDTO, error) {
	items, err := s.repo.ListItems(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.ItemDTO, 0, len(items))
	for i := range items {
		dto, err := s.toDTO(&items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, dto)
	}
	return out, nil
}

// SearchItems returns non-deleted items of userID whose metadata
// contains the given substring.
func (s *Service) SearchItems(ctx context.Context, userID, query string) ([]protocol.ItemDTO, error) {
	if query == "" {
		return s.ListItems(ctx, userID)
	}
	items, err := s.repo.SearchItems(ctx, userID, query)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.ItemDTO, 0, len(items))
	for i := range items {
		dto, err := s.toDTO(&items[i])
		if err != nil {
			return nil, err
		}
		out = append(out, dto)
	}
	return out, nil
}

// GetItem returns a single item owned by userID.
func (s *Service) GetItem(ctx context.Context, userID, itemID string) (*protocol.ItemDTO, error) {
	m, err := s.repo.GetItem(ctx, userID, itemID)
	if err != nil {
		return nil, err
	}
	dto, err := s.toDTO(m)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// Delete soft-deletes the given item. The tombstone is retained so that
// the deletion propagates to other devices on the next pull.
//
// UpdatedAt is bumped to the current server time, but never backwards:
// if the stored item already carries a newer UpdatedAt (e.g. a client
// with a fast clock pushed it), the tombstone's UpdatedAt is set to one
// nanosecond past it. This guarantees the tombstone wins last-write-wins
// and is therefore visible to ItemsSince.
func (s *Service) Delete(ctx context.Context, userID, itemID string) error {
	m, err := s.repo.GetItem(ctx, userID, itemID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !now.After(m.UpdatedAt) {
		now = m.UpdatedAt.Add(time.Nanosecond)
	}
	m.DeletedAt = &now
	m.UpdatedAt = now
	_, err = s.repo.UpsertItem(ctx, m)
	return err
}

// seal encrypts plaintext and returns nonce || ciphertext.
func (s *Service) seal(plaintext []byte) ([]byte, error) {
	ct, nonce, err := s.enc.Encrypt(plaintext)
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(nonce)+len(ct))
	blob = append(blob, nonce...)
	blob = append(blob, ct...)
	return blob, nil
}

// open reverses seal. It returns an error if blob is shorter than a nonce
// or fails authentication.
func (s *Service) open(blob []byte) ([]byte, error) {
	n := s.enc.NonceSize()
	if len(blob) < n {
		return nil, fmt.Errorf("service: ciphertext too short")
	}
	return s.enc.Decrypt(blob[n:], blob[:n])
}

// toDTO converts a stored item into its wire form, decrypting the payload.
func (s *Service) toDTO(m *models.Item) (protocol.ItemDTO, error) {
	plain, err := s.open(m.Data)
	if err != nil {
		return protocol.ItemDTO{}, err
	}
	return protocol.ItemDTO{
		ID:        m.ID,
		Type:      string(m.Type),
		Title:     m.Title,
		Data:      plain,
		Metadata:  m.Metadata,
		Version:   m.Version,
		UpdatedAt: m.UpdatedAt,
		DeletedAt: m.DeletedAt,
	}, nil
}
