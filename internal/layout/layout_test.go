package layout

import "testing"

func TestSplitAndRects(t *testing.T) {
	root := Leaf("one")
	if !root.Split("one", "two", Columns) {
		t.Fatal("split did not find the target pane")
	}
	if !root.Second.Split("two", "three", Rows) {
		t.Fatal("nested split did not find the target pane")
	}

	rects := root.Rects(Rect{Width: 100, Height: 30})
	if got := rects["one"]; got != (Rect{Width: 49, Height: 30}) {
		t.Fatalf("unexpected first rect: %#v", got)
	}
	if got := rects["two"]; got != (Rect{X: 50, Width: 50, Height: 14}) {
		t.Fatalf("unexpected upper-right rect: %#v", got)
	}
	if got := rects["three"]; got != (Rect{X: 50, Y: 15, Width: 50, Height: 15}) {
		t.Fatalf("unexpected lower-right rect: %#v", got)
	}
}

func TestRemoveCollapsesParent(t *testing.T) {
	root := Leaf("one")
	root.Split("one", "two", Columns)

	var removed bool
	root, removed = Remove(root, "one")
	if !removed {
		t.Fatal("expected pane to be removed")
	}
	if !root.IsLeaf() || root.PaneID != "two" {
		t.Fatalf("expected sibling to replace parent, got %#v", root)
	}
}

func TestNeighborUsesPaneGeometry(t *testing.T) {
	root := Leaf("left")
	root.Split("left", "right-top", Columns)
	root.Second.Split("right-top", "right-bottom", Rows)
	area := Rect{Width: 100, Height: 30}

	if got := Neighbor(root, "left", Right, area); got != "right-bottom" {
		t.Fatalf("expected the pane nearest the left pane's center, got %q", got)
	}
	if got := Neighbor(root, "right-top", Down, area); got != "right-bottom" {
		t.Fatalf("expected right-bottom, got %q", got)
	}
	if got := Neighbor(root, "right-bottom", Left, area); got != "left" {
		t.Fatalf("expected left, got %q", got)
	}
}

func TestSwapExchangesPanePositions(t *testing.T) {
	root := Leaf("left")
	root.Split("left", "right", Columns)

	if !Swap(root, "left", "right") {
		t.Fatal("expected panes to be swapped")
	}
	if root.First.PaneID != "right" || root.Second.PaneID != "left" {
		t.Fatalf("unexpected swapped tree: %#v", root)
	}
	if Swap(root, "left", "missing") {
		t.Fatal("swap should fail when a pane is missing")
	}
}

func TestEqualizeUsesLeafCounts(t *testing.T) {
	root := Leaf("one")
	root.Split("one", "two", Columns)
	root.Second.Split("two", "three", Columns)
	root.Second.Second.Split("three", "four", Columns)

	if got := Equalize(root); got != 4 {
		t.Fatalf("expected four leaves, got %d", got)
	}
	rects := root.Rects(Rect{Width: 163, Height: 30})
	for _, paneID := range []string{"one", "two", "three", "four"} {
		if got := rects[paneID].Width; got != 40 {
			t.Fatalf("pane %s: expected balanced width 40, got %d", paneID, got)
		}
	}
}
