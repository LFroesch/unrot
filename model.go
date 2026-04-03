package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/LFroesch/unrot/internal/knowledge"
	"github.com/LFroesch/unrot/internal/ollama"
	"github.com/LFroesch/unrot/internal/state"
)

// Terminal size constraints
const (
	minTerminalWidth  = 50
	minTerminalHeight = 15
)

// --- Phases (9 total) ---

type phase int

const (
	phaseDashboard phase = iota // home screen with stats, due count, domain filter
	phaseTopicList              // browse/search topics (pick mode + domain filtering)
	phaseQuiz                   // question → answer → grade → explanation → next
	phaseLearn                  // learn input + generation + review
	phaseStats                  // full stats view
	phaseSettings               // settings page (quiz types, etc.)
	phaseChallenge              // coding challenge mode (not tied to knowledge files)
	phaseViewer                 // read-only knowledge file viewer
	phaseDone                   // session recap
	phaseError                  // error display
)

// --- Quiz sub-states ---

type quizStep int

const (
	stepLesson   quizStep = iota // showing knowledge file before quiz
	stepLoading                  // spinner while generating question
	stepQuestion                 // showing question, waiting for answer
	stepGrading                  // spinner while evaluating
	stepResult                   // showing verdict + confidence picker
	stepSessionContinue          // session goal reached — offer more questions (arcade “continue”)
)

// --- Learn sub-states ---

type learnStep int

const (
	learnInput      learnStep = iota // text input for topic
	learnChat                        // conversational clarification with ollama
	learnGenerating                  // spinner while generating
	learnReview                      // viewport with generated content
)

// --- Challenge sub-states ---

type challengeStep int

const (
	challengeInput   challengeStep = iota // describe what to practice (learnTA)
	challengeChat                         // conversational clarification  with ollama
	challengeLoading                      // generating challenge (spinner)
	challengeWorking                      // user writing code (textarea)
	challengeGrading                      // ollama evaluating (spinner)
	challengeResult                       // showing feedback + score
)

// --- Question tabs (during stepQuestion) ---

type questionTab int

const (
	qTabQuiz      questionTab = iota // question + answer input
	qTabChat                         // inline concept chat
	qTabKnowledge                    // source material
)

// --- Challenge tabs (during challengeWorking/challengeResult) ---

type challengeTabType int

const (
	cTabCode        challengeTabType = iota // code textarea (working) or submitted code (result)
	cTabChat                                // inline concept chat
	cTabProblem                             // full problem description
	cTabExplanation                         // feedback + grade (result only)
)

// --- Overlay modes ---

type overlayType int

const (
	overlayNone      overlayType = iota
	overlayKnowledge             // k: full knowledge file, scrollable (result step)
	overlayChat                  // c: multi-turn Ollama chat (result/lesson step)
	overlayDomain                // tab: domain picker
	overlayNotes                 // n: edit notes for current file
)

// wrongItem captures a wrong answer for the end-of-session recap.
type wrongItem struct {
	file     string
	question string
	answer   string
	qtype    string
}

type gradeKind int

const (
	gradeCorrect gradeKind = iota
	gradePartial
	gradeWrong
)

type chatEntry struct {
	role       string // "user" or "assistant"
	content    string
	durationMs int64 // ms to receive response (assistant only)
}

// Chat slash commands — typed in chat textarea, expanded before sending to ollama
var chatSlashCmds = map[string]string{
	"/examples": "explain this with concrete examples — show real code or real input→output",
	"/eli5":     "explain this like I'm five — use a simple analogy, no jargon",
	"/gotchas":  "what are the common mistakes, gotchas, or edge cases with this?",
	"/compare":  "compare this with similar or related concepts — what's different and why?",
	"/why":      "why does this matter in practice? when would I actually use this?",
	"/deep":     "go deeper — explain the underlying mechanism and internals",
}

