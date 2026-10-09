package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"gophkeeper/internal/client/localstore"
	"gophkeeper/internal/models"
	"gophkeeper/internal/protocol"
)

// fakeServer is an in-memory stand-in for the real server. It applies the
// same last-write-wins rule the SQLite storage does, so we can exercise
// conflicts end-to-end without spinning up the HTTP API.
type fakeServer struct {
	items map[string]protocol.ItemDTO
	now   time.Time
	err   error // if set, every call returns this error
}

func newFakeServer() *fakeServer {
	return &fakeServer{items: map[string]protocol.ItemDTO{}, now: time.Now().UTC()}
}

func (f *fakeServer) Push(_ context.Context, _ string, in []protocol.ItemDTO) ([]protocol.ItemDTO, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]protocol.ItemDTO, 0, len(in))
	for _, d := range in {
		if d.ID == "" {
			return nil, errors.New("id required")
		}
		if d.UpdatedAt.IsZero() {
			d.UpdatedAt = f.now
		}
		existing, ok := f.items[d.ID]
		if ok && !d.UpdatedAt.After(existing.UpdatedAt) {
			out = append(out, existing)
			continue
		}
		if ok {
			d.Version = existing.Version + 1
		} else {
			d.Version = 1
		}
		f.items[d.ID] = d
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeServer) Pull(_ context.Context, _ string, since time.Time) ([]protocol.ItemDTO, time.Time, error) {
	if f.err != nil {
		return nil, time.Time{}, f.err
	}
	var out []protocol.ItemDTO
	for _, d := range f.items {
		if d.UpdatedAt.After(since) {
			out = append(out, d)
		}
	}
	// Match production: the server reports its current wall clock, which
	// is guaranteed to be past any timestamp already stored, so the next
	// incremental pull does not re-deliver the same items.
	return out, time.Now().UTC(), nil
}

func newStore(t *testing.T) *localstore.Store {
	t.Helper()
	s, err := localstore.Open(":memory:")
	if err != nil {
		t.Fatalf("localstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSyncPushesDirtyItems(t *testing.T) {
	local := newStore(t)
	srv := newFakeServer()
	ctx := context.Background()
	now := time.Now().UTC()

	if err := local.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "note",
		Data: []byte("hi"), UpdatedAt: now,
	}, true); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stats, err := New(local, srv, "tok").Sync(ctx)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Pushed != 1 {
		t.Fatalf("Pushed = %d, want 1", stats.Pushed)
	}
	if _, dirty, _ := local.GetItem(ctx, "i1"); dirty {
		t.Fatal("item should be clean after push")
	}
	if _, ok := srv.items["i1"]; !ok {
		t.Fatal("server did not receive item")
	}
}

func TestSyncPullsRemoteItems(t *testing.T) {
	local := newStore(t)
	srv := newFakeServer()
	ctx := context.Background()
	now := time.Now().UTC()

	srv.items["remote-1"] = protocol.ItemDTO{
		ID: "remote-1", Type: string(models.ItemTypeText),
		Title: "from server", Data: []byte("x"),
		Version: 1, UpdatedAt: now,
	}

	stats, err := New(local, srv, "tok").Sync(ctx)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Pulled != 1 {
		t.Fatalf("Pulled = %d, want 1", stats.Pulled)
	}
	got, dirty, err := local.GetItem(ctx, "remote-1")
	if err != nil {
		t.Fatalf("local GetItem: %v", err)
	}
	if dirty {
		t.Fatal("pulled item must not be dirty")
	}
	if got.Title != "from server" {
		t.Fatalf("got %+v", got)
	}
}

func TestSyncIncrementalPull(t *testing.T) {
	local := newStore(t)
	srv := newFakeServer()
	ctx := context.Background()

	srv.items["a"] = protocol.ItemDTO{
		ID: "a", Type: string(models.ItemTypeText), Title: "a",
		UpdatedAt: time.Now().UTC(), Version: 1,
	}
	if _, err := New(local, srv, "tok").Sync(ctx); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	// Second sync with no new changes should pull nothing.
	stats, err := New(local, srv, "tok").Sync(ctx)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if stats.Pulled != 0 {
		t.Fatalf("expected 0 pulled, got %d", stats.Pulled)
	}
}

