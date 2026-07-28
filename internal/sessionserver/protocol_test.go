package sessionserver

import (
	"encoding/json"
	"image/color"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInputRequestJSONRoundTrip(t *testing.T) {
	originalKey := tea.Key{
		Text: "X", Mod: tea.ModCtrl | tea.ModShift, Code: 'x',
		ShiftedCode: 'X', BaseCode: 'x', IsRepeat: true,
	}
	original, ok := requestForMessage(tea.KeyPressMsg(originalKey))
	if !ok {
		t.Fatal("key message was not accepted")
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded request
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	message, err := decoded.message()
	if err != nil {
		t.Fatal(err)
	}
	keyMessage, ok := message.(tea.KeyPressMsg)
	if !ok || !reflect.DeepEqual(tea.Key(keyMessage), originalKey) {
		t.Fatalf("key changed across protocol: %#v", message)
	}
}

func TestViewFrameRoundTripPreservesCursorAndModes(t *testing.T) {
	original := tea.NewView("\x1b[31mSplit\x1b[0m")
	original.BackgroundColor = color.RGBA{R: 12, G: 13, B: 14, A: 255}
	original.ForegroundColor = color.RGBA{R: 220, G: 221, B: 222, A: 255}
	original.WindowTitle = "Split test"
	original.AltScreen = true
	original.ReportFocus = true
	original.MouseMode = tea.MouseModeAllMotion
	original.DisableBracketedPasteMode = true
	original.Cursor = tea.NewCursor(17, 8)
	original.Cursor.Color = color.RGBA{R: 240, G: 180, B: 90, A: 255}
	original.Cursor.Shape = tea.CursorBar
	original.Cursor.Blink = false

	restored := frameFromView(original).view()
	if restored.Content != original.Content || restored.WindowTitle != original.WindowTitle ||
		restored.AltScreen != original.AltScreen || restored.ReportFocus != original.ReportFocus ||
		restored.MouseMode != original.MouseMode || restored.DisableBracketedPasteMode != original.DisableBracketedPasteMode {
		t.Fatalf("view metadata changed across protocol: %#v", restored)
	}
	if restored.Cursor == nil || restored.Cursor.X != 17 || restored.Cursor.Y != 8 ||
		restored.Cursor.Shape != tea.CursorBar || restored.Cursor.Blink {
		t.Fatalf("cursor changed across protocol: %#v", restored.Cursor)
	}
	if !reflect.DeepEqual(restored.BackgroundColor, original.BackgroundColor) ||
		!reflect.DeepEqual(restored.ForegroundColor, original.ForegroundColor) ||
		!reflect.DeepEqual(restored.Cursor.Color, original.Cursor.Color) {
		t.Fatal("view colors changed across protocol")
	}
}
