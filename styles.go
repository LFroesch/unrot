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

	totalStyle = lipgloss.NewStyle().
			Foreground(colorWarn).
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

	// Full-width header bar
	headerBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Padding(0, 1)

	// Full-width status bar
	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).
			Padding(0, 1)

	// Inline styles for status bar text (need matching background)
	statusKeyStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Background(lipgloss.Color("235")).
			Bold(true).
			Inline(true)

	statusActionStyle = lipgloss.NewStyle().
				Foreground(colorText).
				Background(lipgloss.Color("235")).
				Inline(true)

	statusBulletStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Background(lipgloss.Color("235")).
				Inline(true)

	// Header inline styles
	headerTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPrimary).
				Background(lipgloss.Color("235")).
				Inline(true)

	headerDimStyle = lipgloss.NewStyle().
			Foreground(colorDim).
			Background(lipgloss.Color("235")).
			Inline(true)

	headerPurpleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("201")).
				Background(lipgloss.Color("235")).
				Inline(true)

	headerAccentStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Background(lipgloss.Color("235")).
				Inline(true)

	headerWarnStyle = lipgloss.NewStyle().
			Foreground(colorWarn).
			Background(lipgloss.Color("235")).
			Inline(true)

	headerStreakStyle = lipgloss.NewStyle().
				Foreground(colorYellow).
				Background(lipgloss.Color("235")).
				Bold(true).
				Inline(true)

	headerBarFilledStyle = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Background(lipgloss.Color("235")).
				Inline(true)

	headerBarEmptyStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Background(lipgloss.Color("235")).
				Inline(true)

	headerStatsStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Background(lipgloss.Color("235")).
				Inline(true)

	headerRetryStyle = lipgloss.NewStyle().
				Foreground(colorError).
				Background(lipgloss.Color("235")).
				Bold(true).
				Inline(true)
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
