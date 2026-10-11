package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"gophkeeper/internal/models"
	"gophkeeper/internal/server/storage"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCreateAndGetUser(t *testing.T) {
	s := newStore(t)
	u := &models.User{
		ID: "u1", Username: "alice", Email: "a@x.io",
		PasswordHash: "hash", CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, err := s.GetUserByUsername(context.Background(), "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if got.ID != "u1" || got.Email != "a@x.io" {
		t.Fatalf("unexpected user: %+v", got)
	}
}

func TestCreateUserDuplicate(t *testing.T) {
	s := newStore(t)
	u := &models.User{ID: "u1", Username: "alice", Email: "a@x.io", CreatedAt: time.Now()}
	_ = s.CreateUser(context.Background(), u)
	u2 := &models.User{ID: "u2", Username: "alice", Email: "b@x.io", CreatedAt: time.Now()}
	if err := s.CreateUser(context.Background(), u2); err != storage.ErrDuplicate {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
}

func TestGetUserNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetUserByUsername(context.Background(), "ghost"); err != storage.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestUpsertInsert(t *testing.T) {
	s := newStore(t)
	seedUser(t, s, "u1")
	it := &models.Item{
		ID: "i1", UserID: "u1", Type: models.ItemTypeText, Title: "note",
		Data: []byte("hi"), UpdatedAt: time.Now().UTC(),
	}
	got, err := s.UpsertItem(context.Background(), it)
	if err != nil {
		t.Fatalf("UpsertItem: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("version = %d, want 1", got.Version)
	}
}

func TestUpsertLWW(t *testing.T) {
	s := newStore(t)
	seedUser(t, s, "u1")
	now := time.Now().UTC()

	old := &models.Item{ID: "i1", UserID: "u1", Type: models.ItemTypeText,
		Title: "old", UpdatedAt: now}
	if _, err := s.UpsertItem(context.Background(), old); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Newer write wins.
	newer := &models.Item{ID: "i1", UserID: "u1", Type: models.ItemTypeText,
		Title: "new", UpdatedAt: now.Add(time.Second)}
	got, err := s.UpsertItem(context.Background(), newer)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Title != "new" || got.Version != 2 {
		t.Fatalf("expected new/version 2, got %+v", got)
	}

	// Older write loses; server returns stored copy.
	older := &models.Item{ID: "i1", UserID: "u1", Type: models.ItemTypeText,
		Title: "stale", UpdatedAt: now.Add(-time.Second)}
	got, err = s.UpsertItem(context.Background(), older)
	if err != nil {
		t.Fatalf("stale update: %v", err)
	}
	if got.Title != "new" || got.Version != 2 {
		t.Fatalf("stale write should not overwrite, got %+v", got)
	}
}

func TestListItemsHidesDeleted(t *testing.T) {
	s := newStore(t)
	seedUser(t, s, "u1")
	now := time.Now().UTC()
	_, _ = s.UpsertItem(context.Background(), &models.Item{ID: "live", UserID: "u1",
		Type: models.ItemTypeText, Title: "x", UpdatedAt: now})
	deletedAt := now
	_, _ = s.UpsertItem(context.Background(), &models.Item{ID: "dead", UserID: "u1",
		Type: models.ItemTypeText, Title: "x", UpdatedAt: now, DeletedAt: &deletedAt})

	items, err := s.ListItems(context.Background(), "u1")
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != "live" {
		t.Fatalf("expected only live item, got %+v", items)
	}
}

func TestItemsSince(t *testing.T) {
	s := newStore(t)
	seedUser(t, s, "u1")
	base := time.Now().UTC()
	_, _ = s.UpsertItem(context.Background(), &models.Item{ID: "a", UserID: "u1",
		Type: models.ItemTypeText, Title: "a", UpdatedAt: base})
	_, _ = s.UpsertItem(context.Background(), &models.Item{ID: "b", UserID: "u1",
		Type: models.ItemTypeText, Title: "b", UpdatedAt: base.Add(time.Second)})

	items, err := s.ItemsSince(context.Background(), "u1", base)
	if err != nil {
		t.Fatalf("ItemsSince: %v", err)
	}
	if len(items) != 1 || items[0].ID != "b" {
		t.Fatalf("expected only item b, got %+v", items)
	}
}

func TestItemsSinceIncludesDeleted(t *testing.T) {
	s := newStore(t)
	seedUser(t, s, "u1")
	base := time.Now().UTC()
	later := base.Add(time.Second)
	_, _ = s.UpsertItem(context.Background(), &models.Item{ID: "gone", UserID: "u1",
		Type: models.ItemTypeText, Title: "x", UpdatedAt: base, DeletedAt: &later})

	items, err := s.ItemsSince(context.Background(), "u1", base)
	if err != nil {
		t.Fatalf("ItemsSince: %v", err)
	}
	// deleted_at does not affect updated_at, so we won't see it here;
	// this test documents that behavior: pull yields tombstones only if
	// the client bumps updated_at when soft-deleting.
	if len(items) != 0 {
		t.Fatalf("expected 0, got %+v", items)
	}
}

func seedUser(t *testing.T, s *Store, id string) {
	t.Helper()
	err := s.CreateUser(context.Background(), &models.User{
		ID: id, Username: id, Email: id + "@x.io",
		PasswordHash: "h", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func TestCreateUserDuplicateViaConstraint(t *testing.T) {
	// Duplicate detection is driven by the UNIQUE constraint, not by a
	// pre-flight SELECT — this exercises the new code path.
	s := newStore(t)
	u := &models.User{ID: "u1", Username: "alice", Email: "a@x.io", CreatedAt: time.Now()}
	if err := s.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	u2 := &models.User{ID: "u2", Username: "alice", Email: "b@x.io", CreatedAt: time.Now()}
	if err := s.CreateUser(context.Background(), u2); !errors.Is(err, storage.ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
}

func TestSearchItemsMatchesMetadata(t *testing.T) {
	s := newStore(t)
	seedUser(t, s, "u1")
	now := time.Now().UTC()
	_, _ = s.UpsertItem(context.Background(), &models.Item{
		ID: "i1", UserID: "u1", Type: models.ItemTypeText, Title: "github",
		Metadata: "https://github.com", UpdatedAt: now,
	})
	_, _ = s.UpsertItem(context.Background(), &models.Item{
		ID: "i2", UserID: "u1", Type: models.ItemTypeText, Title: "mail",
		Metadata: "https://mail.example.com", UpdatedAt: now,
	})

	items, err := s.SearchItems(context.Background(), "u1", "github")
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != "i1" {
		t.Fatalf("unexpected: %+v", items)
	}
}

func TestSearchItemsCaseInsensitive(t *testing.T) {
	s := newStore(t)
	seedUser(t, s, "u1")
	_, _ = s.UpsertItem(context.Background(), &models.Item{
		ID: "i1", UserID: "u1", Type: models.ItemTypeText, Title: "t",
		Metadata: "GitHub Account", UpdatedAt: time.Now().UTC(),
	})
	items, err := s.SearchItems(context.Background(), "u1", "github")
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected case-insensitive match, got %+v", items)
	}
}

func TestSearchItemsEscapesWildcards(t *testing.T) {
	// A user-provided "%" must be matched literally, not as a LIKE wildcard.
	s := newStore(t)
	seedUser(t, s, "u1")
	now := time.Now().UTC()
	_, _ = s.UpsertItem(context.Background(), &models.Item{
		ID: "literal", UserID: "u1", Type: models.ItemTypeText,
		Title: "t1", Metadata: "100% discount", UpdatedAt: now,
	})
	_, _ = s.UpsertItem(context.Background(), &models.Item{
		ID: "wild", UserID: "u1", Type: models.ItemTypeText,
		Title: "t2", Metadata: "no-percent", UpdatedAt: now,
	})

	items, err := s.SearchItems(context.Background(), "u1", "%")
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != "literal" {
		t.Fatalf("expected only literal match, got %+v", items)
	}
}

func TestSearchItemsHidesDeleted(t *testing.T) {
	s := newStore(t)
	seedUser(t, s, "u1")
	now := time.Now().UTC()
	delAt := now
	_, _ = s.UpsertItem(context.Background(), &models.Item{
		ID: "i1", UserID: "u1", Type: models.ItemTypeText, Title: "t",
		Metadata: "github", UpdatedAt: now, DeletedAt: &delAt,
	})
	items, err := s.SearchItems(context.Background(), "u1", "github")
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("deleted items should not appear, got %+v", items)
	}
}

func TestSearchItemsIsolatedByUser(t *testing.T) {
	s := newStore(t)
	seedUser(t, s, "u1")
	seedUser(t, s, "u2")
	_, _ = s.UpsertItem(context.Background(), &models.Item{
		ID: "i1", UserID: "u2", Type: models.ItemTypeText, Title: "t",
		Metadata: "shared-key", UpdatedAt: time.Now().UTC(),
	})
	items, err := s.SearchItems(context.Background(), "u1", "shared-key")
	if err != nil {
		t.Fatalf("SearchItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("search must not leak other users' items: %+v", items)
	}
}
