package app

import (
	"fmt"
	"image/color"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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

func (m *Model) sidebarRowAt(y int) (sidebarRow, bool) {
	index := y - sidebarProjectStart
	rows := m.sidebarRows()
	visibleRows := max(0, m.height-2-sidebarProjectStart)
	if index < 0 || index >= len(rows) || index >= visibleRows {
		return sidebarRow{}, false
	}
	return rows[index], true
}

func (m *Model) focusSidebarNavigation(cursor int) {
	m.mode = modeNavigate
	m.modeBeforePrefix = modeNavigate
	m.focus = focusSidebar
	m.sidebarCursor = max(0, min(cursor, len(m.tabs)))
}

func (m *Model) renderProjectSidebarRow(projectIndex, width int) string {
	if projectIndex < 0 || projectIndex >= len(m.tabs) {
		return fitLine("", width)
	}
	item := m.tabs[projectIndex]
	active := projectIndex == m.activeTab
	cursor := projectIndex == m.sidebarCursor && m.focus == focusSidebar
	label := m.renderTabStatus(item) + " " + item.title
	if cursor {
		label = "› " + label
	} else {
		label = "  " + label
	}
	label = fitLine(label, width)
	if active {
		// Status dots carry their own ANSI reset sequences. Strip those
		// before applying the active style so the grey background stays
		// continuous behind the complete project row.
		return styles.activeSession.Render(fitLine(ansi.Strip(label), width))
	}
	return styles.session.Width(width).Render(label)
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
