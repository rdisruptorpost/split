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
