package app

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

var palette = struct {
	background color.Color
	surface    color.Color
	surfaceAlt color.Color
	border     color.Color
	text       color.Color
	muted      color.Color
	accent     color.Color
	secondary  color.Color
	green      color.Color
	yellow     color.Color
	red        color.Color
}{
	background: lipgloss.Color("#0C0E10"),
	surface:    lipgloss.Color("#15181B"),
	surfaceAlt: lipgloss.Color("#272C31"),
	border:     lipgloss.Color("#4A5056"),
	text:       lipgloss.Color("#E8E5DF"),
	muted:      lipgloss.Color("#92989E"),
	accent:     lipgloss.Color("#E1D1B8"),
	secondary:  lipgloss.Color("#C6B9CC"),
	green:      lipgloss.Color("#A6C39E"),
	yellow:     lipgloss.Color("#D8B56E"),
	red:        lipgloss.Color("#DC8B8B"),
}

var styles = struct {
	logo    lipgloss.Style
	eyebrow lipgloss.Style
	text    lipgloss.Style
	muted   lipgloss.Style

	activeSession lipgloss.Style
	session       lipgloss.Style
	paneTitle     lipgloss.Style
	status        lipgloss.Style
}{
	logo: lipgloss.NewStyle().
		Foreground(palette.accent).
		Bold(true),
	eyebrow: lipgloss.NewStyle().
		Foreground(palette.muted).
		Bold(true),
	text: lipgloss.NewStyle().
		Foreground(palette.text),
	muted: lipgloss.NewStyle().
		Foreground(palette.muted),

	activeSession: lipgloss.NewStyle().
		Foreground(palette.text).
		Background(palette.surfaceAlt).
		Bold(true),
	session: lipgloss.NewStyle().
		Foreground(palette.muted),
	paneTitle: lipgloss.NewStyle().
		Foreground(palette.text).
		Bold(true),
	status: lipgloss.NewStyle().
		Foreground(palette.muted).
		Background(palette.surface),
}