type model struct {
	// Config
	brainPath    string
	ollama       *ollama.Client
	state        *state.State
	cliDomain    string // CLI arg: only quiz this domain (immutable)
	domainFilter string // active domain filter (set by tab cycling or CLI)
	maxQuestions int    // -n flag: max questions per session

	// All discovered files
	allFiles []string
	depGraph *knowledge.DepGraph

	// Current phase + sub-states
	phase     phase
	quizStep  quizStep
	learnStep learnStep

	// Overlay
	activeOverlay   overlayType
	overlayViewport viewport.Model

	// Quiz state
	reviewFiles     []string // files for review (sorted by confidence)
	fileIdx         int
	currentQ        *ollama.Question
	currentFile     string
	lastQType       ollama.QuestionType // cached from last question (persists through loading/lesson)
	lastQDiff       ollama.Difficulty   // cached from last question
	lastQSet        bool                // true once a question has been received this session
	grade           gradeKind           // gradeCorrect, gradePartial, gradeWrong
	ratedConfidence int                 // 0=not yet rated, 1-5=user confidence pick
	mcPicked        int                 // multiple choice: selected option index
	mcEliminated    [4]bool             // MC options eliminated by wrong picks
	mcWrongPicks    int                 // wrong MC attempts before reveal (max 2)
	answerRevealed  bool                // typed: true once answer/explanation should show
	gradeFeedback   string              // ollama's feedback on typed answer
	retryQueue      []string
	retryPhase      bool // true when working through retry queue
	retryCount      map[string]int
	reviewQueueCapped bool // true when review list was truncated to maxQuestions (smart review from dashboard)

	// Topic list (browse/pick)
	pickMode      bool
	viewerMode    bool // true when browsing for reading (v), not quizzing
	pickCursor    int
	pickFiles     []string
	pickSearch    textinput.Model
	pickSearching bool

	// Domain filter (overlay picker — tab to open, tab/shift+tab to cycle)
	domainCursor     int
	domainList       []string // "all" at index 0, then discovered domains
	domainCursorPrev int      // saved cursor before overlay opens (for esc revert)

	// Question type picker
	typeCursor  int
	activeTypes []bool // which types are enabled (indexed by AllTypes)

	// Settings page
	settingsCursor  int
	settingsEditing bool // true when editing brain path in settings

	// Typed answer — textarea
	answerTA textarea.Model
	// Learn topic — textarea
	learnTA textarea.Model

	// Hint state
	hints      []string // progressive hints from ctrl+e during question
	userAnswer string   // what the user typed before reveal

	// Learn mode
	learnContent    string // generated knowledge content
	learnDomain     string // domain for saving
	learnSlug       string // filename slug
	learnUpdateFile string // non-empty = updating existing file instead of creating new

	// Teach-first / source view
	sourceContent string      // current file's knowledge content
	questionTab   questionTab // active tab during stepQuestion (quiz/chat/knowledge)

	// Concept chat (overlay)
	conceptChat        []chatEntry
	conceptChatLoading bool // waiting for ollama response
	chatFromLesson     bool // true if chat was entered from lesson step

	// Learn chat (conversational flow before generating)
	learnChatHistory []chatEntry
	learnTopic       string // original topic input
	learnChatLoading bool   // waiting for ollama response

	// Explain-more inline loading
	explainLoading bool

	// Knowledge audit + fix
	auditLoading    bool   // waiting for ollama audit response
	auditResult     string // audit verdict (shown in knowledge overlay/tab)
	auditFixPending string // corrected file content waiting for user confirmation
	auditFixLoading bool   // generating the fix

	// Bank notes preview (ctrl+b flow)
	bankPending string // generated notes waiting for user confirmation
	bankLoading bool   // waiting for ollama to generate summary

	// Saved textarea content across tab switches
	savedQuizInput     string // quiz answer textarea content saved when switching away
	savedChatInput     string // chat textarea content saved when switching away
	savedChallengeCode string // challenge code textarea content saved when switching away

	// XP / achievements
	toast          string            // achievement notification (cleared on next keypress)
	xpGained       int               // XP gained this action (for display)
	xpBreakdown    state.XPBreakdown // breakdown of last XP award (for display)
	sessionMinConf int               // minimum confidence rated this session (for perfect session achievement)
	learnChatCount int               // questions asked in current lesson chat (for deep dive achievement)
	levelUpFrom    int               // previous level before XP award (0 = no level up pending)
	comboCount     int               // consecutive correct answers (reset on wrong)
	comboMax       int               // best combo this session

	// Challenge mode
	challengeStep    challengeStep
	challengeTab     challengeTabType // active tab during working/result
	currentChallenge *ollama.Challenge
	challengeGrade   *ollama.ChallengeGrade
	challengeCount   int               // challenges completed this session
	challengeDiff    ollama.Difficulty // adaptive difficulty for challenges
	challengeCode    string            // submitted code (for viewing on result)
	challengeExplain string            // expanded explanation from `e` key on result

	// Daily goal
	dailyGoal int

	// Lifecycle
	loaded bool // true after first stateLoadedMsg

	// Export report
	reportPath string

	// Logging
	logFile        *os.File
	logQuestionNum int

	// Stats
	sessionCorrect int
	sessionWrong   int
	sessionConfSum int // running sum of confidence ratings
	sessionTotal   int
	sessionWrongs  []wrongItem // wrong answers for recap
	sessionStart   time.Time   // when review started
	sessionDomains map[string]bool

	// UI components
	spinner  spinner.Model
	viewport viewport.Model
	width    int
	height   int
	err      error
}

