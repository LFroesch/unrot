package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LFroesch/unrot/internal/knowledge"
	"github.com/LFroesch/unrot/internal/ollama"
	"github.com/LFroesch/unrot/internal/state"
	"github.com/charmbracelet/lipgloss"
)

// viewportContent builds scrollable content for the current phase/step.
func (m model) viewportContent() string {
	switch m.phase {
	case phaseQuiz:
		switch m.quizStep {
		case stepLesson:
			return m.buildLessonContent()
		case stepResult:
			return m.buildResultContent()
		}
	case phaseLearn:
		if m.learnStep == learnReview {
			return m.learnContent
		}
	case phaseStats:
		return m.buildStatsContent()
	case phaseChallenge:
		switch m.challengeTab {
		case cTabChat:
			return m.buildChatTabContent()
		case cTabProblem:
			return m.buildChallengeProblemContent()
		case cTabCode:
			if m.challengeStep == challengeResult {
				return m.buildChallengeCodeReviewContent()
			}
			return m.buildChallengeProblemContent()
		case cTabExplanation:
			return m.buildChallengeResultContent()
		}
	case phaseViewer:
		return renderMarkdown(m.sourceContent, m.viewport.Width)
	case phaseDone:
		return m.buildDoneContent()
	}
	return ""
}

// buildOverlayContent builds content for the active overlay.
func (m model) buildOverlayContent() string {
	switch m.activeOverlay {
	case overlayKnowledge:
		return renderMarkdown(m.sourceContent, m.overlayViewport.Width-4)
	case overlayChat:
		return m.buildChatOverlayContent()
	case overlayDomain:
		return m.buildDomainOverlayContent()
	}
	return ""
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	// Terminal too small warning
	if m.width < minTerminalWidth || m.height < minTerminalHeight {
		return lipgloss.NewStyle().
			Foreground(colorWarn).
			Bold(true).
			Padding(1).
			Render(fmt.Sprintf(
				"terminal too small: %dx%d\nminimum: %dx%d\n\nplease resize or zoom out",
				m.width, m.height, minTerminalWidth, minTerminalHeight,
			))
	}

	header := m.renderHeader()
	content := m.renderContent()
	status := m.renderStatus()

	// Toast notification
	toastLine := ""
	if m.toast != "" {
		toastLine = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true).
			Render("  ★ "+m.toast) + "\n"
	}

	// Assemble: header + toast + content + padding + status
	// Content panel already has fixed height from contentHeight()
	base := header + "\n" + toastLine + content + "\n" + status

	// Render overlay on top if active
	if m.activeOverlay != overlayNone {
		overlay := m.renderOverlay()
		base = placeOverlay(base, overlay)
	}

	return base
}

func (m model) renderHeader() string {
	title := headerTitleStyle.Render("unrot")

	var parts []string
	parts = append(parts, title)

	// Level badge
	if m.state != nil {
		lvl := m.state.Level()
		cur, needed := m.state.LevelProgress()
		parts = append(parts, headerStreakStyle.Render(fmt.Sprintf("Lv.%d", lvl)))
		// Mini XP bar
		filled := cur * 10 / needed
		if filled > 10 {
			filled = 10
		}
		parts = append(parts, headerBarFilledStyle.Render(strings.Repeat("█", filled))+headerBarEmptyStyle.Render(strings.Repeat("░", 10-filled))+headerDimStyle.Render(fmt.Sprintf(" %d/%d", cur, needed)))
		// Flash +XP after rating
		if m.xpGained > 0 && m.phase == phaseQuiz && m.quizStep == stepResult && m.ratedConfidence > 0 {
			xpText := fmt.Sprintf("+%d XP", m.xpGained)
			if m.xpBreakdown.Bonus >= 50 {
				xpText += " 💎"
			} else if m.xpBreakdown.Bonus >= 20 {
				xpText += " 🎰"
			}
			parts = append(parts, headerStreakStyle.Render(xpText))
		}
		// Streak multiplier (always show if > 1.0)
		if m.state.DayStreak > 0 {
			mult := 1.0 + float64(m.state.DayStreak)*0.1
			if mult > 2.0 {
				mult = 2.0
			}
			if mult > 1.0 {
				parts = append(parts, headerWarnStyle.Render(fmt.Sprintf("×%.1f", mult)))
			}
		}
		// Combo streak
		if m.comboCount >= 2 {
			parts = append(parts, lipgloss.NewStyle().Foreground(colorWarn).Background(lipgloss.Color("235")).Bold(true).Inline(true).Render(fmt.Sprintf("🔥×%d", m.comboCount)))
		}
	}

	if m.phase == phaseChallenge {
		if m.currentChallenge != nil {
			parts = append(parts, headerAccentStyle.Render(m.currentChallenge.Language))
			if m.currentChallenge.Difficulty > 0 {
				parts = append(parts, m.headerDiffStyle(m.currentChallenge.Difficulty))
			}
		}
		parts = append(parts, headerWarnStyle.Render("challenge"))
	} else if m.phase == phaseViewer {
		domain := knowledge.Domain(m.currentFile)
		if domain != "" {
			parts = append(parts, headerAccentStyle.Render(domain))
		}
		parts = append(parts, headerDimStyle.Render("viewer"))
	} else {
		domain := m.currentDomain()
		if domain != "" {
			parts = append(parts, headerAccentStyle.Render(domain))
		}
		if m.phase == phaseQuiz && m.currentFile != "" {
			slug := strings.TrimSuffix(filepath.Base(m.currentFile), ".md")
			parts = append(parts, headerDimStyle.Render(slug))
		}
		if m.phase == phaseQuiz && m.lastQSet {
			parts = append(parts, headerWarnStyle.Render(m.lastQType.String()))
			if m.lastQDiff > 0 {
				parts = append(parts, m.headerDiffStyle(m.lastQDiff))
			}
		}
	}
	if m.retryPhase {
		parts = append(parts, headerRetryStyle.Render("retry"))
	}

	left := strings.Join(parts, headerDimStyle.Render(" · "))

	var rightParts []string
	// Session elapsed time (during quiz or challenge)
	if !m.sessionStart.IsZero() && (m.phase == phaseQuiz || m.phase == phaseChallenge || m.phase == phaseDone) {
		elapsed := int(time.Since(m.sessionStart).Seconds())
		rightParts = append(rightParts, headerDimStyle.Render(formatDuration(elapsed)))
	}
	if m.phase == phaseChallenge && m.challengeCount > 0 {
		rightParts = append(rightParts, headerStatsStyle.Render(fmt.Sprintf("#%d", m.challengeCount)))
	} else if m.sessionTotal > 0 || len(m.reviewFiles) > 0 {
		rightParts = append(rightParts, m.renderHeaderProgress())
	}
	if hint := m.headerScrollHint(); hint != "" {
		rightParts = append(rightParts, hint)
	}
	right := strings.Join(rightParts, "  ")

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2 // -2 for padding
	if gap < 1 {
		gap = 1
	}

	bar := left + strings.Repeat(" ", gap) + right
	return headerBarStyle.Width(m.width).Render(bar)
}

func (m model) headerDiffStyle(d ollama.Difficulty) string {
	bg := lipgloss.Color("235")
	switch d {
	case ollama.DiffAdvanced:
		return lipgloss.NewStyle().Foreground(colorError).Background(bg).Bold(true).Inline(true).Render("hard")
	case ollama.DiffIntermediate:
		return lipgloss.NewStyle().Foreground(colorPrimary).Background(bg).Inline(true).Render("med")
	default:
		return lipgloss.NewStyle().Foreground(colorPrimary).Background(bg).Inline(true).Render("easy")
	}
}

