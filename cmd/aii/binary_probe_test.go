package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/version"
)

// These tests exec this command as it ships. They pin its own main.go:
// the guard against a silent boot and the -version flag. They lived in
// internal/app and linked the command four times per run; here they
// share one build.

// probe is the one build of this command a test run makes.
var probe struct {
	once sync.Once
	dir  string
	bin  string
	out  []byte
	err  error
}

// probeBinary builds this command once per test run with go build —
// which stamps the VCS data the -version probe reports — and answers its
// path.
func probeBinary(t *testing.T) string {
	t.Helper()
	probe.once.Do(func() {
		if probe.dir, probe.err = os.MkdirTemp("", "aii-probe-"); probe.err != nil {
			return
		}
		probe.bin = filepath.Join(probe.dir, "aii")
		if runtime.GOOS == "windows" {
			// Windows will not execute a file without a recognised
			// extension, so the probe binary is unreachable without it.
			probe.bin += ".exe"
		}
		if probe.out, probe.err = exec.Command("go", "build", "-o", probe.bin, ".").CombinedOutput(); probe.err != nil {
			return
		}

		probe.out, probe.err = exec.Command(probe.bin, "-version").CombinedOutput()
	})
	if probe.err != nil {
		t.Fatalf("build: %v\n%s", probe.err, probe.out)
	}
	return probe.bin
}

func TestMain(m *testing.M) {
	code := m.Run()
	if probe.dir != "" {
		if err := os.RemoveAll(probe.dir); err != nil {
			fmt.Fprintf(os.Stderr, "remove the probe binary: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}

const positionalProbeDeadline = 10 * time.Second

func TestUnrecognizedPositionalIsLoudAndDoesNotBoot(t *testing.T) {
	bin := probeBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), positionalProbeDeadline)
	defer cancel()
	// The original specimen's shape: flag first, positional second.
	cmd := exec.CommandContext(ctx, bin, "-dir", t.TempDir(), "version")
	cmd.Dir = t.TempDir() // no config.json — the guard must exit before config regardless
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
	bin := probeBinary(t)
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

// The version surface keeps its contract: it answers from anywhere,
// even with a stray token alongside — the guard's scope is the silent
// BOOT path, and -version never boots.
func TestVersionFlagUnchangedByGuard(t *testing.T) {
	bin := probeBinary(t)
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

// Binary identity has to be ASKABLE, not just stated once into a log
// (internal/app/version_surface_test.go keeps the in-process half).
// The flag must answer without starting an identity, and from anywhere:
// a binary that needs a config directory to say what it is cannot be
// interrogated on the machine it was deployed to.
func TestVersionFlagAnswersWithoutAnIdentity(t *testing.T) {
	bin := probeBinary(t)
	cmd := exec.Command(bin, "-version")
	cmd.Dir = t.TempDir() // no config.json, no ledger, no identity home
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("-version exited %v; a binary must be able to say what it is: %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.HasPrefix(got, "AII OS v") {
		t.Fatalf("-version said %q", got)
	}
	if !strings.Contains(got, "build ") {
		t.Fatalf("-version omitted the build identity: %q", got)
	}
	// Either a real commit or the honest word. Never a fabricated hash.
	if !regexp.MustCompile(`build (unknown|[0-9a-f]{7,12})`).MatchString(got) {
		t.Fatalf("build identity is neither a commit nor \"unknown\": %q", got)
	}
}

// EVERY BUILD SAYS WHAT IT IS AT BOOT (T45). The banner used to wait for
// an -ldflags version stamp, so a plain build — this probe is one, and
// since T45 every build site is — booted without it, and the drills that
// look for it found nothing. The boot is stopped at its first step, the
// log destination, by naming one under a regular file: the banner comes
// before it, and nothing past it runs.
func TestEveryBuildPrintsTheVersionBannerAtBoot(t *testing.T) {
	bin := probeBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blocker"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "config.json"), []byte(`{"logs":{"dir":"blocker/sub"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), positionalProbeDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-dir", dir)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("the boot did not stop at its log destination within %v: %s", positionalProbeDeadline, out)
	}
	if err == nil || !strings.Contains(string(out), "Startup failed") {
		t.Fatalf("the boot was meant to stop at the log destination (%v): %s", err, out)
	}
	want := "AII OS v" + version.Authored() + " sub=boot.start"
	if !strings.Contains(string(out), want) {
		t.Fatalf("a plain build booted without its version banner %q:\n%s", want, out)
	}
}
