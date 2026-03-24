package main

import (
	"fmt"
	"strings"

	"github.com/LFroesch/unrot/internal/ollama"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212"))

	domainStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))

	questionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			PaddingLeft(2)

	answerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("114")).
			PaddingLeft(2)

	hintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	statsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))

	errStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	typeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("99")).
			Italic(true)

	optionStyle = lipgloss.NewStyle().
			PaddingLeft(4)

	correctStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("114")).
			Bold(true)

	wrongStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)
)

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	var b strings.Builder

	// Header
	header := titleStyle.Render("unrot")
	if m.domain != "" {
		header += " " + domainStyle.Render("· "+m.domain)
	}
	if m.currentQ != nil {
		header += " " + typeStyle.Render("· "+m.currentQ.Type.String())
	}
	if m.sessionTotal > 0 {
		header += " " + statsStyle.Render(fmt.Sprintf("· %d/%d correct", m.sessionCorrect, m.sessionTotal))
	}
	b.WriteString(header + "\n\n")

	switch m.phase {
	case phaseLoading:
		b.WriteString(hintStyle.Render("  generating question..."))

	case phaseQuestion:
		b.WriteString(questionStyle.Render(m.currentQ.Text))
		b.WriteString("\n\n")
		if m.currentQ.Type == ollama.TypeMultiChoice {
			for i, opt := range m.currentQ.Options {
				letter := string(rune('a' + i))
				b.WriteString(optionStyle.Render(fmt.Sprintf("%s) %s", letter, opt)))
				b.WriteString("\n")
			}
			b.WriteString("\n")
			b.WriteString(hintStyle.Render("  a/b/c/d answer · s skip · q quit"))
		} else {
			b.WriteString(hintStyle.Render("  enter reveal · s skip · q quit"))
		}

	case phaseRevealed:
		b.WriteString(questionStyle.Render(m.currentQ.Text))
		b.WriteString("\n\n")
		b.WriteString(answerStyle.Render(m.currentQ.Answer))
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("  y correct · n wrong · q quit"))

	case phaseAnswered:
		b.WriteString(questionStyle.Render(m.currentQ.Text))
		b.WriteString("\n\n")
		for i, opt := range m.currentQ.Options {
			letter := string(rune('a' + i))
			line := fmt.Sprintf("%s) %s", letter, opt)
			switch {
			case i == m.currentQ.CorrectIdx && i == m.mcPicked:
				b.WriteString(optionStyle.Render(correctStyle.Render("✓ "+line)))
			case i == m.currentQ.CorrectIdx:
				b.WriteString(optionStyle.Render(correctStyle.Render("→ "+line)))
			case i == m.mcPicked:
				b.WriteString(optionStyle.Render(wrongStyle.Render("✗ "+line)))
			default:
				b.WriteString(optionStyle.Render(hintStyle.Render("  "+line)))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		if m.selfGrade {
			b.WriteString(correctStyle.Render("  Correct!"))
		} else {
			b.WriteString(wrongStyle.Render("  Wrong — answer was: " + m.currentQ.Answer))
		}
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("  enter next · q quit"))

	case phaseError:
		b.WriteString(errStyle.Render(fmt.Sprintf("  error: %v", m.err)))
		b.WriteString("\n\n")
		b.WriteString(hintStyle.Render("  q quit"))

	case phaseDone:
		b.WriteString(titleStyle.Render("  session complete!"))
		b.WriteString("\n\n")
		b.WriteString(fmt.Sprintf("  %d correct, %d wrong out of %d\n",
			m.sessionCorrect, m.sessionWrong, m.sessionTotal))
		b.WriteString("\n")
		b.WriteString(hintStyle.Render("  q quit"))
	}

	return b.String()
}
