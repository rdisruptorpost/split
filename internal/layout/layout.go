package layout

import "math"

const (
	Gap           = 1
	MinPaneWidth  = 12
	MinPaneHeight = 5
)

type Axis uint8

const (
	Columns Axis = iota
	Rows
)

type Direction uint8

const (
	Left Direction = iota
	Right
	Up
	Down
)

type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

func (r Rect) center() (int, int) {
	return r.X + r.Width/2, r.Y + r.Height/2
}

type Node struct {
	PaneID string
	Axis   Axis
	Ratio  float64
	First  *Node
	Second *Node
}

func Leaf(paneID string) *Node {
	return &Node{PaneID: paneID}
}

func (n *Node) IsLeaf() bool {
	return n != nil && n.PaneID != ""
}

func (n *Node) Split(paneID, newPaneID string, axis Axis) bool {
	if n == nil {
		return false
	}
	if n.IsLeaf() {
		if n.PaneID != paneID {
			return false
		}
		oldPaneID := n.PaneID
		*n = Node{
			Axis:   axis,
			Ratio:  0.5,
			First:  Leaf(oldPaneID),
			Second: Leaf(newPaneID),
		}
		return true
	}
	return n.First.Split(paneID, newPaneID, axis) ||
		n.Second.Split(paneID, newPaneID, axis)
}

func (n *Node) Leaves() []string {
	if n == nil {
		return nil
	}
	if n.IsLeaf() {
		return []string{n.PaneID}
	}
	leaves := n.First.Leaves()
	return append(leaves, n.Second.Leaves()...)
}

func (n *Node) ContainsPane(paneID string) bool {
	if n == nil {
		return false
	}
	if n.IsLeaf() {
		return n.PaneID == paneID
	}
	return n.First.ContainsPane(paneID) || n.Second.ContainsPane(paneID)
}

func Swap(n *Node, firstPaneID, secondPaneID string) bool {
	if n == nil || firstPaneID == secondPaneID {
		return false
	}
	var first, second *Node
	var visit func(*Node)
	visit = func(current *Node) {
		if current == nil || (first != nil && second != nil) {
			return
		}
		if current.IsLeaf() {
			switch current.PaneID {
			case firstPaneID:
				first = current
			case secondPaneID:
				second = current
			}
			return
		}
		visit(current.First)
		visit(current.Second)
	}
	visit(n)
	if first == nil || second == nil {
		return false
	}
	first.PaneID, second.PaneID = second.PaneID, first.PaneID
	return true
}

func Equalize(n *Node) int {
	if n == nil {
		return 0
	}
	if n.IsLeaf() {
		return 1
	}
	firstLeaves := Equalize(n.First)
	secondLeaves := Equalize(n.Second)
	total := firstLeaves + secondLeaves
	if total > 0 {
		n.Ratio = float64(firstLeaves) / float64(total)
	}
	return total
}
func Remove(n *Node, paneID string) (*Node, bool) {
	if n == nil {
		return nil, false
	}
	if n.IsLeaf() {
		if n.PaneID == paneID {
			return nil, true
		}
		return n, false
	}

	first, removed := Remove(n.First, paneID)
	if removed {
		if first == nil {
			return n.Second, true
		}
		n.First = first
		return n, true
	}

	second, removed := Remove(n.Second, paneID)
	if removed {
		if second == nil {
			return n.First, true
		}
		n.Second = second
		return n, true
	}
	return n, false
}

func SplitSizes(axis Axis, width, height int, ratio float64) (first, second int) {
	total := width
	minimum := MinPaneWidth
	if axis == Rows {
		total = height
		minimum = MinPaneHeight
	}

	available := max(0, total-Gap)
	if available == 0 {
		return 0, 0
	}
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}

	first = int(math.Floor(float64(available) * ratio))
	if available >= minimum*2 {
		first = max(minimum, min(first, available-minimum))
	} else {
		first = available / 2
	}
	return first, available - first
}

func (n *Node) Rects(area Rect) map[string]Rect {
	rects := make(map[string]Rect)
	n.fillRects(area, rects)
	return rects
}

func (n *Node) fillRects(area Rect, rects map[string]Rect) {
	if n == nil || area.Width <= 0 || area.Height <= 0 {
		return
	}
	if n.IsLeaf() {
		rects[n.PaneID] = area
		return
	}

	first, second := SplitSizes(n.Axis, area.Width, area.Height, n.Ratio)
	if n.Axis == Columns {
		n.First.fillRects(Rect{
			X: area.X, Y: area.Y, Width: first, Height: area.Height,
		}, rects)
		n.Second.fillRects(Rect{
			X: area.X + first + Gap, Y: area.Y, Width: second, Height: area.Height,
		}, rects)
		return
	}

	n.First.fillRects(Rect{
		X: area.X, Y: area.Y, Width: area.Width, Height: first,
	}, rects)
	n.Second.fillRects(Rect{
		X: area.X, Y: area.Y + first + Gap, Width: area.Width, Height: second,
	}, rects)
}

func Neighbor(n *Node, paneID string, direction Direction, area Rect) string {
	rects := n.Rects(area)
	current, ok := rects[paneID]
	if !ok {
		return ""
	}

	cx, cy := current.center()
	bestID := ""
	bestScore := math.MaxInt
	for candidateID, candidate := range rects {
		if candidateID == paneID {
			continue
		}
		x, y := candidate.center()

		var primary, secondary int
		switch direction {
		case Left:
			if x >= cx {
				continue
			}
			primary, secondary = cx-x, abs(cy-y)
		case Right:
			if x <= cx {
				continue
			}
			primary, secondary = x-cx, abs(cy-y)
		case Up:
			if y >= cy {
				continue
			}
			primary, secondary = cy-y, abs(cx-x)
		case Down:
			if y <= cy {
				continue
			}
			primary, secondary = y-cy, abs(cx-x)
		}

		score := secondary*10_000 + primary
		if score < bestScore {
			bestID, bestScore = candidateID, score
		}
	}
	return bestID
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
