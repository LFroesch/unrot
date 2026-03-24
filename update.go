package main

import (
	"fmt"

	"github.com/LFroesch/unrot/internal/knowledge"
	"github.com/LFroesch/unrot/internal/ollama"
	"github.com/LFroesch/unrot/internal/state"

	tea "github.com/charmbracelet/bubbletea"
)

// Messages
type stateLoadedMsg struct {
	state *state.State
	files []string
}

type questionMsg struct {
	question *ollama.Question
	file     string
}

type errMsg struct{ err error }

// Commands
func loadState(brainPath string) tea.Cmd {
	return func() tea.Msg {
		s, err := state.Load()
		if err != nil {
			return errMsg{err}
		}
		files, err := knowledge.Discover(brainPath)
		if err != nil {
			return errMsg{err}
		}
		if len(files) == 0 {
			return errMsg{err: fmt.Errorf("no knowledge files found in %s/knowledge/", brainPath)}
		}
		sorted := s.Stalest(files)
		return stateLoadedMsg{state: s, files: sorted}
	}
}

func generateQuestion(client *ollama.Client, brainPath, filePath string) tea.Cmd {
	return func() tea.Msg {
		content, err := knowledge.ReadFile(brainPath, filePath)
		if err != nil {
			return errMsg{err}
		}
		q, err := client.GenerateQuestion(content, filePath, -1) // random type
		if err != nil {
			return errMsg{err}
		}
		return questionMsg{question: q, file: filePath}
	}
}

func (m model) Init() tea.Cmd {
	return loadState(m.brainPath)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case stateLoadedMsg:
		m.state = msg.state
		m.files = msg.files
		m.fileIdx = 0
		m.currentFile = m.files[0]
		m.phase = phaseLoading
		return m, generateQuestion(m.ollama, m.brainPath, m.currentFile)

	case questionMsg:
		m.currentQ = msg.question
		m.currentFile = msg.file
		m.domain = m.currentDomain()
		m.phase = phaseQuestion
		return m, nil

	case errMsg:
		m.err = msg.err
		m.phase = phaseError
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.state != nil {
			m.state.Save()
		}
		return m, tea.Quit

	case "enter", " ":
		if m.phase == phaseQuestion && m.currentQ.Type != ollama.TypeMultiChoice {
			m.phase = phaseRevealed
			return m, nil
		}
		if m.phase == phaseAnswered {
			return m, m.nextQuestion()
		}

	case "y":
		if m.phase == phaseRevealed {
			m.selfGrade = true
			m.sessionCorrect++
			m.sessionTotal++
			m.state.Record(m.currentFile, true)
			return m, m.nextQuestion()
		}

	case "n":
		if m.phase == phaseRevealed {
			m.selfGrade = false
			m.sessionWrong++
			m.sessionTotal++
			m.state.Record(m.currentFile, false)
			return m, m.nextQuestion()
		}

	case "a", "b", "c", "d":
		if m.phase == phaseQuestion && m.currentQ.Type == ollama.TypeMultiChoice {
			picked := int(msg.String()[0] - 'a')
			m.mcPicked = picked
			correct := picked == m.currentQ.CorrectIdx
			m.selfGrade = correct
			m.sessionTotal++
			if correct {
				m.sessionCorrect++
			} else {
				m.sessionWrong++
			}
			m.state.Record(m.currentFile, correct)
			m.phase = phaseAnswered
			return m, nil
		}

	case "s":
		if m.phase == phaseQuestion {
			return m, m.nextQuestion()
		}
	}

	return m, nil
}

func (m *model) nextQuestion() tea.Cmd {
	m.fileIdx++
	if m.fileIdx >= len(m.files) {
		m.phase = phaseDone
		return nil
	}
	m.currentFile = m.files[m.fileIdx]
	m.phase = phaseLoading
	return generateQuestion(m.ollama, m.brainPath, m.currentFile)
}
