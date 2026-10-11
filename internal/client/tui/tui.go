// Package tui implements a terminal user interface for the GophKeeper
// client, built on top of github.com/charmbracelet/bubbletea.
//
// The TUI is a thin presentation layer: it never talks to the network or
// the database directly. It receives a Store and a Syncer, both narrow
// interfaces that the CLI wires to the real implementations. This keeps
// the model testable with lightweight fakes.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	clientsync "gophkeeper/internal/client/sync"
	"gophkeeper/internal/models"
)

type screen int

const (
	screenList screen = iota
	screenAdd
)

// Store is the subset of localstore.Store used by the TUI.
type Store interface {
	ListItems(ctx context.Context) ([]models.Item, error)
	GetItem(ctx context.Context, id string) (*models.Item, bool, error)
	UpsertItem(ctx context.Context, it *models.Item, dirty bool) error
}

// Syncer is the subset of sync.Syncer used by the TUI.
type Syncer interface {
	Sync(ctx context.Context) (clientsync.Stats, error)
}

// Model is the bubbletea model behind the GophKeeper TUI.
//
// A single Model is not safe for concurrent use; bubbletea drives it
// from a single goroutine.
type Model struct {
	ctx    context.Context
	local  Store
	syncer Syncer

	items  []models.Item
	cursor int
	screen screen

	addType  models.ItemType
	addTitle string
	addBody  string
	addField int // 0 = title, 1 = body

	status string
	err    error

	width  int
	height int
}

// New builds a Model bound to the given Store and Syncer and loads the
// current item list from the store.
func New(ctx context.Context, local Store, syncer Syncer) (*Model, error) {
	m := &Model{ctx: ctx, local: local, syncer: syncer}
	if err := m.reload(); err != nil {
		return nil, err
	}
	return m, nil
}

// Run starts the TUI event loop and blocks until the user quits.
func Run(ctx context.Context, local Store, syncer Syncer) error {
	m, err := New(ctx, local, syncer)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

// Init implements tea.Model. There is no asynchronous work to do on
// startup: New has already loaded the item list synchronously.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.screen == screenAdd {
			return m.updateAdd(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "r":
		if err := m.reload(); err != nil {
			m.err = err
		}
	case "s":
		stats, err := m.syncer.Sync(m.ctx)
		if err != nil {
			m.err = err
			m.status = ""
		} else {
			m.err = nil
			m.status = fmt.Sprintf("Synced: pushed=%d pulled=%d conflicts=%d",
				stats.Pushed, stats.Pulled, stats.Conflicts)
			_ = m.reload()
		}
	case "d":
		if err := m.softDeleteCurrent(); err != nil {
			m.err = err
		} else {
			m.err = nil
			m.status = "Deleted (press s to sync)"
			_ = m.reload()
		}
	case "a":
		m.screen = screenAdd
		m.addType = models.ItemTypeText
		m.addTitle = ""
		m.addBody = ""
		m.addField = 0
		m.err = nil
	}
	return m, nil
}

func (m *Model) updateAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenList
		m.err = nil
	case "tab":
		m.addField = (m.addField + 1) % 2
	case "enter":
		if m.addField == 0 {
			m.addField = 1
		} else if err := m.commitAdd(); err != nil {
			m.err = err
		} else {
			m.err = nil
			m.status = "Added (press s to sync)"
			m.screen = screenList
			_ = m.reload()
		}
	case "backspace":
		if m.addField == 0 && len(m.addTitle) > 0 {
			m.addTitle = m.addTitle[:len(m.addTitle)-1]
		} else if m.addField == 1 && len(m.addBody) > 0 {
			m.addBody = m.addBody[:len(m.addBody)-1]
		}
	default:
		if len(msg.Runes) > 0 {
			if m.addField == 0 {
				m.addTitle += string(msg.Runes)
			} else {
				m.addBody += string(msg.Runes)
			}
		}
	}
	return m, nil
}

// reload refreshes the item list from the store.
func (m *Model) reload() error {
	items, err := m.local.ListItems(m.ctx)
	if err != nil {
		return err
	}
	m.items = items
	if m.cursor >= len(items) {
		m.cursor = max(0, len(items)-1)
	}
	return nil
}

// softDeleteCurrent marks the item under the cursor as deleted and dirty,
// bumping UpdatedAt monotonically so the tombstone wins last-write-wins.
func (m *Model) softDeleteCurrent() error {
	if len(m.items) == 0 {
		return nil
	}
	cur := m.items[m.cursor]
	now := time.Now().UTC()
	if !now.After(cur.UpdatedAt) {
		now = cur.UpdatedAt.Add(time.Nanosecond)
	}
	cur.DeletedAt = &now
	cur.UpdatedAt = now
	return m.local.UpsertItem(m.ctx, &cur, true)
}

// commitAdd persists the in-progress add form as a new dirty item.
func (m *Model) commitAdd() error {
	if m.addTitle == "" {
		return fmt.Errorf("title is required")
	}
	it := &models.Item{
		ID:        uuid.NewString(),
		Type:      m.addType,
		Title:     m.addTitle,
		Data:      []byte(m.addBody),
		UpdatedAt: time.Now().UTC(),
	}
	return m.local.UpsertItem(m.ctx, it, true)
}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	statusStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

// View implements tea.Model.
func (m *Model) View() string {
	if m.screen == screenAdd {
		return m.viewAdd()
	}
	return m.viewList()
}

func (m *Model) viewList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("GophKeeper") + "\n\n")

	if len(m.items) == 0 {
		b.WriteString("  (no items — press a to add, s to sync)\n\n")
	} else {
		for i, it := range m.items {
			id := it.ID
			if len(id) > 8 {
				id = id[:8]
			}
			line := fmt.Sprintf("%s  [%s] %s  v%d", id, it.Type, it.Title, it.Version)
			if i == m.cursor {
				b.WriteString(selectedStyle.Render("▶ "+line) + "\n")
			} else {
				b.WriteString("  " + line + "\n")
			}
		}
		b.WriteString("\n")
	}

	if m.err != nil {
		b.WriteString(errorStyle.Render("error: "+m.err.Error()) + "\n")
	} else if m.status != "" {
		b.WriteString(statusStyle.Render(m.status) + "\n")
	}

	b.WriteString(helpStyle.Render("↑/↓ navigate • a add • d delete • s sync • r refresh • q quit"))
	return b.String()
}

func (m *Model) viewAdd() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Add text note") + "\n\n")
	if m.addField == 0 {
		b.WriteString(selectedStyle.Render("> Title: "+m.addTitle) + "\n")
		b.WriteString("  Data:  " + m.addBody + "\n")
	} else {
		b.WriteString("  Title: " + m.addTitle + "\n")
		b.WriteString(selectedStyle.Render("> Data:  "+m.addBody) + "\n")
	}
	if m.err != nil {
		b.WriteString("\n" + errorStyle.Render("error: "+m.err.Error()))
	}
	b.WriteString("\n\n" + helpStyle.Render("tab switch • enter next/commit • esc cancel"))
	return b.String()
}
