package layout

// ResizeEdge names the side of a pane selected by a resize gesture.
type ResizeEdge uint8

const (
	ResizeRight ResizeEdge = iota
	ResizeLeft
	ResizeBottom
	ResizeTop
)

func (edge ResizeEdge) axis() Axis {
	if edge == ResizeRight || edge == ResizeLeft {
		return Columns
	}
	return Rows
}

func (edge ResizeEdge) far() bool {
	return edge == ResizeRight || edge == ResizeBottom
}

func (edge ResizeEdge) Opposite() ResizeEdge {
	switch edge {
	case ResizeRight:
		return ResizeLeft
	case ResizeLeft:
		return ResizeRight
	case ResizeBottom:
		return ResizeTop
	default:
		return ResizeBottom
	}
}

// CanResize reports whether one pane edge is owned by a split divider. An edge
// on the outside of the complete layout has no divider and cannot be resized.
func (n *Node) CanResize(paneID string, edge ResizeEdge) bool {
	path := n.pathToPane(paneID)
	_, _, ok := resizeOwner(path, edge)
	return ok
}

// ResizeSplit moves the nearest split divider that owns edge to the absolute
// cell coordinate pos. It changes the tree ratio, leaving pane rectangles to
// be derived by Rects on the next render.
func (n *Node) ResizeSplit(paneID string, edge ResizeEdge, pos int, area Rect) bool {
	path := n.pathToPane(paneID)
	owner, ownerIndex, ok := resizeOwner(path, edge)
	if !ok {
		return false
	}

	bounds := area
	for index := 0; index < ownerIndex; index++ {
		parent := path[index]
		first, second := SplitSizes(
			parent.Axis,
			bounds.Width,
			bounds.Height,
			parent.Ratio,
		)
		if parent.Axis == Columns {
			if path[index+1] == parent.First {
				bounds.Width = first
			} else {
				bounds.X += first + Gap
				bounds.Width = second
			}
			continue
		}
		if path[index+1] == parent.First {
			bounds.Height = first
		} else {
			bounds.Y += first + Gap
			bounds.Height = second
		}
	}

	origin, extent := bounds.X, bounds.Width
	if edge.axis() == Rows {
		origin, extent = bounds.Y, bounds.Height
	}
	available := extent - Gap
	if available <= 0 {
		return false
	}

	line := pos
	if !edge.far() {
		line -= Gap
	}
	lo := origin + minExtent(owner.First, edge.axis())
	hi := origin + available - minExtent(owner.Second, edge.axis())
	if lo > hi {
		return false
	}
	line = max(lo, min(line, hi))

	// SplitSizes floors available*ratio. A half-cell bias makes the requested
	// integer divider stable in the face of floating-point rounding.
	owner.Ratio = (float64(line-origin) + 0.5) / float64(available)
	return true
}

func (n *Node) pathToPane(paneID string) []*Node {
	var path []*Node
	var visit func(*Node) bool
	visit = func(current *Node) bool {
		if current == nil {
			return false
		}
		path = append(path, current)
		if current.IsLeaf() {
			if current.PaneID == paneID {
				return true
			}
			path = path[:len(path)-1]
			return false
		}
		if visit(current.First) || visit(current.Second) {
			return true
		}
		path = path[:len(path)-1]
		return false
	}
	if !visit(n) {
		return nil
	}
	return path
}

func resizeOwner(path []*Node, edge ResizeEdge) (*Node, int, bool) {
	for index := len(path) - 2; index >= 0; index-- {
		parent := path[index]
		child := path[index+1]
		if parent.Axis != edge.axis() {
			continue
		}
		if edge.far() == (child == parent.First) {
			return parent, index, true
		}
	}
	return nil, 0, false
}

func minExtent(node *Node, axis Axis) int {
	if node == nil {
		return 0
	}
	if node.IsLeaf() {
		if axis == Columns {
			return MinPaneWidth
		}
		return MinPaneHeight
	}
	first := minExtent(node.First, axis)
	second := minExtent(node.Second, axis)
	if node.Axis == axis {
		return first + Gap + second
	}
	return max(first, second)
}