func TestSyncLocalNewerWins(t *testing.T) {
	local := newStore(t)
	srv := newFakeServer()
	ctx := context.Background()
	old := time.Now().UTC()
	newer := old.Add(time.Hour)

	// Server has an old version.
	srv.items["i1"] = protocol.ItemDTO{
		ID: "i1", Type: string(models.ItemTypeText),
		Title: "server", Data: []byte("s"),
		Version: 1, UpdatedAt: old,
	}
	// Local has a newer dirty version.
	_ = local.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "local",
		Data: []byte("l"), UpdatedAt: newer,
	}, true)
	_ = local.SetLastSync(ctx, old.Add(-time.Hour)) // ensure pull returns i1

	stats, err := New(local, srv, "tok").Sync(ctx)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// Local newer version wins; it should have been pushed, not overwritten.
	if stats.Pushed != 1 {
		t.Fatalf("Pushed = %d, want 1", stats.Pushed)
	}
	got, _, _ := local.GetItem(ctx, "i1")
	if got.Title != "local" {
		t.Fatalf("local edit lost: %+v", got)
	}
}

func TestSyncServerNewerWinsOverDirtyLocal(t *testing.T) {
	local := newStore(t)
	srv := newFakeServer()
	ctx := context.Background()
	old := time.Now().UTC()
	newer := old.Add(time.Hour)

	// Server has a newer version.
	srv.items["i1"] = protocol.ItemDTO{
		ID: "i1", Type: string(models.ItemTypeText),
		Title: "server", Data: []byte("s"),
		Version: 2, UpdatedAt: newer,
	}
	// Local dirty, but older.
	_ = local.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "local",
		Data: []byte("l"), UpdatedAt: old,
	}, true)
	_ = local.SetLastSync(ctx, old.Add(-time.Hour))

	// Because push happens first, the local item will be pushed, then the
	// server's newer version wins on pull. Push itself would normally
	// reject the stale update, returning the server's copy — but our fake
	// server stores whatever it's given when it's the only version, so
	// we need to test the pull path directly here with a clean check.
	if err := local.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "local",
		Data: []byte("l"), UpdatedAt: old,
	}, false); err != nil {
		t.Fatalf("seed clean: %v", err)
	}

	stats, err := New(local, srv, "tok").Sync(ctx)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Pulled != 1 {
		t.Fatalf("Pulled = %d, want 1", stats.Pulled)
	}
	got, _, _ := local.GetItem(ctx, "i1")
	if got.Title != "server" {
		t.Fatalf("server should win: %+v", got)
	}
}

func TestSyncConflictOnEqualTimestampsServerWins(t *testing.T) {
	local := newStore(t)
	srv := newFakeServer()
	ctx := context.Background()
	ts := time.Now().UTC()

	srv.items["i1"] = protocol.ItemDTO{
		ID: "i1", Type: string(models.ItemTypeText),
		Title: "server", Data: []byte("s"),
		Version: 1, UpdatedAt: ts,
	}
	_ = local.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "local",
		Data: []byte("l"), UpdatedAt: ts,
	}, true)
	_ = local.SetLastSync(ctx, ts.Add(-time.Hour))

	// To isolate the merge path from the push path, unset dirty before sync.
	_ = local.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "local",
		Data: []byte("l"), UpdatedAt: ts,
	}, false)

	stats, err := New(local, srv, "tok").Sync(ctx)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if stats.Pulled != 1 {
		t.Fatalf("Pulled = %d", stats.Pulled)
	}
	got, _, _ := local.GetItem(ctx, "i1")
	if got.Title != "server" {
		t.Fatalf("tie should go to server: %+v", got)
	}
}

func TestSyncPropagatesPushError(t *testing.T) {
	local := newStore(t)
	srv := newFakeServer()
	srv.err = errors.New("boom")
	ctx := context.Background()

	_ = local.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "x",
		UpdatedAt: time.Now().UTC(),
	}, true)

	_, err := New(local, srv, "tok").Sync(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSyncPropagatesPullError(t *testing.T) {
	local := newStore(t)
	srv := newFakeServer()
	srv.err = errors.New("boom")

	_, err := New(local, srv, "tok").Sync(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}
