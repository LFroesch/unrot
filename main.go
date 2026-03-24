package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	brainPath := os.Getenv("SECOND_BRAIN")
	if brainPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		brainPath = filepath.Join(home, "projects", "active", "daily_use", "SECOND_BRAIN")
	}

	p := tea.NewProgram(initialModel(brainPath), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
