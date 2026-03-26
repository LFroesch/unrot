package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/LFroesch/unrot/internal/knowledge"
	"github.com/LFroesch/unrot/internal/ollama"
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

	base := lipgloss.JoinVertical(lipgloss.Left, header, rule, content, "", status)

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
	if m.sessionTotal > 0 || len(m.dueFiles) > 0 {
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
		if m.quizStep == stepLesson || m.quizStep == stepResult {
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
	total := len(m.dueFiles) + len(m.retryQueue)
	if m.retryPhase {
		total = m.sessionTotal + len(m.retryQueue) + 1
	}
	if total == 0 {
		total = 1
	}

	pct := float64(m.sessionTotal) / float64(total)
	if pct > 1 {
		pct = 1
	}

	barW := 15
	filled := int(pct * float64(barW))
	bar := barFilledStyle.Render(strings.Repeat("█", filled)) +
		barEmptyStyle.Render(strings.Repeat("░", barW-filled))

	score := fmt.Sprintf("%d/%d", m.sessionCorrect, m.sessionTotal)
	return bar + " " + statsStyle.Render(score)
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

	// Streak badge
	if m.state != nil && m.state.DayStreak > 0 {
		b.WriteString(streakStyle.Render(fmt.Sprintf("  %d day streak", m.state.DayStreak)))
		today := m.state.TodaySessions()
		if len(today) > 0 {
			totalQ := 0
			for _, s := range today {
				totalQ += s.Total
			}
			b.WriteString(dimStyle.Render(fmt.Sprintf(" · %d questions today", totalQ)))
		}
		b.WriteString("\n\n")
	}

	// Due count
	files := m.allFiles
	if m.domainFilter != "" {
		files = knowledge.FilterByDomain(files, m.domainFilter)
	}
	due := m.state.DueItems(files)
	dueCount := len(due)

	b.WriteString(divider("ready to review", m.wrapW()))
	b.WriteString("\n")
	if dueCount > 0 {
		b.WriteString(fmt.Sprintf("  %s topics due    %s start\n",
			correctStyle.Render(fmt.Sprintf("%d", dueCount)),
			dimStyle.Render("[enter]"),
		))
	} else {
		b.WriteString(dimStyle.Render("  nothing due — all caught up!"))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Domain filter (inline)
	b.WriteString("  domain: ")
	for i, d := range m.domainList {
		label := d
		if d == "all" {
			label = "all"
		}
		if i == m.domainCursor {
			b.WriteString(keyStyle.Render("[" + label + "]"))
		} else {
			b.WriteString(dimStyle.Render(" " + label + " "))
		}
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("          tab to cycle"))
	b.WriteString("\n\n")

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
	b.WriteString("\n")

	// Domain tabs
	b.WriteString("  ")
	for i, d := range m.domainList {
		label := d
		if d == "all" {
			label = "all"
		}
		if i == m.domainCursor {
			b.WriteString(keyStyle.Render("[" + label + "]"))
		} else {
			b.WriteString(dimStyle.Render(" " + label + " "))
		}
	}
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
		domainMastery := m.domainAvgStrength(lastDomain)
		b.WriteString(fmt.Sprintf("  %s  %s\n",
			domainStyle.Render(lastDomain),
			dimStyle.Render(fmt.Sprintf("%d%%", int(domainMastery*100))),
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
			domainMastery := m.domainAvgStrength(domain)
			b.WriteString(fmt.Sprintf("  %s  %s\n",
				domainStyle.Render(domain),
				dimStyle.Render(fmt.Sprintf("%d%%", int(domainMastery*100))),
			))
			lastDomain = domain
		}

		strength := 0.0
		if m.state != nil {
			strength = m.state.Strength(file)
		}
		bar := strengthMini(strength)

		if i == m.pickCursor {
			b.WriteString(fmt.Sprintf("  %s %-24s %s\n",
				cursorStyle.Render(">"),
				lipgloss.NewStyle().Foreground(colorText).Render(name),
				bar,
			))
		} else {
			b.WriteString(fmt.Sprintf("    %-24s %s\n",
				name,
				bar,
			))
		}
	}

	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d/%d", m.pickCursor+1, len(m.pickFiles))))

	return b.String()
}

func (m model) domainAvgStrength(domain string) float64 {
	if m.state == nil {
		return 0
	}
	count := 0
	total := 0.0
	for _, f := range m.pickFiles {
		if knowledge.Domain(f) == domain {
			total += m.state.Strength(f)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
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
	case stepRevealed:
		return m.renderRevealed()
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

	b.WriteString("\n")
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

	// Inline knowledge toggle (shown when overlay not used)
	if m.showKnowledge && m.sourceContent != "" {
		b.WriteString("\n\n")
		b.WriteString(divider("knowledge", w))
		b.WriteString("\n")
		b.WriteString(renderMarkdown(m.sourceContent, w))
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

func (m model) renderRevealed() string {
	var b strings.Builder
	w := m.wrapW()

	b.WriteString("\n")
	b.WriteString(questionStyle.Width(w).Render(m.currentQ.Text))
	b.WriteString("\n")

	if m.userAnswer != "" {
		b.WriteString("\n")
		b.WriteString(divider("you said", w))
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().PaddingLeft(4).Width(w).Render(m.userAnswer))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(divider("correct answer", w))
	b.WriteString("\n")
	b.WriteString(answerStyle.Width(w).Render("  " + m.currentQ.Answer))

	if m.currentQ.Explanation != "" {
		b.WriteString("\n\n")
		b.WriteString(divider("why", w))
		b.WriteString("\n")
		b.WriteString(explainStyle.Width(w).Render("  " + m.currentQ.Explanation))
	}

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
			var userStyle lipgloss.Style
			switch m.grade {
			case gradeCorrect:
				userStyle = correctStyle
			case gradePartial:
				userStyle = sortOfStyle
			default:
				userStyle = wrongStyle
			}
			b.WriteString(userStyle.PaddingLeft(4).Width(w).Render(m.userAnswer))
			b.WriteString("\n")
		}

		b.WriteString("\n")
		b.WriteString(divider("correct answer", w))
		b.WriteString("\n")
		b.WriteString(answerStyle.Width(w).Render("  " + m.currentQ.Answer))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	switch m.grade {
	case gradeCorrect:
		b.WriteString(correctStyle.Render("  ✓ correct"))
	case gradePartial:
		b.WriteString(sortOfStyle.Render("  ~ sort of"))
	default:
		b.WriteString(wrongStyle.Render("  ✗ wrong"))
	}

	if m.currentQ.Explanation != "" {
		b.WriteString("\n\n")
		b.WriteString(divider("explanation", w))
		b.WriteString("\n")
		b.WriteString(explainStyle.Width(w).Render("  " + m.currentQ.Explanation))
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

func (m model) buildChatOverlayContent() string {
	var b strings.Builder
	w := m.overlayViewport.Width - 4

	if len(m.conceptChat) == 0 {
		b.WriteString(dimStyle.Render("  ask anything about this concept..."))
		return b.String()
	}

	start := 0
	if len(m.conceptChat) > 8 {
		start = len(m.conceptChat) - 8
	}
	for _, msg := range m.conceptChat[start:] {
		b.WriteString("\n")
		if msg.role == "user" {
			b.WriteString(optionStyle.Width(w).Render(
				keyStyle.Render("you: ") + msg.content,
			))
		} else {
			b.WriteString(lipgloss.NewStyle().Width(w).PaddingLeft(2).Foreground(colorText).Render(msg.content))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// --- Learn ---

func (m model) renderLearn() string {
	switch m.learnStep {
	case learnInput:
		return m.renderLearnInput()
	case learnGenerating:
		return "\n  " + m.spinner.View() + " generating knowledge...\n"
	case learnReview:
		return m.renderLearnReview()
	}
	return ""
}

func (m model) renderLearnInput() string {
	var b strings.Builder

	b.WriteString(questionStyle.Render("what do you want to learn about?"))
	b.WriteString("\n\n")
	b.WriteString("  " + m.learnTA.View())
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("  tip: use domain/topic for organization"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  e.g. docker/multi-stage-builds"))

	return b.String()
}

func (m model) renderLearnReview() string {
	var b strings.Builder

	b.WriteString(domainStyle.Render(fmt.Sprintf("  %s/%s", m.learnDomain, m.learnSlug)))
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

	b.WriteString(dimStyle.Render("  domain          mastery  due/total"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  " + strings.Repeat("─", 40)))
	b.WriteString("\n")

	for _, ds := range stats {
		barW := 8
		filled := int(ds.Mastery * float64(barW))
		bar := barFilledStyle.Render(strings.Repeat("█", filled)) +
			barEmptyStyle.Render(strings.Repeat("░", barW-filled))

		pct := fmt.Sprintf("%3d%%", int(ds.Mastery*100))

		name := ds.Domain
		if len(name) > 14 {
			name = name[:14]
		}
		name = name + strings.Repeat(" ", 14-len(name))

		dueStr := fmt.Sprintf("%d/%d", ds.Due, ds.Total)

		var masteryStyle lipgloss.Style
		switch {
		case ds.Mastery >= 0.7:
			masteryStyle = correctStyle
		case ds.Mastery >= 0.4:
			masteryStyle = actionStyle
		default:
			masteryStyle = wrongStyle
		}

		b.WriteString(fmt.Sprintf("  %s  %s %s  %s\n",
			domainStyle.Render(name),
			bar,
			masteryStyle.Render(pct),
			dimStyle.Render(dueStr),
		))
	}

	b.WriteString("\n")
	totalDue := 0
	for _, ds := range stats {
		totalDue += ds.Due
	}
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d files in bank · %d due for review",
		len(m.allFiles), totalDue)))

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
			{"enter", "start review"}, {"tab", "domain"}, {"b", "topics"}, {"l", "learn"}, {"s", "stats"}, {"q", "quit"},
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
			{"↑↓", "scroll"}, {"esc", "back"},
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
		case overlayKnowledge:
			keys = []struct{ key, action string }{
				{"↑↓", "scroll"}, {"esc", "close"},
			}
		case overlayChat:
			keys = []struct{ key, action string }{
				{"enter", "send"}, {"↑↓", "scroll"}, {"esc", "close"},
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
			{"enter", "start quiz"}, {"c", "chat"}, {"↑↓", "scroll"}, {"esc", "skip"}, {"q", "back"},
		}
	case stepLoading:
		return []struct{ key, action string }{
			{"esc", "back"},
		}
	case stepQuestion:
		if m.currentQ != nil && m.currentQ.Type == ollama.TypeMultiChoice {
			return []struct{ key, action string }{
				{"a-d", "answer"}, {"ctrl+r", "new q"}, {"ctrl+o", "knowledge"}, {"ctrl+y", "chat"}, {"ctrl+e", "hint"}, {"esc", "skip"},
			}
		}
		back := "skip"
		if m.pickMode {
			back = "back"
		}
		return []struct{ key, action string }{
			{"enter", "submit"}, {"tab", "reveal"}, {"ctrl+r", "new q"}, {"ctrl+o", "knowledge"}, {"ctrl+y", "chat"}, {"ctrl+e", "hint"}, {"esc", back},
		}
	case stepGrading:
		return []struct{ key, action string }{
			{"", "thinking..."},
		}
	case stepRevealed:
		return []struct{ key, action string }{
			{"y", "knew it"}, {"s", "sort of"}, {"n", "didn't know"}, {"esc", "back"},
		}
	case stepResult:
		return []struct{ key, action string }{
			{"enter", "next"}, {"r", "re-quiz"}, {"e", "explain"}, {"c", "chat"}, {"ctrl+o", "knowledge"}, {"esc", "back"},
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
	case learnGenerating:
		return []struct{ key, action string }{
			{"", "generating..."},
		}
	case learnReview:
		return []struct{ key, action string }{
			{"s", "save & quiz"}, {"r", "regenerate"}, {"↑↓", "scroll"}, {"esc", "discard"},
		}
	}
	return nil
}