func initialModel(brainPath, domainFilter string, maxQuestions, dailyGoal int) model {
	sp := spinner.New()
	sp.Spinner = spinner.Spinner{
		Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		FPS:    time.Second / 12,
	}
	sp.Style = lipgloss.NewStyle().Foreground(colorAccent)

	ansTA := textarea.New()
	ansTA.Placeholder = "type your answer..."
	ansTA.ShowLineNumbers = false
	ansTA.SetHeight(5)
	ansTA.CharLimit = 500
	ansTA.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ansTA.FocusedStyle.Base = lipgloss.NewStyle()
	ansTA.BlurredStyle.Base = lipgloss.NewStyle()

	learnTA := textarea.New()
	learnTA.Placeholder = "e.g. docker/multi-stage-builds"
	learnTA.ShowLineNumbers = false
	learnTA.SetHeight(1)
	learnTA.CharLimit = 200
	learnTA.FocusedStyle.CursorLine = lipgloss.NewStyle()
	learnTA.FocusedStyle.Base = lipgloss.NewStyle()
	learnTA.BlurredStyle.Base = lipgloss.NewStyle()

	searchTI := textinput.New()
	searchTI.Placeholder = "filter..."
	searchTI.CharLimit = 50

	vp := viewport.New(80, 20)
	ovp := viewport.New(60, 15)

	allOn := make([]bool, len(ollama.AllTypes))
	for i := range allOn {
		allOn[i] = true
	}

	return model{
		brainPath:       brainPath,
		ollama:          ollama.New(),
		cliDomain:       domainFilter,
		domainFilter:    domainFilter,
		maxQuestions:    maxQuestions,
		dailyGoal:       dailyGoal,
		phase:           phaseDashboard,
		quizStep:        stepLoading,
		learnStep:       learnInput,
		activeOverlay:   overlayNone,
		spinner:         sp,
		answerTA:        ansTA,
		learnTA:         learnTA,
		pickSearch:      searchTI,
		viewport:        vp,
		overlayViewport: ovp,
		sessionDomains:  make(map[string]bool),
		activeTypes:     allOn,
		sessionMinConf:  6, // higher than max so first rating always sets it
	}
}

func (m model) currentDomain() string {
	if m.currentFile == "" {
		return ""
	}
	return knowledge.Domain(m.currentFile)
}

