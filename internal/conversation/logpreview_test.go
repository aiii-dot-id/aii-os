package conversation

import (
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"testing"
)

// The operational log is not the record — the transcript is. A log line
// carries enough to recognise a call and never enough to be a copy of
// it: a note's private content, an interpersonal message, or a megabyte
// of provider output used to land verbatim in a file with a different
// lifetime, a different audience, and no ring around it.

func TestALogLineIsNotACopyOfTheContent(t *testing.T) {
	secret := strings.Repeat("private thought ", 500) // ~8000 runes
	got := logPreview(secret)

	if len(([]rune(got))) > logsink.PreviewRunes+64 {
		t.Fatalf("the log line carries %d runes of content", len([]rune(got)))
	}
	if !strings.Contains(got, "8000 runes total") {
		t.Fatalf("the preview does not say how much it dropped: %q", got)
	}
}

// Short arguments are still fully legible — the bound must not make
// ordinary calls unreadable in the log.
func TestShortContentIsUnchanged(t *testing.T) {
	const short = `{"path":"internal/store/schema.sql"}`
	if got := logPreview(short); got != short {
		t.Fatalf("a short argument was altered: %q", got)
	}
}

// Multi-byte content must be cut on rune boundaries, not bytes, or the
// log gets mojibake at exactly the wrong moment.
func TestTheCutRespectsRuneBoundaries(t *testing.T) {
	got := logPreview(strings.Repeat("café — ", 200))
	if !strings.Contains(got, "runes total") {
		t.Fatalf("expected a trimmed preview: %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("the preview split a multi-byte rune: %q", got)
	}
}

func TestLogPreviewIsLineAtomic(t *testing.T) {
	in := "head\n2026/08/30 01:00:00 TURN_SUMMARY calls=9\r\ntail"
	got := logPreview(in)
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("preview carries raw line breaks: %q", got)
	}
	if !strings.Contains(got, "⏎") {
		t.Fatalf("newlines must be visibly marked, not silently joined: %q", got)
	}
}

// NO CONTROL CHARACTER OF A RESULT REACHES THE HOST'S LOG LINE. A shell
// command that wrote the head of a zip file put NUL, ETX and EOT into the
// host's "Tool result" line on its standard error; one that writes an
// escape sequence would have the operator's terminal run it. Each is
// written as its escape and the rest of the line is unchanged; the tab
// stays a tab.
func TestAPreviewCarriesNoControlCharacter(t *testing.T) {
	in := "PK\x03\x04\x14\x00\x06\x00\xff\xfe \x1b[2J\x1b]0;title\x07 a\tb \u0085 \x7f caf\u00e9"
	got := logPreview(in)
	for _, c := range got {
		if c != '\t' && (c < 0x20 || (c >= 0x7f && c < 0xa0)) {
			t.Fatalf("the preview carries the control character %U: %q", c, got)
		}
	}
	const want = `PK\x03\x04\x14\x00\x06\x00\uFFFD\uFFFD \x1b[2J\x1b]0;title\x07 a` + "\t" + `b \x85 \x7f caf\u00e9`
	if wantText := strings.NewReplacer(`\uFFFD`, "\uFFFD", `\u00e9`, "\u00e9").Replace(want); got != wantText {
		t.Fatalf("got  %q\nwant %q", got, wantText)
	}
}