func (m model) renderHeaderProgress() string {
	total := len(m.reviewFiles)
	if m.retryPhase {
		total = m.sessionTotal + len(m.retryQueue) + 1
	}
	if total == 0 {
		total = 1
	}
	progress := fmt.Sprintf("%d/%d", m.sessionTotal, total)
	if m.sessionTotal > 0 {
		avg := float64(m.sessionConfSum) / float64(m.sessionTotal)
		progress += fmt.Sprintf(" · avg %.1f", avg)
	}
	return headerStatsStyle.Render(progress)
}

// headerScrollHint returns scroll indicator for the active viewport (if any).
func (m model) headerScrollHint() string {
	if m.activeOverlay != overlayNone {
		return scrollHint(m.overlayViewport)
	}
	switch m.phase {
	case phaseQuiz:
		if m.quizStep == stepLesson || m.quizStep == stepResult || (m.quizStep == stepQuestion && m.questionTab != qTabQuiz) {
			return scrollHint(m.viewport)
		}
	case phaseStats, phaseDone:
		return scrollHint(m.viewport)
	case phaseChallenge:
		if m.challengeStep == challengeResult || m.challengeStep == challengeWorking {
			return scrollHint(m.viewport)
		}
	case phaseLearn:
		if m.learnStep == learnReview {
			return scrollHint(m.viewport)
		}
	}
	return ""
}

func (m model) renderContent() string {
	panel := panelStyle.Width(m.width).Height(m.contentHeight())

	var content string
	switch m.phase {
	case phaseDashboard:
		content = m.renderDashboard()
	case phaseTopicList:
		content = m.renderTopicList()
	case phaseQuiz:
		content = m.renderQuiz()
	case phaseLearn:
		content = m.renderLearn()
	case phaseStats:
		content = m.viewport.View()
	case phaseSettings:
		content = m.renderSettings()
	case phaseChallenge:
		content = m.renderChallenge()
	case phaseViewer:
		content = m.viewport.View()
	case phaseError:
		content = errStyle.Render(fmt.Sprintf("  error: %v", m.err))
	case phaseDone:
		content = m.viewport.View()
	}

	return panel.Render(content)
}

// --- Dashboard ---

func (m model) renderDashboard() string {
	var b strings.Builder

	// Level + XP bar (Removed)
	if m.state != nil {
		cur, needed := m.state.LevelProgress()
		barW := 30
		filled := cur * barW / needed
		if filled > barW {
			filled = barW
		}

		// Streak
		if m.state.DayStreak > 0 {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  %d day streak", m.state.DayStreak)))
		}
		b.WriteString("\n\n")
	}

	// Daily goal progress
	if m.dailyGoal > 0 && m.state != nil {
		todayTotal := 0
		for _, s := range m.state.TodaySessions() {
			todayTotal += s.Total
		}
		goalStr := fmt.Sprintf("  daily goal: %d/%d", todayTotal, m.dailyGoal)
		if todayTotal >= m.dailyGoal {
			b.WriteString(correctStyle.Render(goalStr + " done!"))
		} else {
			filled := todayTotal * 10 / m.dailyGoal
			if filled > 10 {
				filled = 10
			}
			b.WriteString(dimStyle.Render(goalStr) + "  " + barFilledStyle.Render(strings.Repeat("█", filled)) + barEmptyStyle.Render(strings.Repeat("░", 10-filled)))
		}
		b.WriteString("\n\n")
	}
	// Active domain filter
	domainLabel := "all"
	if m.domainFilter != "" {
		domainLabel = m.domainFilter
	}

	b.WriteString(fmt.Sprintf("  domain: %s  %s\n\n",
		keyStyle.Render(domainLabel),
		dimStyle.Render("[tab]"),
	))

	// Confidence distribution
	files := m.allFiles
	if m.domainFilter != "" {
		files = knowledge.FilterByDomain(files, m.domainFilter)
	}
	tiers := [6]int{} // index 0=new, 1=weak, ..., 5=locked
	for _, f := range files {
		c := m.state.GetConfidence(f)
		tiers[c]++
	}

	b.WriteString(divider("ready to review", m.wrapW()))
	b.WriteString("\n")
	var distParts []string
	tierNames := []string{"new", "weak", "shaky", "okay", "solid", "locked"}
	for i, count := range tiers {
		if count > 0 {
			label := fmt.Sprintf("%d %s", count, tierNames[i])
			color := confidenceColor(i)
			distParts = append(distParts, lipgloss.NewStyle().Foreground(color).Render(label))
		}
	}
	if len(distParts) > 0 {
		b.WriteString("  " + strings.Join(distParts, dimStyle.Render(" · ")))
	} else {
		b.WriteString(dimStyle.Render("  no knowledge files"))
	}
	b.WriteString("\n\n")

	// Quick actions
	favCount := len(m.state.FavoritePaths(m.allFiles))
	actions := []struct{ key, name, desc string }{
		{"r", "review", "smart review based on priority"},
		{"F", "focused", fmt.Sprintf("review favorites only (%d)", favCount)},
		{"i", "challenge", "coding exercises & interview prep"},
		{"b", "browse topics", "drill a specific concept"},
		{"l", "learn new", "learn something new"},
		{"s", "settings", "quiz types"},
		{"a", "stats", "progress & achievements"},
	}
	for _, a := range actions {
		b.WriteString(optionStyle.Render(
			keyStyle.Render("["+a.key+"]") + " " + a.name + "  " + dimStyle.Render(a.desc),
		))
		b.WriteString("\n")
	}

	// Recent sessions
	if m.state != nil {
		today := m.state.TodaySessions()
		if len(today) > 0 {
			b.WriteString("\n")
			b.WriteString(divider("recent", m.wrapW()))
			b.WriteString("\n")
			totalQ, totalC := 0, 0
			var domains []string
			domainSet := make(map[string]bool)
			for _, s := range today {
				totalQ += s.Total
				totalC += s.Correct
				for _, d := range s.Domains {
					if !domainSet[d] {
						domainSet[d] = true
						domains = append(domains, d)
					}
				}
			}
			acc := 0
			if totalQ > 0 {
				acc = totalC * 100 / totalQ
			}
			b.WriteString(fmt.Sprintf("  today   %d/%d correct  (%s)\n",
				totalC, totalQ,
				dimStyle.Render(fmt.Sprintf("%d%%", acc)),
			))
			if len(domains) > 0 {
				b.WriteString(dimStyle.Render("          " + strings.Join(domains, ", ")))
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d files in bank", len(m.allFiles))))
	if m.maxQuestions > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf(" · %d per session", m.maxQuestions)))
	}

	return b.String()
}

// --- Topic List ---

