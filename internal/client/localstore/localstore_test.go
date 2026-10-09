package localstore

import (
	"context"
	"testing"
	"time"

	"gophkeeper/internal/models"
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

func TestUpsertAndGet(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	it := &models.Item{ID: "i1", Type: models.ItemTypeText, Title: "a",
		Data: []byte("x"), UpdatedAt: now}
	if err := s.UpsertItem(ctx, it, true); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, dirty, err := s.GetItem(ctx, "i1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !dirty {
		t.Fatal("expected dirty=true")
	}
	if got.Title != "a" || string(got.Data) != "x" {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestGetMissing(t *testing.T) {
	s := newStore(t)
	if _, _, err := s.GetItem(context.Background(), "nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListDirtyOnlyDirty(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_ = s.UpsertItem(ctx, &models.Item{ID: "d", Type: models.ItemTypeText,
		Title: "d", UpdatedAt: now}, true)
	_ = s.UpsertItem(ctx, &models.Item{ID: "c", Type: models.ItemTypeText,
		Title: "c", UpdatedAt: now}, false)

	dirty, err := s.ListDirty(ctx)
	if err != nil {
		t.Fatalf("ListDirty: %v", err)
	}
	if len(dirty) != 1 || dirty[0].ID != "d" {
		t.Fatalf("unexpected: %+v", dirty)
	}
}

func TestMarkCleanClearsDirty(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_ = s.UpsertItem(ctx, &models.Item{ID: "i1", Type: models.ItemTypeText,
		Title: "t", UpdatedAt: now}, true)
	_ = s.MarkClean(ctx, &models.Item{ID: "i1", Type: models.ItemTypeText,
		Title: "t", UpdatedAt: now, Version: 5})
	_, dirty, _ := s.GetItem(ctx, "i1")
	if dirty {
		t.Fatal("dirty flag should be cleared")
	}
}

func TestLastSyncRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if got, _ := s.LastSync(ctx); !got.IsZero() {
		t.Fatalf("expected zero time, got %v", got)
	}
	now := time.Now().UTC()
	if err := s.SetLastSync(ctx, now); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.LastSync(ctx)
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if !got.Equal(now) {
		t.Fatalf("got %v, want %v", got, now)
	}
}

func TestListItemsHidesDeleted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_ = s.UpsertItem(ctx, &models.Item{ID: "live", Type: models.ItemTypeText,
		Title: "l", UpdatedAt: now}, true)
	deletedAt := now
	_ = s.UpsertItem(ctx, &models.Item{ID: "dead", Type: models.ItemTypeText,
		Title: "d", UpdatedAt: now, DeletedAt: &deletedAt}, true)

	items, _ := s.ListItems(ctx)
	if len(items) != 1 || items[0].ID != "live" {
		t.Fatalf("expected only live, got %+v", items)
	}
}
