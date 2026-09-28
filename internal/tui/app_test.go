package tui

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

func TestFormAcceptsGlobalShortcutCharacters(t *testing.T) {
	m := newModel(nil)
	m.active = apiScreen
	m.apiPath.Focus()
	updated, _ := m.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'q'}})
	got := updated.(*model)
	if got.apiPath.Value() != "q" {
		t.Fatalf("expected q to be entered, got %q", got.apiPath.Value())
	}
}

func TestDashboardUsesWardObserverBranding(t *testing.T) {
	view := newModel(nil).View()
	if !strings.Contains(view, "W A R D") || !strings.Contains(view, "WARD") || !strings.Contains(view, "Observer Support System") {
		t.Fatalf("dashboard branding is missing: %s", view)
	}
}