func (m model) renderTopicList() string {
	var b strings.Builder

	b.WriteString(questionStyle.Render("browse topics"))
	domainLabel := "all"
	if m.domainFilter != "" {
		domainLabel = m.domainFilter
	}
	b.WriteString("  " + keyStyle.Render(domainLabel) + "  " + dimStyle.Render("[tab] change"))
	b.WriteString("\n")

	// Search bar
	if m.pickSearching {
		b.WriteString("\n  " + dimStyle.Render("/") + " " + m.pickSearch.View())
	} else {
		b.WriteString("\n" + dimStyle.Render("  / to search"))
	}
	b.WriteString("\n")

	if len(m.pickFiles) == 0 {
		b.WriteString("\n")
		if m.pickSearching && m.pickSearch.Value() != "" {
			b.WriteString(dimStyle.Render("  no matches"))
		} else {
			b.WriteString(dimStyle.Render("  no knowledge files found"))
		}
		return b.String()
	}

	b.WriteString("\n")

	visible := m.contentHeight() - 12
	if visible < 5 {
		visible = 5
	}

	start := m.pickCursor - visible/2
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > len(m.pickFiles) {
		end = len(m.pickFiles)
		start = end - visible
		if start < 0 {
			start = 0
		}
	}

	lastDomain := ""
	if start > 0 {
		lastDomain = knowledge.Domain(m.pickFiles[start])
		avg := m.pickDomainAvgConfidence(lastDomain)
		b.WriteString(fmt.Sprintf("  %s  %s\n",
			domainStyle.Render(lastDomain),
			confidenceDots(int(avg+0.5)),
		))
	}

	for i := start; i < end; i++ {
		file := m.pickFiles[i]
		domain := knowledge.Domain(file)
		name := strings.TrimSuffix(filepath.Base(file), ".md")

		if domain != lastDomain {
			if lastDomain != "" {
				b.WriteString("\n")
			}
			avg := m.pickDomainAvgConfidence(domain)
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				domainStyle.Render(domain),
				confidenceDots(int(avg+0.5)),
			))
			lastDomain = domain
		}

		conf := 0
		viewedLabel := ""
		if m.state != nil {
			conf = m.state.GetConfidence(file)
			days := m.state.StaleDays(file)
			if days == 0 {
				viewedLabel = "today"
			} else if days == 1 {
				viewedLabel = "1d ago"
			} else if days > 1 {
				viewedLabel = stalenessLabel(days)
				if viewedLabel != "" {
					viewedLabel += " ago"
				}
			}
		}
		dots := confidenceDots(conf)
		favMark := ""
		if m.state != nil && m.state.IsFavorite(file) {
			favMark = streakStyle.Render("★ ")
		}

		nameWidth := m.width - 28
		if nameWidth < 20 {
			nameWidth = 20
		}
		if nameWidth > 40 {
			nameWidth = 40
		}
		nameStyled := lipgloss.NewStyle().Width(nameWidth)
		suffix := dots
		if viewedLabel != "" {
			suffix += " " + dimStyle.Render(viewedLabel)
		}
		if i == m.pickCursor {
			b.WriteString(fmt.Sprintf("  %s %s%s %s\n",
				cursorStyle.Render(">"),
				favMark,
				nameStyled.Foreground(colorText).Render(name),
				suffix,
			))
		} else {
			b.WriteString(fmt.Sprintf("    %s%s %s\n",
				favMark,
				nameStyled.Render(name),
				suffix,
			))
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d/%d", m.pickCursor+1, len(m.pickFiles))))

	return b.String()
}

