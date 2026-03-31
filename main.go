package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	maxQ := flag.Int("n", 0, "max questions per session (0 = use saved setting, default 5)")
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

	dailyGoal := 0
	if g := os.Getenv("UNROT_DAILY_GOAL"); g != "" {
		if n, err := strconv.Atoi(g); err == nil && n > 0 {
			dailyGoal = n
		}
	}

	p := tea.NewProgram(initialModel(brainPath, domainFilter, *maxQ, dailyGoal), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
