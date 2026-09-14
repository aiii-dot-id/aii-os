package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

const positionalProbeDeadline = 10 * time.Second

func buildAiiForPositional(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "aii")
	if runtime.GOOS == "windows" {
		// .
		// .
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "../../cmd/aii")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestUnrecognizedPositionalIsLoudAndDoesNotBoot(t *testing.T) {
	bin := buildAiiForPositional(t)
	ctx, cancel := context.WithTimeout(context.Background(), positionalProbeDeadline)
	defer cancel()
	// .
	cmd := exec.CommandContext(ctx, bin, "-dir", t.TempDir(), "version")
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("process hung %v without exiting — the silent-boot specimen itself, guard is gone: %s", positionalProbeDeadline, out)
	}
	if err == nil {
		t.Fatalf("unrecognized positional exited 0 — the silent-boot defect is back: %s", out)
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("want usage error (exit 2), got %v: %s", err, out)
	}
	combined := string(out)
	if !strings.Contains(combined, "unrecognized argument") {
		t.Fatalf("stderr must name the problem, got: %q", combined)
	}
	if !strings.Contains(combined, `"version"`) {
		t.Fatalf("stderr must name the offending token, got: %q", combined)
	}
	if strings.Contains(combined, "Config error") {
		t.Fatalf("rejection landed after config load — the guard is on the wrong side of the boot: %q", combined)
	}
}

func TestBareUnknownVerbIsLoudToo(t *testing.T) {
	bin := buildAiiForPositional(t)
	ctx, cancel := context.WithTimeout(context.Background(), positionalProbeDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "frobnicate")
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("process hung %v without exiting — silent boot on a bare unknown verb: %s", positionalProbeDeadline, out)
	}
	if err == nil {
		t.Fatalf("unknown verb exited 0: %s", out)
	}
	if !strings.Contains(string(out), `"frobnicate"`) {
		t.Fatalf("stderr must name the token: %q", out)
	}
}

// .
// .
// .
func TestVersionFlagUnchangedByGuard(t *testing.T) {
	bin := buildAiiForPositional(t)
	for _, args := range [][]string{{"-version"}, {"-version", "stray"}} {
		ctx, cancel := context.WithTimeout(context.Background(), positionalProbeDeadline)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = t.TempDir()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v exited %v: %s", args, err, out)
		}
		if !strings.HasPrefix(strings.TrimSpace(string(out)), "AII OS v") {
			t.Fatalf("%v said %q", args, out)
		}
	}
}
