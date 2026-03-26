package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"

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

type prefetchMsg struct {
	question *ollama.Question
	file     string
}

type learnContentMsg struct {
	content string
}

type learnSavedMsg struct {
	relPath string
}

type explainMoreMsg struct {
	text string
}

type hintMsg struct {
	text string
}

type lessonMsg struct {
	content string
	file    string
}

type conceptChatMsg struct {
	text string
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
		return stateLoadedMsg{state: s, files: files}
	}
}

func generateQuestion(client *ollama.Client, brainPath, filePath string, qtype ollama.QuestionType, diff ollama.Difficulty) tea.Cmd {
	return func() tea.Msg {
		content, err := knowledge.ReadFile(brainPath, filePath)
		if err != nil {
			return errMsg{err}
		}
		q, err := client.GenerateQuestion(content, filePath, qtype, diff)
		if err != nil {
			return errMsg{err}
		}
		return questionMsg{question: q, file: filePath}
	}
}

func prefetchQuestion(client *ollama.Client, brainPath, filePath string, qtype ollama.QuestionType, diff ollama.Difficulty) tea.Cmd {
	return func() tea.Msg {
		content, err := knowledge.ReadFile(brainPath, filePath)
		if err != nil {
			return nil
		}
		q, err := client.GenerateQuestion(content, filePath, qtype, diff)
		if err != nil {
			return nil
		}
		return prefetchMsg{question: q, file: filePath}
	}
}

func explainMoreCmd(client *ollama.Client, question, answer, explanation string) tea.Cmd {
	return func() tea.Msg {
		system := `You are a tutor helping someone actually learn a concept they got wrong on a quiz.
Rules:
- Start with the core concept in one plain sentence
- Then give a CONCRETE example: show real code, a real command, or real input→output
- Then explain WHY it works that way — the underlying mechanism
- Keep it to 4-6 sentences total
- Every fact must be correct — do not make up examples
- Output only the explanation text, no labels or formatting`

		user := fmt.Sprintf("Question: %s\nCorrect answer: %s\nBrief explanation: %s\n\nTeach me this concept. Include a concrete example.", question, answer, explanation)

		resp, err := client.Chat(system, user)
		if err != nil {
			return errMsg{err}
		}
		return explainMoreMsg{text: resp}
	}
}

func hintCmd(client *ollama.Client, question, answer string, prevHints []string) tea.Cmd {
	return func() tea.Msg {
		n := len(prevHints)
		var level string
		switch {
		case n == 0:
			level = "Hint 1: Name the general TOPIC or AREA the answer relates to. One sentence."
		case n == 1:
			level = "Hint 2: Give a more specific clue — mention a KEYWORD that appears in the answer, or narrow down to the exact concept. One sentence."
		default:
			level = "Hint 3: Almost give it away — describe the answer without using the exact words. Paraphrase it closely. One sentence."
		}

		system := fmt.Sprintf(`You give hints for a quiz question. The answer is provided so you can give accurate hints.
Rules:
- Output ONLY the hint text, no labels, no "Hint:", no formatting
- One sentence only
- %s`, level)

		user := fmt.Sprintf("Question: %s\nAnswer: %s", question, answer)
		if n > 0 {
			user += fmt.Sprintf("\n\nPrevious hints:\n%s", strings.Join(prevHints, "\n"))
		}

		resp, err := client.Chat(system, user)
		if err != nil {
			return errMsg{err}
		}
		return hintMsg{text: resp}
	}
}

func generateKnowledge(client *ollama.Client, topic string) tea.Cmd {
	return func() tea.Msg {
		content, err := client.GenerateKnowledge(topic)
		if err != nil {
			return errMsg{err}
		}
		return learnContentMsg{content: content}
	}
}

func loadLesson(brainPath, filePath string) tea.Cmd {
	return func() tea.Msg {
		content, err := knowledge.ReadFile(brainPath, filePath)
		if err != nil {
			return errMsg{err}
		}
		return lessonMsg{content: content, file: filePath}
	}
}

