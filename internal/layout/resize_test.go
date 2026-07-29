package layout

import "testing"

func TestResizeSplitMovesNearestOwningDividers(t *testing.T) {
	root := Leaf("left")
	root.Split("left", "top", Columns)
	root.Second.Split("top", "bottom", Rows)
	area := Rect{X: 10, Y: 3, Width: 100, Height: 40}

	if !root.CanResize("bottom", ResizeLeft) || !root.CanResize("bottom", ResizeTop) {
		t.Fatal("bottom pane should own left and top split dividers")
	}
	if root.CanResize("bottom", ResizeRight) || root.CanResize("bottom", ResizeBottom) {
		t.Fatal("outer pane edges must not report a divider")
	}
	if !root.ResizeSplit("bottom", ResizeLeft, 70, area) {
		t.Fatal("left edge did not resize its owning column split")
	}
	if !root.ResizeSplit("bottom", ResizeTop, 25, area) {
		t.Fatal("top edge did not resize its owning row split")
	}

	rects := root.Rects(area)
	if got := rects["left"]; got != (Rect{X: 10, Y: 3, Width: 59, Height: 40}) {
		t.Fatalf("unrelated left pane geometry = %#v", got)
	}
	if got := rects["top"]; got != (Rect{X: 70, Y: 3, Width: 40, Height: 21}) {
		t.Fatalf("top pane geometry = %#v", got)
	}
	if got := rects["bottom"]; got != (Rect{X: 70, Y: 25, Width: 40, Height: 18}) {
		t.Fatalf("bottom pane geometry = %#v", got)
	}
	if root.ResizeSplit("left", ResizeLeft, 20, area) {
		t.Fatal("resizing an outside edge should not invent a divider")
	}
}

func TestResizeSplitClampsForEveryLeafInNeighborSubtree(t *testing.T) {
	root := Leaf("left")
	root.Split("left", "right-one", Columns)
	root.Second.Split("right-one", "right-two", Columns)
	area := Rect{Width: 100, Height: 30}

	if !root.ResizeSplit("right-one", ResizeLeft, 95, area) {
		t.Fatal("expected the ancestor column divider to resize")
	}
	rects := root.Rects(area)
	if got := rects["left"].Width; got != 74 {
		t.Fatalf("left width = %d, want 74", got)
	}
	for _, paneID := range []string{"right-one", "right-two"} {
		if got := rects[paneID].Width; got < MinPaneWidth {
			t.Fatalf("pane %s was squeezed below minimum: %d", paneID, got)
		}
	}
}
