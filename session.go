package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/LFroesch/unrot/internal/knowledge"
	"github.com/LFroesch/unrot/internal/ollama"

	tea "github.com/charmbracelet/bubbletea"
)

const maxRetries = 2

// sessionExtendExtra is how many more questions a "continue session" grants (arcade-style).
const sessionExtendExtra = 5

// resetSession clears all session tracking state for a fresh start.
// Does NOT set pickMode or reviewFiles — caller handles those.
func (m *model) resetSession() {
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
	m.ratedConfidence = 0
	m.sessionMinConf = 6
	m.learnChatCount = 0
	m.comboCount = 0
	m.comboMax = 0
	m.xpGained = 0
	m.levelUpFrom = 0
	m.reviewQueueCapped = false
}

// applyReviewQueuePipeline returns priority-sorted files with prereq insertion and domain interleaving.
func (m *model) applyReviewQueuePipeline(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	reviewFiles := m.state.FilesByPriority(files)

	// Bias toward foundational (prereq) files when overall confidence is low.
	if m.depGraph != nil && m.state.AvgConfidence(reviewFiles) < 3.0 {
		sort.SliceStable(reviewFiles, func(i, j int) bool {
			iConf := m.state.GetConfidence(reviewFiles[i])
			jConf := m.state.GetConfidence(reviewFiles[j])
			iDeps := m.depGraph.DependentCount(reviewFiles[i])
			jDeps := m.depGraph.DependentCount(reviewFiles[j])
			iFoundational := iDeps > 0 && iConf <= 2
			jFoundational := jDeps > 0 && jConf <= 2
			if iFoundational != jFoundational {
				return iFoundational
			}
			return false
		})
	}

	if m.depGraph != nil {
		reviewFiles = m.insertPrereqs(reviewFiles)
	}

	if m.domainFilter == "" && len(reviewFiles) > 1 {
		reviewFiles = interleaveByDomain(reviewFiles, 2, m.depGraph)
	}

	return reviewFiles
}

// rebuildReviewFileQueue rebuilds the capped review list after maxQuestions grows (continue session).
func (m *model) rebuildReviewFileQueue() []string {
	files := m.allFiles
	if m.domainFilter != "" {
		files = knowledge.FilterByDomain(files, m.domainFilter)
	}
	if len(files) == 0 {
		return m.reviewFiles
	}
	reviewFiles := m.applyReviewQueuePipeline(files)
	if m.maxQuestions > 0 && len(reviewFiles) > m.maxQuestions {
		reviewFiles = reviewFiles[:m.maxQuestions]
	}
	return reviewFiles
}

// extendSessionContinue adds more questions and resumes the quiz (see stepSessionContinue).
func (m *model) extendSessionContinue() tea.Cmd {
	m.maxQuestions += sessionExtendExtra
	if m.reviewQueueCapped {
		m.reviewFiles = m.rebuildReviewFileQueue()
	}
	return m.nextQuestion()
}

// startReview begins a priority-ordered review session.
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

	full := m.applyReviewQueuePipeline(files)
	priorLen := len(full)
	m.reviewFiles = full
	if m.maxQuestions > 0 && len(m.reviewFiles) > m.maxQuestions {
		m.reviewFiles = m.reviewFiles[:m.maxQuestions]
	}
	capped := m.maxQuestions > 0 && priorLen > len(m.reviewFiles)

	m.resetSession()
	m.reviewQueueCapped = capped
	m.pickMode = false
	m.phase = phaseQuiz

	return m.startFile(m.reviewFiles[0])
}

// startPickDrill begins a drill session on the currently selected topic.
func (m *model) startPickDrill() tea.Cmd {
	file := m.pickFiles[m.pickCursor]
	m.resetSession()
	m.pickMode = true
	m.reviewFiles = nil
	m.phase = phaseQuiz
	return m.startFile(file)
}

// startFile begins quizzing on a file. Always shows knowledge content first.
func (m *model) startFile(file string) tea.Cmd {
	m.currentFile = file
	m.sourceContent = ""
	m.conceptChat = nil
	m.activeOverlay = overlayNone
	m.clearAuditState()
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
		m.quizStep = stepSessionContinue
		m.syncViewport()
		return nil
	}

	if m.pickMode {
		m.clearAuditState()
		diff := ollama.DifficultyFromConfidence(m.state.GetConfidence(m.currentFile))
		m.quizStep = stepLoading
		m.ratedConfidence = 0
		openQuestionLog(m)
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
		m.clearAuditState()
		diff := ollama.DifficultyFromConfidence(m.state.GetConfidence(file))
		m.quizStep = stepLoading
		m.ratedConfidence = 0
		openQuestionLog(m)
		return generateQuestion(m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	}
	return m.finishSession()
}

// savePartialSession records an in-progress session toward the daily goal if any
// questions were answered. Safe to call multiple times — resets sessionTotal after saving.
func (m *model) savePartialSession() {
	if m.sessionTotal > 0 {
		var domains []string
		for d := range m.sessionDomains {
			domains = append(domains, d)
		}
		m.state.RecordSession(m.sessionCorrect, m.sessionWrong, domains, time.Since(m.sessionStart))
		m.state.Save()
		m.sessionTotal = 0 // prevent double-recording
	}
}

func (m *model) finishSession() tea.Cmd {
	m.savePartialSession()
	if m.logFile != nil {
		m.logFile.Close()
		m.logFile = nil
	}
	m.logQuestionNum = 0
	m.phase = phaseDone
	m.syncViewport()
	return nil
}

// insertPrereqs walks the priority-sorted list and inserts stale prerequisites
// immediately before their dependents, deepest-first. Deduplicates via seen set.
func (m *model) insertPrereqs(files []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, f := range files {
		stale := m.depGraph.StalePrereqs(f, 2, m.state.GetConfidence)
		for _, prereq := range stale {
			if !seen[prereq] {
				seen[prereq] = true
				result = append(result, prereq)
			}
		}
		if !seen[f] {
			seen[f] = true
			result = append(result, f)
		}
	}
	return result
}

// interleaveByDomain reorders files so no more than maxConsecutive files from the
// same domain appear in a row. Prereq pairs are exempt — they stay adjacent.
func interleaveByDomain(files []string, maxConsecutive int, graph *knowledge.DepGraph) []string {
	result := make([]string, len(files))
	copy(result, files)

	for i := 0; i < len(result); i++ {
		consecutive := 1
		for j := i - 1; j >= 0 && knowledge.Domain(result[j]) == knowledge.Domain(result[i]); j-- {
			consecutive++
		}
		if consecutive <= maxConsecutive {
			continue
		}
		if graph != nil {
			if i > 0 && graph.IsPrereqOf(result[i-1], result[i]) {
				continue
			}
			if i+1 < len(result) && graph.IsPrereqOf(result[i], result[i+1]) {
				continue
			}
		}
		currentDomain := knowledge.Domain(result[i])
		swapped := false
		for j := i + 1; j < len(result); j++ {
			if knowledge.Domain(result[j]) != currentDomain {
				tmp := result[j]
				copy(result[i+1:j+1], result[i:j])
				result[i] = tmp
				swapped = true
				break
			}
		}
		if !swapped {
			break
		}
	}
	return result
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
