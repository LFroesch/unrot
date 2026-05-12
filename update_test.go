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

func TestLessonEnterDoesNotStartLoadingWhenOllamaUnavailable(t *testing.T) {
	t.Setenv("DEMO_ENV", "1")

	m := initialModel("", "", 5, 0)
	m.phase = phaseQuiz
	m.quizStep = stepLesson
	m.ollamaOK = false

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if got.quizStep != stepLesson {
		t.Fatalf("quiz step = %v, want lesson", got.quizStep)
	}
	if got.toast == "" {
		t.Fatal("expected explanatory toast when ollama is unavailable")
	}
}
