package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette from the dockterm design mockup. Terminals without true color get
// the nearest 256-color match automatically.
var (
	cText   = lipgloss.Color("#d4d7dd")
	cBright = lipgloss.Color("#ffffff")
	cMuted  = lipgloss.Color("#8b93a1")
	cDim    = lipgloss.Color("#5d6573")
	cLine   = lipgloss.Color("#262c36")
	cBarBg  = lipgloss.Color("#10141a")
	//cPanelBg = lipgloss.Color("#12161c")
	cTrackBg = lipgloss.Color("#1a1f27")
	cSelBg   = lipgloss.Color("#16242c")
	cDark    = lipgloss.Color("#0d1014")
	cAccent  = lipgloss.Color("#7cc4d8")
	cUp      = lipgloss.Color("#6fcf8a")
	cDown    = lipgloss.Color("#e8847b")
	cPause   = lipgloss.Color("#e6b84f")
)

var (
	sText   = lipgloss.NewStyle().Foreground(cText)
	sBright = lipgloss.NewStyle().Foreground(cBright).Bold(true)
	sMuted  = lipgloss.NewStyle().Foreground(cMuted)
	sDim    = lipgloss.NewStyle().Foreground(cDim)
	sAccent = lipgloss.NewStyle().Foreground(cAccent)
	sLine   = lipgloss.NewStyle().Foreground(cLine)
	sUp     = lipgloss.NewStyle().Foreground(cUp)
	sDown   = lipgloss.NewStyle().Foreground(cDown)
	sPause  = lipgloss.NewStyle().Foreground(cPause)

	sKeyChip = lipgloss.NewStyle().Foreground(cAccent).Background(cLine).Bold(true).Padding(0, 1)
	sMode    = lipgloss.NewStyle().Foreground(cDark).Background(cAccent).Bold(true)
)

// stateColor is the color used for a container state everywhere.
func stateColor(state string) color.Color {
	switch state {
	case "running":
		return cUp
	case "paused", "restarting", "created":
		return cPause
	default:
		return cDown
	}
}

func stateGlyph(state string) string {
	switch state {
	case "running":
		return "●"
	case "paused":
		return "‖"
	case "restarting":
		return "↻"
	default:
		return "■"
	}
}

// badge renders a solid label like RUNNING on the given color.
func badge(text string, bg color.Color) string {
	return lipgloss.NewStyle().Foreground(cDark).Background(bg).Bold(true).Padding(0, 1).Render(text)
}
