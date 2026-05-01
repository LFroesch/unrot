package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/LFroesch/unrot/internal/state"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	maxQ := flag.Int("n", 0, "max questions per session (0 = use saved setting, default 5)")
	brainFlag := flag.String("brain", "", "path to knowledge base root (overrides SECOND_BRAIN and saved setting)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "unrot — Quiz TUI for fighting knowledge decay (Ollama-powered)\n\n")
		fmt.Fprintf(os.Stderr, "Usage: unrot [flags] [domain]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("unrot " + version)
		os.Exit(0)
	}

	brainPath := *brainFlag
	if brainPath == "" {
		brainPath = os.Getenv("SECOND_BRAIN")
	}
	if brainPath == "" {
		if st, err := state.Load(); err == nil && st.BrainPath != "" {
			brainPath = st.BrainPath
		}
		// brainPath may still be "" — app handles it gracefully
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
