package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	clientsync "gophkeeper/internal/client/sync"
	"gophkeeper/internal/models"
)

// --- fakes ---------------------------------------------------------------

type fakeStore struct {
	items map[string]*models.Item
}

func newFakeStore() *fakeStore {
	return &fakeStore{items: map[string]*models.Item{}}
}

func (f *fakeStore) ListItems(_ context.Context) ([]models.Item, error) {
	var out []models.Item
	for _, it := range f.items {
		if it.DeletedAt == nil {
			out = append(out, *it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) GetItem(_ context.Context, id string) (*models.Item, bool, error) {
	it, ok := f.items[id]
	if !ok {
		return nil, false, errors.New("not found")
	}
	return it, false, nil
}

func (f *fakeStore) UpsertItem(_ context.Context, it *models.Item, _ bool) error {
	cp := *it
	f.items[it.ID] = &cp
	return nil
}

type fakeSyncer struct {
	stats clientsync.Stats
	err   error
}

func (f *fakeSyncer) Sync(_ context.Context) (clientsync.Stats, error) {
	return f.stats, f.err
}

// --- helpers -------------------------------------------------------------

func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func send(m *Model, s string) *Model {
	next, _ := m.Update(key(s))
	return next.(*Model)
}

func seed(t *testing.T, s *fakeStore, id, title string) {
	t.Helper()
	if err := s.UpsertItem(context.Background(), &models.Item{
		ID: id, Type: models.ItemTypeText, Title: title,
		Data: []byte("x"), UpdatedAt: time.Now().UTC(),
	}, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// --- tests ---------------------------------------------------------------

func TestNewLoadsItems(t *testing.T) {
	s := newFakeStore()
	seed(t, s, "i1", "a")
	m, err := New(context.Background(), s, &fakeSyncer{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(m.items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(m.items))
	}
}

func TestViewContainsTitleAndItems(t *testing.T) {
	s := newFakeStore()
	seed(t, s, "abcdefgh-1234-...", "hello")
	m, _ := New(context.Background(), s, &fakeSyncer{})
	v := m.View()
	if !strings.Contains(v, "GophKeeper") {
		t.Fatalf("view missing app title:\n%s", v)
	}
	if !strings.Contains(v, "hello") {
		t.Fatalf("view missing item title:\n%s", v)
	}
}

func TestCursorNavigation(t *testing.T) {
	s := newFakeStore()
	seed(t, s, "i0", "t0")
	seed(t, s, "i1", "t1")
	seed(t, s, "i2", "t2")
	m, _ := New(context.Background(), s, &fakeSyncer{})

	m = send(m, "down")
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	m = send(m, "down")
	m = send(m, "down") // should not overflow
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (clamped)", m.cursor)
	}
	m = send(m, "up")
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
}

func TestSyncKeyShowsStats(t *testing.T) {
	m, _ := New(context.Background(), newFakeStore(), &fakeSyncer{
		stats: clientsync.Stats{Pushed: 2, Pulled: 3, Conflicts: 1},
	})
	m = send(m, "s")
	if !strings.Contains(m.status, "pushed=2") {
		t.Fatalf("status = %q", m.status)
	}
	if m.err != nil {
		t.Fatalf("err = %v", m.err)
	}
}

func TestSyncErrorSurfaced(t *testing.T) {
	m, _ := New(context.Background(), newFakeStore(), &fakeSyncer{
		err: errors.New("boom"),
	})
	m = send(m, "s")
	if m.err == nil {
		t.Fatal("expected error after sync failure")
	}
	if !strings.Contains(m.View(), "boom") {
		t.Fatalf("view should display error:\n%s", m.View())
	}
}

func TestRefreshReloadsItems(t *testing.T) {
	s := newFakeStore()
	m, _ := New(context.Background(), s, &fakeSyncer{})
	seed(t, s, "i1", "later")
	m = send(m, "r")
	if len(m.items) != 1 {
		t.Fatalf("expected 1 item after refresh, got %d", len(m.items))
	}
}

func TestDeleteKeyRemovesFromList(t *testing.T) {
	s := newFakeStore()
	seed(t, s, "i1", "a")
	m, _ := New(context.Background(), s, &fakeSyncer{})
	m = send(m, "d")
	if m.err != nil {
		t.Fatalf("delete: %v", m.err)
	}
	if len(m.items) != 0 {
		t.Fatalf("expected empty list, got %d", len(m.items))
	}
	if !strings.Contains(m.status, "Deleted") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestAddFlow(t *testing.T) {
	s := newFakeStore()
	m, _ := New(context.Background(), s, &fakeSyncer{})

	m = send(m, "a")
	if m.screen != screenAdd {
		t.Fatal("expected add screen")
	}

	for _, ch := range "note" {
		m = send(m, string(ch))
	}
	m = send(m, "enter") // move to data field
	for _, ch := range "hello" {
		m = send(m, string(ch))
	}
	m = send(m, "enter") // commit

	if m.screen != screenList {
		t.Fatalf("expected list screen, got %v", m.screen)
	}
	if len(s.items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(s.items))
	}
	for _, it := range s.items {
		if it.Title != "note" || string(it.Data) != "hello" {
			t.Fatalf("bad item: %+v", it)
		}
	}
}

func TestAddEscapeCancels(t *testing.T) {
	m, _ := New(context.Background(), newFakeStore(), &fakeSyncer{})
	m = send(m, "a")
	m = send(m, "esc")
	if m.screen != screenList {
		t.Fatal("esc should return to list")
	}
}

func TestAddBackspace(t *testing.T) {
	m, _ := New(context.Background(), newFakeStore(), &fakeSyncer{})
	m = send(m, "a")
	m = send(m, "a")
	m = send(m, "b")
	m = send(m, "backspace")
	if m.addTitle != "a" {
		t.Fatalf("title = %q, want \"a\"", m.addTitle)
	}
}

func TestAddRequiresTitle(t *testing.T) {
	s := newFakeStore()
	m, _ := New(context.Background(), s, &fakeSyncer{})
	m = send(m, "a")
	m = send(m, "enter") // move to data without typing title
	m = send(m, "x")
	m = send(m, "enter") // commit
	if m.err == nil {
		t.Fatal("expected error for empty title")
	}
	if m.screen != screenAdd {
		t.Fatal("should stay on add screen after error")
	}
}

func TestQuitKey(t *testing.T) {
	m, _ := New(context.Background(), newFakeStore(), &fakeSyncer{})
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("expected tea.Quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected QuitMsg, got %T", cmd())
	}
}

func TestWindowSizeIsStored(t *testing.T) {
	m, _ := New(context.Background(), newFakeStore(), &fakeSyncer{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mm := next.(*Model)
	if mm.width != 120 || mm.height != 40 {
		t.Fatalf("size = %dx%d", mm.width, mm.height)
	}
}

// fmt is imported for future tests but currently unused directly.
var _ = fmt.Sprintf
