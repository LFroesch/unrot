package main

import (
	"strings"
	"testing"
	"time"

	"github.com/LFroesch/unrot/internal/ollama"
	"github.com/LFroesch/unrot/internal/state"
	"github.com/charmbracelet/lipgloss"
)

func TestAlignBarFitsWidth(t *testing.T) {
	left := headerTitleStyle.Render("unrot") + headerDimStyle.Render(" · ") + headerAccentStyle.Render(strings.Repeat("projects/", 4))
	right := headerStatsStyle.Render("12/40 · avg 3.2") + headerDimStyle.Render(" · ") + headerStreakStyle.Render("🔥 8")

	line := alignBar(left, right, 36)
	if got := lipgloss.Width(line); got != 36 {
		t.Fatalf("alignBar width = %d, want 36", got)
	}
	if !strings.Contains(line, "🔥 8") {
		t.Fatalf("alignBar dropped right-hand content: %q", line)
	}
}

func TestRenderTopicListKeepsRowsWithinWrapWidth(t *testing.T) {
	m := initialModel("", "", 5, 0)
	m.width = 60
	m.height = 24
	m.phase = phaseTopicList
	m.pickFiles = []string{
		"knowledge/docker/very-long-topic-name-for-images.md",
		"knowledge/go/even-longer-topic-name-for-goroutines-and-scheduling.md",
	}
	m.pickCursor = 1
	m.state = &state.State{
		Files: map[string]*state.FileState{
			m.pickFiles[0]: {Path: m.pickFiles[0], Confidence: 2, LastReviewed: time.Now().AddDate(0, 0, -12)},
			m.pickFiles[1]: {Path: m.pickFiles[1], Confidence: 4, LastReviewed: time.Now().AddDate(0, 0, -40)},
		},
		Favorites: map[string]bool{m.pickFiles[1]: true},
	}

	rendered := m.renderTopicList()
	maxWidth := m.wrapW() + 2
	for _, line := range strings.Split(rendered, "\n") {
		if w := lipgloss.Width(line); w > maxWidth {
			t.Fatalf("topic list line width = %d, want <= %d\nline: %q", w, maxWidth, line)
		}
	}
}

func TestRenderSettingsWrapsLongKnowledgePath(t *testing.T) {
	m := initialModel("/Users/example/src/second-brain/knowledge/projects/very/deep/path/that/should/wrap/cleanly", "", 5, 0)
	m.width = 56
	m.height = 24
	m.phase = phaseSettings
	m.state = &state.State{}
	m.activeTypes = make([]bool, len(ollama.AllTypes))
	m.settingsCursor = len(ollama.AllTypes) + 2

	rendered := m.renderSettings()
	maxWidth := m.wrapW() + 2
	for _, line := range strings.Split(rendered, "\n") {
		if w := lipgloss.Width(line); w > maxWidth {
			t.Fatalf("settings line width = %d, want <= %d\nline: %q", w, maxWidth, line)
		}
	}
	if !strings.Contains(rendered, "current path") {
		t.Fatalf("expected wrapped knowledge path label in settings output")
	}
}
