package logsink

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// .
// .
func auditConsoleProcess(t *testing.T, mode string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuditConsoleOnlyWorker$", "-test.count=1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "AII_AUDIT_CONSOLE_MODE="+mode, EnvDirective+"=")
	if mode == "env" {
		cmd.Env = append(cmd.Env, EnvDirective+"=error,audit=debug")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated logger process: %v\n%s", err, out)
	}
	return string(out)
}

func TestAuditConsoleOnlyLiveLevels(t *testing.T) {
	out := auditConsoleProcess(t, "levels")
	if !strings.Contains(out, "AUDIT_DEBUG_VISIBLE") {
		t.Errorf("live debug threshold did not reach console: %s", out)
	}
	if strings.Contains(out, "AUDIT_INFO_HIDDEN") {
		t.Errorf("live error threshold did not suppress info: %s", out)
	}
	if !strings.Contains(out, "AUDIT_ERROR_VISIBLE") {
		t.Errorf("error missing: %s", out)
	}
}

func TestAuditConsoleOnlyEnvironmentLevels(t *testing.T) {
	out := auditConsoleProcess(t, "env")
	if !strings.Contains(out, "AUDIT_ENV_DEBUG_VISIBLE") || strings.Contains(out, "AUDIT_ENV_INFO_HIDDEN") {
		t.Fatalf("console-only install did not honor environment category/default: %s", out)
	}
}

func TestAuditConsoleOnlyFinalDigest(t *testing.T) {
	out := auditConsoleProcess(t, "digest")
	if !strings.Contains(out, "rhythm 6 AUDIT_PASSES") {
		t.Fatalf("actual install/close lifecycle lost ticks: %s", out)
	}
}

func TestAuditConsoleOnlyWorker(t *testing.T) {
	mode := os.Getenv("AII_AUDIT_CONSOLE_MODE")
	if mode == "" {
		return
	}
	// .
	if err := os.WriteFile(LiveName, []byte("PRIVATE_FIXTURE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Install(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if s != nil {
		defer s.Close()
		if s.Dir() != "" {
			t.Fatalf("console-only sink has directory %q", s.Dir())
		}
		files, err := s.List()
		if err != nil || len(files) != 0 {
			t.Fatalf("console-only viewer listed files: %v %v", files, err)
		}
		if _, err := s.Tail(LiveName, 1); err == nil {
			t.Fatal("console-only viewer read an unrelated file")
		}
	}
	switch mode {
	case "levels":
		SetLevels(slog.LevelDebug, nil)
		Debug("audit", "AUDIT_DEBUG_VISIBLE")
		SetLevels(slog.LevelError, nil)
		Info("audit", "AUDIT_INFO_HIDDEN")
		Error("audit", "AUDIT_ERROR_VISIBLE")
	case "env":
		Debug("audit", "AUDIT_ENV_DEBUG_VISIBLE")
		Info("other", "AUDIT_ENV_INFO_HIDDEN")
	case "digest":
		SetLevels(slog.LevelInfo, nil)
		for i := 0; i < 6; i++ {
			Tick("rhythm", "AUDIT_PASSES", 0)
		}
	default:
		t.Fatalf("unknown worker mode %q", mode)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(LiveName) {
		t.Fatalf("unexpected persistence: %v", entries)
	}
}
