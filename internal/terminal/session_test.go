package terminal

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

func TestTerminalEnvironmentAppliesPerCommandOverrides(t *testing.T) {
	environment := terminalEnvironment([]string{
		"Path=C:\\Windows",
		"TERM=old",
		"SPLIT_PANE_ID=old-pane",
	}, map[string]string{
		"SPLIT_PANE_ID":  "pane-42",
		"SPLIT_PROVIDER": "codex",
	})

	values := make(map[string]string)
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[strings.ToUpper(key)] = value
		}
	}
	if values["TERM"] != "xterm-256color" || values["TERM_PROGRAM"] != "split" {
		t.Fatalf("terminal identity was not applied: %#v", values)
	}
	if values["SPLIT_PANE_ID"] != "pane-42" || values["SPLIT_PROVIDER"] != "codex" {
		t.Fatalf("per-command overrides were not applied: %#v", values)
	}
}

func TestSessionCursorWaitsForChunkedRepaintToSettle(t *testing.T) {
	emulator := vt.NewEmulator(40, 10)
	defer emulator.Close()
	if _, err := emulator.WriteString("\x1b[6;11H"); err != nil {
		t.Fatal(err)
	}
	position := emulator.CursorPosition()
	events := make(chan Event, 4)
	session := &Session{
		id:              "cursor-test",
		emulator:        emulator,
		events:          events,
		cursorShown:     true,
		cursorBlink:     true,
		stableCursorX:   position.X,
		stableCursorY:   position.Y,
		stableCursorSet: true,
		closed:          make(chan struct{}),
	}
	defer func() {
		session.mu.Lock()
		session.cancelCursorSettleLocked()
		session.mu.Unlock()
	}()

	writeChunk := func(value string) {
		session.mu.Lock()
		defer session.mu.Unlock()
		session.ensureStableCursorLocked()
		if _, err := session.emulator.WriteString(value); err != nil {
			t.Fatal(err)
		}
		session.scheduleCursorSettleLocked()
	}

	// Codex begins a regional repaint by moving to a temporary drawing row.
	writeChunk("\x1b[2;3H")
	if cursor := session.Cursor(); cursor.X != 10 || cursor.Y != 5 {
		t.Fatalf("temporary repaint leaked cursor position: %#v", cursor)
	}

	// A later ConPTY read restores the real input cursor. It should move once,
	// after output has been quiet, rather than visiting the temporary row.
	writeChunk("\x1b[8;17H")
	if cursor := session.Cursor(); cursor.X != 10 || cursor.Y != 5 {
		t.Fatalf("cursor moved before repaint settled: %#v", cursor)
	}
	select {
	case event := <-events:
		if event.SessionID != session.id || event.Kind != Updated {
			t.Fatalf("settled cursor event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("cursor did not publish its settled position")
	}
	if cursor := session.Cursor(); cursor.X != 16 || cursor.Y != 7 {
		t.Fatalf("settled cursor = %#v, want 16,7", cursor)
	}
}

func TestSessionScrollbackViewport(t *testing.T) {
	emulator := vt.NewEmulator(16, 3)
	emulator.SetScrollbackSize(50)
	defer emulator.Close()
	session := &Session{
		emulator:    emulator,
		cursorShown: true,
		cursorBlink: true,
		mouseModes:  make(map[int]struct{}),
	}

	if _, err := emulator.WriteString("one\r\ntwo\r\nthree\r\nfour\r\nfive"); err != nil {
		t.Fatal(err)
	}
	if got := emulator.ScrollbackLen(); got < 2 {
		t.Fatalf("expected terminal history, got %d lines", got)
	}
	if live := ansi.Strip(session.Render()); !strings.Contains(live, "five") {
		t.Fatalf("live viewport is missing newest output: %q", live)
	}

	if !session.HandleWheel(4, 1, tea.MouseWheelUp, 0, 3) {
		t.Fatal("wheel up should move into terminal history")
	}
	if session.ScrollOffset() == 0 {
		t.Fatal("scrollback offset should be non-zero after wheel up")
	}
	historical := ansi.Strip(session.Render())
	if !strings.Contains(historical, "one") || strings.Contains(historical, "five") {
		t.Fatalf("historical viewport is incorrect: %q", historical)
	}
	if session.Cursor().Visible {
		t.Fatal("live cursor should be hidden while viewing scrollback")
	}

	if !session.HandleWheel(4, 1, tea.MouseWheelDown, 0, -100) {
		t.Fatal("wheel down should return toward live output")
	}
	if got := session.ScrollOffset(); got != 0 {
		t.Fatalf("scrollback offset = %d, want live bottom", got)
	}
	if live := ansi.Strip(session.Render()); !strings.Contains(live, "five") {
		t.Fatalf("wheel down did not restore live output: %q", live)
	}
	if !session.Cursor().Visible {
		t.Fatal("cursor should return at the live bottom")
	}
}

func TestSessionRapidScrollClampsAcrossLongHistory(t *testing.T) {
	const historyLimit = 5_000
	emulator := vt.NewEmulator(32, 4)
	emulator.SetScrollbackSize(historyLimit)
	defer emulator.Close()
	session := &Session{
		emulator:    emulator,
		cursorShown: true,
		mouseModes:  make(map[int]struct{}),
	}

	if _, err := emulator.WriteString(strings.Repeat("history row\r\n", historyLimit+200)); err != nil {
		t.Fatal(err)
	}
	if got := emulator.ScrollbackLen(); got != historyLimit {
		t.Fatalf("scrollback length = %d, want capped length %d", got, historyLimit)
	}

	for range 2_000 {
		session.HandleWheel(2, 2, tea.MouseWheelUp, 0, 3)
		_ = session.Render()
	}
	if got := session.ScrollOffset(); got != historyLimit {
		t.Fatalf("scroll-up offset = %d, want %d", got, historyLimit)
	}

	for range 2_000 {
		session.HandleWheel(2, 2, tea.MouseWheelDown, 0, -3)
		_ = session.Render()
	}
	if got := session.ScrollOffset(); got != 0 {
		t.Fatalf("scroll-down offset = %d, want live bottom", got)
	}
}

func TestSessionSelectionRendersAndExtractsText(t *testing.T) {
	emulator := vt.NewEmulator(16, 3)
	defer emulator.Close()
	session := &Session{
		emulator:    emulator,
		cursorShown: true,
		cursorBlink: true,
		mouseModes:  make(map[int]struct{}),
	}

	if _, err := emulator.WriteString("alpha beta\r\nsecond line"); err != nil {
		t.Fatal(err)
	}
	live := session.Render()
	if !session.Cursor().Visible {
		t.Fatal("cursor should begin visible")
	}
	if !session.BeginSelection(5, 1) || !session.UpdateSelection(6, 0) || !session.EndSelection(6, 0) {
		t.Fatal("reverse drag should create a visible selection")
	}
	if got, ok := session.SelectedText(); !ok || got != "beta\nsecond" {
		t.Fatalf("selected text = %q, %v; want %q, true", got, ok, "beta\nsecond")
	}
	selected := session.Render()
	plainSelected := ansi.Strip(selected)
	if !strings.Contains(plainSelected, "alpha beta") || !strings.Contains(plainSelected, "second line") {
		t.Fatalf("selection styling lost terminal text: %q", plainSelected)
	}
	if selected == live {
		t.Fatal("visible selection should add highlight styling")
	}
	if session.Cursor().Visible {
		t.Fatal("terminal cursor should be hidden while text is selected")
	}

	session.ClearSelection()
	if session.HasSelection() {
		t.Fatal("ClearSelection should remove the selection")
	}
	if !session.Cursor().Visible {
		t.Fatal("cursor should return after clearing the selection")
	}
	if !session.BeginSelection(2, 0) || session.EndSelection(2, 0) || session.HasSelection() {
		t.Fatal("a click without a drag should not leave a selection")
	}
}

func TestSessionSelectionSnapsWideCharacterContinuation(t *testing.T) {
	emulator := vt.NewEmulator(8, 2)
	defer emulator.Close()
	session := &Session{emulator: emulator, mouseModes: make(map[int]struct{})}

	if _, err := emulator.WriteString("A\u754cB"); err != nil {
		t.Fatal(err)
	}
	// Column 2 is the continuation cell for the two-column glyph at column 1.
	if !session.BeginSelection(2, 0) || !session.UpdateSelection(3, 0) || !session.EndSelection(3, 0) {
		t.Fatal("drag from a wide-character continuation should remain selectable")
	}
	if got, ok := session.SelectedText(); !ok || got != "\u754cB" {
		t.Fatalf("wide-character selected text = %q, %v; want %q, true", got, ok, "\u754cB")
	}
}
func TestSessionSelectionUsesScrolledHistoryCoordinates(t *testing.T) {
	emulator := vt.NewEmulator(12, 3)
	emulator.SetScrollbackSize(50)
	defer emulator.Close()
	session := &Session{
		emulator:    emulator,
		cursorShown: true,
		mouseModes:  make(map[int]struct{}),
	}

	if _, err := emulator.WriteString("one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix"); err != nil {
		t.Fatal(err)
	}
	if !session.HandleWheel(0, 0, tea.MouseWheelUp, 0, 100) {
		t.Fatal("expected to scroll into history")
	}
	if !strings.HasPrefix(ansi.Strip(session.Render()), "one") {
		t.Fatalf("top historical viewport is unexpected: %q", ansi.Strip(session.Render()))
	}
	if !session.BeginSelection(0, 0) || !session.UpdateSelection(2, 0) || !session.EndSelection(2, 0) {
		t.Fatal("historical row should be selectable")
	}
	if got, ok := session.SelectedText(); !ok || got != "one" {
		t.Fatalf("historical selected text = %q, %v; want one, true", got, ok)
	}
}
func TestSessionForwardsPrintableShiftAndLockState(t *testing.T) {
	tests := []struct {
		name string
		key  tea.Key
		want string
	}{
		{
			name: "shifted text",
			key:  tea.Key{Text: "P", Code: 'p', ShiftedCode: 'P', Mod: tea.ModShift},
			want: "P",
		},
		{
			name: "shifted code fallback",
			key:  tea.Key{Code: 'p', ShiftedCode: 'P', Mod: tea.ModShift},
			want: "P",
		},
		{
			name: "shifted punctuation",
			key:  tea.Key{Text: "!", Code: '1', ShiftedCode: '!', Mod: tea.ModShift},
			want: "!",
		},
		{
			name: "caps lock text",
			key:  tea.Key{Text: "P", Code: 'p', Mod: tea.ModCapsLock},
			want: "P",
		},
		{
			name: "alt printable",
			key:  tea.Key{Text: "p", Code: 'p', Mod: tea.ModAlt},
			want: "\x1bp",
		},
		{
			name: "alt gr printable",
			key:  tea.Key{Text: "@", Code: 'q', Mod: tea.ModCtrl | tea.ModAlt},
			want: "@",
		},
		{
			name: "unicode text",
			key:  tea.Key{Text: "É", Code: 'é', ShiftedCode: 'É', Mod: tea.ModShift},
			want: "É",
		},
		{
			name: "control key",
			key:  tea.Key{Code: 'c', Mod: tea.ModCtrl},
			want: "\x03",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			emulator := vt.NewEmulator(8, 2)
			defer emulator.Close()
			session := &Session{emulator: emulator, mouseModes: make(map[int]struct{})}

			type result struct {
				value string
				err   error
			}
			read := make(chan result, 1)
			go func() {
				buffer := make([]byte, len(test.want))
				n, err := emulator.Read(buffer)
				read <- result{value: string(buffer[:n]), err: err}
			}()
			session.SendKey(tea.KeyPressMsg(test.key))

			select {
			case received := <-read:
				if received.err != nil {
					t.Fatal(received.err)
				}
				if received.value != test.want {
					t.Fatalf("forwarded bytes = %q, want %q", received.value, test.want)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for forwarded key bytes")
			}
		})
	}
}
