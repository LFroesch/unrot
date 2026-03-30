package main

import (
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/LFroesch/unrot/internal/knowledge"
	"github.com/LFroesch/unrot/internal/ollama"
	"github.com/LFroesch/unrot/internal/state"
)

// --- Phases (7 total) ---

type phase int

const (
	phaseDashboard phase = iota // home screen with stats, due count, domain filter
	phaseTopicList              // browse/search topics (pick mode + domain filtering)
	phaseQuiz                   // question → answer → grade → explanation → next
	phaseLearn                  // learn input + generation + review
	phaseStats                  // full stats view
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
)

// --- Learn sub-states ---

type learnStep int

const (
	learnInput      learnStep = iota // text input for topic
	learnChat                        // conversational clarification with ollama
	learnGenerating                  // spinner while generating
	learnReview                      // viewport with generated content
)

// --- Overlay modes ---

type overlayType int

const (
	overlayNone      overlayType = iota
	overlayKnowledge             // ctrl+j: full knowledge file, scrollable
	overlayChat                  // c: multi-turn Ollama chat
	overlayDomain                // tab: domain picker
	overlayNotes                 // n: edit notes for current file
	overlayQuizType              // t: quiz type picker
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
	role    string // "user" or "assistant"
	content string
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

	// Current phase + sub-states
	phase     phase
	quizStep  quizStep
	learnStep learnStep

	// Overlay
	activeOverlay   overlayType
	overlayViewport viewport.Model

	// Quiz state
	reviewFiles []string // files for review (sorted by confidence)
	fileIdx     int
	currentQ    *ollama.Question
	currentFile string
	grade           gradeKind // gradeCorrect, gradePartial, gradeWrong
	ratedConfidence int       // 0=not yet rated, 1-5=user confidence pick
	mcPicked        int       // multiple choice: selected option index
	retryQueue  []string
	retryPhase  bool // true when working through retry queue
	retryCount  map[string]int


	// Topic list (browse/pick)
	pickMode      bool
	pickCursor    int
	pickFiles     []string
	pickSearch    textinput.Model
	pickSearching bool

	// Domain filter (overlay picker — tab to open, tab/shift+tab to cycle)
	domainCursor     int
	domainList       []string // "all" at index 0, then discovered domains
	domainCursorPrev int      // saved cursor before overlay opens (for esc revert)

	// Question type picker (inline in topic list)
	typeCursor  int
	activeTypes []bool // which types are enabled (indexed by AllTypes)

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
	sourceContent string // current file's knowledge content
	showNotes     bool   // tab toggle: show notes during question

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

	// XP / achievements
	toast    string // achievement notification (cleared on next keypress)
	xpGained int    // XP gained this action (for display)
	sessionMinConf int // minimum confidence rated this session (for perfect session achievement)
	learnChatCount int // questions asked in current lesson chat (for deep dive achievement)

	// Daily goal
	dailyGoal int

	// Export report
	reportPath string

	// Prefetch
	nextQ    *ollama.Question // pre-cached next question
	nextFile string           // file the prefetch is for

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
	sp.Spinner = spinner.Dot
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
	maxH := m.height - 10
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

// openQuizTypeOverlay opens the quiz type picker overlay.
func (m *model) openQuizTypeOverlay() {
	m.typeCursor = 0
	m.activeOverlay = overlayQuizType
	m.syncQuizTypeOverlay()
}

// syncQuizTypeOverlay rebuilds the quiz type overlay viewport.
func (m *model) syncQuizTypeOverlay() {
	m.overlayViewport.Width = 36
	m.overlayViewport.Height = len(ollama.AllTypes)
	m.overlayViewport.SetContent(m.buildQuizTypeOverlayContent())
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

// renderMarkdown applies basic styling to markdown content (headers, bullets).
func renderMarkdown(content string, w int) string {
	var b strings.Builder
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
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
			b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(4).Render("· " + strings.TrimPrefix(trimmed, "- ")))
			b.WriteString("\n")
		case strings.HasPrefix(trimmed, "```"):
			// skip code fence markers
		case trimmed == "":
			b.WriteString("\n")
		default:
			b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(2).Render(line))
			b.WriteString("\n")
		}
	}
	return b.String()
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
