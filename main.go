package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	maxQ := flag.Int("n", 10, "max questions per session (0 = unlimited)")
	flag.Parse()

	brainPath := os.Getenv("SECOND_BRAIN")
	if brainPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		brainPath = filepath.Join(home, "projects", "active", "daily_use", "SECOND_BRAIN")
	}

	// Optional domain filter: `unrot docker` or `unrot -n 5 docker`
	var domainFilter string
	if flag.NArg() > 0 {
		domainFilter = flag.Arg(0)
	}

	p := tea.NewProgram(initialModel(brainPath, domainFilter, *maxQ), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
