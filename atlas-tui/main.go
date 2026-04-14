package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rodeelh/atlas-tui/client"
	"github.com/rodeelh/atlas-tui/config"
	"github.com/rodeelh/atlas-tui/ui"
)

func main() {
	cfg := config.Load()
	c := client.New(cfg.BaseURL)

	p := tea.NewProgram(
		ui.NewApp(c, cfg.Port),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
