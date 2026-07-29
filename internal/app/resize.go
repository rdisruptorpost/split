package app

import (
	tea "charm.land/bubbletea/v2"

	"split/internal/layout"
)

const (
	minimumSidebarWidth   = sidebarBrandWidth + 2
	maximumSidebarWidth   = 48
	minimumWorkspaceWidth = 42
)

type resizeAxisGesture struct {
	enabled  bool
	edge     layout.ResizeEdge
	startPos int
}

type resizeGestureState struct {
	active            bool
	sidebar           bool
	projectID         string
	paneID            string
	startX            int
	startY            int
	startSidebarWidth int
	horizontal        resizeAxisGesture
	vertical          resizeAxisGesture
}

func normalizeSidebarWidth(width int) int {
	if width <= 0 {
		return sidebarWidth
	}
	return max(minimumSidebarWidth, min(width, maximumSidebarWidth))
}

func (m *Model) maximumEffectiveSidebarWidth() int {
	return min(maximumSidebarWidth, m.width-minimumWorkspaceWidth)
}

func (m *Model) beginResizeGesture(mouse tea.Mouse) bool {
	if mouse.Button != tea.MouseRight || mouse.Mod&tea.ModAlt == 0 {
		return false
	}

	sidebar := m.effectiveSidebarWidth()
	if sidebar > 0 && mouse.X < sidebar {
		m.clearTerminalSelection()
		m.focusSidebarNavigation(m.activeTab)
		m.notice = ""
		m.resizeGesture = resizeGestureState{
			active:            true,
			sidebar:           true,
			startX:            mouse.X,
			startY:            mouse.Y,
			startSidebarWidth: sidebar,
		}
		return true
	}

	workspace := m.workspaceRect()
	if !workspace.Contains(mouse.X, mouse.Y) {
		return false
	}
	active := m.active()
	if active == nil || active.root == nil {
		return true
	}

	for paneID, rect := range active.root.Rects(workspace) {
		if !rect.Contains(mouse.X, mouse.Y) {
			continue
		}

		horizontalPreferred := layout.ResizeRight
		if mouse.X < rect.X+rect.Width/2 {
			horizontalPreferred = layout.ResizeLeft
		}
		verticalPreferred := layout.ResizeBottom
		if mouse.Y < rect.Y+rect.Height/2 {
			verticalPreferred = layout.ResizeTop
		}

		horizontalEdge, horizontal := nearestResizableEdge(
			active.root,
			paneID,
			horizontalPreferred,
		)
		verticalEdge, vertical := nearestResizableEdge(
			active.root,
			paneID,
			verticalPreferred,
		)
		if !horizontal && !vertical {
			m.notice = "This pane has no divider to resize"
			return true
		}

		m.clearTerminalSelection()
		active.activePane = paneID
		m.focus = focusPanes
		if m.mode == modePrefix {
			m.mode = modeNavigate
		}
		m.notice = ""
		m.resizeGesture = resizeGestureState{
			active:    true,
			projectID: active.id,
			paneID:    paneID,
			startX:    mouse.X,
			startY:    mouse.Y,
			horizontal: resizeAxisGesture{
				enabled:  horizontal,
				edge:     horizontalEdge,
				startPos: resizeEdgePosition(rect, horizontalEdge),
			},
			vertical: resizeAxisGesture{
				enabled:  vertical,
				edge:     verticalEdge,
				startPos: resizeEdgePosition(rect, verticalEdge),
			},
		}
		return true
	}
	return true
}

func nearestResizableEdge(
	root *layout.Node,
	paneID string,
	preferred layout.ResizeEdge,
) (layout.ResizeEdge, bool) {
	if root.CanResize(paneID, preferred) {
		return preferred, true
	}
	opposite := preferred.Opposite()
	return opposite, root.CanResize(paneID, opposite)
}

func resizeEdgePosition(rect layout.Rect, edge layout.ResizeEdge) int {
	switch edge {
	case layout.ResizeRight:
		return rect.X + rect.Width
	case layout.ResizeLeft:
		return rect.X
	case layout.ResizeBottom:
		return rect.Y + rect.Height
	default:
		return rect.Y
	}
}

func (m *Model) handleResizeGestureMotion(mouse tea.Mouse) bool {
	gesture := m.resizeGesture
	if !gesture.active {
		return false
	}
	if mouse.Button != tea.MouseRight {
		m.finishResizeGesture()
		return true
	}
	if gesture.sidebar {
		maximum := m.maximumEffectiveSidebarWidth()
		if maximum < minimumSidebarWidth {
			return true
		}
		m.sidebarSize = max(
			minimumSidebarWidth,
			min(gesture.startSidebarWidth+mouse.X-gesture.startX, maximum),
		)
		return true
	}

	active := m.active()
	if active == nil || active.id != gesture.projectID || active.root == nil ||
		!active.root.ContainsPane(gesture.paneID) {
		m.resizeGesture = resizeGestureState{}
		return true
	}
	workspace := m.workspaceRect()
	if gesture.horizontal.enabled {
		active.root.ResizeSplit(
			gesture.paneID,
			gesture.horizontal.edge,
			gesture.horizontal.startPos+mouse.X-gesture.startX,
			workspace,
		)
	}
	if gesture.vertical.enabled {
		active.root.ResizeSplit(
			gesture.paneID,
			gesture.vertical.edge,
			gesture.vertical.startPos+mouse.Y-gesture.startY,
			workspace,
		)
	}
	return true
}

func (m *Model) handleResizeGestureRelease(mouse tea.Mouse) bool {
	if !m.resizeGesture.active {
		return false
	}
	m.handleResizeGestureMotion(mouse)
	m.finishResizeGesture()
	return true
}

func (m *Model) finishResizeGesture() {
	if !m.resizeGesture.active {
		return
	}
	m.resizeGesture = resizeGestureState{}
	m.resizeActivePanes()
	m.persist()
}
