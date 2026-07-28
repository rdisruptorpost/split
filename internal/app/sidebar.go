package app

import (
	"fmt"
	"image/color"
	"time"

	"charm.land/lipgloss/v2"

	"split/internal/agent"
)

type sidebarRowKind uint8

const (
	sidebarProjectRow sidebarRowKind = iota
	sidebarAgentRow
	sidebarNewProjectRow
)

type sidebarRow struct {
	kind         sidebarRowKind
	projectIndex int
	paneID       string
}

func (m *Model) sidebarRows() []sidebarRow {
	rows := make([]sidebarRow, 0, len(m.tabs)+len(m.agents)+1)
	for projectIndex, project := range m.tabs {
		rows = append(rows, sidebarRow{kind: sidebarProjectRow, projectIndex: projectIndex})
		if project == nil || project.root == nil {
			continue
		}
		for _, paneID := range project.root.Leaves() {
			if _, exists := m.agents[paneID]; !exists {
				continue
			}
			rows = append(rows, sidebarRow{
				kind:         sidebarAgentRow,
				projectIndex: projectIndex,
				paneID:       paneID,
			})
		}
	}
	return append(rows, sidebarRow{kind: sidebarNewProjectRow, projectIndex: len(m.tabs)})
}

func (m *Model) agentDisplayName(row sidebarRow) string {
	current, exists := m.agents[row.paneID]
	if !exists {
		return "Agent"
	}
	project := m.tabs[row.projectIndex]
	matching := 0
	ordinal := 0
	for _, paneID := range project.root.Leaves() {
		candidate, exists := m.agents[paneID]
		if !exists || candidate.Kind != current.Kind {
			continue
		}
		matching++
		if paneID == row.paneID {
			ordinal = matching
		}
	}
	if matching > 1 {
		return fmt.Sprintf("%s %d", current.Kind.Label(), ordinal)
	}
	return current.Kind.Label()
}

func (m *Model) renderAgentSidebarRow(row sidebarRow, width int) string {
	current, exists := m.agents[row.paneID]
	if !exists {
		return ""
	}
	icon, iconColor := agentStatusIcon(current.Status)
	iconStyle := lipgloss.NewStyle().Foreground(iconColor).Bold(true)
	nameStyle := lipgloss.NewStyle().Foreground(palette.text)
	statusStyle := lipgloss.NewStyle().Foreground(iconColor)

	label := "    " + iconStyle.Render(icon) + " " +
		nameStyle.Render(m.agentDisplayName(row)) +
		styles.muted.Render(" · ") +
		statusStyle.Render(current.Status.Label())
	return fitLine(label, width)
}

func agentStatusIcon(status agent.Status) (string, color.Color) {
	switch status {
	case agent.StatusLoading:
		return spinnerFrame(), palette.muted
	case agent.StatusWorking:
		return spinnerFrame(), palette.yellow
	case agent.StatusBlocked:
		return "!", palette.red
	case agent.StatusFinished:
		return "✓", palette.green
	case agent.StatusInterrupted, agent.StatusExited:
		return "×", palette.red
	default:
		return "○", palette.green
	}
}

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinnerFrame() string {
	index := (time.Now().UnixMilli() / 80) % int64(len(spinnerFrames))
	return spinnerFrames[index]
}
