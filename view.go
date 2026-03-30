package main

import (
	"fmt"
	"path/filepath"
	"strings"

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
	case overlayQuizType:
		return m.buildQuizTypeOverlayContent()
	}
	return ""
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	header := m.renderHeader()
	rule := dimStyle.Render(strings.Repeat("─", m.width))
	content := m.renderContent()
	status := m.renderStatus()

	// Toast notification
	var toast string
	if m.toast != "" {
		toast = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true).
			Render("  ★ " + m.toast)
	}

	var lines []string
	lines = append(lines, header, rule)
	if toast != "" {
		lines = append(lines, toast)
	}
	lines = append(lines, content, "", status)
	base := lipgloss.JoinVertical(lipgloss.Left, lines...)

	// Render overlay on top if active
	if m.activeOverlay != overlayNone {
		overlay := m.renderOverlay()
		base = placeOverlay(base, overlay)
	}

	return base
}

func (m model) renderHeader() string {
	title := titleStyle.Render("unrot")

	var parts []string
	parts = append(parts, title)

	// Level badge
	if m.state != nil {
		lvl := m.state.Level()
		cur, _ := m.state.LevelProgress()
		parts = append(parts, streakStyle.Render(fmt.Sprintf("Lv.%d", lvl)))
		// Mini XP bar (5 chars)
		filled := cur * 5 / 100
		if filled > 5 {
			filled = 5
		}
		parts = append(parts, barFilledStyle.Render(strings.Repeat("█", filled))+barEmptyStyle.Render(strings.Repeat("░", 5-filled)))
	}

	domain := m.currentDomain()
	if domain != "" {
		parts = append(parts, domainStyle.Render(domain))
	}
	if m.currentQ != nil && m.phase == phaseQuiz {
		parts = append(parts, typeStyle.Render(m.currentQ.Type.String()))
		if m.currentQ.Difficulty > 0 {
			parts = append(parts, diffStyle(m.currentQ.Difficulty))
		}
	}
	if m.retryPhase {
		parts = append(parts, retryStyle.Render("retry"))
	}

	left := strings.Join(parts, dimStyle.Render(" · "))

	var rightParts []string
	if m.sessionTotal > 0 || len(m.reviewFiles) > 0 {
		rightParts = append(rightParts, m.renderProgress())
	}
	if hint := m.headerScrollHint(); hint != "" {
		rightParts = append(rightParts, hint)
	}
	right := strings.Join(rightParts, "  ")

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	return left + strings.Repeat(" ", gap) + right
}

// headerScrollHint returns scroll indicator for the active viewport (if any).
func (m model) headerScrollHint() string {
	if m.activeOverlay != overlayNone {
		return scrollHint(m.overlayViewport)
	}
	switch m.phase {
	case phaseQuiz:
		if m.quizStep == stepLesson || m.quizStep == stepResult || (m.quizStep == stepQuestion && m.showNotes) {
			return scrollHint(m.viewport)
		}
	case phaseStats, phaseDone:
		return scrollHint(m.viewport)
	case phaseLearn:
		if m.learnStep == learnReview {
			return scrollHint(m.viewport)
		}
	}
	return ""
}

func diffStyle(d ollama.Difficulty) string {
	switch d {
	case ollama.DiffAdvanced:
		return wrongStyle.Render("hard")
	case ollama.DiffIntermediate:
		return actionStyle.Render("med")
	default:
		return correctStyle.Render("easy")
	}
}