// pickDomainAvgConfidence returns avg confidence for a domain within pickFiles.
func (m model) pickDomainAvgConfidence(domain string) float64 {
	if m.state == nil {
		return 0
	}
	count := 0
	total := 0
	for _, f := range m.pickFiles {
		if knowledge.Domain(f) == domain {
			total += m.state.GetConfidence(f)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return float64(total) / float64(count)
}

// --- Quiz ---

func (m model) renderQuiz() string {
	switch m.quizStep {
	case stepLesson:
		return m.viewport.View()
	case stepLoading:
		return "\n  " + m.spinner.View() + " generating question...\n"
	case stepQuestion:
		return m.renderQuestion()
	case stepGrading:
		return m.renderGrading()
	case stepResult:
		return m.viewport.View()
	}
	return ""
}

func (m model) buildLessonContent() string {
	var b strings.Builder
	w := m.wrapW()

	b.WriteString(labelStyle.Render("  study first"))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Width(w).PaddingLeft(2).Render(strings.Repeat("─", w-4)))
	b.WriteString("\n")
	b.WriteString(renderMarkdown(m.sourceContent, w))
	b.WriteString("\n")

	return b.String()
}

func (m model) renderQuestionTabs() string {
	active := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	tabs := []struct {
		label string
		tab   questionTab
	}{
		{"chat", qTabChat},
		{"quiz", qTabQuiz},
		{"knowledge", qTabKnowledge},
	}
	var parts []string
	for _, t := range tabs {
		if m.questionTab == t.tab {
			parts = append(parts, active.Render("["+t.label+"]"))
		} else {
			parts = append(parts, dimStyle.Render(" "+t.label+" "))
		}
	}
	return "  " + strings.Join(parts, " ")
}

func (m model) renderQuestion() string {
	var b strings.Builder
	w := m.wrapW()

	// Tab bar
	b.WriteString(m.renderQuestionTabs())
	b.WriteString("\n\n")

	switch m.questionTab {
	case qTabChat:
		b.WriteString(m.viewport.View())
		b.WriteString("\n")
		b.WriteString(labelStyle.Render("  ask a question"))
		b.WriteString("\n\n" + m.answerTA.View())
		b.WriteString("\n")
		return b.String()

	case qTabKnowledge:
		b.WriteString(m.viewport.View())
		b.WriteString("\n")
		return b.String()
	}

	// qTabQuiz — default
	isCodeQ := m.currentQ.Type == ollama.TypeFinishCode || m.currentQ.Type == ollama.TypeDebug || m.currentQ.Type == ollama.TypeCodeOutput
	if isCodeQ {
		codeStyle := lipgloss.NewStyle().
			PaddingLeft(2).PaddingTop(1).PaddingBottom(1).
			Foreground(colorText)
		var cb strings.Builder
		for _, line := range strings.Split(m.currentQ.Text, "\n") {
			cb.WriteString(highlightSyntax(line))
			cb.WriteString("\n")
		}
		b.WriteString(codeStyle.Width(w).Render(cb.String()))
	} else {
		b.WriteString(questionStyle.Width(w).Render(m.currentQ.Text))
	}
	b.WriteString("\n")

	if m.currentQ.Type == ollama.TypeMultiChoice {
		b.WriteString("\n")
		for i, opt := range m.currentQ.Options {
			letter := string(rune('a' + i))
			if m.mcEliminated[i] {
				b.WriteString(optionStyle.Width(w).Render(
					wrongStyle.Render(letter+") ") + dimStyle.Render(opt),
				))
			} else {
				b.WriteString(optionStyle.Width(w).Render(
					keyStyle.Render(letter) + dimStyle.Render(") ") + opt,
				))
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("\n")
		var ansLabel string
		switch m.currentQ.Type {
		case ollama.TypeFinishCode:
			ansLabel = "  complete the missing line"
		case ollama.TypeDebug:
			ansLabel = "  describe the bug and fix"
		case ollama.TypeCodeOutput:
			ansLabel = "  what does this output?"
		default:
			ansLabel = "  your answer"
		}
		b.WriteString(labelStyle.Render(ansLabel))
		b.WriteString("\n\n")
		b.WriteString(m.answerTA.View())
	}

	if len(m.hints) > 0 {
		b.WriteString("\n\n")
		b.WriteString(divider("hints", w))
		for _, h := range m.hints {
			b.WriteString("\n")
			b.WriteString(explainStyle.Width(w).Render("  " + h))
		}
	}

	b.WriteString("\n")
	return b.String()
}

func (m model) renderGrading() string {
	var b strings.Builder
	w := m.wrapW()

	b.WriteString("\n")
	if m.currentQ != nil {
		b.WriteString(questionStyle.Width(w).Render(m.currentQ.Text))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString("  " + m.spinner.View() + " thinking...")
	b.WriteString("\n")

	return b.String()
}

func (m model) buildResultContent() string {
	var b strings.Builder
	w := m.wrapW()

	b.WriteString("\n")
	b.WriteString(questionStyle.Width(w).Render(m.currentQ.Text))
	b.WriteString("\n")

	if m.currentQ.Type == ollama.TypeMultiChoice {
		b.WriteString("\n")
		for i, opt := range m.currentQ.Options {
			letter := string(rune('a' + i))
			line := fmt.Sprintf("%s) %s", letter, opt)
			switch {
			case i == m.currentQ.CorrectIdx && i == m.mcPicked:
				b.WriteString(optionStyle.Width(w).Render(correctStyle.Render("  ✓ " + line)))
			case i == m.currentQ.CorrectIdx:
				b.WriteString(optionStyle.Width(w).Render(correctStyle.Render("  > " + line)))
			case i == m.mcPicked:
				b.WriteString(optionStyle.Width(w).Render(wrongStyle.Render("  ✗ " + line)))
			case m.mcEliminated[i]:
				b.WriteString(optionStyle.Width(w).Render(dimStyle.Render("  ✗ " + line)))
			default:
				b.WriteString(optionStyle.Width(w).Render(dimStyle.Render("    " + line)))
			}
			b.WriteString("\n")
		}
	} else {
		if m.userAnswer != "" {
			b.WriteString("\n")
			b.WriteString(divider("you said", w))
			b.WriteString("\n")
			var answerColor lipgloss.Color
			if m.ratedConfidence > 0 {
				answerColor = confidenceColor(m.ratedConfidence)
			} else if m.grade == gradeCorrect {
				answerColor = colorPrimary
			} else if m.grade == gradeWrong {
				answerColor = colorError
			} else {
				answerColor = colorText
			}
			b.WriteString(lipgloss.NewStyle().PaddingLeft(4).Width(w).Foreground(answerColor).Render(m.userAnswer))
			b.WriteString("\n")
		}

		if m.gradeFeedback != "" {
			b.WriteString("\n")
			if m.grade == gradeCorrect {
				b.WriteString(divider("✓ correct", w))
			} else {
				b.WriteString(divider("✗ incorrect", w))
			}
			b.WriteString("\n")
			b.WriteString(renderExplanation(m.gradeFeedback, w))
		}

		if m.answerRevealed {
			b.WriteString("\n")
			b.WriteString(divider("correct answer", w))
			b.WriteString("\n")
			b.WriteString(answerStyle.Width(w).Render("  " + m.currentQ.Answer))
			b.WriteString("\n")
		}
	}

	if m.answerRevealed && m.currentQ.Explanation != "" {
		b.WriteString("\n")
		b.WriteString(divider("explanation", w))
		b.WriteString("\n")
		b.WriteString(renderExplanation(m.currentQ.Explanation, w))
	}
	if m.explainLoading {
		b.WriteString("\n\n")
		b.WriteString("  " + m.spinner.View() + " expanding explanation...")
	}

	// Confidence picker (or retry hint)
	b.WriteString("\n\n")
	if !m.answerRevealed && m.currentQ.Type != ollama.TypeMultiChoice {
		b.WriteString(divider("r to retry with hint, or rate to reveal answer", w))
	} else {
		b.WriteString(divider("how confident are you?", w))
	}
	b.WriteString("\n")
	labels := []struct{ key, label string }{
		{"1", "weak"}, {"2", "shaky"}, {"3", "okay"}, {"4", "solid"}, {"5", "locked"},
	}
	for _, l := range labels {
		n := int(l.key[0] - '0')
		if m.ratedConfidence == n {
			color := confidenceColor(n)
			style := lipgloss.NewStyle().Foreground(color).Bold(true)
			b.WriteString(style.Render(fmt.Sprintf("  [%s] %s", l.key, l.label)))
		} else {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  [%s] %s", l.key, l.label)))
		}
	}
	b.WriteString("\n")
	if m.ratedConfidence > 0 {
		b.WriteString("\n  " + confidenceDots(m.ratedConfidence) + " " + confidenceLabel(m.ratedConfidence))
		if m.comboCount >= 3 {
			b.WriteString("  " + lipgloss.NewStyle().Foreground(colorWarn).Bold(true).Render(fmt.Sprintf("🔥 %d combo!", m.comboCount)))
		}
		if m.xpGained > 0 {
			b.WriteString("  " + streakStyle.Render(fmt.Sprintf("+%d XP", m.xpGained)))
			b.WriteString("\n  " + renderXPBreakdown(m.xpBreakdown, "conf"))
		}
	} else {
		b.WriteString("\n" + dimStyle.Render("  press 1-5 to rate"))
	}

	b.WriteString("\n")
	return b.String()
}

// renderXPBreakdown formats the XP gain line with breakdown details.
// confLabel is "conf" for quiz, "score" for challenges.
func renderXPBreakdown(bd state.XPBreakdown, confLabel string) string {
	parts := []string{fmt.Sprintf("base %d", bd.Base)}
	if bd.Confidence > 0 {
		parts = append(parts, fmt.Sprintf("%s +%d", confLabel, bd.Confidence))
	}
	if bd.Difficulty > 0 {
		parts = append(parts, fmt.Sprintf("diff +%d", bd.Difficulty))
	}
	if bd.Staleness > 0 {
		parts = append(parts, fmt.Sprintf("stale +%d", bd.Staleness))
	}
	if bd.StreakMultiplier > 1.0 {
		parts = append(parts, fmt.Sprintf("×%.1f streak", bd.StreakMultiplier))
	}
	if bd.Bonus > 0 {
		icon := "🎲"
		if bd.Bonus >= 50 {
			icon = "💎 JACKPOT"
		} else if bd.Bonus >= 20 {
			icon = "🎰"
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(colorWarn).Bold(true).Render(fmt.Sprintf("%s +%d", icon, bd.Bonus)))
	}
	return dimStyle.Render(strings.Join(parts, " · "))
}

// --- Challenge ---

func (m model) renderChallenge() string {
	switch m.challengeStep {
	case challengeLoading:
		return fmt.Sprintf("\n\n  %s generating challenge...", m.spinner.View())
	case challengeGrading:
		return fmt.Sprintf("\n\n  %s evaluating your code...", m.spinner.View())
	case challengeWorking, challengeResult:
		var b strings.Builder
		b.WriteString(m.renderChallengeTabs())
		b.WriteString("\n\n")

		switch m.challengeTab {
		case cTabChat:
			b.WriteString(m.viewport.View())
			b.WriteString("\n")
			b.WriteString(labelStyle.Render("  ask a question"))
			b.WriteString("\n\n" + m.answerTA.View())
			b.WriteString("\n")
		case cTabProblem:
			b.WriteString(m.viewport.View())
			b.WriteString("\n")
		case cTabCode:
			if m.challengeStep == challengeWorking {
				b.WriteString(m.viewport.View())
				b.WriteString("\n")
				b.WriteString(divider("", m.wrapW()))
				b.WriteString("\n")
				b.WriteString(m.answerTA.View())
				b.WriteString("\n")
			} else {
				// Result: show submitted code read-only
				b.WriteString(m.viewport.View())
				b.WriteString("\n")
			}
		case cTabExplanation:
			b.WriteString(m.viewport.View())
			b.WriteString("\n")
		}
		return b.String()
	}
	return ""
}

func (m model) renderChallengeTabs() string {
	active := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	type tab struct {
		label string
		tab   challengeTabType
	}
	tabs := []tab{
		{"chat", cTabChat},
		{"problem", cTabProblem},
		{"code", cTabCode},
	}
	if m.challengeStep == challengeResult {
		tabs = append(tabs, tab{"explanation", cTabExplanation})
	}
	var parts []string
	for _, t := range tabs {
		if m.challengeTab == t.tab {
			parts = append(parts, active.Render("["+t.label+"]"))
		} else {
			parts = append(parts, dimStyle.Render(" "+t.label+" "))
		}
	}
	return "  " + strings.Join(parts, " ")
}

func (m model) buildChallengeProblemContent() string {
	w := m.wrapW()
	var b strings.Builder
	if m.currentChallenge != nil {
		b.WriteString("\n")
		b.WriteString(questionStyle.Render(m.currentChallenge.Title))
		b.WriteString("\n\n")
		b.WriteString(renderExplanation(m.currentChallenge.Description, w))
	}
	return b.String()
}

func (m model) buildChallengeCodeReviewContent() string {
	w := m.wrapW()
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("  submitted code"))
	b.WriteString("\n\n")
	// Render as a fenced code block
	b.WriteString(renderExplanation("```\n"+m.challengeCode+"\n```", w))
	return b.String()
}

func (m model) buildChallengeResultContent() string {
	w := m.wrapW()
	var b strings.Builder

	if m.challengeGrade != nil {
		g := m.challengeGrade
		// Score + correctness + efficiency
		sStyle := correctStyle
		if g.Score < 60 {
			sStyle = wrongStyle
		} else if g.Score < 80 {
			sStyle = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
		}
		scoreLine := sStyle.Render(fmt.Sprintf("  %d/100", g.Score))
		if g.Correct {
			scoreLine += "  " + correctStyle.Render("✓ correct")
		} else {
			scoreLine += "  " + wrongStyle.Render("✗ incorrect")
		}
		if g.Efficiency != "" {
			effStyle := dimStyle
			switch g.Efficiency {
			case "optimal":
				effStyle = correctStyle
			case "suboptimal":
				effStyle = lipgloss.NewStyle().Foreground(colorYellow)
			}
			scoreLine += "  " + effStyle.Render(g.Efficiency)
		}
		b.WriteString("\n")
		b.WriteString(scoreLine)
		b.WriteString("\n")

		// Feedback
		if g.Feedback != "" {
			b.WriteString("\n")
			b.WriteString(divider("feedback", w))
			b.WriteString("\n")
			b.WriteString(renderExplanation(g.Feedback, w))
		}
	}

	// XP
	if m.xpGained > 0 {
		b.WriteString("\n")
		b.WriteString("  " + streakStyle.Render(fmt.Sprintf("+%d XP", m.xpGained)))
		b.WriteString("\n  " + renderXPBreakdown(m.xpBreakdown, "score"))
	}

	// Explain more content
	if m.challengeExplain != "" {
		w := m.wrapW()
		b.WriteString("\n")
		b.WriteString(divider("explanation", w))
		b.WriteString("\n")
		b.WriteString(renderExplanation(m.challengeExplain, w))
	} else if m.explainLoading {
		b.WriteString("\n\n  " + m.spinner.View() + " explaining...")
	}

	b.WriteString("\n")
	return b.String()
}

// --- Overlays ---

func (m model) renderOverlay() string {
	w := m.width - 12
	if w < 30 {
		w = 30
	}

	var title string
	var body string
	var footer string

	// Compact overlays — use narrower width
	if m.activeOverlay == overlayDomain {
		w = 40
		if w > m.width-8 {
			w = m.width - 8
		}
		if w < 30 {
			w = 30
		}
	}

	switch m.activeOverlay {
	case overlayKnowledge:
		name := strings.TrimSuffix(filepath.Base(m.currentFile), ".md")
		title = m.currentDomain() + "/" + name
		body = m.overlayViewport.View()
		if m.auditLoading {
			footer = m.spinner.View() + " " + dimStyle.Render("auditing... · esc close")
		} else {
			footer = dimStyle.Render("a audit · ↑↓ scroll · esc close")
		}

	case overlayChat:
		name := strings.TrimSuffix(filepath.Base(m.currentFile), ".md")
		title = "chat · " + m.currentDomain() + "/" + name
		body = m.overlayViewport.View()
		body += "\n" + labelStyle.Render("  ask a question") + "\n\n" + m.answerTA.View()
		footer = dimStyle.Render("enter send · ↑↓ scroll · ctrl+l clear · esc close")

	case overlayNotes:
		name := strings.TrimSuffix(filepath.Base(m.currentFile), ".md")
		title = "notes · " + m.currentDomain() + "/" + name
		body = m.answerTA.View()
		footer = dimStyle.Render("ctrl+s save · esc close")

	case overlayDomain:
		title = "pick domain"
		body = m.overlayViewport.View()
		footer = dimStyle.Render("tab/shift+tab cycle · enter select · esc cancel")

	}

	titleLine := domainStyle.Render("  " + title)
	footerLine := "  " + footer

	content := titleLine + "\n\n" + body + "\n\n" + footerLine

	overlayStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Width(w)

	return overlayStyle.Render(content)
}

func (m model) buildDomainOverlayContent() string {
	var lines []string
	for i, d := range m.domainList {
		avg := m.domainAvgConfidence(d)
		dots := confidenceDots(int(avg + 0.5))
		if i == m.domainCursor {
			lines = append(lines, cursorStyle.Render("> ")+keyStyle.Render(d)+"  "+dots)
		} else {
			lines = append(lines, "  "+dimStyle.Render(d)+"  "+dots)
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) buildChatContent(w int) string {
	var b strings.Builder

	if len(m.conceptChat) == 0 {
		b.WriteString(dimStyle.Render("  ask anything about this concept..."))
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render("  quick commands:"))
		cmds := []string{"/eli5", "/examples", "/gotchas", "/compare", "/why", "/deep"}
		for _, cmd := range cmds {
			b.WriteString("\n")
			b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).PaddingLeft(4).Render(cmd))
		}
		return b.String()
	}

	userStyle := lipgloss.NewStyle().Width(w).PaddingLeft(2).Foreground(colorAccent)
	timingStyle := lipgloss.NewStyle().Foreground(colorDim)
	for _, msg := range m.conceptChat {
		b.WriteString("\n")
		if msg.role == "user" {
			b.WriteString(userStyle.Render("👤 " + msg.content))
		} else {
			timing := ""
			if msg.durationMs > 0 {
				timing = timingStyle.Render(fmt.Sprintf(" (%dms)", msg.durationMs))
			}
			b.WriteString("  🤖" + timing + "\n")
			b.WriteString(renderExplanation(msg.content, w))
		}
		b.WriteString("\n")
	}

	if m.conceptChatLoading {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(2).Foreground(colorDim).Render("🤖 " + m.spinner.View() + " thinking..."))
		b.WriteString("\n")
	}

	if m.bankLoading {
		b.WriteString("\n")
		b.WriteString(divider(m.spinner.View()+" summarizing chat...", w))
		b.WriteString("\n")
	}

	if m.bankPending != "" {
		b.WriteString("\n")
		b.WriteString(divider("bank preview", w))
		b.WriteString("\n")
		b.WriteString(renderExplanation(m.bankPending, w))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  enter save · r regenerate · esc discard"))
		b.WriteString("\n")
	}

	return b.String()
}

func (m model) buildChatOverlayContent() string {
	return m.buildChatContent(m.overlayViewport.Width - 4)
}

func (m model) buildChatTabContent() string {
	return m.buildChatContent(m.wrapW())
}

// --- Learn ---

func (m model) renderLearn() string {
	switch m.learnStep {
	case learnInput:
		return m.renderLearnInput()
	case learnChat:
		return m.renderLearnChat()
	case learnGenerating:
		return "\n  " + m.spinner.View() + " generating knowledge...\n"
	case learnReview:
		return m.renderLearnReview()
	}
	return ""
}

func (m model) renderLearnChat() string {
	var b strings.Builder
	w := m.wrapW()

	if m.learnUpdateFile != "" {
		b.WriteString(questionStyle.Render(fmt.Sprintf("updating: %s", m.learnUpdateFile)))
		if len(m.learnChatHistory) == 0 {
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("  found existing file — tell me what to add"))
		}
	} else {
		b.WriteString(questionStyle.Render(fmt.Sprintf("learning: %s", m.learnTopic)))
		if len(m.learnChatHistory) == 0 {
			b.WriteString("\n")
			b.WriteString(dimStyle.Render("  new topic — no existing file found"))
		}
	}
	b.WriteString("\n\n")

	// Chat history
	for _, msg := range m.learnChatHistory {
		if msg.role == "user" {
			b.WriteString(optionStyle.Width(w).Render("👤 " + msg.content))
		} else {
			b.WriteString("  🤖\n")
			b.WriteString(renderExplanation(msg.content, w))
		}
		b.WriteString("\n")
	}

	if m.learnChatLoading {
		b.WriteString("  " + m.spinner.View() + " thinking...\n")
	} else {
		b.WriteString("  " + m.learnTA.View())
	}

	return b.String()
}