// currentConfidence returns the confidence level of the current file (0-5).
func (m model) currentConfidence() int {
	if m.state == nil || m.currentFile == "" {
		return 0
	}
	return m.state.GetConfidence(m.currentFile)
}

// randomActiveType picks a random question type from the enabled set.
func (m model) randomActiveType() ollama.QuestionType {
	var enabled []ollama.QuestionType
	for i, on := range m.activeTypes {
		if on {
			enabled = append(enabled, ollama.AllTypes[i])
		}
	}
	if len(enabled) == 0 {
		return ollama.AllTypes[rand.Intn(len(ollama.AllTypes))]
	}
	return enabled[rand.Intn(len(enabled))]
}

// wrapW returns usable text width inside the panel (accounts for padding).
func (m model) wrapW() int {
	w := m.width - 8 // 2 padding each side from panelStyle + 2 extra
	if w < 20 {
		w = 20
	}
	return w
}

// findMatchingFile does deterministic fuzzy matching of a topic against existing knowledge files.
// Returns the relative path of the best match, or "" if no match.
func (m model) findMatchingFile(topic string) string {
	topic = strings.ToLower(strings.TrimSpace(topic))
	// Strip domain/ prefix if present for matching
	bare := topic
	if parts := strings.SplitN(topic, "/", 2); len(parts) == 2 {
		bare = parts[1]
	}
	// Normalize: "multi stage builds" -> "multi-stage-builds"
	normalized := strings.ReplaceAll(bare, " ", "-")
	words := strings.Fields(strings.ReplaceAll(bare, "-", " "))

	// Pass 1: exact slug match
	for _, f := range m.allFiles {
		slug := slugFromPath(f)
		if slug == normalized {
			return f
		}
	}
	// Pass 2: all words present in slug
	var best string
	bestCount := 0
	for _, f := range m.allFiles {
		slug := slugFromPath(f)
		matched := 0
		for _, w := range words {
			if strings.Contains(slug, w) {
				matched++
			}
		}
		if matched == len(words) && matched > bestCount {
			best = f
			bestCount = matched
		}
	}
	return best
}

// slugFromPath extracts the slug (filename without extension) from a knowledge path.
func slugFromPath(relPath string) string {
	base := filepath.Base(relPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// sortByDomain returns files sorted by domain then filename.
func sortByDomain(files []string) []string {
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		di, dj := knowledge.Domain(sorted[i]), knowledge.Domain(sorted[j])
		if di != dj {
			return di < dj
		}
		return sorted[i] < sorted[j]
	})
	return sorted
}

// filterPickFiles rebuilds pickFiles from allFiles based on the search query and domain filter.
func (m *model) filterPickFiles() {
	query := strings.ToLower(strings.TrimSpace(m.pickSearch.Value()))
	var source []string
	if m.domainFilter != "" {
		source = knowledge.FilterByDomain(m.allFiles, m.domainFilter)
	} else {
		source = m.allFiles
	}
	if query != "" {
		var filtered []string
		for _, f := range source {
			if strings.Contains(strings.ToLower(f), query) {
				filtered = append(filtered, f)
			}
		}
		source = filtered
	}
	// Sort by domain, then confidence ascending within domain
	sorted := make([]string, len(source))
	copy(sorted, source)
	sort.Slice(sorted, func(i, j int) bool {
		di, dj := knowledge.Domain(sorted[i]), knowledge.Domain(sorted[j])
		if di != dj {
			return di < dj
		}
		ci, cj := 0, 0
		if m.state != nil {
			ci = m.state.GetConfidence(sorted[i])
			cj = m.state.GetConfidence(sorted[j])
		}
		if ci != cj {
			return ci < cj
		}
		return sorted[i] < sorted[j]
	})
	m.pickFiles = sorted
	if m.pickCursor >= len(m.pickFiles) {
		if len(m.pickFiles) > 0 {
			m.pickCursor = len(m.pickFiles) - 1
		} else {
			m.pickCursor = 0
		}
	}
}