func (m model) renderProgress() string {
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
	return statsStyle.Render(progress)
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

	// Level + XP bar
	if m.state != nil {
		lvl := m.state.Level()
		cur, needed := m.state.LevelProgress()
		barW := 30
		filled := cur * barW / needed
		if filled > barW {
			filled = barW
		}
		b.WriteString(streakStyle.Render(fmt.Sprintf("  Lv.%d", lvl)))
		b.WriteString("  " + barFilledStyle.Render(strings.Repeat("█", filled)) + barEmptyStyle.Render(strings.Repeat("░", barW-filled)))
		b.WriteString(dimStyle.Render(fmt.Sprintf("  %d/%d XP", cur, needed)))
		b.WriteString("\n")

		// Streak + today count
		var infoParts []string
		if m.state.DayStreak > 0 {
			infoParts = append(infoParts, fmt.Sprintf("%d day streak", m.state.DayStreak))
		}
		today := m.state.TodaySessions()
		if len(today) > 0 {
			totalQ := 0
			for _, s := range today {
				totalQ += s.Total
			}
			infoParts = append(infoParts, fmt.Sprintf("%d questions today", totalQ))
		}
		if len(infoParts) > 0 {
			b.WriteString(dimStyle.Render("  " + strings.Join(infoParts, " · ")))
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
		b.WriteString("    " + dimStyle.Render("[enter] start"))
	} else {
		b.WriteString(dimStyle.Render("  no knowledge files"))
	}
	b.WriteString("\n\n")

	// Active domain filter
	domainLabel := "all"
	if m.domainFilter != "" {
		domainLabel = m.domainFilter
	}
	// Active quiz types summary
	var activeTypeNames []string
	typeNames := []string{"flash", "explain", "fill", "MC"}
	for i, name := range typeNames {
		if i < len(m.activeTypes) && m.activeTypes[i] {
			activeTypeNames = append(activeTypeNames, name)
		}
	}
	typeSummary := strings.Join(activeTypeNames, ", ")

	b.WriteString(fmt.Sprintf("  domain: %s  %s    types: %s  %s\n\n",
		keyStyle.Render(domainLabel),
		dimStyle.Render("[tab]"),
		keyStyle.Render(typeSummary),
		dimStyle.Render("[t]"),
	))

	// Quick actions
	actions := []struct{ key, name, desc string }{
		{"b", "browse topics", "drill a specific concept"},
		{"l", "learn new", "learn something new"},
		{"s", "full stats", "see your progress"},
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
		if m.state != nil {
			conf = m.state.GetConfidence(file)
		}
		dots := confidenceDots(conf)

		nameWidth := m.width - 20
		if nameWidth < 20 {
			nameWidth = 20
		}
		if nameWidth > 40 {
			nameWidth = 40
		}
		nameStyle := lipgloss.NewStyle().Width(nameWidth)
		if i == m.pickCursor {
			b.WriteString(fmt.Sprintf("  %s %s %s\n",
				cursorStyle.Render(">"),
				nameStyle.Foreground(colorText).Render(name),
				dots,
			))
		} else {
			b.WriteString(fmt.Sprintf("    %s %s\n",
				nameStyle.Render(name),
				dots,
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

	name := strings.TrimSuffix(filepath.Base(m.currentFile), ".md")
	domain := m.currentDomain()

	b.WriteString(labelStyle.Render("  study first"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  %s %s\n",
		domainStyle.Render(domain),
		lipgloss.NewStyle().Bold(true).Foreground(colorText).Render(name),
	))
	b.WriteString("\n")
	b.WriteString(dimStyle.Width(w).PaddingLeft(2).Render(strings.Repeat("─", w-4)))
	b.WriteString("\n")
	b.WriteString(renderMarkdown(m.sourceContent, w))
	b.WriteString("\n")

	return b.String()
}

func (m model) renderQuestion() string {
	var b strings.Builder
	w := m.wrapW()

	// Tab toggle indicator
	if m.showNotes {
		notesTab := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("[notes]")
		questionTab := dimStyle.Render(" question ")
		b.WriteString("  " + notesTab + " " + questionTab)
		b.WriteString("\n\n")
		b.WriteString(m.viewport.View())
		b.WriteString("\n")
		return b.String()
	}

	notesTab := dimStyle.Render(" notes ")
	questionTab := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("[question]")
	b.WriteString("  " + notesTab + " " + questionTab)
	b.WriteString("\n\n")

	b.WriteString(questionStyle.Width(w).Render(m.currentQ.Text))
	b.WriteString("\n")

	if m.currentQ.Type == ollama.TypeMultiChoice {
		b.WriteString("\n")
		for i, opt := range m.currentQ.Options {
			letter := string(rune('a' + i))
			b.WriteString(optionStyle.Width(w).Render(
				keyStyle.Render(letter) + dimStyle.Render(") ") + opt,
			))
			b.WriteString("\n")
		}
	} else {
		b.WriteString("\n")
		b.WriteString(labelStyle.Render("  your answer"))
		b.WriteString("\n\n")
		b.WriteString("    " + m.answerTA.View())
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
			if m.ratedConfidence > 0 {
				color := confidenceColor(m.ratedConfidence)
				b.WriteString(lipgloss.NewStyle().PaddingLeft(4).Width(w).Foreground(color).Render(m.userAnswer))
			} else {
				b.WriteString(lipgloss.NewStyle().PaddingLeft(4).Width(w).Foreground(colorText).Render(m.userAnswer))
			}
			b.WriteString("\n")
		}

		b.WriteString("\n")
		b.WriteString(divider("correct answer", w))
		b.WriteString("\n")
		b.WriteString(answerStyle.Width(w).Render("  " + m.currentQ.Answer))
		b.WriteString("\n")
	}

	if m.currentQ.Explanation != "" {
		b.WriteString("\n")
		b.WriteString(divider("explanation", w))
		b.WriteString("\n")
		b.WriteString(explainStyle.Width(w).Render("  " + m.currentQ.Explanation))
	}
	if m.explainLoading {
		b.WriteString("\n\n")
		b.WriteString("  " + m.spinner.View() + " expanding explanation...")
	}

	// Confidence picker
	b.WriteString("\n\n")
	b.WriteString(divider("how confident are you?", w))
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
		if m.xpGained > 0 {
			b.WriteString("  " + streakStyle.Render(fmt.Sprintf("+%d XP", m.xpGained)))
		}
	} else {
		b.WriteString("\n" + dimStyle.Render("  press 1-5 to rate"))
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
	if m.activeOverlay == overlayDomain || m.activeOverlay == overlayQuizType {
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
		footer = dimStyle.Render("↑↓ scroll · esc close")

	case overlayChat:
		name := strings.TrimSuffix(filepath.Base(m.currentFile), ".md")
		title = "chat · " + m.currentDomain() + "/" + name
		body = m.overlayViewport.View()
		body += "\n" + labelStyle.Render("  ask a question") + "\n\n  " + m.answerTA.View()
		footer = dimStyle.Render("enter send · ↑↓ scroll · esc close")

	case overlayNotes:
		name := strings.TrimSuffix(filepath.Base(m.currentFile), ".md")
		title = "notes · " + m.currentDomain() + "/" + name
		body = "  " + m.answerTA.View()
		footer = dimStyle.Render("ctrl+s save · esc close")

	case overlayDomain:
		title = "pick domain"
		body = m.overlayViewport.View()
		footer = dimStyle.Render("tab/shift+tab cycle · enter select · esc cancel")

	case overlayQuizType:
		title = "quiz types"
		body = m.overlayViewport.View()
		footer = dimStyle.Render("↑↓ move · enter toggle · esc close")
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

func (m model) buildQuizTypeOverlayContent() string {
	typeNames := []string{"flashcard", "explain", "fill-blank", "multiple-choice"}
	var lines []string
	for i, name := range typeNames {
		if i >= len(m.activeTypes) {
			break
		}
		marker := dimStyle.Render("○")
		label := dimStyle.Render(name)
		if m.activeTypes[i] {
			marker = lipgloss.NewStyle().Foreground(colorAccent).Render("●")
			label = name
		}
		if i == m.typeCursor {
			lines = append(lines, cursorStyle.Render("> ")+marker+" "+label)
		} else {
			lines = append(lines, "  "+marker+" "+label)
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) buildChatOverlayContent() string {
	var b strings.Builder
	w := m.overlayViewport.Width - 4

	if len(m.conceptChat) == 0 {
		b.WriteString(dimStyle.Render("  ask anything about this concept..."))
		return b.String()
	}

	msgStyle := lipgloss.NewStyle().Width(w).PaddingLeft(2)
	for _, msg := range m.conceptChat {
		b.WriteString("\n")
		if msg.role == "user" {
			b.WriteString(msgStyle.Foreground(colorAccent).Render("👤 " + msg.content))
		} else {
			b.WriteString(msgStyle.Foreground(colorText).Render("🤖 " + msg.content))
		}
		b.WriteString("\n")
	}

	if m.conceptChatLoading {
		b.WriteString("\n")
		b.WriteString(msgStyle.Foreground(colorDim).Render("🤖 " + m.spinner.View() + " thinking..."))
		b.WriteString("\n")
	}

	return b.String()
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
			b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(2).Foreground(colorText).Render("🤖 " + msg.content))
		}
		b.WriteString("\n\n")
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

	b.WriteString(titleStyle.Render("  Knowledge Stats"))
	b.WriteString("\n\n")

	if m.state.DayStreak > 0 {
		b.WriteString(streakStyle.Render(fmt.Sprintf("  %d day streak", m.state.DayStreak)))
		b.WriteString("\n")
	}

	today := m.state.TodaySessions()
	if len(today) > 0 {
		totalQ, totalC := 0, 0
		for _, s := range today {
			totalQ += s.Total
			totalC += s.Correct
		}
		acc := 0
		if totalQ > 0 {
			acc = totalC * 100 / totalQ
		}
		b.WriteString(dimStyle.Render(fmt.Sprintf("  today: %d sessions, %d questions, %d%% accuracy",
			len(today), totalQ, acc)))
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

	// Achievements
	if m.state != nil && len(m.state.Achievements) > 0 {
		b.WriteString("\n\n")
		b.WriteString(divider("achievements", 40))
		b.WriteString("\n")
		for _, id := range m.state.Achievements {
			if info, ok := state.AchievementInfo[id]; ok {
				b.WriteString(streakStyle.Render("  ★ "+info.Name) + dimStyle.Render(" — "+info.Desc) + "\n")
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d files in bank · %d total XP · Lv.%d",
		len(m.allFiles), m.state.TotalXP, m.state.Level())))

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
			{"enter", "start review"}, {"1-4", "toggle types"}, {"tab", "domain"}, {"b", "topics"}, {"l", "learn"}, {"s", "stats"}, {"q", "quit"},
		}
	case phaseTopicList:
		if m.pickSearching {
			keys = []struct{ key, action string }{
				{"type", "filter"}, {"↑↓", "navigate"}, {"enter", "drill"}, {"esc", "clear"},
			}
		} else {
			keys = []struct{ key, action string }{
				{"/", "search"}, {"j/k", "navigate"}, {"enter", "drill"}, {"x", "reset"}, {"+", "add"}, {"tab", "domain"}, {"esc", "back"},
			}
		}
	case phaseQuiz:
		keys = m.quizStatusKeys()
	case phaseLearn:
		keys = m.learnStatusKeys()
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
			keys = []struct{ key, action string }{
				{"↑↓", "scroll"}, {"esc", "close"},
			}
		case overlayChat:
			keys = []struct{ key, action string }{
				{"enter", "send"}, {"↑↓", "scroll"}, {"esc", "close"},
			}
		case overlayDomain:
			keys = []struct{ key, action string }{
				{"tab", "next"}, {"shift+tab", "prev"}, {"enter", "select"}, {"esc", "cancel"},
			}
		case overlayQuizType:
			keys = []struct{ key, action string }{
				{"↑↓", "move"}, {"enter", "toggle"}, {"esc", "close"},
			}
		}
	}

	var parts []string
	for i, k := range keys {
		if i > 0 {
			parts = append(parts, bulletStyle.Render(" · "))
		}
		if k.key != "" {
			parts = append(parts, keyStyle.Render(k.key), " ", actionStyle.Render(k.action))
		} else {
			parts = append(parts, dimStyle.Render(k.action))
		}
	}

	return strings.Join(parts, "")
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
		if m.showNotes {
			return []struct{ key, action string }{
				{"tab", "question"}, {"n", "notes"}, {"c", "chat"}, {"h", "hint"}, {"↑↓", "scroll"}, {"esc", "question"},
			}
		}
		if m.currentQ != nil && m.currentQ.Type == ollama.TypeMultiChoice {
			return []struct{ key, action string }{
				{"a-d", "answer"}, {"tab", "notes"}, {"h", "hint"}, {"c", "chat"}, {"esc", "skip"},
			}
		}
		back := "skip"
		if m.pickMode {
			back = "back"
		}
		return []struct{ key, action string }{
			{"enter", "submit"}, {"tab", "notes"}, {"ctrl+e", "hint"}, {"ctrl+y", "chat"}, {"esc", back},
		}
	case stepGrading:
		return []struct{ key, action string }{
			{"", "thinking..."},
		}
	case stepResult:
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
			{"enter", "send"}, {"g", "generate"}, {"esc", "back"},
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
