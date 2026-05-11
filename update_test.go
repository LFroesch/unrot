package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestQuestionMarkTypesIntoLearnTextarea(t *testing.T) {
	m := initialModel("", "", 5, 0)
	m.phase = phaseLearn
	m.learnStep = learnInput
	m.learnTA.Focus()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	got := next.(model)

	if got.showHelp {
		t.Fatalf("expected help to stay closed while typing in learn textarea")
	}
	if got.learnTA.Value() != "?" {
		t.Fatalf("learn textarea value = %q, want ?", got.learnTA.Value())
	}
}
