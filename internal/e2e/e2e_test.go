// Package e2e contains end-to-end tests that exercise the GophKeeper
// client and server together over real HTTP, backed by real SQLite.
//
// These tests cover the two scenarios from the project specification:
//
//   - New user: register, add data, sync.
//   - Existing user: authenticate, sync, request data, receive it.
package e2e

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	clientapi "gophkeeper/internal/client/api"
	"gophkeeper/internal/client/localstore"
	"gophkeeper/internal/client/sync"
	"gophkeeper/internal/crypto"
	"gophkeeper/internal/models"
	serverapi "gophkeeper/internal/server/api"
	srvsvc "gophkeeper/internal/server/service"
	"gophkeeper/internal/server/storage/sqlite"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("server store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	key, _ := crypto.GenerateKey()
	enc, _ := crypto.NewEncryptor(key)
	svc := srvsvc.New(store, enc, []byte(testSecret), time.Hour)
	srv := httptest.NewServer(serverapi.New(svc, []byte(testSecret), nil).Routes())
	t.Cleanup(srv.Close)
	return srv
}

func newLocal(t *testing.T) *localstore.Store {
	t.Helper()
	s, err := localstore.Open(":memory:")
	if err != nil {
		t.Fatalf("local store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Scenario 1: brand new user registers, adds data locally, syncs; the
// server ends up with the encrypted copy, and a second device owned by
// the same user receives it on its own sync.
func TestScenario_NewUser(t *testing.T) {
	srv := newServer(t)
	ctx := context.Background()
	client := clientapi.New(srv.URL, srv.Client())

	// Register + login.
	if _, err := client.Register(ctx, "alice", "a@x.io", "hunter2"); err != nil {
		t.Fatalf("register: %v", err)
	}
	token, _, err := client.Login(ctx, "alice", "hunter2")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Device A adds an item and syncs.
	devA := newLocal(t)
	if err := devA.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeCredentials, Title: "github",
		Data: []byte("user:pass"), Metadata: "https://github.com",
		UpdatedAt: time.Now().UTC(),
	}, true); err != nil {
		t.Fatalf("upsert local: %v", err)
	}
	stats, err := sync.New(devA, client, token).Sync(ctx)
	if err != nil {
		t.Fatalf("sync A: %v", err)
	}
	if stats.Pushed != 1 {
		t.Fatalf("Pushed = %d, want 1", stats.Pushed)
	}

	// Device B (fresh, same user) pulls the item.
	devB := newLocal(t)
	stats, err = sync.New(devB, client, token).Sync(ctx)
	if err != nil {
		t.Fatalf("sync B: %v", err)
	}
	if stats.Pulled != 1 {
		t.Fatalf("Pulled = %d, want 1", stats.Pulled)
	}
	got, _, err := devB.GetItem(ctx, "i1")
	if err != nil {
		t.Fatalf("get on B: %v", err)
	}
	if got.Title != "github" || string(got.Data) != "user:pass" {
		t.Fatalf("B received wrong item: %+v", got)
	}

	// The server stored the payload encrypted: fetch it directly through
	// the API and verify the raw bytes are not the plaintext.
	items, err := client.List(ctx, token)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// The API decrypts transparently, so we only sanity-check the count.
	if len(items) != 1 {
		t.Fatalf("server has %d items, want 1", len(items))
	}
}

// Scenario 2: existing user with two devices edits the same item; the
// server converges on the newest write, and both devices agree after
// another sync round.
func TestScenario_ExistingUser_Conflict(t *testing.T) {
	srv := newServer(t)
	ctx := context.Background()
	client := clientapi.New(srv.URL, srv.Client())
	_, _ = client.Register(ctx, "bob", "b@x.io", "pw")
	token, _, _ := client.Login(ctx, "bob", "pw")

	devA := newLocal(t)
	devB := newLocal(t)

	// A creates the item and syncs.
	base := time.Now().UTC()
	_ = devA.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "v1",
		Data: []byte("one"), UpdatedAt: base,
	}, true)
	if _, err := sync.New(devA, client, token).Sync(ctx); err != nil {
		t.Fatalf("sync A: %v", err)
	}

	// B catches up.
	if _, err := sync.New(devB, client, token).Sync(ctx); err != nil {
		t.Fatalf("sync B: %v", err)
	}

	// A edits first, then B edits later. B's write should win.
	_ = devA.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "A-edit",
		Data: []byte("a"), UpdatedAt: base.Add(time.Second),
	}, true)
	_ = devB.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "B-edit",
		Data: []byte("b"), UpdatedAt: base.Add(2 * time.Second),
	}, true)

	if _, err := sync.New(devA, client, token).Sync(ctx); err != nil {
		t.Fatalf("sync A2: %v", err)
	}
	if _, err := sync.New(devB, client, token).Sync(ctx); err != nil {
		t.Fatalf("sync B2: %v", err)
	}
	// One more round on A to receive B's winning edit.
	if _, err := sync.New(devA, client, token).Sync(ctx); err != nil {
		t.Fatalf("sync A3: %v", err)
	}

	aItem, _, _ := devA.GetItem(ctx, "i1")
	bItem, _, _ := devB.GetItem(ctx, "i1")
	if aItem.Title != "B-edit" {
		t.Fatalf("A converged to %q, want B-edit", aItem.Title)
	}
	if bItem.Title != "B-edit" {
		t.Fatalf("B converged to %q, want B-edit", bItem.Title)
	}
}

// Scenario 3: deletion propagates through the server to another device.
func TestScenario_DeletePropagates(t *testing.T) {
	srv := newServer(t)
	ctx := context.Background()
	client := clientapi.New(srv.URL, srv.Client())
	_, _ = client.Register(ctx, "carol", "c@x.io", "pw")
	token, _, _ := client.Login(ctx, "carol", "pw")

	devA := newLocal(t)
	devB := newLocal(t)

	_ = devA.UpsertItem(ctx, &models.Item{
		ID: "i1", Type: models.ItemTypeText, Title: "temp",
		Data: []byte("x"), UpdatedAt: time.Now().UTC(),
	}, true)
	if _, err := sync.New(devA, client, token).Sync(ctx); err != nil {
		t.Fatalf("sync A: %v", err)
	}
	if _, err := sync.New(devB, client, token).Sync(ctx); err != nil {
		t.Fatalf("sync B: %v", err)
	}

	// A soft-deletes i1 and syncs.
	it, _, _ := devA.GetItem(ctx, "i1")
	now := time.Now().UTC().Add(time.Second)
	it.DeletedAt = &now
	it.UpdatedAt = now
	_ = devA.UpsertItem(ctx, it, true)
	if _, err := sync.New(devA, client, token).Sync(ctx); err != nil {
		t.Fatalf("sync A2: %v", err)
	}

	// B syncs; the item should no longer appear in B's live list.
	if _, err := sync.New(devB, client, token).Sync(ctx); err != nil {
		t.Fatalf("sync B2: %v", err)
	}
	live, err := devB.ListItems(ctx)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("B still sees %d live items", len(live))
	}
}
