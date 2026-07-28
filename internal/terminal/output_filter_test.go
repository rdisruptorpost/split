package terminal

import (
	"bytes"
	"testing"
)

func TestOutputFilterRemovesChunkedTitleSequences(t *testing.T) {
	var filter outputFilter
	var output []byte
	for _, chunk := range [][]byte{
		[]byte("before\x1b]0;✳ Cl"),
		[]byte("aude Code"),
		[]byte("\x07after\x1b]2;agent"),
		[]byte(" title\x1b\\done"),
	} {
		output = append(output, filter.Filter(chunk)...)
	}
	if got, want := string(output), "beforeafterdone"; got != want {
		t.Fatalf("unexpected filtered output: got %q want %q", got, want)
	}
}

func TestOutputFilterPreservesOtherEscapeAndOSCSequences(t *testing.T) {
	var filter outputFilter
	input := []byte("▝ UTF-8 \x1b[31mred\x1b[0m\x1b]8;;https://example.com\x07link\x1b]8;;\x07")
	var output []byte
	for _, chunk := range [][]byte{input[:5], input[5:17], input[17:]} {
		output = append(output, filter.Filter(chunk)...)
	}
	if !bytes.Equal(output, input) {
		t.Fatalf("non-title escapes changed:\n got %q\nwant %q", output, input)
	}
}