func (m model) renderLearnInput() string {
	var b strings.Builder

	b.WriteString(questionStyle.Render("what do you want to learn about?"))
	b.WriteString("\n\n")
	b.WriteString("  " + m.learnTA.View())
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  tip: just type a concept, or domain/topic for explicit placement"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  e.g. mutex, docker/multi-stage-builds"))

	return b.String()
}

func (m model) renderLearnReview() string {
	var b strings.Builder

	if m.learnUpdateFile != "" {
		b.WriteString(domainStyle.Render(fmt.Sprintf("  updating: %s/%s", m.learnDomain, m.learnSlug)))
	} else {
		b.WriteString(domainStyle.Render(fmt.Sprintf("  %s/%s", m.learnDomain, m.learnSlug)))
	}
	b.WriteString("\n\n")
	b.WriteString(m.viewport.View())

	return b.String()
}

// --- Stats ---

func (m model) buildStatsContent() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(titleStyle.Render("  Knowledge Stats"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d files in bank · %d total XP · Lv.%d",
		len(m.allFiles), m.state.TotalXP, m.state.Level())))
	b.WriteString("\n")

	if m.state.DayStreak > 0 {
		b.WriteString(streakStyle.Render(fmt.Sprintf("  %d day streak", m.state.DayStreak)))
		b.WriteString("\n")
	}

	today := m.state.TodaySessions()
	if len(today) > 0 {
		totalQ, totalC, totalSec := 0, 0, 0
		for _, s := range today {
			totalQ += s.Total
			totalC += s.Correct
			totalSec += s.DurationSec
		}
		acc := 0
		if totalQ > 0 {
			acc = totalC * 100 / totalQ
		}
		timeStr := formatDuration(totalSec)
		b.WriteString(dimStyle.Render(fmt.Sprintf("  today: %d sessions, %d questions, %d%% accuracy, %s",
			len(today), totalQ, acc, timeStr)))
		b.WriteString("\n")
	}

	// All-time session time
	totalAllSec := 0
	for _, sr := range m.state.Sessions {
		totalAllSec += sr.DurationSec
	}
	if totalAllSec > 0 {
		avgSec := totalAllSec / len(m.state.Sessions)
		b.WriteString(dimStyle.Render(fmt.Sprintf("  total time: %s · avg session: %s",
			formatDuration(totalAllSec), formatDuration(avgSec))))
		b.WriteString("\n")
	}

	week := m.state.WeekActivity()
	hasActivity := false
	for _, c := range week {
		if c > 0 {
			hasActivity = true
			break
		}
	}
	if hasActivity {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  last 7 days: "))
		maxQ := 1
		for _, c := range week {
			if c > maxQ {
				maxQ = c
			}
		}
		for _, c := range week {
			if c == 0 {
				b.WriteString(barEmptyStyle.Render("░"))
			} else {
				level := c * 4 / maxQ
				blocks := []string{"▁", "▃", "▅", "█"}
				if level >= len(blocks) {
					level = len(blocks) - 1
				}
				b.WriteString(barFilledStyle.Render(blocks[level]))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")

	stats := m.state.DomainStats(m.allFiles, knowledge.Domain)
	if len(stats) == 0 {
		b.WriteString(dimStyle.Render("  No quiz history yet. Start reviewing!"))
		return b.String()
	}

	b.WriteString(dimStyle.Render("  domain          confidence   files"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 40)))
	b.WriteString("\n")

	for _, ds := range stats {
		name := ds.Domain
		if len(name) > 14 {
			name = name[:14]
		}
		name = name + strings.Repeat(" ", 14-len(name))

		dots := confidenceDots(int(ds.AvgConfidence + 0.5))
		avg := fmt.Sprintf("%.1f", ds.AvgConfidence)

		b.WriteString(fmt.Sprintf("  %s  %s %s  %s\n",
			domainStyle.Render(name),
			dots,
			dimStyle.Render(avg),
			dimStyle.Render(fmt.Sprintf("%d", ds.Total)),
		))
	}

	// Achievements — show all, grey out unearned, count progress
	if m.state != nil {
		earned := 0
		for _, id := range state.AllAchievements {
			if m.state.HasAchievement(id) {
				earned++
			}
		}
		b.WriteString("\n")
		b.WriteString(divider(fmt.Sprintf("achievements %d/%d", earned, len(state.AllAchievements)), 40))
		b.WriteString("\n")
		for _, id := range state.AllAchievements {
			info := state.AchievementInfo[id]
			if m.state.HasAchievement(id) {
				b.WriteString(streakStyle.Render("  ★ "+info.Name) + dimStyle.Render(" — "+info.Desc) + "\n")
			} else {
				b.WriteString(dimStyle.Render("  ☆ "+info.Name+" — "+info.Desc) + "\n")
			}
		}
	}

	return b.String()
}

// --- Settings ---

func (m model) renderSettings() string {
	var b strings.Builder

	b.WriteString(questionStyle.Render("settings"))
	b.WriteString("\n\n")

	// Quiz types section
	b.WriteString(divider("quiz types", m.wrapW()))
	b.WriteString("\n\n")

	for i, t := range ollama.AllTypes {
		if i >= len(m.activeTypes) {
			break
		}
		name := t.String()
		marker := dimStyle.Render("○")
		label := dimStyle.Render(name)
		if m.activeTypes[i] {
			marker = lipgloss.NewStyle().Foreground(colorAccent).Render("●")
			label = name
		}
		if i == m.settingsCursor {
			b.WriteString(fmt.Sprintf("  %s %s %s\n", cursorStyle.Render(">"), marker, label))
		} else {
			b.WriteString(fmt.Sprintf("    %s %s\n", marker, label))
		}
	}

	// Session length setting
	b.WriteString("\n")
	b.WriteString(divider("session", m.wrapW()))
	b.WriteString("\n\n")

	idxSession := len(ollama.AllTypes)
	qLabel := fmt.Sprintf("%d questions", m.maxQuestions)
	if m.settingsCursor == idxSession {
		b.WriteString(fmt.Sprintf("  %s %s  %s\n",
			cursorStyle.Render(">"),
			qLabel,
			dimStyle.Render("← / → to adjust")))
	} else {
		b.WriteString(fmt.Sprintf("    %s\n", qLabel))
	}

	// Challenge difficulty setting
	b.WriteString("\n")
	b.WriteString(divider("challenge", m.wrapW()))
	b.WriteString("\n\n")

	idxChallDiff := len(ollama.AllTypes) + 1
	diffNames := []string{"adaptive", "basic", "intermediate", "advanced"}
	diffVal := 0
	if m.state != nil {
		diffVal = m.state.ChallengeDiff
	}
	if diffVal < 0 || diffVal >= len(diffNames) {
		diffVal = 0
	}
	diffLabel := fmt.Sprintf("difficulty: %s", diffNames[diffVal])
	if m.settingsCursor == idxChallDiff {
		b.WriteString(fmt.Sprintf("  %s %s  %s\n",
			cursorStyle.Render(">"),
			diffLabel,
			dimStyle.Render("enter / ← → to cycle")))
	} else {
		b.WriteString(fmt.Sprintf("    %s\n", diffLabel))
	}

	// Brain path setting
	b.WriteString("\n")
	b.WriteString(divider("knowledge path", m.wrapW()))
	b.WriteString("\n\n")

	idxBrainPath := len(ollama.AllTypes) + 2
	if m.settingsEditing && m.settingsCursor == idxBrainPath {
		b.WriteString(fmt.Sprintf("  %s %s\n", cursorStyle.Render(">"), m.learnTA.View()))
	} else if m.settingsCursor == idxBrainPath {
		b.WriteString(fmt.Sprintf("  %s %s  %s\n",
			cursorStyle.Render(">"),
			m.brainPath,
			dimStyle.Render("enter to edit")))
	} else {
		b.WriteString(fmt.Sprintf("    %s\n", dimStyle.Render(m.brainPath)))
	}

	// Log ollama calls setting
	b.WriteString("\n")
	b.WriteString(divider("debug", m.wrapW()))
	b.WriteString("\n\n")

	idxLogCalls := len(ollama.AllTypes) + 3
	logOn := m.state != nil && m.state.LogCalls
	logMarker := dimStyle.Render("○")
	logLabel := dimStyle.Render("log ollama calls to file")
	if logOn {
		logMarker = lipgloss.NewStyle().Foreground(colorAccent).Render("●")
		logLabel = "log ollama calls to file"
	}
	if m.settingsCursor == idxLogCalls {
		b.WriteString(fmt.Sprintf("  %s %s %s\n", cursorStyle.Render(">"), logMarker, logLabel))
		if logOn {
			home, _ := os.UserHomeDir()
			logPath := filepath.Join(home, ".local", "share", "unrot", "logs")
			b.WriteString(fmt.Sprintf("    %s\n", dimStyle.Render(logPath)))
		}
	} else {
		b.WriteString(fmt.Sprintf("    %s %s\n", logMarker, logLabel))
	}

	return b.String()
}

// --- Done ---

func (m model) buildDoneContent() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("  Session Complete"))
	b.WriteString("\n\n")

	if m.sessionTotal == 0 {
		b.WriteString(dimStyle.Render("  Nothing due for review right now. Come back later!"))
		return b.String()
	}

	pct := m.sessionCorrect * 100 / m.sessionTotal

	b.WriteString(fmt.Sprintf("  %s  %s\n",
		dimStyle.Render("correct"),
		correctStyle.Render(fmt.Sprintf("%d", m.sessionCorrect)),
	))
	b.WriteString(fmt.Sprintf("  %s    %s\n",
		dimStyle.Render("wrong"),
		wrongStyle.Render(fmt.Sprintf("%d", m.sessionWrong)),
	))
	b.WriteString(fmt.Sprintf("  %s    %s\n",
		dimStyle.Render("score"),
		actionStyle.Render(fmt.Sprintf("%d%%", pct)),
	))

	if m.comboMax >= 2 {
		comboStyle := lipgloss.NewStyle().Foreground(colorWarn).Bold(true)
		b.WriteString(fmt.Sprintf("  %s    %s\n",
			dimStyle.Render("combo"),
			comboStyle.Render(fmt.Sprintf("🔥 %d best streak", m.comboMax)),
		))
	}

	if m.state != nil {
		lvl := m.state.Level()
		cur, needed := m.state.LevelProgress()
		b.WriteString(fmt.Sprintf("  %s    %s\n",
			dimStyle.Render("level"),
			streakStyle.Render(fmt.Sprintf("Lv.%d  %d/%d XP", lvl, cur, needed)),
		))
	}

	if m.reportPath != "" {
		b.WriteString("\n")
		b.WriteString(correctStyle.Render(fmt.Sprintf("  saved → %s", m.reportPath)))
		b.WriteString("\n")
	}

	if len(m.sessionWrongs) > 0 {
		b.WriteString("\n")
		ww := m.wrapW()
		b.WriteString(divider("review these", ww))
		b.WriteString("\n\n")

		for _, w := range m.sessionWrongs {
			domain := knowledge.Domain(w.file)
			b.WriteString(fmt.Sprintf("  %s %s\n",
				domainStyle.Render(domain),
				dimStyle.Render("· "+w.qtype),
			))
			b.WriteString("    ")
			b.WriteString(lipgloss.NewStyle().Width(ww - 4).Render(w.question))
			b.WriteString("\n")
			b.WriteString("    ")
			b.WriteString(answerStyle.Width(ww - 4).Render(w.answer))
			b.WriteString("\n\n")
		}
	}

	return b.String()
}

