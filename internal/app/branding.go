package app

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var sidebarBrandFrame = []string{
	"                     ",
	"██▀▀██▀████▀█▀█▀█▀█▀█",
	"█▀▀███▀▀██▀██ █ █████",
	"█▀▀▀█▀██▀▀ ██▀▀▀██▀██",
}

// The masks preserve the two foreground tones from split_bubble_tea.go.
// A dot leaves the terminal's default foreground untouched.
var sidebarBrandMasks = []string{
	".....................",
	"011101110011011101110",
	"0111011101100.1.00100",
	"01110100.1.0011100100",
}

func renderSidebarBrand() []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#7F7F7F"))
	bright := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	rendered := make([]string, len(sidebarBrandFrame))

	for y, row := range sidebarBrandFrame {
		var line strings.Builder
		mask := []rune(sidebarBrandMasks[y])
		for x, char := range []rune(row) {
			switch mask[x] {
			case '0':
				line.WriteString(dim.Render(string(char)))
			case '1':
				line.WriteString(bright.Render(string(char)))
			default:
				line.WriteRune(char)
			}
		}
		rendered[y] = line.String()
	}
	return rendered
}
