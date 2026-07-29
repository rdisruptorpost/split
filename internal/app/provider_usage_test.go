package app

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"split/internal/layout"
	"split/internal/state"
)

func TestStatusRightRendersWeeklyUsageInCompactFormat(t *testing.T) {
	now := time.Date(2026, time.July, 30, 20, 14, 0, 0, time.Local)
	model := newModel(t.TempDir())
	model.tabs = []*tab{{
		id:    "project-1",
		title: "project",
		root: &layout.Node{
			Axis:   layout.Columns,
			Ratio:  0.5,
			First:  layout.Leaf("pane-1"),
			Second: layout.Leaf("pane-2"),
		},
		activePane: "pane-1",
	}}
	model.providerUsage = map[string]state.ProviderUsage{
		"codex": {
			Provider: "codex", UsedPercent: 28,
			UpdatedAt: now, ResetsAt: now.Add(4 * 24 * time.Hour),
		},
		"claude": {
			Provider: "claude", UsedPercent: 56,
			UpdatedAt: now, ResetsAt: now.Add(5 * 24 * time.Hour),
		},
	}

	got := ansi.Strip(model.renderStatusRight(now))
	want := " Cdx 72% left  Cl 44% left "
	if got != want {
		t.Fatalf("footer right = %q, want %q", got, want)
	}
}

func TestExpiredProviderUsageIsUnavailable(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	model := newModel(t.TempDir())
	model.providerUsage["codex"] = state.ProviderUsage{
		Provider: "codex", UsedPercent: 28,
		UpdatedAt: now.Add(-time.Hour), ResetsAt: now.Add(-time.Second),
	}
	if _, available := model.providerRemaining("codex", now); available {
		t.Fatal("an expired weekly window should not remain visible")
	}
}