// --- Status bar ---

func (m model) renderStatus() string {
	var keys []struct{ key, action string }

	switch m.phase {
	case phaseDashboard:
		keys = []struct{ key, action string }{
			{"enter/r", "review"}, {"F", "focused"}, {"i", "challenge"}, {"v", "viewer"}, {"tab", "domain"}, {"b", "topics"}, {"l", "learn"}, {"s", "settings"}, {"a", "stats"}, {"q", "quit"},
		}
	case phaseTopicList:
		enterAction := "drill"
		if m.viewerMode {
			enterAction = "read"
		}
		if m.pickSearching {
			keys = []struct{ key, action string }{
				{"type", "filter"}, {"↑↓", "navigate"}, {"enter", enterAction}, {"esc", "clear"},
			}
		} else {
			keys = []struct{ key, action string }{
				{"/", "search"}, {"j/k", "navigate"}, {"enter", enterAction}, {"f", "fav"}, {"x", "reset"}, {"+", "add"}, {"tab", "domain"}, {"esc", "back"},
			}
		}
	case phaseQuiz:
		keys = m.quizStatusKeys()
	case phaseLearn:
		keys = m.learnStatusKeys()
	case phaseChallenge:
		switch m.challengeStep {
		case challengeLoading, challengeGrading:
			keys = []struct{ key, action string }{
				{"esc", "back"},
			}
		case challengeWorking:
			if m.challengeTab == cTabChat {
				keys = []struct{ key, action string }{
					{"enter", "send"}, {"ctrl+b", "bank"}, {"ctrl+y", "copy"}, {"ctrl+l", "clear"}, {"tab", "next tab"}, {"esc", "code"},
				}
			} else {
				keys = []struct{ key, action string }{
					{"tab", "switch tab"}, {"ctrl+s", "submit"}, {"ctrl+y", "copy"}, {"esc", "back"},
				}
			}
		case challengeResult:
			if m.challengeTab == cTabChat {
				keys = []struct{ key, action string }{
					{"enter", "send"}, {"ctrl+b", "bank"}, {"ctrl+y", "copy"}, {"ctrl+l", "clear"}, {"tab", "next tab"}, {"esc", "code"},
				}
			} else {
				keys = []struct{ key, action string }{
					{"tab", "switch tab"}, {"e", "explain"}, {"enter", "next"}, {"ctrl+y", "copy"}, {"esc", "back"},
				}
			}
		}
	case phaseSettings:
		keys = []struct{ key, action string }{
			{"↑↓", "move"}, {"enter", "toggle"}, {"esc", "back"},
		}
	case phaseViewer:
		if m.auditFixPending != "" {
			keys = []struct{ key, action string }{
				{"enter", "save fix"}, {"esc", "discard"}, {"↑↓", "scroll"},
			}
		} else if m.auditFixLoading {
			keys = []struct{ key, action string }{
				{"", "generating fix..."}, {"↑↓", "scroll"},
			}
		} else if m.auditLoading {
			keys = []struct{ key, action string }{
				{"", "auditing..."}, {"↑↓", "scroll"}, {"esc", "back"},
			}
		} else if m.auditResult != "" {
			keys = []struct{ key, action string }{
				{"enter", "fix"}, {"esc", "dismiss"}, {"↑↓", "scroll"},
			}
		} else {
			keys = []struct{ key, action string }{
				{"a", "audit"}, {"c", "chat"}, {"n", "notes"}, {"↑↓", "scroll"}, {"esc", "back"},
			}
		}
	case phaseStats:
		keys = []struct{ key, action string }{
			{"↑↓", "scroll"}, {"esc", "back"},
		}
	case phaseDone:
		doneKeys := []struct{ key, action string }{
			{"w", "export"}, {"↑↓", "scroll"}, {"esc", "back"},
		}
		if len(m.sessionWrongs) > 0 {
			doneKeys = append([]struct{ key, action string }{{"r", "retry wrongs"}}, doneKeys...)
		}
		keys = doneKeys
	case phaseError:
		keys = []struct{ key, action string }{
			{"esc", "back"},
		}
	}

	// Override with overlay keys if active
	if m.activeOverlay != overlayNone {
		switch m.activeOverlay {
		case overlayNotes:
			keys = []struct{ key, action string }{
				{"ctrl+s", "save"}, {"esc", "close"},
			}
		case overlayKnowledge:
			if m.auditLoading {
				keys = []struct{ key, action string }{
					{"", "auditing..."}, {"↑↓", "scroll"}, {"esc", "close"},
				}
			} else {
				keys = []struct{ key, action string }{
					{"a", "audit"}, {"↑↓", "scroll"}, {"esc", "close"},
				}
			}
		case overlayChat:
			if m.bankPending != "" {
				keys = []struct{ key, action string }{
					{"enter", "save"}, {"r", "regenerate"}, {"esc", "discard"},
				}
			} else if m.bankLoading {
				keys = []struct{ key, action string }{
					{"", "summarizing..."}, {"↑↓", "scroll"}, {"esc", "close"},
				}
			} else {
				keys = []struct{ key, action string }{
					{"enter", "send"}, {"ctrl+b", "bank notes"}, {"ctrl+y", "copy"}, {"ctrl+l", "clear"}, {"↑↓", "scroll"}, {"esc", "close"},
				}
			}
		case overlayDomain:
			keys = []struct{ key, action string }{
				{"tab", "next"}, {"shift+tab", "prev"}, {"enter", "select"}, {"esc", "cancel"},
			}
		}
	}

	var parts []string
	for i, k := range keys {
		if i > 0 {
			parts = append(parts, statusBulletStyle.Render(" · "))
		}
		if k.key != "" {
			parts = append(parts, statusKeyStyle.Render(k.key+" "), statusActionStyle.Render(k.action))
		} else {
			parts = append(parts, statusBulletStyle.Render(k.action))
		}
	}

	return statusBarStyle.Width(m.width).Render(strings.Join(parts, ""))
}

