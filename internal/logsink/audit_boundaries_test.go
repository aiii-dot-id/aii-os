package logsink

import (
	"bytes"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func TestAuditLegacyAlertSurvivesConsoleThreshold(t *testing.T) {
	file, console := installForTest(t)
	SetLevels(slog.LevelError, nil)
	// .
	// .
	log.Printf("SAFE MODE: entering — synthetic audit reason; operator intervention required.")
	if !strings.Contains(file.String(), "synthetic audit reason") {
		t.Fatal("fixture: file lost the alert")
	}
	if !strings.Contains(console.String(), "synthetic audit reason") {
		t.Fatalf("legacy SAFE alert filtered at error: console=%q", console.String())
	}
}

func TestAuditWarningFloorSurvivesConsoleThreshold(t *testing.T) {
	file, console := installForTest(t)
	SetLevels(slog.LevelError, nil)
	Warn("logs", "synthetic warning")
	if !strings.Contains(file.String(), "synthetic warning") {
		t.Fatal("fixture: file lost warning")
	}
	if !strings.Contains(console.String(), "synthetic warning") {
		t.Fatalf("warning floor filtered at error: console=%q", console.String())
	}
}

// .
type auditRedacted string

func (auditRedacted) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

func TestAuditLogValuerRedactionIsHonored(t *testing.T) {
	var file bytes.Buffer
	logger := slog.New(newHandler(&file, nil))
	logger.Info("synthetic credential", "token", auditRedacted("audit-only-marker-314"))
	got := file.String()
	if strings.Contains(got, "audit-only-marker-314") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redaction contract bypassed: %q", got)
	}
}

func TestAuditGroupOrderKeepsAttributeMeaning(t *testing.T) {
	var file bytes.Buffer
	logger := slog.New(newHandler(&file, nil)).With("id", "root").WithGroup("request").With("id", "child")
	logger.Info("names", "id", "leaf")
	got := file.String()
	if !strings.Contains(got, " id=root request.id=child request.id=leaf") {
		t.Fatalf("group provenance flattened: %q", got)
	}
}

func TestAuditTypedInfoRemainsFilteredAndRetained(t *testing.T) {
	file, console := installForTest(t)
	SetLevels(slog.LevelError, nil)
	Info("voice", "synthetic routine event")
	if !strings.Contains(file.String(), "synthetic routine event") || console.Len() != 0 {
		t.Fatalf("file=%q console=%q", file.String(), console.String())
	}
}