// buildDomainList returns ["all", domain1, domain2, ...] sorted.
func (m *model) buildDomainList() {
	seen := make(map[string]bool)
	for _, f := range m.allFiles {
		seen[knowledge.Domain(f)] = true
	}
	domains := make([]string, 0, len(seen)+1)
	domains = append(domains, "all")
	sorted := make([]string, 0, len(seen))
	for d := range seen {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)
	domains = append(domains, sorted...)
	m.domainList = domains
	m.domainCursor = 0
}

// cycleDomainFilter cycles forward through domain options.
func (m *model) cycleDomainFilter() {
	if len(m.domainList) == 0 {
		m.buildDomainList()
	}
	m.domainCursor = (m.domainCursor + 1) % len(m.domainList)
	m.applyDomainCursor()
}

// cycleDomainFilterReverse cycles backward through domain options.
func (m *model) cycleDomainFilterReverse() {
	if len(m.domainList) == 0 {
		m.buildDomainList()
	}
	m.domainCursor--
	if m.domainCursor < 0 {
		m.domainCursor = len(m.domainList) - 1
	}
	m.applyDomainCursor()
}

// applyDomainCursor sets domainFilter based on domainCursor.
func (m *model) applyDomainCursor() {
	if m.domainCursor == 0 {
		m.domainFilter = "" // "all"
	} else {
		m.domainFilter = m.domainList[m.domainCursor]
	}
}

// openDomainOverlay saves state and opens the domain picker overlay.
func (m *model) openDomainOverlay() {
	if len(m.domainList) == 0 {
		m.buildDomainList()
	}
	m.domainCursorPrev = m.domainCursor
	m.activeOverlay = overlayDomain
	m.syncDomainOverlay()
}

// syncDomainOverlay rebuilds the domain overlay viewport and scrolls cursor into view.
func (m *model) syncDomainOverlay() {
	// Compact overlay: limit height to domain count + chrome, capped by terminal
	// Account for overlay chrome: border(2) + padding(2) + title(1) + blank lines(2) + footer(1) = 8
	maxH := m.height - 16
	if maxH < 5 {
		maxH = 5
	}
	listH := len(m.domainList)
	if listH > maxH {
		listH = maxH
	}
	m.overlayViewport.Width = 36 // inner content width
	m.overlayViewport.Height = listH
	m.overlayViewport.SetContent(m.buildDomainOverlayContent())

	// Scroll so cursor is visible
	if m.domainCursor < m.overlayViewport.YOffset {
		m.overlayViewport.SetYOffset(m.domainCursor)
	} else if m.domainCursor >= m.overlayViewport.YOffset+m.overlayViewport.Height {
		m.overlayViewport.SetYOffset(m.domainCursor - m.overlayViewport.Height + 1)
	}
}

// domainAvgConfidence returns the average confidence for files in a domain.
func (m model) domainAvgConfidence(domain string) float64 {
	if m.state == nil {
		return 0
	}
	files := m.allFiles
	if domain != "all" {
		files = knowledge.FilterByDomain(files, domain)
	}
	if len(files) == 0 {
		return 0
	}
	total := 0
	for _, f := range files {
		total += m.state.GetConfidence(f)
	}
	return float64(total) / float64(len(files))
}

// confidenceDots renders confidence as filled/empty dots with tier coloring.
func confidenceDots(level int) string {
	filled := level
	if filled < 0 {
		filled = 0
	}
	if filled > 5 {
		filled = 5
	}
	empty := 5 - filled
	color := confidenceColor(level)
	style := lipgloss.NewStyle().Foreground(color).Bold(true)
	return style.Render(strings.Repeat("●", filled)) +
		dimStyle.Render(strings.Repeat("○", empty))
}

