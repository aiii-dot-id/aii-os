package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A SCOPE THAT PASSED WITH A RELEASE-CRITICAL TEST SKIPPED DID NOT PASS. The
// real runner, the real report and a list, in a module of three tests: one
// the list requires here, one that skips with a reason, one that passes.
// While the required test runs the scope passes and prints what skipped and
// why; when it skips, or is not there, the scope fails and names it.
func TestAScopeFailsWhenAReleaseCriticalTestDidNotRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host shell runner")
	}
	goBin := os.Getenv("AII_GO")
	if goBin == "" {
		var err error
		if goBin, err = exec.LookPath("go"); err != nil {
			t.Skip("no Go toolchain")
		}
	}
	root := t.TempDir()
	put := func(path string, content []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), content, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	copyIn := func(path, from string) {
		t.Helper()
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		put(path, b)
	}
	put("go.mod", []byte("module fixture\n\ngo 1.24\n"))
	copyIn("test/run_race_scope.sh", "run_race_scope.sh")
	copyIn("test/skipreport/main.go", "skipreport/main.go")
	put("test/release_critical.txt", []byte("internal/app TestNeeded linux darwin windows\n"))
	fixture := func(needed string) {
		put("internal/app/fixture_test.go", []byte(`package app
import "testing"
`+needed+`
func TestElsewhere(t *testing.T) { t.Skip("only where there is a second disk") }
func TestPlain(t *testing.T) {}
`))
	}
	run := func() (string, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "test/run_race_scope.sh", "app")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOWORK=off", "GOFLAGS=", "AII_GO="+goBin, "AII_TEST_SHARDS=2")
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("the runner timed out:\n%s", out)
		}
		return string(out), err
	}

	fixture(`func TestNeeded(t *testing.T) {}`)
	out, err := run()
	if err != nil {
		t.Fatalf("a scope whose required test ran was refused: %v\n%s", err, out)
	}
	for _, want := range []string{
		"=== SKIPS scope=app ===",
		"SKIPPED fixture/internal/app TestElsewhere: only where there is a second disk",
		"release-critical tests: every one required on " + runtime.GOOS + " ran and passed",
		"race scope app: PASS",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a passing scope did not print %q:\n%s", want, out)
		}
	}

	fixture(`func TestNeeded(t *testing.T) { t.Skip("the rig skips itself here") }`)
	out, err = run()
	if err == nil {
		t.Fatalf("a scope passed with its required test skipped:\n%s", out)
	}
	if !strings.Contains(out, "RELEASE_CRITICAL_SKIPPED internal/app TestNeeded: the rig skips itself here") || strings.Contains(out, "race scope app: PASS") {
		t.Errorf("the refusal does not name the skipped test and its reason, or the scope still said PASS:\n%s", out)
	}

	fixture(`func TestRenamed(t *testing.T) {}`)
	out, err = run()
	if err == nil {
		t.Fatalf("a scope passed with its required test gone:\n%s", out)
	}
	if !strings.Contains(out, "RELEASE_CRITICAL_MISSING internal/app TestNeeded: the package ran and this test did not") {
		t.Errorf("the refusal does not say the required test did not run:\n%s", out)
	}
}
