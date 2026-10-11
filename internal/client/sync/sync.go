// Package sync implements the GophKeeper client's synchronisation engine.
//
// A single Sync call performs the full exchange with the server:
//
//  1. Push every locally-dirty item and apply the server's canonical
//     version locally (clearing the dirty flag).
//
//  2. Pull every item the server has changed since the last successful
//     pull, and merge those changes into the local cache using the same
//     last-write-wins rule the server uses.
//
//  3. Advance the local "last sync" marker to the server timestamp
//     reported by pull, so the next call transfers only the delta.
//
// Conflict resolution is intentionally asymmetric:
//
//   - Push: the server is authoritative. Whatever it returns becomes
//     the new local copy.
//
//   - Pull: for each arriving item, if the local copy is clean, the
//     server wins unconditionally. If the local copy is dirty (i.e. it
//     has unsynchronised local edits), the newer UpdatedAt wins. A tie
//     goes to the server to keep the algorithm deterministic.
package sync

import (
	"context"
	"fmt"
	"time"

	"gophkeeper/internal/client/api"
	"gophkeeper/internal/client/localstore"
	"gophkeeper/internal/models"
	"gophkeeper/internal/protocol"
)

// Store is the subset of localstore.Store used by the sync engine.
// Declaring it as an interface keeps the engine testable in isolation.
type Store interface {
	UpsertItem(ctx context.Context, it *models.Item, dirty bool) error
	GetItem(ctx context.Context, id string) (*models.Item, bool, error)
	ListDirty(ctx context.Context) ([]models.Item, error)
	MarkClean(ctx context.Context, it *models.Item) error
	LastSync(ctx context.Context) (time.Time, error)
	SetLastSync(ctx context.Context, t time.Time) error
}

// Server is the subset of api.Client used by the sync engine.
type Server interface {
	Push(ctx context.Context, token string, items []protocol.ItemDTO) ([]protocol.ItemDTO, error)
	Pull(ctx context.Context, token string, since time.Time) ([]protocol.ItemDTO, time.Time, error)
}

// Syncer performs a full push/pull exchange against a single server.
type Syncer struct {
	local  Store
	remote Server
	token  string
}

// New returns a Syncer bound to the given local store, remote server and
// authentication token.
func New(local Store, remote Server, token string) *Syncer {
	return &Syncer{local: local, remote: remote, token: token}
}

// Stats reports what a single Sync call transferred.
type Stats struct {
	// Pushed is the number of items sent to the server.
	Pushed int
	// Pulled is the number of items received from the server.
	Pulled int
	// Conflicts is the number of pulled items that had to be resolved
	// by last-write-wins against a locally-dirty copy.
	Conflicts int
}

// Sync performs one full synchronisation round. See the package
// documentation for the precise semantics.
func (s *Syncer) Sync(ctx context.Context) (Stats, error) {
	var stats Stats

	// --- Push phase --------------------------------------------------
	dirty, err := s.local.ListDirty(ctx)
	if err != nil {
		return stats, fmt.Errorf("sync: list dirty: %w", err)
	}
	if len(dirty) > 0 {
		dtos := make([]protocol.ItemDTO, len(dirty))
		for i := range dirty {
			dtos[i] = toDTO(&dirty[i])
		}
		canonical, err := s.remote.Push(ctx, s.token, dtos)
		if err != nil {
			return stats, fmt.Errorf("sync: push: %w", err)
		}
		if len(canonical) != len(dtos) {
			return stats, fmt.Errorf("sync: push returned %d items, want %d",
				len(canonical), len(dtos))
		}
		for i := range canonical {
			m := fromDTO(canonical[i])
			if err := s.local.MarkClean(ctx, m); err != nil {
				return stats, fmt.Errorf("sync: mark clean: %w", err)
			}
		}
		stats.Pushed = len(dtos)
	}

	// --- Pull phase --------------------------------------------------
	since, err := s.local.LastSync(ctx)
	if err != nil {
		return stats, fmt.Errorf("sync: last sync: %w", err)
	}
	remote, serverNow, err := s.remote.Pull(ctx, s.token, since)
	if err != nil {
		return stats, fmt.Errorf("sync: pull: %w", err)
	}
	stats.Pulled = len(remote)

	for i := range remote {
		conflict, err := s.merge(ctx, &remote[i])
		if err != nil {
			return stats, err
		}
		if conflict {
			stats.Conflicts++
		}
	}

	if err := s.local.SetLastSync(ctx, serverNow); err != nil {
		return stats, fmt.Errorf("sync: set last sync: %w", err)
	}
	return stats, nil
}

// merge applies a single server-supplied item to the local store and
// reports whether a conflict had to be resolved.
func (s *Syncer) merge(ctx context.Context, dto *protocol.ItemDTO) (bool, error) {
	local, dirty, err := s.local.GetItem(ctx, dto.ID)
	if err != nil && err != localstore.ErrNotFound {
		return false, fmt.Errorf("sync: get local: %w", err)
	}

	incoming := fromDTO(*dto)

	// No local copy: accept unconditionally.
	if err == localstore.ErrNotFound {
		if err := s.local.UpsertItem(ctx, incoming, false); err != nil {
			return false, fmt.Errorf("sync: insert pulled: %w", err)
		}
		return false, nil
	}

	// Local copy is clean: server always wins.
	if !dirty {
		if err := s.local.UpsertItem(ctx, incoming, false); err != nil {
			return false, fmt.Errorf("sync: overwrite clean: %w", err)
		}
		return false, nil
	}

	// Both sides changed: last-write-wins. Ties go to the server to keep
	// the algorithm deterministic.
	if !incoming.UpdatedAt.After(local.UpdatedAt) {
		// Local is newer — keep it dirty so it is pushed next round.
		return true, nil
	}
	if err := s.local.UpsertItem(ctx, incoming, false); err != nil {
		return true, fmt.Errorf("sync: overwrite dirty: %w", err)
	}
	return true, nil
}

func toDTO(m *models.Item) protocol.ItemDTO {
	return protocol.ItemDTO{
		ID:        m.ID,
		Type:      string(m.Type),
		Title:     m.Title,
		Data:      m.Data,
		Metadata:  m.Metadata,
		Version:   m.Version,
		UpdatedAt: m.UpdatedAt,
		DeletedAt: m.DeletedAt,
	}
}

func fromDTO(d protocol.ItemDTO) *models.Item {
	return &models.Item{
		ID:        d.ID,
		Type:      models.ItemType(d.Type),
		Title:     d.Title,
		Data:      d.Data,
		Metadata:  d.Metadata,
		Version:   d.Version,
		UpdatedAt: d.UpdatedAt,
		DeletedAt: d.DeletedAt,
	}
}

// compile-time assertion that *api.Client satisfies Server.
var _ Server = (*api.Client)(nil)