// confidenceLabel returns a text label for a confidence level.
func confidenceLabel(level int) string {
	switch level {
	case 1:
		return "weak"
	case 2:
		return "shaky"
	case 3:
		return "okay"
	case 4:
		return "solid"
	case 5:
		return "locked"
	default:
		return "new"
	}
}

// stalenessLabel returns a human-readable staleness indicator (e.g. "3w ago").
// Returns "" for recently reviewed (<3 days) or never-reviewed files.
func stalenessLabel(days int) string {
	switch {
	case days < 0:
		return "" // never reviewed
	case days < 3:
		return ""
	case days < 7:
		return fmt.Sprintf("%dd", days)
	case days < 30:
		return fmt.Sprintf("%dw", days/7)
	case days < 365:
		return fmt.Sprintf("%dmo", days/30)
	default:
		return fmt.Sprintf("%dy", days/365)
	}
}

// renderMarkdown applies basic styling to markdown content (headers, bullets, code blocks, inline formatting).
func renderMarkdown(content string, w int) string {
	var b strings.Builder
	lines := strings.Split(content, "\n")
	inCodeBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				b.WriteString("\n")
			}
		case inCodeBlock:
			b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(4).Render(highlightSyntax(line)))
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "## "):
			header := strings.TrimPrefix(trimmed, "## ")
			b.WriteString("\n")
			b.WriteString(sectionHeaderStyle.PaddingLeft(2).Render(header))
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "# "):
			header := strings.TrimPrefix(trimmed, "# ")
			b.WriteString(titleStyle.PaddingLeft(2).Width(w).Render(header))
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "- "):
			b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(4).Render("· " + renderInline(strings.TrimPrefix(trimmed, "- "))))
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "* "):
			b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(4).Render("· " + renderInline(strings.TrimPrefix(trimmed, "* "))))
			b.WriteString("\n")
		case trimmed == "":
			b.WriteString("\n")
		default:
			b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(2).Render(renderInline(line)))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderExplanation renders explanation text with markdown formatting in the explanation color.
func renderExplanation(content string, w int) string {
	var b strings.Builder
	lines := strings.Split(content, "\n")
	inCodeBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				b.WriteString("\n")
			}
		case inCodeBlock:
			b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(4).Render(highlightSyntax(line)))
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "## "):
			header := strings.TrimPrefix(trimmed, "## ")
			b.WriteString("\n")
			b.WriteString(sectionHeaderStyle.PaddingLeft(2).Render(header))
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "# "):
			header := strings.TrimPrefix(trimmed, "# ")
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorText).PaddingLeft(2).Width(w).Render(header))
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "- "):
			b.WriteString(explainStyle.Width(w).PaddingLeft(4).Render("· " + renderInline(strings.TrimPrefix(trimmed, "- "), explainStyle)))
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "* "):
			b.WriteString(explainStyle.Width(w).PaddingLeft(4).Render("· " + renderInline(strings.TrimPrefix(trimmed, "* "), explainStyle)))
			b.WriteString("\n")
		case trimmed == "":
			b.WriteString("\n")
		default:
			b.WriteString(explainStyle.Width(w).PaddingLeft(2).Render(renderInline(line, explainStyle)))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderInline applies basic inline markdown formatting (**bold**, `code`).
