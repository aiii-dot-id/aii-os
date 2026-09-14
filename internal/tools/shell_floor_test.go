package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

func TestSubstrateFloorAgreesInEitherCase(t *testing.T) {
	r, _ := relRegistry(t)
	for _, spelling := range [][2]string{
		{"cat providers.json", "cat Providers.json"},
		{"head data/ledger.jsonl", "head data/Ledger.jsonl"},
		{"sqlite3 aii.db .tables", "sqlite3 AII.DB .tables"},
		{"cat config.json", "cat Config.JSON"},
	} {
		plain, mixed := r.shellRefusal(spelling[0]), r.shellRefusal(spelling[1])
		if plain == "" {
			t.Errorf("the floor did not hold in the plain spelling: %q", spelling[0])
			continue
		}
		if mixed == "" {
			t.Errorf("a protected name was reachable by CASE alone: %q was allowed, %q was refused (%s)",
				spelling[1], spelling[0], plain)
		}
	}
}

// .
// .
// .
// .
// .
func TestMixedCaseRefusalNamesTheTokenAsWritten(t *testing.T) {
	r, _ := relRegistry(t)
	why := r.shellRefusal("cat Providers.json")
	if why == "" {
		t.Fatal("a protected name was reachable by case alone: cat Providers.json")
	}
	if !strings.Contains(why, "Providers.json") {
		t.Fatalf("the refusal did not name the offending token: %q", why)
	}
}

// .
// .
// .
func TestExemptRootsCannotBeWalkedOutOf(t *testing.T) {
	r, _ := relRegistry(t)
	for _, spelling := range [][2]string{
		{"cat /etc/passwd", "cat /usr/../etc/passwd"},
		{"cat /etc/passwd", "cat /bin/../etc/passwd"},
		{"cat /etc/passwd", "cat /usr/bin/../../etc/passwd"},
	} {
		direct, viaExempt := r.shellRefusal(spelling[0]), r.shellRefusal(spelling[1])
		if direct == "" {
			// .
			// .
			// .
			// .
			// .
			// .
			t.Logf("the direct spelling is not refused on this platform: %q", spelling[0])
			continue
		}
		if viaExempt == "" {
			t.Errorf("a walk out of an exempt root was ALLOWED: %q, while %q is refused",
				spelling[1], spelling[0])
		}
	}
}

// .
// .
// .
func TestSystemBinaryInvocationIsStillAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		// .
		// .
		// .
		// .
		// .
		t.Skip("unix system-binary paths; Windows form is TestWindowsSystemCommandsNeedNoPath")
	}
	r, _ := relRegistry(t)
	for _, cmd := range []string{
		"/usr/bin/env true",
		"/bin/ls",
		"/bin/ls -la",
		"ls /usr/bin",
		"cat /dev/null",
		"/usr/bin/../bin/env true",
	} {
		if why := r.shellRefusal(cmd); why != "" {
			t.Errorf("a system-binary invocation was refused: %q -> %s", cmd, why)
		}
	}
}

// .
// .
// .
// .
func TestSubstrateFloorHoldsForMixedCaseGlobs(t *testing.T) {
	r, sandbox := relRegistry(t)
	if err := os.WriteFile(filepath.Join(sandbox, "providers.json"), []byte(`{"api_key":"sk-live"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"cat Provi*.json",
		"cat P?ovi*.json",
		"cat PROVI*.JSON",
		// .
		// .
		// .
		"cat Provi*.json|head -1",
		"cat provi*.json|head -1",
		"(cat Provi*.json)",
		"cat Provi*.json;",
		"cat Provi*.json&&true",
	} {
		why := r.shellRefusal(cmd)
		if why == "" {
			t.Errorf("a protected file was reachable by a mixed-case GLOB: %q was allowed", cmd)
			continue
		}
		if !strings.Contains(why, "providers.json") {
			t.Errorf("the refusal did not name what the glob reached: %q -> %s", cmd, why)
		}
	}
}

// .
// .
// .
func TestInnocentGlobsInsideTheSandboxAreStillAllowed(t *testing.T) {
	r, sandbox := relRegistry(t)
	if err := os.MkdirAll(filepath.Join(sandbox, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandbox, "notes", "todo.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"cat notes/*.md",
		"cat Notes/*.md",
		"wc -l notes/todo*",
	} {
		if why := r.shellRefusal(cmd); why != "" {
			t.Errorf("an innocent glob inside the sandbox was refused: %q -> %s", cmd, why)
		}
	}
}
