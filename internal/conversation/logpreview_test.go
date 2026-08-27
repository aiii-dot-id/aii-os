package conversation

import (
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

	if len(([]rune(got))) > logPreviewRunes+64 {
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
