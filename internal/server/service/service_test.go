package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"gophkeeper/internal/crypto"
	"gophkeeper/internal/models"
	"gophkeeper/internal/protocol"
	"gophkeeper/internal/server/auth"
	"gophkeeper/internal/server/storage"
	"gophkeeper/internal/server/storage/sqlite"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func newService(t *testing.T) *Service {
	t.Helper()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	key, _ := crypto.GenerateKey()
	enc, _ := crypto.NewEncryptor(key)
	return New(store, enc, testSecret, time.Hour)
}

func TestRegisterAndLogin(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	id, err := s.Register(ctx, "alice", "a@x.io", "hunter2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if id == "" {
		t.Fatal("empty user id")
	}

	tok, uid, err := s.Login(ctx, "alice", "hunter2")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if uid != id {
		t.Fatalf("uid = %q, want %q", uid, id)
	}
	sub, err := auth.Verify(tok, testSecret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if sub != id {
		t.Fatalf("sub = %q, want %q", sub, id)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	if _, err := s.Register(ctx, "alice", "a@x.io", "pw"); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := s.Register(ctx, "alice", "a@x.io", "pw")
	if !errors.Is(err, storage.ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	s := newService(t)
	if _, err := s.Register(context.Background(), "", "e", "p"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
	if _, err := s.Register(context.Background(), "u", "e", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestLoginErrors(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, _ = s.Register(ctx, "alice", "a@x.io", "pw")

	if _, _, err := s.Login(ctx, "alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials for wrong pw, got %v", err)
	}
	if _, _, err := s.Login(ctx, "ghost", "pw"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("want ErrInvalidCredentials for unknown user, got %v", err)
	}
}

func TestPushPullRoundTrip(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	uid, _ := s.Register(ctx, "alice", "a@x.io", "pw")

	now := time.Now().UTC()
	in := protocol.ItemDTO{
		ID: "i1", Type: string(models.ItemTypeCredentials),
		Title: "github", Data: []byte("user:pass"),
		Metadata: "https://github.com", UpdatedAt: now,
	}
	out, err := s.Push(ctx, uid, []protocol.ItemDTO{in})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(out) != 1 || out[0].Version != 1 {
		t.Fatalf("unexpected push result: %+v", out)
	}
	if string(out[0].Data) != "user:pass" {
		t.Fatalf("data roundtrip failed: %q", out[0].Data)
	}

	items, _, err := s.Pull(ctx, uid, time.Time{})
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(items) != 1 || items[0].ID != "i1" {
		t.Fatalf("unexpected pull result: %+v", items)
	}
	if string(items[0].Data) != "user:pass" {
		t.Fatalf("decrypted data mismatch: %q", items[0].Data)
	}
}

func TestPushAssignsIDAndTime(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	uid, _ := s.Register(ctx, "alice", "a@x.io", "pw")

	out, err := s.Push(ctx, uid, []protocol.ItemDTO{
		{Type: string(models.ItemTypeText), Title: "note", Data: []byte("hi")},
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if out[0].ID == "" {
		t.Fatal("expected generated ID")
	}
	if out[0].UpdatedAt.IsZero() {
		t.Fatal("expected UpdatedAt to be set")
	}
}

func TestPushRejectsUnknownType(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	uid, _ := s.Register(ctx, "alice", "a@x.io", "pw")

	_, err := s.Push(ctx, uid, []protocol.ItemDTO{
		{ID: "x", Type: "totally-unknown", Title: "t", UpdatedAt: time.Now()},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestPushLWWKeepsServerCopy(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	uid, _ := s.Register(ctx, "alice", "a@x.io", "pw")
	base := time.Now().UTC()

	_, _ = s.Push(ctx, uid, []protocol.ItemDTO{
		{ID: "i1", Type: string(models.ItemTypeText), Title: "first",
			Data: []byte("v1"), UpdatedAt: base},
	})
	out, err := s.Push(ctx, uid, []protocol.ItemDTO{
		{ID: "i1", Type: string(models.ItemTypeText), Title: "stale",
			Data: []byte("old"), UpdatedAt: base.Add(-time.Second)},
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if out[0].Title != "first" || out[0].Version != 1 {
		t.Fatalf("server should keep first version, got %+v", out[0])
	}
}

func TestGetListAndDelete(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	uid, _ := s.Register(ctx, "alice", "a@x.io", "pw")
	now := time.Now().UTC()

	_, _ = s.Push(ctx, uid, []protocol.ItemDTO{
		{ID: "i1", Type: string(models.ItemTypeText), Title: "a",
			Data: []byte("x"), UpdatedAt: now},
		{ID: "i2", Type: string(models.ItemTypeText), Title: "b",
			Data: []byte("y"), UpdatedAt: now},
	})

	got, err := s.GetItem(ctx, uid, "i1")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got.Title != "a" {
		t.Fatalf("got %q", got.Title)
	}

	list, err := s.ListItems(ctx, uid)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}

	if err := s.Delete(ctx, uid, "i1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, _ = s.ListItems(ctx, uid)
	if len(list) != 1 || list[0].ID != "i2" {
		t.Fatalf("after delete want only i2, got %+v", list)
	}
}

func TestDeletePropagatesThroughPull(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	uid, _ := s.Register(ctx, "alice", "a@x.io", "pw")
	before := time.Now().UTC()

	_, _ = s.Push(ctx, uid, []protocol.ItemDTO{
		{ID: "i1", Type: string(models.ItemTypeText), Title: "a",
			Data: []byte("x"), UpdatedAt: before.Add(time.Second)},
	})
	time.Sleep(2 * time.Millisecond)
	if err := s.Delete(ctx, uid, "i1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	items, _, err := s.Pull(ctx, uid, before)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 tombstone, got %d", len(items))
	}
	if items[0].DeletedAt == nil {
		t.Fatal("expected DeletedAt to be set")
	}
}

func TestGetItemNotFound(t *testing.T) {
	s := newService(t)
	uid, _ := s.Register(context.Background(), "alice", "a@x.io", "pw")
	_, err := s.GetItem(context.Background(), uid, "nope")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
