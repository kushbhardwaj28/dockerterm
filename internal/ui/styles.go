package ui

import "charm.land/lipgloss/v2"

// 256-color palette, matching the design mockup.
var (
	colorMuted      = lipgloss.Color("244")
	colorAccent     = lipgloss.Color("117")
	colorRunning    = lipgloss.Color("114")
	colorStopped    = lipgloss.Color("210")
	colorWarn       = lipgloss.Color("221")
	colorBar        = lipgloss.Color("235")
	colorBorder     = lipgloss.Color("240")
	colorSelectedFg = lipgloss.Color("229")
	colorSelectedBg = lipgloss.Color("24")
)

var (
	mutedStyle   = lipgloss.NewStyle().Foreground(colorMuted)
	accentStyle  = lipgloss.NewStyle().Foreground(colorAccent)
	runningStyle = lipgloss.NewStyle().Foreground(colorRunning)
	stoppedStyle = lipgloss.NewStyle().Foreground(colorStopped)
	warnStyle    = lipgloss.NewStyle().Foreground(colorWarn)
	barStyle     = lipgloss.NewStyle().Background(colorBar)
	keyStyle     = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
)
