package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"go.dalton.dog/bubbleup"

	"github.com/LFroesch/unrot/internal/knowledge"
	"github.com/LFroesch/unrot/internal/ollama"
	"github.com/LFroesch/unrot/internal/state"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) Init() tea.Cmd {
	return tea.Batch(loadState(m.brainPath), m.spinner.Tick, m.alerts.Init())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Fire next queued alert after delay
	if _, ok := msg.(fireNextAlertMsg); ok {
		if m.alerts != nil && len(m.pendingAlerts) > 0 {
			next := m.pendingAlerts[0]
			m.pendingAlerts = m.pendingAlerts[1:]
			alertCmd := m.alerts.NewAlertCmd(next.alertType, next.message)
			if len(m.pendingAlerts) > 0 {
				delay := tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return fireNextAlertMsg{} })
				return m, tea.Batch(alertCmd, delay)
			}
			return m, alertCmd
		}
		return m, nil
	}

	// Route all messages through bubbleup for timer/dismiss handling
	var alertCmd tea.Cmd
	if m.alerts != nil {
		updatedAlerts, cmd := m.alerts.Update(msg)
		if am, ok := updatedAlerts.(bubbleup.AlertModel); ok {
			m.alerts = &am
		}
		alertCmd = cmd
	}
	newM, cmd := m.update(msg)
	// Flush any queued alert cmds from queueAlert() calls
	if mm, ok := newM.(model); ok {
		cmd = mm.flushAlerts(cmd)
		newM = mm
	}
	if alertCmd != nil {
		return newM, tea.Batch(cmd, alertCmd)
	}
	return newM, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.width < minTerminalWidth {
			m.width = minTerminalWidth
		}
		if m.height < minTerminalHeight {
			m.height = minTerminalHeight
		}
		m.syncViewport()
		m.overlayViewport.Width = m.width - 16
		m.overlayViewport.Height = m.height - 10
		m.answerTA.SetWidth(m.width - 8)
		m.learnTA.SetWidth(m.width - 8)
		m.pickSearch.Width = m.width - 12
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.conceptChatLoading {
			if m.activeOverlay == overlayChat {
				m.syncOverlayViewport()
			} else if m.questionTab == qTabChat {
				m.syncQuestionChatViewport()
			}
		}
		return m, cmd

	case stateLoadedMsg:
		firstLoad := !m.loaded
		m.loaded = true
		m.state = msg.state
		applyCallLogger(&m)
		if msg.needsPath {
			m.phase = phaseSettings
			m.settingsCursor = len(ollama.AllTypes) + 2 // brain path row
			m.toast = "set your knowledge path to get started"
			return m, nil
		}
		m.allFiles = msg.files
		m.depGraph = msg.graph
		m.buildDomainList()
		if m.maxQuestions == 0 {
			if m.state.MaxQuestions > 0 {
				m.maxQuestions = m.state.MaxQuestions
			} else {
				m.maxQuestions = 5
			}
		}
		if firstLoad {
			if m.domainFilter != "" {
				return m, m.startReview()
			}
			m.phase = phaseDashboard
		} else {
			m.toast = "knowledge path updated"
		}
		return m, nil

	case questionMsg:
		m.currentQ = msg.question
		m.currentFile = msg.file
		m.lastQType = msg.question.Type
		m.lastQDiff = msg.question.Difficulty
		m.lastQSet = true
		m.sessionDomains[m.currentDomain()] = true
		m.answerTA.Reset()
		m.answerTA.Focus()
		m.configureAnswerTAForQuestion()
		m.hints = nil
		m.userAnswer = ""
		m.questionTab = qTabQuiz
		m.savedQuizInput = ""
		m.savedChatInput = ""
		m.ratedConfidence = 0
		m.mcEliminated = [4]bool{}
		m.mcWrongPicks = 0
		m.answerRevealed = false
		m.gradeFeedback = ""
		m.quizStep = stepQuestion
		// Mark file as reviewed (staleness tracking)
		m.state.MarkReviewed(msg.file)

	case auditMsg:
		m.auditLoading = false
		m.auditResult = msg.text
		hasIssues := !strings.Contains(strings.ToLower(msg.text), "looks good")
		w := m.activeViewportWidth()
		content := renderMarkdown(m.sourceContent, w)
		content += "\n" + divider("audit", w) + "\n"
		content += renderExplanation(m.auditResult, w)
		if hasIssues {
			content += "\n" + dimStyle.Render("  enter fix · esc dismiss")
			m.toast = "enter to auto-fix · esc to dismiss"
		}
		m.setActiveViewportContent(content, false)
		return m, nil

	case auditFixMsg:
		m.auditFixLoading = false
		m.auditFixPending = msg.content
		m.toast = "review fix: enter save · esc discard"
		w := m.activeViewportWidth()
		content := renderMarkdown(m.auditFixPending, w)
		content += "\n" + dimStyle.Render("  enter save · esc discard")
		m.setActiveViewportContent(content, true)
		return m, nil

	case answerGradeMsg:
		m.gradeFeedback = msg.feedback
		if msg.correct {
			m.grade = gradeCorrect
			m.answerRevealed = true
		} else {
			m.grade = gradeWrong
			m.answerRevealed = false
		}
		m.quizStep = stepResult
		m.syncViewport()
		return m, nil

	case hintMsg:
		if m.phase == phaseChallenge {
			m.challengeHints = append(m.challengeHints, msg.text)
			m.syncViewport()
		} else {
			m.hints = append(m.hints, msg.text)
			m.quizStep = stepQuestion
		}
		return m, nil

	case explainMoreMsg:
		m.currentQ.Explanation = msg.text
		m.explainLoading = false
		m.syncViewport()
		return m, nil

	case clipboardMsg:
		if msg.ok {
			(&m).queueAlert(alertHint, "copied to clipboard")
		} else {
			(&m).queueAlert(alertHint, "clipboard not available")
		}
		return m, nil

	case lessonMsg:
		m.sourceContent = msg.content
		m.currentFile = msg.file
		if m.phase == phaseViewer {
			// Viewer mode: just show the file content
			m.viewport.SetContent(renderMarkdown(m.sourceContent, m.viewport.Width))
			m.viewport.GotoTop()
			return m, nil
		}
		m.sessionDomains[m.currentDomain()] = true
		m.phase = phaseQuiz
		m.quizStep = stepLesson
		m.syncViewport()
		return m, nil

	case conceptChatMsg:
		m.conceptChatLoading = false
		m.conceptChat = append(m.conceptChat, chatEntry{role: "assistant", content: msg.text, durationSec: msg.durationSec})
		if m.activeOverlay == overlayChat {
			m.syncOverlayViewport()
		} else if m.questionTab == qTabChat {
			m.syncQuestionChatViewport()
		}
		return m, nil

	case learnChatMsg:
		m.learnChatHistory = append(m.learnChatHistory, chatEntry{role: "assistant", content: msg.text})
		m.learnChatLoading = false
		return m, nil

	case challengeChatMsg:
		m.challengeChatHistory = append(m.challengeChatHistory, chatEntry{role: "assistant", content: msg.text})
		m.challengeChatLoading = false
		return m, nil

	case projectStaleCheckMsg:
		if !msg.hasAny {
			// No existing knowledge — go straight to proposing
			openProjectLog(&m)
			m.projectStep = projectProposing
			tree := listRepoTree(m.projectRepoPath)
			return m, proposeSubsystemsCmd(m.ollamaCtx, m.ollama, m.projectRepoPath, m.projectArchContext, tree)
		}
		m.projectStaleEntries = msg.entries
		m.projectStep = projectStaleResult
		return m, nil

	case projectSubsystemsMsg:
		if len(msg.proposals) == 0 {
			m.toast = "no subsystems proposed — try a repo with more code"
			closeProjectLog(&m)
			m.goHome()
			return m, nil
		}
		// Build batch entries from proposals (file mappings already resolved)
		entries := make([]projectBatchEntry, len(msg.proposals))
		for i, p := range msg.proposals {
			entries[i] = projectBatchEntry{
				slug:      p.slug,
				fileCount: len(p.files),
				files:     p.files,
				status:    "pending",
			}
		}
		m.projectBatchEntries = entries
		// Start first subsystem with two-pass extraction
		first := entries[0]
		m.projectSubsystem = first.slug
		if len(entries) > 1 {
			m.projectBatchQueue = nil
			for _, e := range entries[1:] {
				m.projectBatchQueue = append(m.projectBatchQueue, e.slug)
			}
		}
		m.projectStep = projectGenerating
		projectLog("batch start: %s — %d subsystems", m.projectName, len(entries))
		return m, startSubsystemExtraction(&m, 0)

	case projectFileNotesMsg:
		// Accumulate notes from file extraction
		m.projectRunningNotes = msg.notes
		m.projectFileIdx++
		// Find current batch entry
		var entry *projectBatchEntry
		for i := range m.projectBatchEntries {
			if m.projectBatchEntries[i].slug == m.projectSubsystem {
				entry = &m.projectBatchEntries[i]
				break
			}
		}
		if entry != nil && m.projectFileIdx < len(entry.files) {
			// More files to extract
			m.projectScanStatus = fmt.Sprintf("extracting %s (%d/%d files)...", m.projectSubsystem, m.projectFileIdx+1, len(entry.files))
			return m, extractFileNotesCmd(m.ollamaCtx, m.ollama, m.projectRepoPath, m.projectSubsystem, entry.files[m.projectFileIdx], m.projectRunningNotes)
		}
		// All files extracted — synthesize
		if entry != nil {
			entry.status = "synthesizing"
			entry.notes = m.projectRunningNotes
		}
		m.projectScanStatus = fmt.Sprintf("synthesizing %s...", m.projectSubsystem)
		return m, synthesizeSubsystemCmd(m.ollamaCtx, m.ollama, m.projectName, m.projectSubsystem, m.projectArchContext, m.projectRunningNotes)

	case projectContentMsg:
		m.projectContent = msg.content
		// Update batch entry
		for i := range m.projectBatchEntries {
			if m.projectBatchEntries[i].slug == m.projectSubsystem {
				m.projectBatchEntries[i].status = "saving"
				if len(msg.files) > 0 {
					m.projectBatchEntries[i].fileCount = len(msg.files)
				}
			}
		}
		// Build file list from batch entry for source metadata
		var fileList []string
		for _, e := range m.projectBatchEntries {
			if e.slug == m.projectSubsystem {
				fileList = e.files
				break
			}
		}
		m.projectSourceFiles = strings.Join(fileList, ", ")
		projectLog("generated doc for %s, auto-saving...", m.projectSubsystem)
		return m, batchSaveKnowledgeCmd(m.brainPath, m.projectRepoPath, m.projectName, m.projectSubsystem, msg.content, m.projectSourceFiles)

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
		if m.state.UnlockAchievement(state.AchScholar) {
			info := state.AchievementInfo[state.AchScholar]
			(&m).queueAlert(alertAchievement, fmt.Sprintf("%s — %s", info.Name, info.Desc))
		}
		newAch := m.state.CheckAchievements(state.AchievementContext{
			FileCount: len(m.allFiles) + 1,
		})
		if len(newAch) > 0 {
			info := state.AchievementInfo[newAch[0]]
			(&m).queueAlert(alertAchievement, fmt.Sprintf("%s — %s", info.Name, info.Desc))
		}
		m.state.Save()
		// Project mode: advance to next subsystem or show done screen
		if m.phase == phaseProject {
			(&m).queueAlert(alertXP, fmt.Sprintf("+50 XP — saved %s", m.projectSubsystem))
			// Mark current entry as done
			for i := range m.projectBatchEntries {
				if m.projectBatchEntries[i].slug == m.projectSubsystem {
					m.projectBatchEntries[i].status = "done"
					m.projectBatchEntries[i].savedPath = msg.relPath
				}
			}
			if len(m.projectBatchQueue) > 0 {
				next := m.projectBatchQueue[0]
				m.projectBatchQueue = m.projectBatchQueue[1:]
				m.projectSubsystem = next
				m.projectSourceFiles = ""
				m.projectStep = projectGenerating
				// Find batch index for next subsystem
				nextIdx := -1
				for i := range m.projectBatchEntries {
					if m.projectBatchEntries[i].slug == next {
						nextIdx = i
						break
					}
				}
				projectLog("starting next subsystem: %s (%d remaining)", next, len(m.projectBatchQueue))
				return m, startSubsystemExtraction(&m, nextIdx)
			}
			// All done
			doneCount := len(m.projectBatchEntries)
			projectLog("batch complete: %d subsystems generated", doneCount)
			closeProjectLog(&m)
			m.projectStep = projectDone
			return m, nil
		}
		m.phase = phaseQuiz
		m.quizStep = stepLoading
		openQuestionLog(&m)
		return m, generateQuestion(m.ollamaCtx, m.ollama, m.brainPath, msg.relPath, m.randomActiveType(), ollama.DiffBasic)

	case reportSavedMsg:
		m.reportPath = msg.path
		m.syncViewport()
		return m, nil

	case bankNotesMsg:
		// Show preview instead of auto-saving
		m.bankPending = msg.notes
		m.bankLoading = false
		m.toast = "review notes: enter save · esc discard · r regenerate"
		if m.activeOverlay == overlayChat {
			m.syncOverlayViewport()
		} else if m.questionTab == qTabChat {
			m.syncQuestionChatViewport()
		}
		return m, nil

	case notesSavedMsg:
		m.sourceContent = msg.content
		m.syncViewport()
		return m, nil

	case challengeGenMsg:
		m.currentChallenge = msg.challenge
		m.challengeStep = challengeWorking
		m.challengeTab = cTabCode
		m.challengeCode = ""
		m.savedChallengeCode = ""
		m.conceptChatLoading = false
		m.challengeHints = nil
		m.answerTA.Reset()
		m.answerTA.CharLimit = 3000
		m.answerTA.Placeholder = "write your code..."
		m.answerTA.Focus()
		m.syncViewport()
		return m, nil

	case challengeGradeMsg:
		m.challengeGrade = msg.grade
		m.challengeStep = challengeResult
		if !m.challengeRetrying {
			// Map score to confidence equivalent for XP calc
			conf := msg.grade.Score / 20 // 0-100 → 0-5
			if conf < 1 {
				conf = 1
			}
			if conf > 5 {
				conf = 5
			}
			prevLevel := m.state.Level()
			xp, breakdown := state.CalcXP(conf, int(m.currentChallenge.Difficulty), m.state.DayStreak, 0)
			bonus, casinoTierHit := rollCasinoBonus(0)
			xp += bonus
			breakdown.Bonus = bonus
			breakdown.Total = xp

			m.state.AwardXP(xp)
			m.state.TotalChallenges++
			m.xpGained = xp
			m.xpBreakdown = breakdown
			m.challengeCount++
			m.sessionTotal++
			if m.domainFilter != "" {
				m.sessionDomains[m.domainFilter] = true
			} else if m.currentChallenge != nil && m.currentChallenge.Language != "" {
				m.sessionDomains[m.currentChallenge.Language] = true
			}
			if msg.grade.Correct {
				m.sessionCorrect++
			} else {
				m.sessionWrong++
			}

			// Adaptive difficulty (only when set to adaptive mode)
			if m.state.ChallengeDiff == 0 {
				if msg.grade.Score >= 80 && m.challengeDiff < ollama.DiffAdvanced {
					m.challengeDiff++
				} else if msg.grade.Score < 40 && m.challengeDiff > ollama.DiffBasic {
					m.challengeDiff--
				}
			}

			// Level up
			newLevel := m.state.Level()
			if newLevel > prevLevel {
				m.levelUpFrom = prevLevel
				(&m).queueAlert(alertLevelUp, fmt.Sprintf("LEVEL UP! Lv.%d → Lv.%d", prevLevel, newLevel))
			}

			// XP toast
			switch casinoTierHit {
			case casinoJackpot:
				(&m).queueAlert(alertXP, fmt.Sprintf("+%d XP  💎 JACKPOT!", xp))
			case casinoLucky:
				(&m).queueAlert(alertXP, fmt.Sprintf("+%d XP  🎰 lucky!", xp))
			default:
				(&m).queueAlert(alertXP, fmt.Sprintf("+%d XP", xp))
			}

			// Achievement checks
			newAch := m.state.CheckAchievements(state.AchievementContext{
				SessionTotal:   m.sessionTotal,
				DomainCount:    len(m.sessionDomains),
				FileCount:      len(m.allFiles),
				LockedCount:    m.state.CountLocked(),
				HitJackpot:     casinoTierHit == casinoJackpot,
				IsChallenge:    true,
				ChallengeScore: msg.grade.Score,
			})
			for _, id := range newAch {
				info := state.AchievementInfo[id]
				(&m).queueAlert(alertAchievement, fmt.Sprintf("%s — %s", info.Name, info.Desc))
			}

			(&m).syncSessionRecordToState()
			m.state.Save()
		}
		m.challengeRetrying = false
		m.syncViewport()
		return m, nil

	case enrichDoneMsg:
		if msg.err != nil {
			m.enrichErrors++
		}
		m.enrichIdx++
		if m.phase == phaseSettings {
			m.viewport.SetContent(m.renderSettings())
		}
		if m.enrichIdx < len(m.enrichFiles) {
			return m, enrichFileCmd(m.ollamaCtx, m.ollama, m.brainPath, m.enrichIndex, m.enrichFiles[m.enrichIdx])
		}
		// All done
		m.enrichRunning = false
		total := len(m.enrichFiles)
		errs := m.enrichErrors
		m.enrichFiles = nil
		if errs > 0 {
			(&m).queueAlert(alertHint, fmt.Sprintf("Enriched %d files (%d errors)", total, errs))
		} else {
			(&m).queueAlert(alertXP, fmt.Sprintf("Enriched %d files", total))
		}
		return m, (&m).flushAlerts(nil)

	case errMsg:
		if context.Cause(m.ollamaCtx) != nil || msg.err == context.Canceled {
			// Cancelled by user (esc) — silently ignore
			return m, nil
		}
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
				if m.questionTab == qTabKnowledge || m.questionTab == qTabChat {
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
		case phaseStats, phaseDone, phaseViewer, phaseSettings:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case phaseChallenge:
			if m.challengeStep == challengeResult || m.challengeStep == challengeWorking {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// settingsCursorLine returns the approximate line number of the current cursor in settings.
func (m *model) settingsCursorLine() int {
	// Quiz types: 3 header lines + 1 line per type
	if m.settingsCursor < len(ollama.AllTypes) {
		return 3 + m.settingsCursor
	}
	// After quiz types: each section adds ~4 lines (blank + divider + blank + content)
	base := 3 + len(ollama.AllTypes)
	return base + (m.settingsCursor-len(ollama.AllTypes)+1)*4
}

// ensureSettingsCursorVisible scrolls the settings viewport so the cursor row is visible.
func (m *model) ensureSettingsCursorVisible() {
	line := m.settingsCursorLine()
	if line >= m.viewport.YOffset+m.viewport.Height-1 {
		m.viewport.SetYOffset(line - m.viewport.Height + 3)
	} else if line < m.viewport.YOffset {
		off := line - 1
		if off < 0 {
			off = 0
		}
		m.viewport.SetYOffset(off)
	}
}

// goHome returns to the dashboard, resetting session domain filter.
// Saves any in-progress session so partial work counts toward the daily goal.
func (m *model) goHome() {
	m.savePartialSession()
	m.phase = phaseDashboard
	m.domainFilter = m.cliDomain
	m.activeOverlay = overlayNone
	if m.savedActiveTypes != nil {
		m.activeTypes = m.savedActiveTypes
		m.savedActiveTypes = nil
	}
}

// resetAnswerTA restores the answer textarea to default quiz state.
func (m *model) resetAnswerTA() {
	m.answerTA.SetHeight(5)
	m.answerTA.CharLimit = 500
	m.answerTA.Placeholder = "type your answer..."
	m.answerTA.ShowLineNumbers = false
}

// configureAnswerTAForQuestion sets textarea height/placeholder based on question type.
func (m *model) configureAnswerTAForQuestion() {
	switch m.currentQ.Type {
	case ollama.TypeFinishCode:
		m.answerTA.SetHeight(3)
		m.answerTA.CharLimit = 500
		m.answerTA.Placeholder = "// your code here"
		m.answerTA.ShowLineNumbers = false
	case ollama.TypeDebug:
		m.answerTA.SetHeight(6)
		m.answerTA.CharLimit = 500
		m.answerTA.Placeholder = "describe the bug and the fix..."
		m.answerTA.ShowLineNumbers = false
	case ollama.TypeCodeOutput:
		m.answerTA.SetHeight(3)
		m.answerTA.CharLimit = 200
		m.answerTA.Placeholder = "expected output..."
		m.answerTA.ShowLineNumbers = false
	default:
		m.resetAnswerTA()
	}
}

// handleAuditFixKey handles enter/esc when an audit fix is pending (3 contexts).
// Returns (handled, cmd). If !handled, caller should proceed normally.
func (m *model) handleAuditFixKey(key string) (bool, tea.Cmd) {
	if m.auditFixPending == "" {
		return false, nil
	}
	switch key {
	case "enter":
		content := m.auditFixPending
		m.auditFixPending = ""
		m.auditResult = ""
		m.queueAlert(alertHint, "saved fixed version")
		// Check AuditFix achievement
		if newAch := m.state.CheckAchievements(state.AchievementContext{AppliedAuditFix: true}); len(newAch) > 0 {
			info := state.AchievementInfo[newAch[0]]
			m.queueAlert(alertAchievement, fmt.Sprintf("%s — %s", info.Name, info.Desc))
		}
		return true, saveAuditFixCmd(m.brainPath, m.currentFile, content)
	case "esc":
		m.auditFixPending = ""
		m.queueAlert(alertHint, "discarded fix")
		m.setActiveViewportContent(renderMarkdown(m.sourceContent, m.activeViewportWidth()), true)
		return true, nil
	}
	return false, nil
}

// handleAuditResultKey handles enter when audit found issues (triggers fix generation).
// Returns (handled, cmd).
func (m *model) handleAuditResultKey(key string) (bool, tea.Cmd) {
	if m.auditResult == "" || m.auditFixLoading || key != "enter" {
		return false, nil
	}
	m.auditFixLoading = true
	m.toast = ""
	return true, auditFixCmd(m.ollamaCtx, m.ollama, m.sourceContent, m.auditResult)
}

// startAudit begins an audit if not already in progress. Returns nil cmd if busy.
func (m *model) startAudit() tea.Cmd {
	if m.auditLoading || m.auditFixLoading || m.sourceContent == "" {
		return nil
	}
	m.auditLoading = true
	m.auditResult = ""
	return auditKnowledgeCmd(m.ollamaCtx, m.ollama, m.sourceContent, m.currentFile)
}

// clearAuditState resets all audit-related state.
func (m *model) clearAuditState() {
	m.auditResult = ""
	m.auditLoading = false
	m.auditFixPending = ""
	m.auditFixLoading = false
}

// handleBankPreviewKey handles enter/r/esc when bank notes preview is showing.
// Returns (handled, cmd).
func (m *model) handleBankPreviewKey(key string) (bool, tea.Cmd) {
	if m.bankPending == "" {
		return false, nil
	}
	switch key {
	case "enter":
		existing := knowledge.ExtractNotes(m.sourceContent)
		merged := existing
		if merged != "" {
			merged += "\n\n"
		}
		merged += m.bankPending
		m.bankPending = ""
		m.state.NotesBanked++
		m.state.Save()
		m.queueAlert(alertHint, "banked chat insights to notes")
		// Check NoteTaker achievement
		if newAch := m.state.CheckAchievements(state.AchievementContext{}); len(newAch) > 0 {
			info := state.AchievementInfo[newAch[0]]
			m.queueAlert(alertAchievement, fmt.Sprintf("%s — %s", info.Name, info.Desc))
		}
		return true, saveNotesCmd(m.brainPath, m.currentFile, merged)
	case "r":
		m.bankPending = ""
		m.bankLoading = true
		m.toast = ""
		return true, bankChatToNotesCmd(m.ollamaCtx, m.ollama, m.conceptChat, m.brainPath, m.currentFile, m.sourceContent)
	case "esc":
		m.bankPending = ""
		m.queueAlert(alertHint, "discarded bank notes")
		return true, nil
	}
	return false, nil
}

// sendConceptChat sends a chat message from the user, handling slash command expansion.
// contextSource is the knowledge/challenge context to send to ollama.
// syncFn is called after adding the message to sync the appropriate viewport.
func (m *model) sendConceptChat(contextSource string, syncFn func()) tea.Cmd {
	question := strings.TrimSpace(m.answerTA.Value())
	if question == "" {
		return nil
	}
	display := question
	if expanded, ok := chatSlashCmds[strings.ToLower(question)]; ok {
		display = question
		question = expanded
	}
	m.conceptChat = append(m.conceptChat, chatEntry{role: "user", content: display})
	m.conceptChatLoading = true
	m.learnChatCount++
	m.answerTA.Reset()
	m.answerTA.Focus()
	syncFn()
	history := make([]chatEntry, len(m.conceptChat))
	copy(history, m.conceptChat)
	if display != question {
		history[len(history)-1].content = question
	}
	var qText, qAnswer string
	if m.currentQ != nil {
		qText = m.currentQ.Text
		qAnswer = m.currentQ.Answer
	}
	return conceptChatCmd(m.ollamaCtx, m.ollama, qText, qAnswer, contextSource, history)
}

// nextChallengeCmd resets challenge state and generates the next challenge.
// If the session started with a chat topic, generates from that context; otherwise random.
func (m *model) nextChallengeCmd() tea.Cmd {
	m.challengeGrade = nil
	m.xpGained = 0
	m.levelUpFrom = 0
	m.challengeStep = challengeLoading
	m.clearAuditState()
	openQuestionLog(m)
	if m.challengeTopic != "" && len(m.challengeChatHistory) > 0 {
		return generateChallengeFromChatCmd(m.ollamaCtx, m.ollama, m.challengeTopic, m.challengeChatHistory, m.challengeDiff)
	}
	return generateChallengeCmd(m.ollamaCtx, m.ollama, m.domainFilter, m.challengeDiff)
}

// activeViewportWidth returns the width for content in the currently active viewport (overlay or main).
func (m model) activeViewportWidth() int {
	if m.activeOverlay == overlayKnowledge {
		return m.overlayViewport.Width - 4
	}
	return m.wrapW()
}

// setActiveViewportContent sets content on the correct viewport (overlay or main) and scrolls.
func (m *model) setActiveViewportContent(content string, toTop bool) {
	if m.activeOverlay == overlayKnowledge {
		m.overlayViewport.SetContent(content)
		if toTop {
			m.overlayViewport.GotoTop()
		} else {
			m.overlayViewport.GotoBottom()
		}
	} else {
		m.viewport.SetContent(content)
		if toTop {
			m.viewport.GotoTop()
		} else {
			m.viewport.GotoBottom()
		}
	}
}

// contentHeight returns usable height for viewport content.
// Chrome: header(1) + \n(1) + \n(1) + status(1) = 4
func (m model) contentHeight() int {
	h := m.height - 4
	if h < 5 {
		h = 5
	}
	return h
}

func (m *model) syncViewport() {
	m.viewport.Width = m.width - 4
	// Challenge working: layout depends on active tab
	if m.phase == phaseChallenge && m.challengeStep == challengeWorking {
		totalH := m.contentHeight()
		switch m.challengeTab {
		case cTabCode:
			// Split: small problem viewport on top, code textarea below
			problemH := totalH / 3
			if problemH < 3 {
				problemH = 3
			}
			if problemH > 8 {
				problemH = 8
			}
			taH := totalH - problemH - 2 // 2 for divider + gap
			if taH < 5 {
				taH = 5
			}
			m.viewport.Height = problemH
			m.viewport.SetContent(m.buildChallengeProblemContent())
			m.answerTA.SetHeight(taH)
		case cTabProblem:
			// Full viewport showing problem
			m.viewport.Height = totalH
			m.viewport.SetContent(m.buildChallengeProblemContent())
		case cTabChat:
			// Viewport for chat history, small textarea for input
			m.viewport.Height = totalH - 9
			if m.viewport.Height < 3 {
				m.viewport.Height = 3
			}
			m.viewport.SetContent(m.buildChatTabContent())
			m.viewport.GotoBottom()
			m.answerTA.SetHeight(2)
		}
		return
	}
	// Challenge result: layout depends on active tab
	if m.phase == phaseChallenge && m.challengeStep == challengeResult {
		totalH := m.contentHeight()
		switch m.challengeTab {
		case cTabChat:
			m.viewport.Height = totalH - 9
			if m.viewport.Height < 3 {
				m.viewport.Height = 3
			}
			m.viewport.SetContent(m.buildChatTabContent())
			m.viewport.GotoBottom()
		default:
			m.viewport.Height = totalH
			m.viewport.SetContent(m.viewportContent())
			m.viewport.GotoTop()
		}
		return
	}
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

// applyChallengeTab configures the textarea for the current challengeTab after switching.
func (m *model) applyChallengeTab() {
	switch m.challengeTab {
	case cTabCode:
		m.answerTA.Reset()
		m.answerTA.CharLimit = 3000
		m.answerTA.Placeholder = "write your code..."
		m.answerTA.ShowLineNumbers = false
		if m.savedChallengeCode != "" {
			m.answerTA.SetValue(m.savedChallengeCode)
		}
		m.answerTA.Focus()
	case cTabProblem:
		m.answerTA.Blur()
	case cTabChat:
		m.answerTA.Reset()
		m.answerTA.CharLimit = 500
		m.answerTA.SetHeight(2)
		m.answerTA.Placeholder = "ask about this challenge..."
		m.answerTA.ShowLineNumbers = false
		if m.savedChatInput != "" {
			m.answerTA.SetValue(m.savedChatInput)
		}
		m.answerTA.Focus()
	}
}

// syncChallengeChatViewport rebuilds the challenge inline chat viewport.
func (m *model) syncChallengeChatViewport() {
	m.viewport.Height = m.contentHeight() - 9
	if m.viewport.Height < 3 {
		m.viewport.Height = 3
	}
	m.viewport.SetContent(m.buildChatTabContent())
	m.viewport.GotoBottom()
}

// syncQuestionChatViewport rebuilds the inline chat viewport for the chat tab.
// Chat tab chrome: tab bar(2) + label(1) + textarea(~4) + gaps(2) = 9 lines
func (m *model) syncQuestionChatViewport() {
	m.viewport.Height = m.contentHeight() - 9
	if m.viewport.Height < 3 {
		m.viewport.Height = 3
	}
	m.viewport.SetContent(m.buildChatTabContent())
	m.viewport.GotoBottom()
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Global quit
	if key == "ctrl+c" {
		m.savePartialSession()
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
	case phaseSettings:
		return m.handleSettings(msg)
	case phaseChallenge:
		return m.handleChallenge(msg)
	case phaseViewer:
		return m.handleViewer(msg)
	case phaseProject:
		return m.handleProject(msg)
	case phaseRecent:
		return m.handleRecent(key)
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
	case "r":
		return m, m.startReview()
	case "b":
		m.pickFiles = sortByDomain(m.allFiles)
		m.pickCursor = 0
		m.pickMode = false
		m.viewerMode = false
		m.pickSearching = false
		m.pickSearch.SetValue("")
		m.filterPickFiles()
		m.phase = phaseTopicList
		return m, nil
	case "l":
		m.learnTA.Reset()
		m.learnTA.Focus()
		m.learnStep = learnInput
		m.phase = phaseLearn
		return m, nil
	case "i":
		m.resetSession()
		m.challengeCount = 0
		switch m.state.ChallengeDiff {
		case 1:
			m.challengeDiff = ollama.DiffBasic
		case 2:
			m.challengeDiff = ollama.DiffIntermediate
		case 3:
			m.challengeDiff = ollama.DiffAdvanced
		default:
			m.challengeDiff = ollama.DiffIntermediate
		}
		m.currentChallenge = nil
		m.challengeGrade = nil
		m.challengeTopic = ""
		m.challengeChatHistory = nil
		m.challengeChatLoading = false
		m.challengeStep = challengeInput
		m.challengeTab = cTabCode
		m.savedChallengeCode = ""
		m.learnTA.Reset()
		m.learnTA.Placeholder = "e.g. binary search in Go, React hooks, SQL joins..."
		m.learnTA.Focus()
		m.phase = phaseChallenge
		return m, nil
	case "I":
		// Interview mode: review all project knowledge files with decision/architecture/refactor types only.
		projectFiles := make([]string, 0)
		for _, f := range m.allFiles {
			if knowledge.IsProjectDomain(knowledge.Domain(f)) {
				projectFiles = append(projectFiles, f)
			}
		}
		if len(projectFiles) == 0 {
			m.queueAlert(alertHint, "No project knowledge files found. Run project scan first (p).")
			return m, m.flushAlerts(nil)
		}
		// Save current types and restrict to the 3 project-focused types.
		m.savedActiveTypes = make([]bool, len(m.activeTypes))
		copy(m.savedActiveTypes, m.activeTypes)
		interviewTypes := map[ollama.QuestionType]bool{
			ollama.TypeDecision:     true,
			ollama.TypeArchitecture: true,
			ollama.TypeRefactor:     true,
		}
		for i, t := range ollama.AllTypes {
			m.activeTypes[i] = interviewTypes[t]
		}
		m.reviewFiles = m.applyReviewQueuePipeline(projectFiles)
		m.resetSession()
		m.pickMode = false
		m.phase = phaseQuiz
		return m, m.startFile(m.reviewFiles[0])
	case "s":
		m.phase = phaseSettings
		m.settingsCursor = 0
		m.viewport.SetContent(m.renderSettings())
		m.viewport.GotoTop()
		return m, nil
	case "a":
		m.viewport.SetContent(m.buildStatsContent())
		m.viewport.GotoTop()
		m.phase = phaseStats
		return m, nil
	case "F":
		favFiles := m.state.FavoritePaths(m.allFiles)
		if len(favFiles) == 0 {
			(&m).queueAlert(alertHint, "no favorites — press f in topic list")
			return m, nil
		}
		m.reviewFiles = m.state.FilesByPriority(favFiles)
		m.resetSession()
		m.pickMode = false
		m.phase = phaseQuiz
		return m, m.startFile(m.reviewFiles[0])
	case "p":
		m.learnTA.Reset()
		m.learnTA.Placeholder = "repo path (e.g. ~/projects/myapp)"
		m.learnTA.Focus()
		m.projectStep = projectRepoInput
		m.projectRepoPath = ""
		m.projectName = ""
		m.projectArchContext = ""
		m.projectSubsystem = ""
		m.projectContent = ""
		m.projectBatchQueue = nil
		m.projectBatchEntries = nil
		m.projectSourceFiles = ""
		m.projectFileIdx = 0
		m.projectRunningNotes = ""
		m.projectStaleEntries = nil
		m.phase = phaseProject
		return m, nil
	case "v":
		m.pickFiles = sortByDomain(m.allFiles)
		m.pickCursor = 0
		m.pickMode = false
		m.pickSearching = false
		m.viewerMode = true
		m.filterPickFiles()
		m.phase = phaseTopicList
		return m, nil
	case "R":
		m.recentCursor = 0
		m.phase = phaseRecent
		return m, nil
	case "tab":
		m.openDomainOverlay()
		return m, nil
	case "q":
		if m.state != nil {
			m.state.Save()
		}
		return m, tea.Quit
	}
	return m, nil
}

// --- Recent Zone ---

func (m model) handleRecent(key string) (model, tea.Cmd) {
	recent := m.state.RecentQuestions
	switch key {
	case "esc":
		m.goHome()
		return m, nil
	case "j", "down":
		if m.recentCursor < len(recent)-1 {
			m.recentCursor++
		}
	case "k", "up":
		if m.recentCursor > 0 {
			m.recentCursor--
		}
	case "enter":
		if len(recent) == 0 {
			return m, nil
		}
		file := recent[m.recentCursor].File
		m.reviewFiles = []string{file}
		m.resetSession()
		m.pickMode = false
		m.phase = phaseQuiz
		return m, m.startFile(file)
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
			m.pickSearch.SetValue("")
			m.filterPickFiles()
			m.pickCursor = 0
			return m, nil
		case "enter":
			m.pickSearching = false
			m.pickSearch.Blur()
			if len(m.pickFiles) > 0 {
				if m.viewerMode {
					file := m.pickFiles[m.pickCursor]
					m.currentFile = file
					m.auditResult = ""
					m.auditLoading = false
					m.phase = phaseViewer
					return m, loadLesson(m.brainPath, file)
				}
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
		m.resetConfirm = false
		if m.pickCursor < len(m.pickFiles)-1 {
			m.pickCursor++
		}
	case "k", "up":
		m.resetConfirm = false
		if m.pickCursor > 0 {
			m.pickCursor--
		}
	case "enter":
		if len(m.pickFiles) > 0 {
			if m.viewerMode {
				file := m.pickFiles[m.pickCursor]
				m.currentFile = file
				m.auditResult = ""
				m.auditLoading = false
				m.phase = phaseViewer
				return m, loadLesson(m.brainPath, file)
			}
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
	case "f":
		if len(m.pickFiles) > 0 && m.pickCursor < len(m.pickFiles) {
			file := m.pickFiles[m.pickCursor]
			faved := m.state.ToggleFavorite(file)
			m.state.Save()
			if faved {
				(&m).queueAlert(alertHint, "★ favorited")
			} else {
				(&m).queueAlert(alertHint, "unfavorited")
			}
		}
	case "x":
		if len(m.pickFiles) > 0 && m.pickCursor < len(m.pickFiles) {
			if m.resetConfirm {
				file := m.pickFiles[m.pickCursor]
				m.state.ResetFile(file)
				m.state.Save()
				m.resetConfirm = false
				(&m).queueAlert(alertHint, "reset "+filepath.Base(file))
			} else {
				m.resetConfirm = true
			}
		}
	case "n":
		m.resetConfirm = false
	case "tab":
		m.openDomainOverlay()
		return m, nil
	case "esc":
		if m.resetConfirm {
			m.resetConfirm = false
			return m, nil
		}
		m.pickSearch.SetValue("")
		m.pickSearching = false
		m.goHome()
	}
	return m, nil
}

// --- Quiz (consolidated) ---

func (m model) handleQuiz(msg tea.KeyMsg) (model, tea.Cmd) {
	switch m.quizStep {
	case stepLesson:
		return m.handleLesson(msg)
	case stepSessionContinue:
		return m.handleSessionContinue(msg)
	case stepLoading, stepGrading:
		key := msg.String()
		if key == "esc" {
			m.cancelOllama()
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

func (m model) handleSessionContinue(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	m.toast = ""
	switch key {
	case "y", "Y", "enter":
		return m, (&m).extendSessionContinue()
	case "n", "N", "esc":
		return m, (&m).finishSession()
	default:
		return m, nil
	}
}

func (m model) handleLesson(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "enter", " ":
		diff := ollama.DifficultyFromConfidence(m.currentConfidence())
		m.quizStep = stepLoading
		openQuestionLog(&m)
		return m, generateQuestion(m.ollamaCtx, m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
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
		m.openNotesOverlay()
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

	// Tab/shift+tab cycle through tabs (intercepted before textarea)
	if key == "tab" || key == "shift+tab" {
		// Save current textarea content before switching
		switch m.questionTab {
		case qTabQuiz:
			m.savedQuizInput = m.answerTA.Value()
		case qTabChat:
			m.savedChatInput = m.answerTA.Value()
		}
		if key == "tab" {
			m.questionTab = (m.questionTab + 1) % 3
		} else {
			m.questionTab = (m.questionTab + 2) % 3
		}
		switch m.questionTab {
		case qTabChat:
			m.answerTA.Reset()
			m.answerTA.SetHeight(2)
			m.answerTA.Placeholder = "ask about this concept..."
			if m.savedChatInput != "" {
				m.answerTA.SetValue(m.savedChatInput)
			}
			m.answerTA.Focus()
			// Shrink viewport for chat chrome
			m.viewport.Height = m.contentHeight() - 9
			if m.viewport.Height < 3 {
				m.viewport.Height = 3
			}
			m.viewport.SetContent(m.buildChatTabContent())
			m.viewport.GotoBottom()
		case qTabKnowledge:
			// Shrink viewport for tab bar chrome
			m.viewport.Height = m.contentHeight() - 3
			if m.viewport.Height < 3 {
				m.viewport.Height = 3
			}
			content := renderMarkdown(m.sourceContent, m.wrapW())
			if m.auditResult != "" {
				content += "\n" + divider("audit", m.wrapW()) + "\n"
				content += renderExplanation(m.auditResult, m.wrapW())
			}
			m.viewport.SetContent(content)
			m.viewport.GotoTop()
		default:
			// Back to quiz — restore saved input
			m.answerTA.Reset()
			m.configureAnswerTAForQuestion()
			if m.savedQuizInput != "" {
				m.answerTA.SetValue(m.savedQuizInput)
			}
			m.answerTA.Focus()
			m.viewport.Height = m.contentHeight()
		}
		return m, nil
	}

	// --- Chat tab: send messages, scroll history ---
	if m.questionTab == qTabChat {
		if handled, cmd := m.handleBankPreviewKey(key); handled {
			return m, cmd
		}
		switch key {
		case "ctrl+b":
			if m.currentFile != "" && len(m.conceptChat) > 0 {
				m.bankLoading = true
				return m, bankChatToNotesCmd(m.ollamaCtx, m.ollama, m.conceptChat, m.brainPath, m.currentFile, m.sourceContent)
			}
			return m, nil
		case "ctrl+y":
			if len(m.conceptChat) > 0 {
				return m, copyToClipboardCmd(formatChatLog(m.conceptChat))
			}
			return m, nil
		case "ctrl+l":
			m.conceptChat = nil
			m.syncQuestionChatViewport()
			return m, nil
		case "enter":
			return m, m.sendConceptChat(m.sourceContent, func() { m.syncQuestionChatViewport() })
		case "up":
			m.viewport.ScrollUp(1)
			return m, nil
		case "down":
			m.viewport.ScrollDown(1)
			return m, nil
		case "pgup":
			m.viewport.HalfPageUp()
			return m, nil
		case "pgdown":
			m.viewport.HalfPageDown()
			return m, nil
		case "esc":
			m.savedChatInput = m.answerTA.Value()
			m.questionTab = qTabQuiz
			m.answerTA.Reset()
			m.resetAnswerTA()
			if m.savedQuizInput != "" {
				m.answerTA.SetValue(m.savedQuizInput)
			}
			m.answerTA.Focus()
			return m, nil
		default:
			var cmd tea.Cmd
			m.answerTA, cmd = m.answerTA.Update(msg)
			return m, cmd
		}
	}

	// --- Knowledge tab: scrollable, single-key actions ---
	if m.questionTab == qTabKnowledge {
		if handled, cmd := m.handleAuditFixKey(key); handled {
			return m, cmd
		}
		if handled, cmd := m.handleAuditResultKey(key); handled {
			return m, cmd
		}
		switch key {
		case "a":
			return m, m.startAudit()
		case "h":
			m.quizStep = stepGrading
			m.questionTab = qTabQuiz
			return m, hintCmd(m.ollamaCtx, m.ollama, m.currentQ.Text, m.currentQ.Answer, m.hints)
		case "n":
			m.openNotesOverlay()
			return m, nil
		case "esc":
			m.questionTab = qTabQuiz
			m.clearAuditState()
			m.answerTA.Reset()
			m.configureAnswerTAForQuestion()
			m.answerTA.Focus()
			return m, nil
		default:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}

	// --- Quiz tab ---

	// Hint: h for MC, ctrl+e for typed
	if key == "ctrl+e" || (key == "h" && m.currentQ.Type == ollama.TypeMultiChoice) {
		m.quizStep = stepGrading
		return m, hintCmd(m.ollamaCtx, m.ollama, m.currentQ.Text, m.currentQ.Answer, m.hints)
	}

	if key == "n" && m.currentQ.Type == ollama.TypeMultiChoice {
		m.openNotesOverlay()
		return m, nil
	}

	// Regenerate question
	if key == "ctrl+r" {
		diff := ollama.DifficultyFromConfidence(m.currentConfidence())
		m.quizStep = stepLoading
		m.hints = nil
		m.mcEliminated = [4]bool{}
		m.mcWrongPicks = 0
		m.answerRevealed = false
		m.gradeFeedback = ""
		m.questionTab = qTabQuiz
		openQuestionLog(&m)
		return m, generateQuestion(m.ollamaCtx, m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	}

	// Multiple choice — pick a/b/c/d
	if m.currentQ.Type == ollama.TypeMultiChoice {
		switch {
		case key == "a" || key == "b" || key == "c" || key == "d":
			picked := int(key[0] - 'a')
			if m.mcEliminated[picked] {
				return m, nil // already eliminated
			}
			m.mcPicked = picked
			correct := picked == m.currentQ.CorrectIdx
			if correct {
				m.grade = gradeCorrect
				m.answerRevealed = true
				m.ratedConfidence = 0
				m.questionTab = qTabQuiz
				m.quizStep = stepResult
				m.syncViewport()
				return m, nil
			}
			// Wrong pick — eliminate option, stay on question
			m.mcEliminated[picked] = true
			m.mcWrongPicks++
			if m.mcWrongPicks >= 2 {
				// After 2 wrong picks, reveal the answer
				m.grade = gradeWrong
				m.answerRevealed = true
				m.ratedConfidence = 0
				m.questionTab = qTabQuiz
				m.quizStep = stepResult
				m.syncViewport()
				return m, nil
			}
			(&m).queueAlert(alertHint, "not quite — try again")
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

	// Non-MC: typed answer → ollama grades → result
	isCodeType := m.currentQ.Type == ollama.TypeFinishCode || m.currentQ.Type == ollama.TypeDebug || m.currentQ.Type == ollama.TypeCodeOutput
	submitKey := "enter"
	if isCodeType {
		submitKey = "ctrl+s"
	}
	switch {
	case key == submitKey:
		m.userAnswer = strings.TrimSpace(m.answerTA.Value())
		m.answerTA.Reset()
		m.ratedConfidence = 0
		m.answerRevealed = false
		m.gradeFeedback = ""
		m.questionTab = qTabQuiz
		m.quizStep = stepGrading
		return m, gradeAnswerCmd(m.ollamaCtx, m.ollama, m.currentQ.Type, m.currentQ.Text, m.currentQ.Answer, m.userAnswer)
	case key == "esc":
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
		conf := int(key[0] - '0')
		m.answerRevealed = true
		if m.ratedConfidence > 0 {
			// Re-rating: adjust confidence and XP delta
			oldXP := m.xpGained
			diffLevel := int(m.currentQ.Difficulty)
			staleDays := m.state.StaleDays(m.currentFile)
			if staleDays < 0 {
				staleDays = 0
			}
			newXP, breakdown := state.CalcXP(conf, diffLevel, m.state.DayStreak, staleDays)
			m.state.TotalXP += newXP - oldXP
			m.xpGained = newXP
			m.xpBreakdown = breakdown

			m.sessionConfSum += conf - m.ratedConfidence
			m.ratedConfidence = conf
			m.state.SetConfidence(m.currentFile, conf)
			if conf < m.sessionMinConf {
				m.sessionMinConf = conf
			}
			// Grade already set by ollama for typed answers, no override needed
			m.state.Save()
			m.syncViewport()
			return m, nil
		}
		m.ratedConfidence = conf
		m.state.SetConfidence(m.currentFile, conf)
		m.sessionConfSum += conf
		if conf < m.sessionMinConf {
			m.sessionMinConf = conf
		}

		correct := m.grade == gradeCorrect
		m.state.Record(m.currentFile, correct)
		if m.currentQ != nil {
			m.state.AddRecentQuestion(m.currentFile, m.currentQ.Type.String(), m.currentQ.Text, correct)
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

		// Combo tracking
		if m.grade == gradeCorrect || m.grade == gradePartial {
			m.comboCount++
			if m.comboCount > m.comboMax {
				m.comboMax = m.comboCount
			}
		} else {
			m.comboCount = 0
		}

		// Award XP
		prevLevel := m.state.Level()
		diffLevel := int(m.currentQ.Difficulty)
		staleDays := m.state.StaleDays(m.currentFile)
		if staleDays < 0 {
			staleDays = 0
		}
		xp, breakdown := state.CalcXP(conf, diffLevel, m.state.DayStreak, staleDays)

		bonus, casinoTierHit := rollCasinoBonus(m.comboCount)
		xp += bonus
		breakdown.Bonus = bonus
		breakdown.Total = xp

		m.state.AwardXP(xp)
		m.xpGained = xp
		m.xpBreakdown = breakdown

		// Level up detection
		newLevel := m.state.Level()
		if newLevel > prevLevel {
			m.levelUpFrom = prevLevel
			(&m).queueAlert(alertLevelUp, fmt.Sprintf("LEVEL UP! Lv.%d → Lv.%d", prevLevel, newLevel))
		}

		// XP toast (always shown, casino tier appended if notable)
		switch casinoTierHit {
		case casinoJackpot:
			(&m).queueAlert(alertXP, fmt.Sprintf("+%d XP  💎 JACKPOT!", xp))
		case casinoLucky:
			(&m).queueAlert(alertXP, fmt.Sprintf("+%d XP  🎰 lucky!", xp))
		default:
			(&m).queueAlert(alertXP, fmt.Sprintf("+%d XP", xp))
		}

		// Session milestone toasts
		switch m.sessionTotal {
		case 10, 25, 50, 100:
			(&m).queueAlert(alertHint, fmt.Sprintf("%d questions this session!", m.sessionTotal))
		}

		// Daily goal toast
		if m.dailyGoal > 0 {
			today := time.Now().Format("2006-01-02")
			todayQ := 0
			for _, s := range m.state.Sessions {
				if s.Date == today {
					todayQ += s.Total
				}
			}
			todayQ += m.sessionTotal
			if todayQ == m.dailyGoal {
				(&m).queueAlert(alertAchievement, fmt.Sprintf("daily goal hit! %d questions", m.dailyGoal))
			}
		}

		// Comeback detection: wrong on this file before, correct now
		isComeback := false
		if m.retryPhase && (m.grade == gradeCorrect || m.grade == gradePartial) {
			isComeback = true
		}

		// Check achievements
		newAch := m.state.CheckAchievements(state.AchievementContext{
			SessionTotal:    m.sessionTotal,
			SessionMinConf:  m.sessionMinConf,
			SessionDuration: time.Since(m.sessionStart),
			ChatQuestions:   m.learnChatCount,
			ComboMax:        m.comboMax,
			DomainCount:     len(m.sessionDomains),
			FileCount:       len(m.allFiles),
			LockedCount:     m.state.CountLocked(),
			HitJackpot:      casinoTierHit == casinoJackpot,
			IsComeback:      isComeback,
		})

		// Check domain mastery
		if !m.state.HasAchievement(state.AchSpecialist) {
			domain := m.currentDomain()
			if domain != "" && m.state.IsDomainMastered(m.allFiles, knowledge.Domain, domain) {
				if m.state.UnlockAchievement(state.AchSpecialist) {
					newAch = append(newAch, state.AchSpecialist)
				}
			}
		}
		for _, id := range newAch {
			info := state.AchievementInfo[id]
			(&m).queueAlert(alertAchievement, fmt.Sprintf("%s — %s", info.Name, info.Desc))
		}

		(&m).syncSessionRecordToState()
		m.state.Save()
		m.syncViewport()
		return m, nil
	case "r":
		if !m.answerRevealed {
			// Retry: go back to question with auto-hint
			m.quizStep = stepGrading
			m.answerTA.Reset()
			m.answerTA.Focus()
			m.questionTab = qTabQuiz
			return m, hintCmd(m.ollamaCtx, m.ollama, m.currentQ.Text, m.currentQ.Answer, m.hints)
		}
		// Re-quiz: generate a new question on the same topic
		diff := ollama.DifficultyFromConfidence(m.currentConfidence())
		m.quizStep = stepLoading
		m.hints = nil
		m.mcEliminated = [4]bool{}
		m.mcWrongPicks = 0
		m.answerRevealed = false
		m.gradeFeedback = ""
		m.ratedConfidence = 0
		m.activeOverlay = overlayNone
		openQuestionLog(&m)
		return m, generateQuestion(m.ollamaCtx, m.ollama, m.brainPath, m.currentFile, m.randomActiveType(), diff)
	case "e":
		m.explainLoading = true
		m.syncViewport()
		return m, explainMoreCmd(m.ollamaCtx, m.ollama, m.currentQ.Text, m.currentQ.Answer, m.currentQ.Explanation, m.sourceContent)
	case "n":
		m.openNotesOverlay()
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
		m.levelUpFrom = 0
		m.xpGained = 0
		return m, m.nextQuestion()
	case "esc":
		m.activeOverlay = overlayNone
		if m.ratedConfidence == 0 {
			// Auto-record neutral confidence so scheduling data isn't lost
			conf := 2
			if m.grade == gradeWrong {
				conf = 1
			}
			m.ratedConfidence = conf
			m.state.SetConfidence(m.currentFile, conf)
			m.sessionConfSum += conf
			if conf < m.sessionMinConf {
				m.sessionMinConf = conf
			}
			correct := m.grade == gradeCorrect
			m.state.Record(m.currentFile, correct)
			if m.currentQ != nil {
				m.state.AddRecentQuestion(m.currentFile, m.currentQ.Type.String(), m.currentQ.Text, correct)
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
			diffLevel := int(m.currentQ.Difficulty)
			staleDays := m.state.StaleDays(m.currentFile)
			if staleDays < 0 {
				staleDays = 0
			}
			xp, _ := state.CalcXP(conf, diffLevel, m.state.DayStreak, staleDays)
			m.state.TotalXP += xp
			(&m).syncSessionRecordToState()
			m.state.Save()
		}
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
		if handled, cmd := m.handleAuditFixKey(key); handled {
			return m, cmd
		}
		if handled, cmd := m.handleAuditResultKey(key); handled {
			return m, cmd
		}
		switch key {
		case "esc", "k":
			m.activeOverlay = overlayNone
			m.clearAuditState()
			return m, nil
		case "a":
			return m, m.startAudit()
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

	case overlayNotes:
		switch key {
		case "ctrl+s":
			notes := strings.TrimSpace(m.answerTA.Value())
			m.activeOverlay = overlayNone
			m.resetAnswerTA()
			return m, saveNotesCmd(m.brainPath, m.currentFile, notes)
		case "esc":
			m.activeOverlay = overlayNone
			m.resetAnswerTA()
			return m, nil
		default:
			var cmd tea.Cmd
			m.answerTA, cmd = m.answerTA.Update(msg)
			return m, cmd
		}

	case overlayChat:
		if handled, cmd := m.handleBankPreviewKey(key); handled {
			return m, cmd
		}
		switch key {
		case "ctrl+b":
			if m.currentFile != "" && len(m.conceptChat) > 0 {
				m.bankLoading = true
				return m, bankChatToNotesCmd(m.ollamaCtx, m.ollama, m.conceptChat, m.brainPath, m.currentFile, m.sourceContent)
			}
			return m, nil
		case "ctrl+y":
			if len(m.conceptChat) > 0 {
				return m, copyToClipboardCmd(formatChatLog(m.conceptChat))
			}
			return m, nil
		case "ctrl+l":
			m.conceptChat = nil
			m.syncOverlayViewport()
			return m, nil
		case "esc":
			m.activeOverlay = overlayNone
			m.conceptChatLoading = false
			m.resetAnswerTA()
			return m, nil
		case "up", "down", "pgup", "pgdown":
			var cmd tea.Cmd
			m.overlayViewport, cmd = m.overlayViewport.Update(msg)
			return m, cmd
		case "enter":
			return m, m.sendConceptChat(m.sourceContent, func() { m.syncOverlayViewport() })
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
			m.learnTA.Placeholder = "answer or press ctrl+g to generate..."
			m.learnTA.Focus()
			return m, learnClarifyCmd(m.ollamaCtx, m.ollama, topic, nil, m.allFiles)
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
		case "ctrl+g":
			// Generate knowledge from conversation context
			m.learnStep = learnGenerating
			var files []string
			if !strings.Contains(m.learnTopic, "/") && m.learnUpdateFile == "" {
				files = m.allFiles
			}
			return m, generateKnowledgeFromChat(m.ollamaCtx, m.ollama, m.learnTopic, m.learnChatHistory, files, m.brainPath, m.learnUpdateFile)
		case "enter":
			response := strings.TrimSpace(m.learnTA.Value())
			if response == "" {
				return m, nil
			}
			m.learnChatHistory = append(m.learnChatHistory, chatEntry{role: "user", content: response})
			m.learnTA.Reset()
			m.learnTA.Focus()
			m.learnChatLoading = true
			return m, learnClarifyCmd(m.ollamaCtx, m.ollama, m.learnTopic, m.learnChatHistory, m.allFiles)
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
			m.cancelOllama()
			m.learnStep = learnInput
			m.learnTA.Reset()
			m.learnTA.Placeholder = "e.g. docker/multi-stage-builds"
			m.learnTA.Focus()
			return m, nil
		}

	case learnReview:
		switch key {
		case "s":
			if m.learnDomain == "" {
				m.learnDomain = "general"
			}
			if m.learnSlug == "" {
				m.learnSlug = slugify(m.learnTopic)
			}
			if m.learnSlug == "" {
				return m, nil // can't save without a slug
			}
			return m, saveKnowledge(m.brainPath, m.learnDomain, m.learnSlug, m.learnContent)
		case "r":
			m.learnStep = learnGenerating
			var files []string
			if !strings.Contains(m.learnTopic, "/") && m.learnUpdateFile == "" {
				files = m.allFiles
			}
			return m, generateKnowledgeFromChat(m.ollamaCtx, m.ollama, m.learnTopic, m.learnChatHistory, files, m.brainPath, m.learnUpdateFile)
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

// --- Viewer (read-only knowledge browser) ---

func (m model) handleViewer(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()

	if handled, cmd := m.handleAuditFixKey(key); handled {
		return m, cmd
	}
	if handled, cmd := m.handleAuditResultKey(key); handled {
		return m, cmd
	}
	// Audit result: esc dismisses and restores content
	if m.auditResult != "" && key == "esc" {
		m.auditResult = ""
		m.viewport.SetContent(renderMarkdown(m.sourceContent, m.wrapW()))
		m.viewport.GotoTop()
		return m, nil
	}

	switch key {
	case "a":
		return m, m.startAudit()
	case "c":
		m.conceptChat = nil
		m.answerTA.Reset()
		m.answerTA.SetHeight(2)
		m.answerTA.Placeholder = "ask about this concept..."
		m.answerTA.Focus()
		m.activeOverlay = overlayChat
		m.syncOverlayViewport()
		return m, nil
	case "n":
		m.openNotesOverlay()
		return m, nil
	case "esc":
		m.auditResult = ""
		m.phase = phaseTopicList
		m.filterPickFiles()
		return m, nil
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
}

// applyCallLogger handles toggling the ollama logger on/off.
// Actual per-question files are opened by openQuestionLog.
func applyCallLogger(m *model) {
	if m.state == nil {
		return
	}
	if !m.state.LogCalls {
		if m.logFile != nil {
			m.logFile.Close()
			m.logFile = nil
		}
		m.ollama.SetLogger(nil)
	}
	// When LogCalls is turned on, the next question/challenge will open a file.
}

// openQuestionLog closes any existing log file and opens a new one for the current question.
func openQuestionLog(m *model) {
	if m.state == nil || !m.state.LogCalls {
		return
	}
	if m.logFile != nil {
		m.logFile.Close()
		m.logFile = nil
	}
	m.logQuestionNum++
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".local", "share", "unrot", "logs")
	_ = os.MkdirAll(dir, 0755)
	slug := strings.ReplaceAll(filepath.Base(strings.TrimSuffix(m.currentFile, ".md")), " ", "-")
	if m.phase == phaseChallenge {
		slug = "challenge-" + m.sessionStart.Format("150405")
	}
	name := fmt.Sprintf("%s_%03d_%s.log", time.Now().Format("2006-01-02"), m.logQuestionNum, slug)
	if f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		m.logFile = f
		m.ollama.SetLogger(f)
	}
}

// startSubsystemExtraction begins two-pass extraction for a subsystem at the given batch index.
func startSubsystemExtraction(m *model, batchIdx int) tea.Cmd {
	entry := &m.projectBatchEntries[batchIdx]
	entry.status = "extracting"
	m.projectFileIdx = 0
	m.projectRunningNotes = ""
	m.projectScanStatus = fmt.Sprintf("extracting %s (1/%d files)...", entry.slug, len(entry.files))
	if len(entry.files) == 0 {
		// No files — synthesize from arch context alone
		entry.status = "synthesizing"
		m.projectScanStatus = fmt.Sprintf("synthesizing %s...", entry.slug)
		return synthesizeSubsystemCmd(m.ollamaCtx, m.ollama, m.projectName, entry.slug, m.projectArchContext, "")
	}
	return extractFileNotesCmd(m.ollamaCtx, m.ollama, m.projectRepoPath, entry.slug, entry.files[0], "")
}

// openProjectLog opens a single log file for the entire project scan session.
func openProjectLog(m *model) {
	if m.state == nil || !m.state.LogCalls {
		return
	}
	if m.projectLogFile != nil {
		m.projectLogFile.Close()
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".local", "share", "unrot", "logs")
	_ = os.MkdirAll(dir, 0755)
	name := fmt.Sprintf("project_%s_%s.log", m.projectName, time.Now().Format("2006-01-02_150405"))
	if f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		m.projectLogFile = f
		m.ollama.SetLogger(f)
	}
}

// closeProjectLog closes the project log and clears the logger.
func closeProjectLog(m *model) {
	if m.projectLogFile != nil {
		m.projectLogFile.Close()
		m.projectLogFile = nil
		m.ollama.SetLogger(nil)
	}
}

// --- Settings ---

func (m model) handleSettings(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	idxSession := len(ollama.AllTypes)       // session length row
	idxChallDiff := len(ollama.AllTypes) + 1 // challenge difficulty row
	idxBrainPath := len(ollama.AllTypes) + 2 // brain path row
	idxLogCalls := len(ollama.AllTypes) + 3  // log ollama calls row
	maxIdx := idxLogCalls

	// Brain path editing mode — textarea captures all keys
	if m.settingsEditing {
		switch key {
		case "enter":
			newPath := strings.TrimSpace(m.learnTA.Value())
			if newPath != "" && newPath != m.brainPath {
				m.brainPath = newPath
				m.state.BrainPath = newPath
				m.state.Save()
				m.ollama = ollama.New()
				m.settingsEditing = false
				m.learnTA.Blur()
				return m, loadState(m.brainPath)
			}
			m.settingsEditing = false
			m.learnTA.Blur()
		case "esc":
			m.settingsEditing = false
			m.learnTA.Blur()
		default:
			var cmd tea.Cmd
			m.learnTA, cmd = m.learnTA.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	switch key {
	case "j", "down":
		if m.settingsCursor < maxIdx {
			m.settingsCursor++
		}
		m.viewport.SetContent(m.renderSettings())
		m.ensureSettingsCursorVisible()
	case "k", "up":
		if m.settingsCursor > 0 {
			m.settingsCursor--
		}
		m.viewport.SetContent(m.renderSettings())
		m.ensureSettingsCursorVisible()
	case "enter", " ":
		if m.settingsCursor < len(ollama.AllTypes) {
			// Toggle quiz type
			m.activeTypes[m.settingsCursor] = !m.activeTypes[m.settingsCursor]
			// Don't allow disabling all types
			anyOn := false
			for _, on := range m.activeTypes {
				if on {
					anyOn = true
					break
				}
			}
			if !anyOn {
				m.activeTypes[m.settingsCursor] = true
			}
		} else if m.settingsCursor == idxChallDiff {
			// Cycle challenge difficulty: adaptive → basic → intermediate → advanced → adaptive
			m.state.ChallengeDiff = (m.state.ChallengeDiff + 1) % 4
			m.state.Save()
		} else if m.settingsCursor == idxBrainPath {
			m.settingsEditing = true
			m.learnTA.SetValue(m.brainPath)
			m.learnTA.SetHeight(1)
			m.learnTA.CharLimit = 500
			m.learnTA.Placeholder = "path to knowledge files..."
			m.learnTA.Focus()
		} else if m.settingsCursor == idxLogCalls {
			m.state.LogCalls = !m.state.LogCalls
			m.state.Save()
			applyCallLogger(&m)
		}
	case "l", "right":
		if m.settingsCursor == idxSession {
			m.maxQuestions++
			m.state.MaxQuestions = m.maxQuestions
			m.state.Save()
		} else if m.settingsCursor == idxChallDiff {
			m.state.ChallengeDiff = (m.state.ChallengeDiff + 1) % 4
			m.state.Save()
		}
	case "h", "left":
		if m.settingsCursor == idxSession && m.maxQuestions > 1 {
			m.maxQuestions--
			m.state.MaxQuestions = m.maxQuestions
			m.state.Save()
		} else if m.settingsCursor == idxChallDiff {
			m.state.ChallengeDiff = (m.state.ChallengeDiff + 3) % 4 // wrap backwards
			m.state.Save()
		}
	case "e":
		// Start batch enrichment of all knowledge files
		if !m.enrichRunning && len(m.allFiles) > 0 {
			m.enrichRunning = true
			m.enrichIdx = 0
			m.enrichErrors = 0
			m.enrichFiles = make([]string, len(m.allFiles))
			copy(m.enrichFiles, m.allFiles)
			indexPath := filepath.Join(m.brainPath, "INDEX.md")
			indexContent, _ := os.ReadFile(indexPath)
			m.enrichIndex = string(indexContent)
			return m, enrichFileCmd(m.ollamaCtx, m.ollama, m.brainPath, m.enrichIndex, m.enrichFiles[0])
		}
	case "esc":
		m.goHome()
		return m, nil
	default:
		// Forward scroll keys to viewport
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.viewport.SetContent(m.renderSettings())
		return m, cmd
	}
	m.viewport.SetContent(m.renderSettings())
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

// --- Challenge ---

func (m model) handleChallenge(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()
	m.toast = ""
	switch m.challengeStep {
	case challengeInput:
		switch key {
		case "esc":
			m.goHome()
			return m, nil
		case "enter":
			topic := strings.TrimSpace(m.learnTA.Value())
			if topic == "" {
				// No topic = random challenge (original behavior)
				m.challengeStep = challengeLoading
				openQuestionLog(&m)
				return m, generateChallengeCmd(m.ollamaCtx, m.ollama, m.domainFilter, m.challengeDiff)
			}
			m.challengeTopic = topic
			m.challengeChatHistory = nil
			m.challengeChatLoading = true
			m.challengeStep = challengeChat
			m.learnTA.Reset()
			m.learnTA.Placeholder = "answer or press ctrl+g to generate..."
			m.learnTA.Focus()
			openQuestionLog(&m)
			return m, challengeClarifyCmd(m.ollamaCtx, m.ollama, topic, nil)
		default:
			var cmd tea.Cmd
			m.learnTA, cmd = m.learnTA.Update(msg)
			return m, cmd
		}

	case challengeChat:
		if m.challengeChatLoading {
			if key == "esc" {
				m.challengeChatLoading = false
				m.challengeStep = challengeInput
				m.learnTA.Reset()
				m.learnTA.Placeholder = "e.g. binary search in Go, React hooks, SQL joins..."
				m.learnTA.Focus()
				return m, nil
			}
			return m, nil
		}
		switch key {
		case "ctrl+g":
			m.challengeStep = challengeLoading
			return m, generateChallengeFromChatCmd(m.ollamaCtx, m.ollama, m.challengeTopic, m.challengeChatHistory, m.challengeDiff)
		case "enter":
			response := strings.TrimSpace(m.learnTA.Value())
			if response == "" {
				return m, nil
			}
			m.challengeChatHistory = append(m.challengeChatHistory, chatEntry{role: "user", content: response})
			m.learnTA.Reset()
			m.learnTA.Focus()
			m.challengeChatLoading = true
			return m, challengeClarifyCmd(m.ollamaCtx, m.ollama, m.challengeTopic, m.challengeChatHistory)
		case "esc":
			m.challengeStep = challengeInput
			m.learnTA.Reset()
			m.learnTA.Placeholder = "e.g. binary search in Go, React hooks, SQL joins..."
			m.learnTA.Focus()
			m.challengeChatHistory = nil
			return m, nil
		default:
			var cmd tea.Cmd
			m.learnTA, cmd = m.learnTA.Update(msg)
			return m, cmd
		}

	case challengeLoading, challengeGrading:
		if key == "esc" {
			m.cancelOllama()
			m.goHome()
			return m, nil
		}
	case challengeWorking:
		// Tab/shift+tab cycle through tabs
		if key == "tab" || key == "shift+tab" {
			if m.challengeTab == cTabCode {
				m.savedChallengeCode = m.answerTA.Value()
			} else if m.challengeTab == cTabChat {
				m.savedChatInput = m.answerTA.Value()
			}
			if key == "tab" {
				m.challengeTab = (m.challengeTab + 1) % 3
			} else {
				m.challengeTab = (m.challengeTab + 2) % 3
			}
			m.applyChallengeTab()
			m.syncViewport()
			return m, nil
		}
		// esc on non-code tab returns to code tab; on code tab goes home
		if key == "esc" {
			if m.challengeTab != cTabCode {
				if m.challengeTab == cTabChat {
					m.savedChatInput = m.answerTA.Value()
				}
				m.challengeTab = cTabCode
				m.applyChallengeTab()
				m.syncViewport()
				return m, nil
			}
			m.goHome()
			return m, nil
		}
		// Chat tab: send messages
		if m.challengeTab == cTabChat {
			switch key {
			case "ctrl+y":
				if len(m.conceptChat) > 0 {
					return m, copyToClipboardCmd(formatChatLog(m.conceptChat))
				}
				return m, nil
			case "ctrl+l":
				m.conceptChat = nil
				m.syncViewport()
				return m, nil
			case "enter":
				ctx := ""
				if m.currentChallenge != nil {
					ctx = m.currentChallenge.Description
				}
				return m, m.sendConceptChat(ctx, func() { m.syncChallengeChatViewport() })
			case "pgup", "pgdown", "up", "down":
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			default:
				var cmd tea.Cmd
				m.answerTA, cmd = m.answerTA.Update(msg)
				return m, cmd
			}
		}
		// Problem tab: scroll only
		if m.challengeTab == cTabProblem {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
		// Code tab
		switch key {
		case "ctrl+e":
			if m.currentChallenge != nil {
				return m, challengeHintCmd(m.ollamaCtx, m.ollama, m.currentChallenge.Description, m.challengeHints)
			}
			return m, nil
		case "ctrl+s":
			code := strings.TrimSpace(m.answerTA.Value())
			if code == "" {
				return m, nil
			}
			m.challengeCode = code
			m.challengeStep = challengeGrading
			m.challengeTab = cTabCode
			return m, gradeChallengeCmd(m.ollamaCtx, m.ollama, m.currentChallenge, code)
		case "pgup", "pgdown", "up", "down":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		default:
			var cmd tea.Cmd
			m.answerTA, cmd = m.answerTA.Update(msg)
			return m, cmd
		}
	case challengeResult:
		// Tab/shift+tab cycle through tabs
		if key == "tab" || key == "shift+tab" {
			if m.challengeTab == cTabChat {
				m.savedChatInput = m.answerTA.Value()
			}
			if key == "tab" {
				m.challengeTab = (m.challengeTab + 1) % 3
			} else {
				m.challengeTab = (m.challengeTab + 2) % 3
			}
			m.applyChallengeTab()
			m.syncViewport()
			return m, nil
		}
		// esc on non-code tab returns to code tab; on code tab goes home
		if key == "esc" {
			if m.challengeTab != cTabCode {
				if m.challengeTab == cTabChat {
					m.savedChatInput = m.answerTA.Value()
				}
				m.challengeTab = cTabCode
				m.applyChallengeTab()
				m.syncViewport()
				return m, nil
			}
			m.goHome()
			return m, nil
		}
		// Chat tab: send messages
		if m.challengeTab == cTabChat {
			switch key {
			case "ctrl+y":
				if len(m.conceptChat) > 0 {
					return m, copyToClipboardCmd(formatChatLog(m.conceptChat))
				}
				return m, nil
			case "ctrl+l":
				m.conceptChat = nil
				m.syncViewport()
				return m, nil
			case "enter":
				ctx := ""
				if m.currentChallenge != nil {
					ctx = m.currentChallenge.Description
				}
				return m, m.sendConceptChat(ctx, func() { m.syncChallengeChatViewport() })
			case "pgup", "pgdown", "up", "down":
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			default:
				var cmd tea.Cmd
				m.answerTA, cmd = m.answerTA.Update(msg)
				return m, cmd
			}
		}
		switch key {
		case "r":
			m.challengeRetrying = true
			m.challengeGrade = nil
			m.challengeCode = ""
			m.xpGained = 0
			m.challengeStep = challengeWorking
			m.challengeTab = cTabCode
			m.savedChallengeCode = ""
			m.answerTA.Reset()
			m.answerTA.CharLimit = 3000
			m.answerTA.Placeholder = "write your code..."
			m.answerTA.Focus()
			m.syncViewport()
			return m, nil
		case "enter", " ":
			return m, m.nextChallengeCmd()
		case "esc":
			m.goHome()
			return m, nil
		default:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	}
	return m, nil
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

// --- Project Scan ---

func (m model) handleProject(msg tea.KeyMsg) (model, tea.Cmd) {
	key := msg.String()

	switch m.projectStep {
	case projectRepoInput:
		switch key {
		case "esc":
			m.goHome()
			return m, nil
		case "enter":
			raw := strings.TrimSpace(m.learnTA.Value())
			if raw == "" {
				return m, nil
			}
			// Expand ~ to home dir
			if strings.HasPrefix(raw, "~/") {
				home, _ := os.UserHomeDir()
				raw = filepath.Join(home, raw[2:])
			}
			// Validate directory exists
			if info, err := os.Stat(raw); err != nil || !info.IsDir() {
				m.toast = "not a valid directory: " + raw
				return m, nil
			}
			m.projectRepoPath = raw
			m.projectName = filepath.Base(raw)
			m.projectArchContext = readProjectContext(raw)
			m.projectStartTime = time.Now()
			m.projectStep = projectCheckingStale
			return m, checkProjectStalenessCmd(m.brainPath, raw, m.projectName)
		default:
			var cmd tea.Cmd
			m.learnTA, cmd = m.learnTA.Update(msg)
			return m, cmd
		}

	case projectCheckingStale:
		if key == "esc" {
			m.projectStep = projectRepoInput
			m.learnTA.Reset()
			m.learnTA.Placeholder = "repo path (e.g. ~/projects/myapp)"
			m.learnTA.Focus()
			return m, nil
		}
		return m, nil

	case projectStaleResult:
		switch key {
		case "esc":
			m.projectStep = projectRepoInput
			m.learnTA.Reset()
			m.learnTA.Placeholder = "repo path (e.g. ~/projects/myapp)"
			m.learnTA.Focus()
			return m, nil
		case "enter":
			// Re-scan stale subsystems only (drift > 0 or unknown)
			var stale []projectBatchEntry
			for _, si := range m.projectStaleEntries {
				if si.drift != 0 {
					files := strings.Split(si.files, ", ")
					stale = append(stale, projectBatchEntry{
						slug:      si.slug,
						files:     files,
						fileCount: len(files),
						status:    "pending",
					})
				}
			}
			if len(stale) == 0 {
				m.toast = "all subsystems are up to date"
				return m, nil
			}
			openProjectLog(&m)
			m.projectBatchEntries = stale
			m.projectSubsystem = stale[0].slug
			m.projectBatchQueue = nil
			for _, e := range stale[1:] {
				m.projectBatchQueue = append(m.projectBatchQueue, e.slug)
			}
			m.projectStep = projectGenerating
			projectLog("stale re-scan: %s — %d subsystems", m.projectName, len(stale))
			return m, startSubsystemExtraction(&m, 0)
		case "a":
			// Full re-scan — propose fresh
			openProjectLog(&m)
			m.projectStep = projectProposing
			tree := listRepoTree(m.projectRepoPath)
			return m, proposeSubsystemsCmd(m.ollamaCtx, m.ollama, m.projectRepoPath, m.projectArchContext, tree)
		}
		return m, nil

	case projectProposing:
		if key == "esc" {
			closeProjectLog(&m)
			m.projectStep = projectRepoInput
			m.learnTA.Reset()
			m.learnTA.Placeholder = "repo path (e.g. ~/projects/myapp)"
			m.learnTA.Focus()
			return m, nil
		}
		return m, nil

	case projectGenerating:
		if key == "esc" {
			closeProjectLog(&m)
			m.goHome()
			return m, nil
		}
		return m, nil

	case projectDone:
		if key == "esc" || key == "enter" {
			m.goHome()
			return m, nil
		}
		return m, nil
	}
	return m, nil
}
