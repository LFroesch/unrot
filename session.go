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
	m.sessionRecordIdx = -1
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

	reviewFiles = m.applyDifficultyGating(reviewFiles)

	return reviewFiles
}

// rebuildReviewFileQueue rebuilds the capped review list after maxQuestions grows (continue session).
func (m *model) rebuildReviewFileQueue() []string {
	files := m.allFiles
	if m.domainFilter == "" || !knowledge.IsProjectDomain(m.domainFilter) {
		files = nonProjectFiles(files)
	}
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

// nonProjectFiles returns all files excluding project knowledge (projects/ domain).
// Project files are only quizzable via interview mode (I) or direct drill.
func nonProjectFiles(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if !knowledge.IsProjectDomain(knowledge.Domain(f)) {
			out = append(out, f)
		}
	}
	return out
}

// startReview begins a priority-ordered review session.
func (m *model) startReview() tea.Cmd {
	files := m.allFiles
	// Exclude project files from normal review — use interview mode (I) for those.
	if m.domainFilter == "" || !knowledge.IsProjectDomain(m.domainFilter) {
		files = nonProjectFiles(files)
	}
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
		return generateQuestion(m.ollamaCtx, m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
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
		return generateQuestion(m.ollamaCtx, m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	}
	return m.finishSession()
}

// syncSessionRecordToState writes current session counters to state.Sessions (upsert).
func (m *model) syncSessionRecordToState() {
	if m.state == nil || m.sessionTotal <= 0 {
		return
	}
	domains := make([]string, 0, len(m.sessionDomains))
	for d := range m.sessionDomains {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	dur := int(time.Since(m.sessionStart).Seconds())
	m.state.UpsertSessionRecord(&m.sessionRecordIdx, m.sessionCorrect, m.sessionWrong, domains, dur)
}

// savePartialSession flushes session stats and clears in-memory session counters.
// Per-question saves also call syncSessionRecordToState so stats survive abrupt exit.
func (m *model) savePartialSession() {
	if m.state == nil || m.sessionTotal <= 0 {
		return
	}
	m.syncSessionRecordToState()
	m.state.Save()
	m.sessionTotal = 0
	m.sessionRecordIdx = -1
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

// applyDifficultyGating reorders files within each domain so that medium/hard topics
// are only promoted once the easier tier avg confidence crosses a threshold.
// Below the threshold, harder files are moved to the back of the domain's slot.
// Threshold: avg confidence >= 3 in the lower tier unlocks the next tier.
func (m *model) applyDifficultyGating(files []string) []string {
	if len(files) == 0 {
		return files
	}

	// Group by domain and tier, compute avg confidence per tier per domain.
	type tierKey struct{ domain, diff string }
	tierConf := make(map[tierKey][]float64)
	fileDiff := make(map[string]string)

	for _, f := range files {
		dom := knowledge.Domain(f)
		d := knowledge.ReadDifficultyFromFile(m.brainPath, f)
		fileDiff[f] = d
		k := tierKey{dom, d}
		tierConf[k] = append(tierConf[k], float64(m.state.GetConfidence(f)))
	}

	avgTier := func(dom, diff string) float64 {
		vals := tierConf[tierKey{dom, diff}]
		if len(vals) == 0 {
			return 0
		}
		var sum float64
		for _, v := range vals {
			sum += v
		}
		return sum / float64(len(vals))
	}

	// Determine readiness: medium unlocked when easy avg >= 3; hard when medium avg >= 3.
	// If no files exist at the lower tier in this domain, treat the tier as unlocked
	// (avoids permanently deferring project files which have no "easy" tier).
	unlocked := func(dom, diff string) bool {
		switch diff {
		case "easy":
			return true
		case "medium":
			return len(tierConf[tierKey{dom, "easy"}]) == 0 || avgTier(dom, "easy") >= 3.0
		case "hard":
			return len(tierConf[tierKey{dom, "medium"}]) == 0 || avgTier(dom, "medium") >= 3.0
		}
		return true
	}

	// Stable partition: move locked (not yet unlocked) files to end of their domain group.
	// We do a single pass keeping relative order within each partition.
	var ready, notReady []string
	for _, f := range files {
		dom := knowledge.Domain(f)
		d := fileDiff[f]
		if unlocked(dom, d) {
			ready = append(ready, f)
		} else {
			notReady = append(notReady, f)
		}
	}
	return append(ready, notReady...)
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
