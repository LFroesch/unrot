package main

import (
	"fmt"
	"os"
	"path/filepath"
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
	domain  string
	slug    string
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

type learnChatMsg struct {
	text string
}

type reportSavedMsg struct{ path string }

type notesSavedMsg struct{ content string }

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

func learnClarifyCmd(client *ollama.Client, topic string, history []chatEntry, existingFiles []string) tea.Cmd {
	return func() tea.Msg {
		var updateNote string
		if len(existingFiles) > 0 {
			// Let ollama know what already exists for context (but no overlap detection — that's deterministic)
			var sb strings.Builder
			sb.WriteString("\n\nExisting knowledge files in the user's library:\n")
			for _, f := range existingFiles {
				sb.WriteString("- " + f + "\n")
			}
			updateNote = sb.String()
		}

		system := fmt.Sprintf(`You are helping a developer decide what to learn and bank as a knowledge document.
Given their topic and any conversation so far, ask 1-2 clarifying questions to understand:
- Their current level with this topic (beginner/intermediate/advanced)
- Whether they want the general concept or a specific technology's implementation
- What aspects they find confusing or want to focus on

Rules:
- Keep it conversational — 2-3 sentences max
- If you already have enough context from the conversation, say: "I have a good picture. Press g to generate your knowledge doc."
- Don't repeat questions they already answered%s`, updateNote)

		msgs := make([]ollama.Message, 0, len(history)+1)
		msgs = append(msgs, ollama.Message{Role: "user", Content: fmt.Sprintf("I want to learn about: %s", topic)})
		for _, h := range history {
			msgs = append(msgs, ollama.Message{Role: h.role, Content: h.content})
		}

		resp, err := client.ChatWithHistory(system, msgs)
		if err != nil {
			return errMsg{err}
		}
		return learnChatMsg{text: resp}
	}
}

func generateKnowledgeFromChat(client *ollama.Client, topic string, chatHistory []chatEntry, existingFiles []string, brainPath string, updateFile string) tea.Cmd {
	return func() tea.Msg {
		// Build enriched topic from conversation
		var context strings.Builder
		context.WriteString(fmt.Sprintf("Topic: %s\n\n", topic))
		if len(chatHistory) > 0 {
			context.WriteString("Conversation context:\n")
			for _, h := range chatHistory {
				if h.role == "user" {
					context.WriteString(fmt.Sprintf("User: %s\n", h.content))
				} else {
					context.WriteString(fmt.Sprintf("Assistant: %s\n", h.content))
				}
			}
		}

		// Update mode: read existing file and ask ollama to merge
		if updateFile != "" && brainPath != "" {
			existing, err := knowledge.ReadFile(brainPath, updateFile)
			if err != nil {
				return errMsg{err}
			}
			content, err := client.UpdateKnowledge(context.String(), existing)
			if err != nil {
				return errMsg{err}
			}
			domain := knowledge.Domain(updateFile)
			base := filepath.Base(updateFile)
			slug := strings.TrimSuffix(base, filepath.Ext(base))
			return learnContentMsg{content: content, domain: domain, slug: slug}
		}

		content, domain, slug, err := client.GenerateKnowledge(context.String(), existingFiles)
		if err != nil {
			return errMsg{err}
		}
		return learnContentMsg{content: content, domain: domain, slug: slug}
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

func saveNotesCmd(brainPath, relPath, notes string) tea.Cmd {
	return func() tea.Msg {
		if err := knowledge.UpdateNotes(brainPath, relPath, notes); err != nil {
			return errMsg{err}
		}
		content, err := knowledge.ReadFile(brainPath, relPath)
		if err != nil {
			return errMsg{err}
		}
		return notesSavedMsg{content: content}
	}
}

func (m model) exportReportCmd() tea.Cmd {
	correct := m.sessionCorrect
	wrong := m.sessionWrong
	total := m.sessionTotal
	confSum := m.sessionConfSum
	wrongs := m.sessionWrongs
	domains := m.sessionDomains
	start := m.sessionStart

	return func() tea.Msg {
		var b strings.Builder
		b.WriteString("# Unrot Session Report\n\n")
		b.WriteString(fmt.Sprintf("**Date:** %s\n", time.Now().Format("2006-01-02 15:04")))
		b.WriteString(fmt.Sprintf("**Duration:** %s\n", time.Since(start).Round(time.Second)))
		b.WriteString(fmt.Sprintf("**Questions:** %d\n", total))
		if total > 0 {
			pct := correct * 100 / total
			avg := float64(confSum) / float64(total)
			b.WriteString(fmt.Sprintf("**Score:** %d%% (%d correct, %d wrong)\n", pct, correct, wrong))
			b.WriteString(fmt.Sprintf("**Avg Confidence:** %.1f\n", avg))
		}

		var domainList []string
		for d := range domains {
			domainList = append(domainList, d)
		}
		if len(domainList) > 0 {
			b.WriteString(fmt.Sprintf("**Domains:** %s\n", strings.Join(domainList, ", ")))
		}

		if len(wrongs) > 0 {
			b.WriteString("\n## Review These\n\n")
			for _, w := range wrongs {
				b.WriteString(fmt.Sprintf("### %s (%s)\n\n", w.file, w.qtype))
				b.WriteString(fmt.Sprintf("**Q:** %s\n\n", w.question))
				b.WriteString(fmt.Sprintf("**A:** %s\n\n", w.answer))
			}
		}

		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".local", "share", "unrot", "reports")
		os.MkdirAll(dir, 0755)
		filename := fmt.Sprintf("session-%s.md", time.Now().Format("2006-01-02-150405"))
		path := filepath.Join(dir, filename)
		os.WriteFile(path, []byte(b.String()), 0644)

		return reportSavedMsg{path: path}
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
		if m.conceptChatLoading && m.activeOverlay == overlayChat {
			m.syncOverlayViewport()
		}
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
		m.showNotes = false
		m.ratedConfidence = 0
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
		m.explainLoading = false
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
		m.conceptChatLoading = false
		m.conceptChat = append(m.conceptChat, chatEntry{role: "assistant", content: msg.text})
		// Stay in overlay — just rebuild overlay content
		m.syncOverlayViewport()
		return m, nil

	case learnChatMsg:
		m.learnChatHistory = append(m.learnChatHistory, chatEntry{role: "assistant", content: msg.text})
		m.learnChatLoading = false
		return m, nil

	case learnContentMsg:
		m.learnContent = msg.content
		if msg.domain != "" {
			m.learnDomain = msg.domain
		}
		if msg.slug != "" {
			m.learnSlug = msg.slug
		}
		m.learnStep = learnReview
		m.syncViewport()
		return m, nil

	case learnSavedMsg:
		m.currentFile = msg.relPath
		// Only add to allFiles if this is a new file (not an update)
		if m.learnUpdateFile == "" {
			m.allFiles = append(m.allFiles, msg.relPath)
		}
		// Award XP for learning
		m.state.AwardBonusXP(50)
		m.xpGained = 50
		newAch := m.state.CheckAchievements(0, 6, 0, 0)
		if m.state.UnlockAchievement(state.AchScholar) {
			newAch = append(newAch, state.AchScholar)
		}
		if len(newAch) > 0 {
			info := state.AchievementInfo[newAch[0]]
			m.toast = fmt.Sprintf("achievement unlocked: %s — %s", info.Name, info.Desc)
		}
		m.state.Save()
		m.phase = phaseQuiz
		m.quizStep = stepLoading
		return m, generateQuestion(m.ollama, m.brainPath, msg.relPath, m.randomActiveType(), ollama.DiffBasic)

	case reportSavedMsg:
		m.reportPath = msg.path
		m.syncViewport()
		return m, nil

	case notesSavedMsg:
		m.sourceContent = msg.content
		m.syncViewport()
		return m, nil

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
		case phaseTopicList:
			if msg.Button == tea.MouseButtonWheelUp && m.pickCursor > 0 {
				m.pickCursor--
			} else if msg.Button == tea.MouseButtonWheelDown && m.pickCursor < len(m.pickFiles)-1 {
				m.pickCursor++
			}
			return m, nil
		case phaseQuiz:
			switch m.quizStep {
			case stepLesson, stepResult:
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			case stepQuestion:
				if m.showNotes {
					var cmd tea.Cmd
					m.viewport, cmd = m.viewport.Update(msg)
					return m, cmd
				}
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
		m.openDomainOverlay()
		return m, nil
	case "t":
		m.openQuizTypeOverlay()
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
		m.openDomainOverlay()
		return m, nil
	case "esc":
		m.goHome()
	}
	return m, nil
}

func (m *model) startPickDrill() tea.Cmd {
	file := m.pickFiles[m.pickCursor]
	m.pickMode = true
	m.reviewFiles = nil
	m.fileIdx = 0
	m.sessionCorrect = 0
	m.sessionWrong = 0
	m.sessionTotal = 0
	m.sessionConfSum = 0
	m.sessionWrongs = nil
	m.sessionStart = time.Now()
	m.sessionDomains = make(map[string]bool)
	m.retryQueue = nil
	m.retryPhase = false
	m.retryCount = nil
	m.nextQ = nil
	m.nextFile = ""
	m.ratedConfidence = 0
	m.sessionMinConf = 6
	m.learnChatCount = 0
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
			m.showNotes = false
			m.nextQ = nil
			m.nextFile = ""
			m.quizStep = stepQuestion
			return m, m.prefetchNext()
		}
		diff := ollama.DifficultyFromConfidence(m.currentConfidence())
		m.quizStep = stepLoading
		return m, generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	case "c":
		m.chatFromLesson = true
		m.answerTA.Reset()
		m.answerTA.SetHeight(2)
		m.answerTA.Placeholder = "ask about this concept..."
		m.answerTA.Focus()
		m.activeOverlay = overlayChat
		m.syncOverlayViewport()
		return m, nil
	case "n":
		if m.currentFile != "" {
			notes := knowledge.ExtractNotes(m.sourceContent)
			m.answerTA.SetHeight(10)
			m.answerTA.CharLimit = 2000
			m.answerTA.Placeholder = "add notes about this topic..."
			m.answerTA.SetValue(notes)
			m.answerTA.Focus()
			m.activeOverlay = overlayNotes
		}
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
	m.toast = "" // clear toast on any keypress

	// --- Notes view mode: single-key actions, scrollable, no textarea ---
	if m.showNotes {
		switch key {
		case "tab":
			m.showNotes = false
			return m, nil
		case "n":
			if m.currentFile != "" {
				notes := knowledge.ExtractNotes(m.sourceContent)
				m.answerTA.SetHeight(10)
				m.answerTA.CharLimit = 2000
				m.answerTA.Placeholder = "add notes about this topic..."
				m.answerTA.SetValue(notes)
				m.answerTA.Focus()
				m.activeOverlay = overlayNotes
			}
			return m, nil
		case "c":
			m.chatFromLesson = false
			m.answerTA.Reset()
			m.answerTA.SetHeight(2)
			m.answerTA.Placeholder = "ask about this concept..."
			m.answerTA.Focus()
			m.activeOverlay = overlayChat
			m.syncOverlayViewport()
			return m, nil
		case "h":
			m.quizStep = stepGrading
			m.showNotes = false
			return m, hintCmd(m.ollama, m.currentQ.Text, m.currentQ.Answer, m.hints)
		case "esc":
			m.showNotes = false
			return m, nil
		default:
			// Scroll viewport
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}

	// --- Question view ---

	// Tab toggles to notes view (intercepted before textarea)
	if key == "tab" {
		m.showNotes = true
		// Build notes content in viewport
		m.viewport.SetContent(renderMarkdown(m.sourceContent, m.wrapW()))
		m.viewport.GotoTop()
		return m, nil
	}

	// Hint: h for MC, ctrl+e for typed
	if key == "ctrl+e" || (key == "h" && m.currentQ.Type == ollama.TypeMultiChoice) {
		m.quizStep = stepGrading
		return m, hintCmd(m.ollama, m.currentQ.Text, m.currentQ.Answer, m.hints)
	}

	// Chat: c for MC, ctrl+y for typed
	if key == "ctrl+y" || (key == "c" && m.currentQ.Type == ollama.TypeMultiChoice) {
		m.chatFromLesson = false
		m.answerTA.Reset()
		m.answerTA.SetHeight(2)
		m.answerTA.Placeholder = "ask about this concept..."
		m.answerTA.Focus()
		m.activeOverlay = overlayChat
		m.syncOverlayViewport()
		return m, nil
	}

	// Notes: n for MC (single key safe)
	if key == "n" && m.currentQ.Type == ollama.TypeMultiChoice {
		if m.currentFile != "" {
			notes := knowledge.ExtractNotes(m.sourceContent)
			m.answerTA.SetHeight(10)
			m.answerTA.CharLimit = 2000
			m.answerTA.Placeholder = "add notes about this topic..."
			m.answerTA.SetValue(notes)
			m.answerTA.Focus()
			m.activeOverlay = overlayNotes
		}
		return m, nil
	}

	// Regenerate question
	if key == "ctrl+r" {
		diff := ollama.DifficultyFromConfidence(m.currentConfidence())
		m.quizStep = stepLoading
		m.hints = nil
		m.showNotes = false
		return m, generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
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
			m.ratedConfidence = 0
			m.showNotes = false
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

	// Non-MC: typed answer → straight to result with confidence picker
	switch key {
	case "enter":
		m.userAnswer = strings.TrimSpace(m.answerTA.Value())
		m.answerTA.Reset()
		m.ratedConfidence = 0
		m.showNotes = false
		m.quizStep = stepResult
		m.syncViewport()
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

func (m model) handleResult(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	m.toast = "" // clear toast on any keypress
	switch key {
	case "1", "2", "3", "4", "5":
		if m.ratedConfidence > 0 {
			return m, nil // already rated
		}
		conf := int(key[0] - '0')
		m.ratedConfidence = conf
		m.state.SetConfidence(m.currentFile, conf)
		m.sessionConfSum += conf
		if conf < m.sessionMinConf {
			m.sessionMinConf = conf
		}

		if m.currentQ.Type == ollama.TypeMultiChoice {
			correct := m.grade == gradeCorrect
			m.state.Record(m.currentFile, correct)
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
		} else {
			m.sessionTotal++
			switch {
			case conf >= 4:
				m.grade = gradeCorrect
				m.state.Record(m.currentFile, true)
				m.sessionCorrect++
			case conf == 3:
				m.grade = gradePartial
				m.state.Record(m.currentFile, true)
				m.sessionCorrect++
			default:
				m.grade = gradeWrong
				m.state.Record(m.currentFile, false)
				m.sessionWrong++
				m.sessionWrongs = append(m.sessionWrongs, wrongItem{
					file:     m.currentFile,
					question: m.currentQ.Text,
					answer:   m.currentQ.Answer,
					qtype:    m.currentQ.Type.String(),
				})
				m.enqueueRetry(m.currentFile)
			}
		}

		// Award XP
		diffLevel := int(m.currentQ.Difficulty)
		xp := state.CalcXP(conf, diffLevel, m.state.DayStreak)
		m.state.AwardXP(xp)
		m.xpGained = xp

		// Check achievements
		newAch := m.state.CheckAchievements(
			m.sessionTotal, m.sessionMinConf,
			time.Since(m.sessionStart), m.learnChatCount,
		)
		if len(newAch) > 0 {
			info := state.AchievementInfo[newAch[0]]
			m.toast = fmt.Sprintf("achievement unlocked: %s — %s", info.Name, info.Desc)
		}

		m.state.Save()
		m.syncViewport()
		return m, nil
	case "r":
		// Re-quiz: generate a new question on the same topic
		diff := ollama.DifficultyFromConfidence(m.currentConfidence())
		m.quizStep = stepLoading
		m.hints = nil
		m.ratedConfidence = 0
		m.activeOverlay = overlayNone
		return m, generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	case "e":
		m.explainLoading = true
		m.syncViewport()
		return m, explainMoreCmd(m.ollama, m.currentQ.Text, m.currentQ.Answer, m.currentQ.Explanation)
	case "n":
		if m.currentFile != "" {
			notes := knowledge.ExtractNotes(m.sourceContent)
			m.answerTA.SetHeight(10)
			m.answerTA.CharLimit = 2000
			m.answerTA.Placeholder = "add notes about this topic..."
			m.answerTA.SetValue(notes)
			m.answerTA.Focus()
			m.activeOverlay = overlayNotes
		}
		return m, nil
	case "c":
		m.chatFromLesson = false
		m.answerTA.Reset()
		m.answerTA.SetHeight(2)
		m.answerTA.Placeholder = "ask about this concept..."
		m.answerTA.Focus()
		m.activeOverlay = overlayChat
		m.syncOverlayViewport()
		return m, nil
	case "k":
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
		if m.ratedConfidence == 0 {
			return m, nil // must rate confidence first
		}
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
		case "esc", "k":
			m.activeOverlay = overlayNone
			return m, nil
		default:
			var cmd tea.Cmd
			m.overlayViewport, cmd = m.overlayViewport.Update(msg)
			return m, cmd
		}

	case overlayDomain:
		switch key {
		case "tab", "j", "down":
			m.cycleDomainFilter()
			m.syncDomainOverlay()
			return m, nil
		case "shift+tab", "k", "up":
			m.cycleDomainFilterReverse()
			m.syncDomainOverlay()
			return m, nil
		case "enter":
			m.activeOverlay = overlayNone
			if m.phase == phaseTopicList {
				m.filterPickFiles()
			}
			return m, nil
		case "esc":
			m.domainCursor = m.domainCursorPrev
			m.applyDomainCursor()
			m.activeOverlay = overlayNone
			return m, nil
		}
		return m, nil

	case overlayQuizType:
		switch key {
		case "j", "down":
			m.typeCursor = (m.typeCursor + 1) % len(m.activeTypes)
			m.syncQuizTypeOverlay()
			return m, nil
		case "k", "up":
			m.typeCursor--
			if m.typeCursor < 0 {
				m.typeCursor = len(m.activeTypes) - 1
			}
			m.syncQuizTypeOverlay()
			return m, nil
		case "enter", " ":
			m.activeTypes[m.typeCursor] = !m.activeTypes[m.typeCursor]
			// Don't allow disabling all types
			anyOn := false
			for _, on := range m.activeTypes {
				if on {
					anyOn = true
					break
				}
			}
			if !anyOn {
				m.activeTypes[m.typeCursor] = true
			}
			m.syncQuizTypeOverlay()
			return m, nil
		case "esc":
			m.activeOverlay = overlayNone
			return m, nil
		}
		return m, nil

	case overlayNotes:
		switch key {
		case "ctrl+s":
			notes := strings.TrimSpace(m.answerTA.Value())
			m.activeOverlay = overlayNone
			m.answerTA.SetHeight(5)
			m.answerTA.CharLimit = 500
			m.answerTA.Placeholder = "type your answer..."
			return m, saveNotesCmd(m.brainPath, m.currentFile, notes)
		case "esc":
			m.activeOverlay = overlayNone
			m.answerTA.SetHeight(5)
			m.answerTA.CharLimit = 500
			m.answerTA.Placeholder = "type your answer..."
			return m, nil
		default:
			var cmd tea.Cmd
			m.answerTA, cmd = m.answerTA.Update(msg)
			return m, cmd
		}

	case overlayChat:
		switch key {
		case "esc":
			m.activeOverlay = overlayNone
			m.conceptChatLoading = false
			m.answerTA.SetHeight(5)
			m.answerTA.Placeholder = "type your answer..."
			return m, nil
		case "enter":
			question := strings.TrimSpace(m.answerTA.Value())
			if question == "" {
				return m, nil
			}
			m.conceptChat = append(m.conceptChat, chatEntry{role: "user", content: question})
			m.conceptChatLoading = true
			m.learnChatCount++
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
			m.learnTopic = topic
			parts := strings.SplitN(topic, "/", 2)
			if len(parts) == 2 {
				m.learnDomain = strings.TrimSpace(parts[0])
				m.learnSlug = slugify(strings.TrimSpace(parts[1]))
			} else {
				m.learnDomain = "general"
				m.learnSlug = slugify(topic)
			}
			// Deterministic check: does this topic match an existing file?
			m.learnChatHistory = nil
			m.learnChatLoading = true
			m.learnUpdateFile = "" // reset from previous learn
			if match := m.findMatchingFile(topic); match != "" {
				m.learnUpdateFile = match
				m.learnDomain = knowledge.Domain(match)
				base := filepath.Base(match)
				m.learnSlug = strings.TrimSuffix(base, filepath.Ext(base))
			}
			m.learnStep = learnChat
			m.learnTA.Reset()
			m.learnTA.Placeholder = "answer or press g to generate..."
			m.learnTA.Focus()
			return m, learnClarifyCmd(m.ollama, topic, nil, m.allFiles)
		default:
			var cmd tea.Cmd
			m.learnTA, cmd = m.learnTA.Update(msg)
			return m, cmd
		}

	case learnChat:
		if m.learnChatLoading {
			if key == "esc" {
				m.learnChatLoading = false
				m.learnStep = learnInput
				m.learnTA.Reset()
				m.learnTA.Placeholder = "e.g. docker/multi-stage-builds"
				m.learnTA.Focus()
				return m, nil
			}
			return m, nil
		}
		switch key {
		case "g":
			// Generate knowledge from conversation context
			m.learnStep = learnGenerating
			var files []string
			if !strings.Contains(m.learnTopic, "/") && m.learnUpdateFile == "" {
				files = m.allFiles
			}
			return m, generateKnowledgeFromChat(m.ollama, m.learnTopic, m.learnChatHistory, files, m.brainPath, m.learnUpdateFile)
		case "enter":
			response := strings.TrimSpace(m.learnTA.Value())
			if response == "" {
				return m, nil
			}
			m.learnChatHistory = append(m.learnChatHistory, chatEntry{role: "user", content: response})
			m.learnTA.Reset()
			m.learnTA.Focus()
			m.learnChatLoading = true
			return m, learnClarifyCmd(m.ollama, m.learnTopic, m.learnChatHistory, m.allFiles)
		case "esc":
			m.learnStep = learnInput
			m.learnTA.Reset()
			m.learnTA.Placeholder = "e.g. docker/multi-stage-builds"
			m.learnTA.Focus()
			m.learnChatHistory = nil
			m.learnUpdateFile = ""
			return m, nil
		default:
			var cmd tea.Cmd
			m.learnTA, cmd = m.learnTA.Update(msg)
			return m, cmd
		}

	case learnGenerating:
		if key == "esc" {
			m.learnStep = learnInput
			m.learnTA.Reset()
			m.learnTA.Placeholder = "e.g. docker/multi-stage-builds"
			m.learnTA.Focus()
			return m, nil
		}

	case learnReview:
		switch key {
		case "s":
			return m, saveKnowledge(m.brainPath, m.learnDomain, m.learnSlug, m.learnContent)
		case "r":
			m.learnStep = learnGenerating
			var files []string
			if !strings.Contains(m.learnTopic, "/") && m.learnUpdateFile == "" {
				files = m.allFiles
			}
			return m, generateKnowledgeFromChat(m.ollama, m.learnTopic, m.learnChatHistory, files, m.brainPath, m.learnUpdateFile)
		case "esc":
			m.learnTA.Reset()
			m.learnTA.Placeholder = "e.g. docker/multi-stage-builds"
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
	case "w":
		if m.sessionTotal > 0 {
			return m, m.exportReportCmd()
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

	m.reviewFiles = m.state.FilesByConfidence(files)

	if m.maxQuestions > 0 && len(m.reviewFiles) > m.maxQuestions {
		m.reviewFiles = m.reviewFiles[:m.maxQuestions]
	}

	m.fileIdx = 0
	m.pickMode = false
	m.sessionCorrect = 0
	m.sessionWrong = 0
	m.sessionTotal = 0
	m.sessionConfSum = 0
	m.sessionWrongs = nil
	m.sessionStart = time.Now()
	m.sessionDomains = make(map[string]bool)
	m.retryQueue = nil
	m.retryPhase = false
	m.retryCount = nil
	m.nextQ = nil
	m.nextFile = ""
	m.ratedConfidence = 0
	m.sessionMinConf = 6
	m.learnChatCount = 0
	m.phase = phaseQuiz

	return m.startFile(m.reviewFiles[0])
}

// startFile begins quizzing on a file. Always shows knowledge content first.
func (m *model) startFile(file string) tea.Cmd {
	m.currentFile = file
	m.sourceContent = ""
	m.conceptChat = nil
	m.activeOverlay = overlayNone
	return loadLesson(m.brainPath, file)
}

// skipToNextFile advances past the current file without answering.
func (m *model) skipToNextFile() tea.Cmd {
	m.fileIdx++
	if m.fileIdx < len(m.reviewFiles) {
		return m.startFile(m.reviewFiles[m.fileIdx])
	}
	return m.finishSession()
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
		diff := ollama.DifficultyFromConfidence(m.state.GetConfidence(m.currentFile))
		m.quizStep = stepLoading
		m.ratedConfidence = 0
		return generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	}

	m.fileIdx++
	if m.fileIdx < len(m.reviewFiles) {
		return m.startFile(m.reviewFiles[m.fileIdx])
	}
	if len(m.retryQueue) > 0 {
		m.retryPhase = true
		file := m.retryQueue[0]
		m.retryQueue = m.retryQueue[1:]
		m.currentFile = file
		m.conceptChat = nil
		diff := ollama.DifficultyFromConfidence(m.state.GetConfidence(file))
		m.quizStep = stepLoading
		m.ratedConfidence = 0
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
	if nextIdx >= len(m.reviewFiles) {
		return nil
	}
	nextFile := m.reviewFiles[nextIdx]
	diff := ollama.DifficultyFromConfidence(m.state.GetConfidence(nextFile))
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