// Accepts an optional base style applied to plain text segments to prevent
// ANSI reset sequences from breaking parent coloring (fixes yellow/grey alternation).
func renderInline(text string, base ...lipgloss.Style) string {
	var b strings.Builder
	var plain strings.Builder
	hasBase := len(base) > 0

	flushPlain := func() {
		if plain.Len() > 0 {
			if hasBase {
				b.WriteString(base[0].Render(plain.String()))
			} else {
				b.WriteString(plain.String())
			}
			plain.Reset()
		}
	}

	i := 0
	for i < len(text) {
		// Bold: **text**
		if i+1 < len(text) && text[i] == '*' && text[i+1] == '*' {
			end := strings.Index(text[i+2:], "**")
			if end >= 0 {
				flushPlain()
				style := lipgloss.NewStyle().Bold(true)
				if hasBase {
					style = style.Inherit(base[0])
				}
				b.WriteString(style.Render(text[i+2 : i+2+end]))
				i = i + 2 + end + 2
				continue
			}
		}
		// Inline code: `text`
		if text[i] == '`' {
			end := strings.Index(text[i+1:], "`")
			if end >= 0 {
				flushPlain()
				b.WriteString(lipgloss.NewStyle().Foreground(colorYellow).Render(text[i+1 : i+1+end]))
				i = i + 1 + end + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		plain.WriteRune(r)
		i += size
	}
	flushPlain()
	return b.String()
}

// --- Syntax highlighting for code blocks ---

var codeKeywords = map[string]bool{
	// Go
	"func": true, "return": true, "if": true, "else": true, "for": true,
	"range": true, "var": true, "const": true, "type": true, "struct": true,
	"interface": true, "switch": true, "case": true, "default": true,
	"go": true, "defer": true, "select": true, "chan": true, "map": true,
	"import": true, "package": true, "break": true, "continue": true,
	"nil": true, "true": true, "false": true, "make": true, "new": true,
	"append": true, "len": true, "cap": true, "delete": true, "close": true,
	"panic": true, "recover": true,
	// Python
	"def": true, "class": true, "self": true, "print": true, "from": true,
	"while": true, "try": true, "except": true, "finally": true, "with": true,
	"as": true, "yield": true, "lambda": true, "in": true, "not": true,
	"and": true, "or": true, "is": true, "async": true, "await": true,
	"None": true, "True": true, "False": true, "pass": true, "raise": true,
	// JS/TS
	"let": true, "function": true, "console": true, "require": true,
	"export": true, "throw": true, "catch": true, "typeof": true,
	"instanceof": true, "this": true, "null": true, "undefined": true,
	"void": true, "static": true, "extends": true, "implements": true,
	// Rust
	"fn": true, "mut": true, "pub": true, "impl": true,
	"trait": true, "enum": true, "match": true, "mod": true, "use": true,
	"crate": true, "super": true, "where": true, "unsafe": true,
	"move": true, "ref": true, "loop": true,
	// SQL (uppercase — matches convention, avoids collisions with select/from/where in other langs)
	"SELECT": true, "FROM": true, "WHERE": true, "INSERT": true, "UPDATE": true,
	"DELETE": true, "CREATE": true, "ALTER": true, "DROP": true, "TABLE": true,
	"INDEX": true, "JOIN": true, "LEFT": true, "RIGHT": true, "INNER": true,
	"OUTER": true, "ON": true, "AND": true, "OR": true, "NOT": true,
	"IN": true, "EXISTS": true, "GROUP": true, "BY": true, "ORDER": true,
	"HAVING": true, "LIMIT": true, "OFFSET": true, "AS": true, "SET": true,
	"VALUES": true, "INTO": true, "DISTINCT": true, "BETWEEN": true,
	"LIKE": true, "NULL": true, "PRIMARY": true, "KEY": true, "FOREIGN": true,
	"REFERENCES": true, "CONSTRAINT": true, "BEGIN": true, "COMMIT": true,
	"ROLLBACK": true, "TRANSACTION": true, "UNION": true, "ALL": true,
	// Shell/Bash
	"echo": true, "grep": true, "sed": true, "awk": true, "curl": true,
	"wget": true, "chmod": true, "chown": true, "mkdir": true, "sudo": true,
	"apt": true, "yum": true, "pip": true, "npm": true, "yarn": true,
	"git": true, "docker": true, "kubectl": true,
	"fi": true, "do": true, "done": true, "then": true, "elif": true,
	// Docker
	"COPY": true, "RUN": true, "CMD": true, "ENTRYPOINT": true, "EXPOSE": true,
	"ENV": true, "ARG": true, "WORKDIR": true, "VOLUME": true,
	// HTML/CSS (distinct enough to not collide with variable names)
	"DOCTYPE": true, "getElementById": true, "querySelector": true,
	"addEventListener": true, "innerHTML": true, "className": true,
	// Shared
	"string": true, "int": true, "float": true, "bool": true, "byte": true,
	"error": true, "fmt": true, "os": true,
}

// highlightSyntax applies basic keyword/string/comment/number coloring to a code line.
func highlightSyntax(line string) string {
	var b strings.Builder
	i := 0
	for i < len(line) {
		// Comments: // or #
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '/' {
			b.WriteString(dimStyle.Render(line[i:]))
			return b.String()
		}
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			b.WriteString(dimStyle.Render(line[i:]))
			return b.String()
		}
		// Strings
		if line[i] == '"' || line[i] == '\'' || line[i] == '`' {
			quote := line[i]
			end := strings.IndexByte(line[i+1:], quote)
			if end >= 0 {
				b.WriteString(lipgloss.NewStyle().Foreground(colorPrimary).Render(line[i : i+2+end]))
				i = i + 2 + end
				continue
			}
		}
		// Identifiers / keywords
		if isCodeLetter(line[i]) {
			j := i
			for j < len(line) && (isCodeLetter(line[j]) || isCodeDigit(line[j])) {
				j++
			}
			word := line[i:j]
			if codeKeywords[word] {
				b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(word))
			} else {
				b.WriteString(lipgloss.NewStyle().Foreground(colorYellow).Render(word))
			}
			i = j
			continue
		}
		// Numbers
		if isCodeDigit(line[i]) {
			j := i
			for j < len(line) && (isCodeDigit(line[j]) || line[j] == '.' || line[j] == 'x') {
				j++
			}
			b.WriteString(lipgloss.NewStyle().Foreground(colorWarn).Render(line[i:j]))
			i = j
			continue
		}
		// Operators / punctuation — subtle
		b.WriteString(lipgloss.NewStyle().Foreground(colorYellow).Render(string(line[i])))
		i++
	}
	return b.String()
}

func isCodeLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func isCodeDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// rollCasinoBonus generates a random XP bonus with tiered drop rates.
// Combo count amplifies the bonus. Returns bonus amount and whether jackpot was hit.
func rollCasinoBonus(comboCount int) (bonus int, hitJackpot bool) {
	bonus = 3 + rand.Intn(6) // 3-8 always
	roll := rand.Intn(100)
	if roll < 3 {
		bonus += 50 + rand.Intn(51)
		hitJackpot = true
	} else if roll < 15 {
		bonus += 20 + rand.Intn(21)
	} else if roll < 35 {
		bonus += 8 + rand.Intn(13)
	}
	if comboCount >= 2 {
		bonus = int(float64(bonus) * (1.0 + float64(comboCount)*0.15))
	}
	return bonus, hitJackpot
}

// openNotesOverlay opens the notes editor overlay for the current file.
func (m *model) openNotesOverlay() {
	if m.currentFile == "" {
		return
	}
	notes := knowledge.ExtractNotes(m.sourceContent)
	m.answerTA.SetHeight(10)
	m.answerTA.CharLimit = 2000
	m.answerTA.Placeholder = "add notes about this topic..."
	m.answerTA.SetValue(notes)
	m.answerTA.Focus()
	m.activeOverlay = overlayNotes
}

// divider renders a labeled section divider line.
func divider(label string, w int) string {
	if w < 10 {
		w = 10
	}
	w -= 4 // padding
	if label == "" {
		return dimStyle.Render("  " + strings.Repeat("─", w))
	}
	text := " " + label + " "
	remaining := w - len(text) - 2
	if remaining < 2 {
		remaining = 2
	}
	return dimStyle.Render("  ──" + text + strings.Repeat("─", remaining))
}
