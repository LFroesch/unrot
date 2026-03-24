package main

import (
	"github.com/LFroesch/unrot/internal/knowledge"
	"github.com/LFroesch/unrot/internal/ollama"
	"github.com/LFroesch/unrot/internal/state"
)

type phase int

const (
	phaseLoading phase = iota
	phaseQuestion
	phaseRevealed
	phaseAnswered
	phaseError
	phaseDone
)

type model struct {
	// Config
	brainPath string
	ollama    *ollama.Client
	state     *state.State

	// Quiz state
	files       []string // sorted by staleness
	fileIdx     int
	currentQ    *ollama.Question
	currentFile string
	phase       phase
	selfGrade   bool // true = correct, after grading
	mcPicked    int  // multiple choice: selected option index

	// Stats
	sessionCorrect int
	sessionWrong   int
	sessionTotal   int

	// UI
	width     int
	height    int
	err       error
	feedback  string
	domain    string
}

func initialModel(brainPath string) model {
	return model{
		brainPath: brainPath,
		ollama:    ollama.New(),
		phase:     phaseLoading,
	}
}

func (m model) currentDomain() string {
	if m.currentFile == "" {
		return ""
	}
	return knowledge.Domain(m.currentFile)
}
