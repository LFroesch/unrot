package main

import "github.com/charmbracelet/lipgloss"

var (
	// Colors — consistent palette (matches tui-hub apps)
	colorPrimary = lipgloss.Color("#5AF78E") // green
	colorAccent  = lipgloss.Color("#57C7FF") // blue
	colorWarn    = lipgloss.Color("#FF6AC1") // pink
	colorError   = lipgloss.Color("#FF5C57") // red
	colorDim     = lipgloss.Color("#606060")
	colorText    = lipgloss.Color("#EEEEEE")
	colorYellow  = lipgloss.Color("#F3F99D")

	// Header
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	domainStyle = lipgloss.NewStyle().
			Foreground(colorAccent)

	typeStyle = lipgloss.NewStyle().
			Foreground(colorWarn).
			Italic(true)

	statsStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	retryStyle = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	// Content
	questionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorText).
			PaddingLeft(2)

	answerStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			PaddingLeft(2)

	explainStyle = lipgloss.NewStyle().
			Foreground(colorYellow)

	optionStyle = lipgloss.NewStyle().
			PaddingLeft(4)

	correctStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	wrongStyle = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	errStyle = lipgloss.NewStyle().
			Foreground(colorError)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	cursorStyle = lipgloss.NewStyle().
			Foreground(colorAccent)

	// Status bar
	keyStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	actionStyle = lipgloss.NewStyle().
			Foreground(colorPrimary)

	bulletStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	// Progress bar
	barFilledStyle = lipgloss.NewStyle().
			Foreground(colorPrimary)

	barEmptyStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	// Streak
	streakStyle = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true)

	// Section label (dim bold for divider labels)
	labelStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			Bold(true)

	// Section headers in markdown rendering
	sectionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAccent)

	// Content panel — no side borders, just padding
	panelStyle = lipgloss.NewStyle().
			Padding(0, 2)
)

// confidenceColor returns the appropriate color for a confidence level.
func confidenceColor(level int) lipgloss.Color {
	switch {
	case level >= 4:
		return colorPrimary // green
	case level >= 2:
		return colorYellow
	case level == 1:
		return colorError // red
	default:
		return colorDim
	}
}
