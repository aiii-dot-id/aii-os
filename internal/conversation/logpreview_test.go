package conversation

import (
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .

func TestALogLineIsNotACopyOfTheContent(t *testing.T) {
	secret := strings.Repeat("private thought ", 500)
	got := logPreview(secret)

	if len(([]rune(got))) > logsink.PreviewRunes+64 {
		t.Fatalf("the log line carries %d runes of content", len([]rune(got)))
	}
	if !strings.Contains(got, "8000 runes total") {
		t.Fatalf("the preview does not say how much it dropped: %q", got)
	}
}

// .
// .
func TestShortContentIsUnchanged(t *testing.T) {
	const short = `{"path":"internal/store/schema.sql"}`
	if got := logPreview(short); got != short {
		t.Fatalf("a short argument was altered: %q", got)
	}
}

// .
// .
func TestTheCutRespectsRuneBoundaries(t *testing.T) {
	got := logPreview(strings.Repeat("café — ", 200))
	if !strings.Contains(got, "runes total") {
		t.Fatalf("expected a trimmed preview: %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("the preview split a multi-byte rune: %q", got)
	}
}

// .
// .
// .
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
