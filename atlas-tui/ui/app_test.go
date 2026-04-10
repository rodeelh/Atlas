package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ralhassan/atlas-tui/client"
	"github.com/ralhassan/atlas-tui/config"
)

// TestAppModelSmoke verifies the root TUI model constructs, initializes,
// renders, and handles basic key/window events without panicking.
// This is the TUI's primary smoke test for release validation.
func TestAppModelSmoke(t *testing.T) {
	c := client.New("http://localhost:1984")
	cfg := &config.Config{BaseURL: "http://localhost:1984", OnboardingDone: true}

	m := NewAppModel(c, cfg)

	// Init must not panic and must return (cmd may be nil).
	_ = m.Init()

	// Window resize first — TUI models only render once dimensions are known.
	m2i, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := m2i.(AppModel)

	// View must produce non-empty content after sizing.
	if got := m2.View(); got == "" {
		t.Error("View returned empty string after WindowSizeMsg")
	}

	// Tab key should not panic and should still render.
	m3i, _ := m2.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v := m3i.(AppModel).View(); v == "" {
		t.Error("View empty after Tab keypress")
	}
}

func TestSplashAndChatModelsConstruct(t *testing.T) {
	c := client.New("http://localhost:1984")
	cfg := &config.Config{BaseURL: "http://localhost:1984"}

	splash := NewSplashModel()
	si, _ := splash.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if v := si.View(); v == "" {
		t.Error("splash view empty after sizing")
	}

	chat := NewChatModel(c, cfg)
	// Chat View may render an empty-state placeholder; just ensure no panic.
	_ = chat.View()
}