func conceptChatCmd(client *ollama.Client, question, answer, source string, history []chatEntry) tea.Cmd {
	return func() tea.Msg {
		var system string
		if question != "" {
			system = fmt.Sprintf(`You are a tutor helping someone understand a concept they were quizzed on.

Quiz question: %s
Correct answer: %s`, question, answer)
		} else {
			system = `You are a tutor helping someone understand a concept they are studying.`
		}
		if source != "" {
			system += fmt.Sprintf("\n\nSource material:\n%s", source)
		}
		system += `

Rules:
- Answer their questions about this concept clearly and concisely
- Use concrete examples (code, commands, input→output)
- If they're confused, break it down step by step
- Keep responses to 3-5 sentences
- Be accurate — don't make things up`

		msgs := make([]ollama.Message, len(history))
		for i, h := range history {
			msgs[i] = ollama.Message{Role: h.role, Content: h.content}
		}
		resp, err := client.ChatWithHistory(system, msgs)
		if err != nil {
			return errMsg{err}
		}
		return conceptChatMsg{text: resp}
	}
}

func saveKnowledge(brainPath, domain, slug, content string) tea.Cmd {
	return func() tea.Msg {
		relPath, err := knowledge.WriteFile(brainPath, domain, slug, content)
		if err != nil {
			return errMsg{err}
		}
		return learnSavedMsg{relPath: relPath}
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(loadState(m.brainPath), m.spinner.Tick)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = m.contentHeight()
		m.overlayViewport.Width = msg.Width - 16
		m.overlayViewport.Height = msg.Height - 10
		m.answerTA.SetWidth(msg.Width - 8)
		m.learnTA.SetWidth(msg.Width - 8)
		m.pickSearch.Width = msg.Width - 12
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case stateLoadedMsg:
		m.state = msg.state
		m.allFiles = msg.files
		m.buildDomainList()
		if m.domainFilter != "" {
			return m, m.startReview()
		}
		m.phase = phaseDashboard
		return m, nil

	case questionMsg:
		m.currentQ = msg.question
		m.currentFile = msg.file
		m.sessionDomains[m.currentDomain()] = true
		m.answerTA.Reset()
		m.answerTA.Focus()
		m.hints = nil
		m.userAnswer = ""
		m.showKnowledge = false
		m.quizStep = stepQuestion
		// Prefetch next question
		return m, m.prefetchNext()

	case prefetchMsg:
		if msg.question != nil {
			m.nextQ = msg.question
			m.nextFile = msg.file
		}
		return m, nil

	case hintMsg:
		m.hints = append(m.hints, msg.text)
		m.quizStep = stepQuestion
		return m, nil

	case explainMoreMsg:
		m.currentQ.Explanation = msg.text
		m.quizStep = stepResult
		m.syncViewport()
		return m, nil

	case lessonMsg:
		m.sourceContent = msg.content
		m.currentFile = msg.file
		m.sessionDomains[m.currentDomain()] = true
		m.phase = phaseQuiz
		m.quizStep = stepLesson
		m.syncViewport()
		return m, nil


	case conceptChatMsg:
		m.conceptChat = append(m.conceptChat, chatEntry{role: "assistant", content: msg.text})
		// Stay in overlay — just rebuild overlay content
		m.syncOverlayViewport()
		return m, nil

	case learnContentMsg:
		m.learnContent = msg.content
		m.learnStep = learnReview
		m.syncViewport()
		return m, nil

	case learnSavedMsg:
		m.currentFile = msg.relPath
		m.allFiles = append(m.allFiles, msg.relPath)
		m.phase = phaseQuiz
		m.quizStep = stepLoading
		return m, generateQuestion(m.ollama, m.brainPath, msg.relPath, m.randomActiveType(), ollama.DiffBasic)

	case errMsg:
		m.err = msg.err
		m.phase = phaseError
		return m, nil

	case tea.MouseMsg:
		if m.activeOverlay != overlayNone {
			var cmd tea.Cmd
			m.overlayViewport, cmd = m.overlayViewport.Update(msg)
			return m, cmd
		}
		switch m.phase {
		case phaseQuiz:
			switch m.quizStep {
			case stepLesson, stepResult:
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		case phaseLearn:
			if m.learnStep == learnReview {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		case phaseStats, phaseDone:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// goHome returns to the dashboard, resetting session domain filter.
func (m *model) goHome() {
	m.phase = phaseDashboard
	m.domainFilter = m.cliDomain
	m.activeOverlay = overlayNone
}

// contentHeight returns usable height for viewport content.
// Chrome: header(1) + rule(1) + blank before status(1) + status(1) = 4
func (m model) contentHeight() int {
	h := m.height - 4
	if h < 5 {
		h = 5
	}
	return h
}

func (m *model) syncViewport() {
	m.viewport.Width = m.width - 4
	h := m.contentHeight()
	if m.phase == phaseQuiz && m.quizStep == stepResult && m.activeOverlay == overlayChat {
		h -= 6
	}
	if h < 5 {
		h = 5
	}
	m.viewport.Height = h
	m.viewport.SetContent(m.viewportContent())
	m.viewport.GotoTop()
}

func (m *model) syncOverlayViewport() {
	w := m.width - 16
	if w < 30 {
		w = 30
	}
	// Overlay chrome: border(2) + padding(2) + title(1) + gaps(2) + footer(1) = 8
	// Chat overlay also has: label(1) + textarea(~3) + gap(1) = 5 extra
	// Scroll hint may take 1 extra line
	chrome := 10
	if m.activeOverlay == overlayChat {
		chrome += 5
	}
	h := m.height - chrome
	if h < 5 {
		h = 5
	}
	m.overlayViewport.Width = w
	m.overlayViewport.Height = h
	m.overlayViewport.SetContent(m.buildOverlayContent())
	m.overlayViewport.GotoBottom()
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global quit
	if key == "ctrl+c" {
		if m.state != nil {
			m.state.Save()
		}
		return m, tea.Quit
	}

	// Overlay keys take priority
	if m.activeOverlay != overlayNone {
		return m.handleOverlay(msg)
	}

	switch m.phase {
	case phaseDashboard:
		return m.handleDashboard(key)
	case phaseTopicList:
		return m.handleTopicList(msg)
	case phaseQuiz:
		return m.handleQuiz(msg)
	case phaseLearn:
		return m.handleLearn(msg)
	case phaseStats:
		return m.handleStats(msg)
	case phaseDone:
		return m.handleDone(msg)
	case phaseError:
		if key == "esc" {
			m.goHome()
			m.err = nil
			return m, nil
		}
	}

	return m, nil
}

// --- Dashboard ---

func (m model) handleDashboard(key string) (model, tea.Cmd) {
	switch key {
	case "enter":
		return m, m.startReview()
	case "b":
		m.pickFiles = sortByDomain(m.allFiles)
		m.pickCursor = 0
		m.pickMode = false
		m.pickSearching = false
		m.filterPickFiles()
		m.phase = phaseTopicList
		return m, nil
	case "l":
		m.learnTA.Reset()
		m.learnTA.Focus()
		m.learnStep = learnInput
		m.phase = phaseLearn
		return m, nil
	case "s":
		m.phase = phaseStats
		m.syncViewport()
		return m, nil
	case "tab":
		m.cycleDomainFilter()
		return m, nil
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// --- Topic List ---

func (m model) handleTopicList(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()

	if m.pickSearching {
		switch key {
		case "esc":
			m.pickSearching = false
			m.pickSearch.Blur()
			m.filterPickFiles()
			m.pickCursor = 0
			return m, nil
		case "enter":
			m.pickSearching = false
			m.pickSearch.Blur()
			if len(m.pickFiles) > 0 {
				return m, m.startPickDrill()
			}
			return m, nil
		case "up", "ctrl+p":
			if m.pickCursor > 0 {
				m.pickCursor--
			}
			return m, nil
		case "down", "ctrl+n":
			if m.pickCursor < len(m.pickFiles)-1 {
				m.pickCursor++
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.pickSearch, cmd = m.pickSearch.Update(msg)
			m.filterPickFiles()
			return m, cmd
		}
	}

	switch key {
	case "/":
		m.pickSearching = true
		m.pickSearch.SetValue("")
		m.pickSearch.Focus()
		return m, nil
	case "j", "down":
		if m.pickCursor < len(m.pickFiles)-1 {
			m.pickCursor++
		}
	case "k", "up":
		if m.pickCursor > 0 {
			m.pickCursor--
		}
	case "enter":
		if len(m.pickFiles) > 0 {
			return m, m.startPickDrill()
		}
	case "+":
		domain := ""
		if len(m.pickFiles) > 0 && m.pickCursor < len(m.pickFiles) {
			domain = knowledge.Domain(m.pickFiles[m.pickCursor])
		}
		m.learnTA.Reset()
		if domain != "" {
			m.learnTA.SetValue(domain + "/")
		}
		m.learnTA.Focus()
		m.learnStep = learnInput
		m.phase = phaseLearn
		return m, nil
	case "x":
		if len(m.pickFiles) > 0 && m.pickCursor < len(m.pickFiles) {
			file := m.pickFiles[m.pickCursor]
			m.state.ResetFile(file)
			m.state.Save()
		}
	case "tab":
		m.cycleDomainFilter()
		m.filterPickFiles()
		return m, nil
	case "esc":
		m.goHome()
	}
	return m, nil
}

func (m *model) startPickDrill() tea.Cmd {
	file := m.pickFiles[m.pickCursor]
	m.pickMode = true
	m.dueFiles = nil
	m.fileIdx = 0
	m.sessionCorrect = 0
	m.sessionWrong = 0
	m.sessionTotal = 0
	m.sessionWrongs = nil
	m.sessionStart = time.Now()
	m.sessionDomains = make(map[string]bool)
	m.retryQueue = nil
	m.retryPhase = false
	m.retryCount = nil
	m.nextQ = nil
	m.nextFile = ""
	m.phase = phaseQuiz
	return m.startFile(file)
}

// --- Quiz (consolidated) ---

func (m model) handleQuiz(msg tea.KeyMsg) (model, tea.Cmd) {
	switch m.quizStep {
	case stepLesson:
		return m.handleLesson(msg)
	case stepLoading, stepGrading:
		key := msg.String()
		if key == "esc" {
			if m.pickMode {
				m.phase = phaseTopicList
				return m, nil
			}
			m.goHome()
			return m, nil
		}
	case stepQuestion:
		return m.handleQuestion(msg)
	case stepRevealed:
		return m.handleRevealed(msg.String())
	case stepResult:
		return m.handleResult(msg)
	}
	return m, nil
}

func (m model) handleLesson(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "enter", " ":
		if m.nextQ != nil && m.nextFile == m.currentFile {
			m.currentQ = m.nextQ
			m.answerTA.Reset()
			m.answerTA.Focus()
				m.hints = nil
			m.userAnswer = ""
				m.nextQ = nil
			m.nextFile = ""
			m.quizStep = stepQuestion
			return m, m.prefetchNext()
		}
		diff := ollama.DifficultyFromStrength(m.state.Strength(m.currentFile))
		m.quizStep = stepLoading
		return m, generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	case "c":
		m.conceptChat = nil
		m.chatFromLesson = true
		m.answerTA.Reset()
		m.answerTA.SetHeight(2)
		m.answerTA.Placeholder = "ask about this concept..."
		m.answerTA.Focus()
		m.activeOverlay = overlayChat
		m.syncOverlayViewport()
		return m, nil
	case "esc":
		if m.pickMode {
			m.phase = phaseTopicList
			return m, nil
		}
		return m, m.skipToNextFile()
	case "q":
		if m.pickMode {
			m.phase = phaseTopicList
			return m, nil
		}
		m.goHome()
		return m, nil
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
}

func (m model) handleQuestion(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.currentQ == nil {
		return m, nil
	}
	key := msg.String()

	// ctrl+e = hint
	if key == "ctrl+e" {
		m.quizStep = stepGrading
		return m, hintCmd(m.ollama, m.currentQ.Text, m.currentQ.Answer, m.hints)
	}

	// Regenerate question
	if key == "ctrl+r" {
		diff := ollama.DifficultyFromStrength(m.currentStrength())
		m.quizStep = stepLoading
		m.hints = nil
		m.showKnowledge = false
		return m, generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	}

	// Knowledge overlay toggle (ctrl+o — ctrl+j/k intercepted by Cursor)
	if key == "ctrl+o" && m.sourceContent != "" {
		if m.activeOverlay == overlayKnowledge {
			m.activeOverlay = overlayNone
		} else {
			m.activeOverlay = overlayKnowledge
			m.overlayViewport.SetContent(renderMarkdown(m.sourceContent, m.overlayViewport.Width-4))
			m.overlayViewport.GotoTop()
		}
		return m, nil
	}

	// Chat overlay mid-question (ctrl+y — ctrl+t intercepted by Cursor)
	if key == "ctrl+y" {
		m.conceptChat = nil
		m.chatFromLesson = false
		m.answerTA.Reset()
		m.answerTA.SetHeight(2)
		m.answerTA.Placeholder = "ask about this concept..."
		m.answerTA.Focus()
		m.activeOverlay = overlayChat
		m.syncOverlayViewport()
		return m, nil
	}

	// Multiple choice — pick a/b/c/d
	if m.currentQ.Type == ollama.TypeMultiChoice {
		switch {
		case key == "a" || key == "b" || key == "c" || key == "d":
			picked := int(key[0] - 'a')
			m.mcPicked = picked
			correct := picked == m.currentQ.CorrectIdx
			if correct {
				m.grade = gradeCorrect
			} else {
				m.grade = gradeWrong
			}
			m.sessionTotal++
			if correct {
				m.sessionCorrect++
			} else {
				m.sessionWrong++
				m.sessionWrongs = append(m.sessionWrongs, wrongItem{
					file:     m.currentFile,
					question: m.currentQ.Text,
					answer:   m.currentQ.Answer,
					qtype:    m.currentQ.Type.String(),
				})
				m.enqueueRetry(m.currentFile)
			}
			m.state.Record(m.currentFile, correct)
			m.quizStep = stepResult
			m.syncViewport()
			return m, nil
		case key == "esc":
			if m.pickMode {
				m.phase = phaseTopicList
				return m, nil
			}
			return m, m.nextQuestion()
		}
		return m, nil
	}

	// Non-MC: typed answer
	switch key {
	case "tab":
		m.quizStep = stepRevealed
		return m, nil
	case "enter":
		answer := strings.TrimSpace(m.answerTA.Value())
		if answer == "" {
			return m, nil
		}
		m.userAnswer = answer
		m.answerTA.Reset()
		m.quizStep = stepRevealed
		return m, nil
	case "esc":
		m.answerTA.Reset()
		if m.pickMode {
			m.phase = phaseTopicList
			return m, nil
		}
		return m, m.nextQuestion()
	default:
		var cmd tea.Cmd
		m.answerTA, cmd = m.answerTA.Update(msg)
		return m, cmd
	}
}

func (m model) handleRevealed(key string) (model, tea.Cmd) {
	switch key {
	case "y":
		m.grade = gradeCorrect
		m.sessionCorrect++
		m.sessionTotal++
		m.state.Record(m.currentFile, true)
		m.quizStep = stepResult
		m.syncViewport()
		return m, nil
	case "s":
		m.grade = gradePartial
		m.sessionCorrect++
		m.sessionTotal++
		m.state.RecordPartial(m.currentFile)
		m.quizStep = stepResult
		m.syncViewport()
		return m, nil
	case "n":
		m.grade = gradeWrong
		m.sessionWrong++
		m.sessionTotal++
		m.state.Record(m.currentFile, false)
		m.sessionWrongs = append(m.sessionWrongs, wrongItem{
			file:     m.currentFile,
			question: m.currentQ.Text,
			answer:   m.currentQ.Answer,
			qtype:    m.currentQ.Type.String(),
		})
		m.enqueueRetry(m.currentFile)
		m.quizStep = stepResult
		m.syncViewport()
		return m, nil
	case "esc":
		if m.pickMode {
			m.phase = phaseTopicList
			return m, nil
		}
		return m, m.nextQuestion()
	}
	return m, nil
}

func (m model) handleResult(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "r":
		// Re-quiz: generate a new question on the same topic
		diff := ollama.DifficultyFromStrength(m.currentStrength())
		m.quizStep = stepLoading
		m.hints = nil
		m.activeOverlay = overlayNone
		return m, generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	case "e":
		m.quizStep = stepGrading
		return m, explainMoreCmd(m.ollama, m.currentQ.Text, m.currentQ.Answer, m.currentQ.Explanation)
	case "c":
		m.conceptChat = nil
		m.chatFromLesson = false
		m.answerTA.Reset()
		m.answerTA.SetHeight(2)
		m.answerTA.Placeholder = "ask about this concept..."
		m.answerTA.Focus()
		m.activeOverlay = overlayChat
		m.syncOverlayViewport()
		return m, nil
	case "ctrl+o":
		if m.sourceContent != "" {
			if m.activeOverlay == overlayKnowledge {
				m.activeOverlay = overlayNone
			} else {
				m.activeOverlay = overlayKnowledge
				m.overlayViewport.SetContent(renderMarkdown(m.sourceContent, m.overlayViewport.Width-4))
				m.overlayViewport.GotoTop()
			}
		}
		return m, nil
	case "enter", " ":
		m.activeOverlay = overlayNone
		return m, m.nextQuestion()
	case "esc":
		m.activeOverlay = overlayNone
		if m.pickMode {
			m.pickMode = false
			m.phase = phaseTopicList
			return m, nil
		}
		m.goHome()
		return m, nil
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
}

// --- Overlay handling ---

func (m model) handleOverlay(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()

	switch m.activeOverlay {
	case overlayKnowledge:
		switch key {
		case "esc", "ctrl+o":
			m.activeOverlay = overlayNone
			return m, nil
		default:
			var cmd tea.Cmd
			m.overlayViewport, cmd = m.overlayViewport.Update(msg)
			return m, cmd
		}

	case overlayChat:
		switch key {
		case "esc":
			m.activeOverlay = overlayNone
			m.answerTA.SetHeight(5)
			m.answerTA.Placeholder = "type your answer..."
			return m, nil
		case "enter":
			question := strings.TrimSpace(m.answerTA.Value())
			if question == "" {
				return m, nil
			}
			m.conceptChat = append(m.conceptChat, chatEntry{role: "user", content: question})
			m.answerTA.Reset()
			m.answerTA.Focus()
			m.syncOverlayViewport()
			var qText, qAnswer string
			if m.currentQ != nil {
				qText = m.currentQ.Text
				qAnswer = m.currentQ.Answer
			}
			return m, conceptChatCmd(m.ollama, qText, qAnswer, m.sourceContent, m.conceptChat)
		default:
			var cmd tea.Cmd
			m.answerTA, cmd = m.answerTA.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

// --- Learn ---

func (m model) handleLearn(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()

	switch m.learnStep {
	case learnInput:
		switch key {
		case "esc":
			m.goHome()
			return m, nil
		case "enter":
			topic := strings.TrimSpace(m.learnTA.Value())
			if topic == "" {
				return m, nil
			}
			parts := strings.SplitN(topic, "/", 2)
			if len(parts) == 2 {
				m.learnDomain = strings.TrimSpace(parts[0])
				m.learnSlug = slugify(strings.TrimSpace(parts[1]))
			} else {
				m.learnDomain = "general"
				m.learnSlug = slugify(topic)
			}
			m.learnStep = learnGenerating
			return m, generateKnowledge(m.ollama, topic)
		default:
			var cmd tea.Cmd
			m.learnTA, cmd = m.learnTA.Update(msg)
			return m, cmd
		}

	case learnGenerating:
		if key == "esc" {
			m.learnStep = learnInput
			return m, nil
		}

	case learnReview:
		switch key {
		case "s":
			return m, saveKnowledge(m.brainPath, m.learnDomain, m.learnSlug, m.learnContent)
		case "r":
			m.learnStep = learnGenerating
			return m, generateKnowledge(m.ollama, m.learnTA.Value())
		case "esc":
			m.learnTA.Reset()
			m.learnTA.Focus()
			m.learnStep = learnInput
			return m, nil
		default:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

// --- Stats ---

func (m model) handleStats(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	if key == "esc" {
		m.goHome()
		return m, nil
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// --- Done ---

func (m model) handleDone(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "r":
		if len(m.sessionWrongs) > 0 {
			return m, m.startReview()
		}
	case "esc", "q":
		m.goHome()
		return m, nil
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

// --- Review session management ---

func (m *model) startReview() tea.Cmd {
	files := m.allFiles
	if m.domainFilter != "" {
		files = knowledge.FilterByDomain(files, m.domainFilter)
		if len(files) == 0 {
			m.err = fmt.Errorf("no knowledge files found for domain %q", m.domainFilter)
			m.phase = phaseError
			return nil
		}
	}

	m.dueFiles = m.state.DueItems(files)
	if len(m.dueFiles) == 0 {
		m.phase = phaseDone
		m.syncViewport()
		return nil
	}

	m.dueFiles = warmupOrder(m.dueFiles)

	if m.maxQuestions > 0 && len(m.dueFiles) > m.maxQuestions {
		m.dueFiles = m.dueFiles[:m.maxQuestions]
	}

	m.fileIdx = 0
	m.pickMode = false
	m.sessionCorrect = 0
	m.sessionWrong = 0
	m.sessionTotal = 0
	m.sessionWrongs = nil
	m.sessionStart = time.Now()
	m.sessionDomains = make(map[string]bool)
	m.retryQueue = nil
	m.retryPhase = false
	m.retryCount = nil
	m.nextQ = nil
	m.nextFile = ""
	m.phase = phaseQuiz

	return m.startFile(m.dueFiles[0].Path)
}

// startFile begins quizzing on a file. Always shows knowledge content first.
func (m *model) startFile(file string) tea.Cmd {
	m.currentFile = file
	m.sourceContent = ""
	m.activeOverlay = overlayNone
	return loadLesson(m.brainPath, file)
}

// skipToNextFile advances past the current file without answering.
func (m *model) skipToNextFile() tea.Cmd {
	m.fileIdx++
	if m.fileIdx < len(m.dueFiles) {
		return m.startFile(m.dueFiles[m.fileIdx].Path)
	}
	return m.finishSession()
}

// warmupOrder puts up to 2 strong files first for session confidence building.
func warmupOrder(due []state.DueFile) []state.DueFile {
	if len(due) <= 3 {
		return due
	}

	type candidate struct {
		idx      int
		strength float64
	}
	var warm []candidate
	for i, d := range due {
		if !d.IsNew && d.Strength >= 0.5 {
			warm = append(warm, candidate{i, d.Strength})
		}
	}
	if len(warm) == 0 {
		return due
	}
	sort.Slice(warm, func(i, j int) bool {
		return warm[i].strength > warm[j].strength
	})

	n := 2
	if len(warm) < n {
		n = len(warm)
	}

	result := make([]state.DueFile, 0, len(due))
	used := make(map[int]bool)
	for i := 0; i < n; i++ {
		result = append(result, due[warm[i].idx])
		used[warm[i].idx] = true
	}
	for i, d := range due {
		if !used[i] {
			result = append(result, d)
		}
	}
	return result
}

const maxRetries = 2

func (m *model) enqueueRetry(file string) {
	if m.retryCount == nil {
		m.retryCount = make(map[string]int)
	}
	if m.retryCount[file] < maxRetries {
		m.retryQueue = append(m.retryQueue, file)
		m.retryCount[file]++
	}
}

func (m *model) nextQuestion() tea.Cmd {
	if !m.pickMode && m.maxQuestions > 0 && m.sessionTotal >= m.maxQuestions {
		return m.finishSession()
	}

	if m.pickMode {
		diff := ollama.DifficultyFromStrength(m.state.Strength(m.currentFile))
		m.quizStep = stepLoading
		return generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	}

	m.fileIdx++
	if m.fileIdx < len(m.dueFiles) {
		return m.startFile(m.dueFiles[m.fileIdx].Path)
	}
	if len(m.retryQueue) > 0 {
		m.retryPhase = true
		file := m.retryQueue[0]
		m.retryQueue = m.retryQueue[1:]
		m.currentFile = file
		diff := ollama.DifficultyFromStrength(m.state.Strength(file))
		m.quizStep = stepLoading
		return generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	}
	return m.finishSession()
}

func (m *model) finishSession() tea.Cmd {
	if m.sessionTotal > 0 {
		var domains []string
		for d := range m.sessionDomains {
			domains = append(domains, d)
		}
		m.state.RecordSession(m.sessionCorrect, m.sessionWrong, domains, time.Since(m.sessionStart))
		m.state.Save()
	}
	m.phase = phaseDone
	m.syncViewport()
	return nil
}

func (m *model) prefetchNext() tea.Cmd {
	nextIdx := m.fileIdx + 1
	if nextIdx >= len(m.dueFiles) {
		return nil
	}
	nextFile := m.dueFiles[nextIdx].Path
	diff := ollama.DifficultyFromStrength(m.dueFiles[nextIdx].Strength)
	return prefetchQuestion(m.ollama, m.brainPath, nextFile, m.randomActiveType(), diff)
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
