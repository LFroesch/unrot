package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/lipgloss"
)

// placeOverlay centers fg (dialog box) over bg (background content), merging them line by line.
// Uses ANSI-aware truncation so terminal colors in the background are preserved.
func placeOverlay(bg, fg string) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	bgHeight := len(bgLines)
	fgHeight := len(fgLines)

	fgWidth := 0
	for _, line := range fgLines {
		if w := lipgloss.Width(line); w > fgWidth {
			fgWidth = w
		}
	}

	bgWidth := 0
	for _, line := range bgLines {
		if w := lipgloss.Width(line); w > bgWidth {
			bgWidth = w
		}
	}
	startX := (bgWidth - fgWidth) / 2
	startY := (bgHeight - fgHeight) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	result := make([]string, bgHeight)
	copy(result, bgLines)

	for i, fgLine := range fgLines {
		bgIdx := startY + i
		if bgIdx < 0 || bgIdx >= bgHeight {
			continue
		}

		bgLine := bgLines[bgIdx]
		bgLineWidth := lipgloss.Width(bgLine)

		left := xansi.Truncate(bgLine, startX, "")
		leftWidth := lipgloss.Width(left)
		if leftWidth < startX {
			left += strings.Repeat(" ", startX-leftWidth)
		}

		right := ""
		rightStart := startX + fgWidth
		if rightStart < bgLineWidth {
			right = xansi.Cut(bgLine, rightStart, bgLineWidth)
		}

		result[bgIdx] = left + fgLine + right
	}

	return strings.Join(result, "\n")
}

// scrollHint returns a compact dim scroll indicator for the header bar.
// Returns "" if content fits in the viewport.
func scrollHint(vp viewport.Model) string {
	if vp.TotalLineCount() <= vp.Height {
		return ""
	}
	up := vp.YOffset > 0
	down := vp.YOffset < vp.TotalLineCount()-vp.Height
	pct := 0
	if vp.TotalLineCount()-vp.Height > 0 {
		pct = vp.YOffset * 100 / (vp.TotalLineCount() - vp.Height)
	}

	var hint string
	switch {
	case up && down:
		hint = fmt.Sprintf("▲▼ %d%%", pct)
	case up:
		hint = "▲ top"
	case down:
		hint = "▼ more"
	}
	return headerDimStyle.Render(hint)
}

// formatDuration formats seconds into a human-readable string (e.g. "1h 23m", "45m", "2m").
func formatDuration(totalSec int) string {
	if totalSec < 60 {
		return fmt.Sprintf("%ds", totalSec)
	}
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