func (m model) quizStatusKeys() []struct{ key, action string } {
	switch m.quizStep {
	case stepLesson:
		return []struct{ key, action string }{
			{"enter", "start quiz"}, {"c", "chat"}, {"n", "notes"}, {"↑↓", "scroll"}, {"esc", "skip"}, {"q", "back"},
		}
	case stepLoading:
		return []struct{ key, action string }{
			{"esc", "back"},
		}
	case stepQuestion:
		switch m.questionTab {
		case qTabChat:
			if m.bankPending != "" {
				return []struct{ key, action string }{
					{"enter", "save"}, {"r", "regenerate"}, {"esc", "discard"},
				}
			}
			if m.bankLoading {
				return []struct{ key, action string }{
					{"", "summarizing..."}, {"tab", "next tab"}, {"esc", "quiz"},
				}
			}
			return []struct{ key, action string }{
				{"enter", "send"}, {"ctrl+b", "bank"}, {"ctrl+y", "copy"}, {"ctrl+l", "clear"}, {"tab", "next tab"}, {"esc", "quiz"},
			}
		case qTabKnowledge:
			if m.auditLoading {
				return []struct{ key, action string }{
					{"", "auditing..."}, {"tab", "next tab"}, {"↑↓", "scroll"}, {"esc", "quiz"},
				}
			}
			return []struct{ key, action string }{
				{"a", "audit"}, {"tab", "next tab"}, {"n", "notes"}, {"h", "hint"}, {"↑↓", "scroll"}, {"esc", "quiz"},
			}
		default:
			if m.currentQ != nil && m.currentQ.Type == ollama.TypeMultiChoice {
				return []struct{ key, action string }{
					{"a-d", "answer"}, {"tab", "next tab"}, {"h", "hint"}, {"esc", "skip"},
				}
			}
			back := "skip"
			if m.pickMode {
				back = "back"
			}
			if m.currentQ != nil && (m.currentQ.Type == ollama.TypeFinishCode || m.currentQ.Type == ollama.TypeDebug || m.currentQ.Type == ollama.TypeCodeOutput) {
				return []struct{ key, action string }{
					{"ctrl+s", "submit"}, {"enter", "newline"}, {"tab", "next tab"}, {"ctrl+e", "hint"}, {"esc", back},
				}
			}
			return []struct{ key, action string }{
				{"enter", "submit"}, {"tab", "next tab"}, {"ctrl+e", "hint"}, {"esc", back},
			}
		}
	case stepGrading:
		return []struct{ key, action string }{
			{"", "thinking..."},
		}
	case stepResult:
		if !m.answerRevealed {
			return []struct{ key, action string }{
				{"r", "retry+hint"}, {"1-5", "reveal + rate"}, {"c", "chat"}, {"k", "knowledge"}, {"esc", "back"},
			}
		}
		return []struct{ key, action string }{
			{"1-5", "rate"}, {"enter", "next"}, {"r", "re-quiz"}, {"e", "explain"}, {"n", "notes"}, {"c", "chat"}, {"k", "knowledge"}, {"esc", "back"},
		}
	}
	return nil
}

func (m model) learnStatusKeys() []struct{ key, action string } {
	switch m.learnStep {
	case learnInput:
		return []struct{ key, action string }{
			{"enter", "submit"}, {"esc", "back"},
		}
	case learnChat:
		if m.learnChatLoading {
			return []struct{ key, action string }{
				{"", "thinking..."}, {"esc", "back"},
			}
		}
		return []struct{ key, action string }{
			{"enter", "send"}, {"ctrl+g", "generate"}, {"esc", "back"},
		}
	case learnGenerating:
		return []struct{ key, action string }{
			{"", "generating..."},
		}
	case learnReview:
		saveLabel := "save & quiz"
		if m.learnUpdateFile != "" {
			saveLabel = "update & quiz"
		}
		return []struct{ key, action string }{
			{"s", saveLabel}, {"r", "regenerate"}, {"↑↓", "scroll"}, {"esc", "discard"},
		}
	}
	return nil
}
