package main

import (
	"math/rand"
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
	stepRevealed                 // reveal mode: show answer, self-grade y/s/n
	stepResult                   // showing verdict + explanation + grade buttons
)

// --- Learn sub-states ---

type learnStep int

const (
	learnInput      learnStep = iota // text input for topic
	learnGenerating                  // spinner while generating
	learnReview                      // viewport with generated content
)

// --- Overlay modes ---

type overlayType int

const (
	overlayNone      overlayType = iota
	overlayKnowledge             // ctrl+j: full knowledge file, scrollable
	overlayChat                  // c: multi-turn Ollama chat
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
	dueFiles    []state.DueFile // files due for review
	fileIdx     int
	currentQ    *ollama.Question
	currentFile string
	grade       gradeKind // gradeCorrect, gradePartial, gradeWrong
	mcPicked    int       // multiple choice: selected option index
	retryQueue  []string
	retryPhase  bool // true when working through retry queue
	retryCount  map[string]int


	// Topic list (browse/pick)
	pickMode      bool
	pickCursor    int
	pickFiles     []string
	pickSearch    textinput.Model
	pickSearching bool

	// Domain filter (inline — tab to cycle on dashboard/topic list)
	domainCursor int
	domainList   []string // "all" at index 0, then discovered domains

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
	learnContent string // generated knowledge content
	learnDomain  string // domain for saving
	learnSlug    string // filename slug

	// Teach-first / source view
	sourceContent string // current file's knowledge content

	// Concept chat (overlay)
	conceptChat    []chatEntry
	showKnowledge  bool // legacy compat: knowledge shown inline during question
	chatFromLesson bool // true if chat was entered from lesson step

	// Prefetch
	nextQ    *ollama.Question // pre-cached next question
	nextFile string           // file the prefetch is for

	// Stats
	sessionCorrect int
	sessionWrong   int
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

func initialModel(brainPath, domainFilter string, maxQuestions int) model {
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
	}
}

func (m model) currentDomain() string {
	if m.currentFile == "" {
		return ""
	}
	return knowledge.Domain(m.currentFile)
}

// currentStrength returns the mastery strength of the current file (0-1).
func (m model) currentStrength() float64 {
	if m.state == nil || m.currentFile == "" {
		return 0
	}
	return m.state.Strength(m.currentFile)
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
	if query == "" {
		m.pickFiles = sortByDomain(source)
	} else {
		var filtered []string
		for _, f := range source {
			if strings.Contains(strings.ToLower(f), query) {
				filtered = append(filtered, f)
			}
		}
		m.pickFiles = sortByDomain(filtered)
	}
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

// cycleDomainFilter cycles through domain options via tab.
func (m *model) cycleDomainFilter() {
	if len(m.domainList) == 0 {
		m.buildDomainList()
	}
	m.domainCursor = (m.domainCursor + 1) % len(m.domainList)
	if m.domainCursor == 0 {
		m.domainFilter = "" // "all"
	} else {
		m.domainFilter = m.domainList[m.domainCursor]
	}
}

// strengthMini renders a small inline mastery bar (5 blocks).
func strengthMini(s float64) string {
	blocks := 5
	filled := int(s * float64(blocks))
	var style lipgloss.Style
	switch {
	case s >= 0.7:
		style = correctStyle
	case s >= 0.3:
		style = actionStyle
	default:
		if s > 0 {
			style = wrongStyle
		} else {
			style = dimStyle
		}
	}
	return style.Render(strings.Repeat("█", filled)) +
		barEmptyStyle.Render(strings.Repeat("░", blocks-filled))
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
