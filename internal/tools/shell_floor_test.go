package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The substrate floor and the system-binary exemption must not depend on
// how a path is SPELLED — the law relpath_test.go pins for ..-walks,
// extended to the spellings the shell scan still answered by.
//
// CASE. macOS and Windows filesystems are case-INSENSITIVE while this
// scan compared raw, so "cat Providers.json" passed the floor, joined the
// sandbox, ran with cmd.Dir inside it, and the kernel opened the real
// providers.json — the operator's API keys into the transcript, on a file
// read and grep refuse in every spelling.
//
// PREFIX. The /usr,/bin exemption was a lexical prefix test with no
// cleaning, so "/usr/../etc/passwd" wore the prefix straight out of the
// sandbox and was never containment-checked at all.
//
// GLOB. CASE again one metacharacter later: the expansion this scan
// matches on is filepath.Glob, case-SENSITIVE, while bash under
// `shopt -s nocaseglob` is not — so "cat Provi*.json" expanded to
// nothing here and to the same providers.json in the shell.
//
// Best-effort by design (the honest boundary is a container): these claim
// that the spellings AGREE, not adversarial completeness. They reuse
// relRegistry so both files judge the same seam.

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

// The refusal has to name the token the identity actually wrote, or the
// only thing left to vary is the command. Matching the offender raw could
// not find the mixed-case field the floor had just refused, and the
// message degraded to "something this command expands to" — true of
// nothing here, and unfixable to read.
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

// An exempt root is exempt for what is UNDER it. A token that only starts
// with one and then walks out is the same file as its direct spelling,
// and must get the same answer.
func TestExemptRootsCannotBeWalkedOutOf(t *testing.T) {
	r, _ := relRegistry(t)
	for _, spelling := range [][2]string{
		{"cat /etc/passwd", "cat /usr/../etc/passwd"},
		{"cat /etc/passwd", "cat /bin/../etc/passwd"},
		{"cat /etc/passwd", "cat /usr/bin/../../etc/passwd"},
	} {
		direct, viaExempt := r.shellRefusal(spelling[0]), r.shellRefusal(spelling[1])
		if direct == "" {
			// TestExistingRefusalsAreUnchanged owns "/etc/passwd is
			// refused". Where that does not hold — Windows, which reads a
			// unix-absolute path as relative to the sandbox — the pair has
			// nothing to say and must not claim otherwise. Logged and
			// skipped one pair at a time: t.Skipf here is a Goexit, and it
			// would carry every LATER pair out of the test unrun.
			t.Logf("the direct spelling is not refused on this platform: %q", spelling[0])
			continue
		}
		if viaExempt == "" {
			t.Errorf("a walk out of an exempt root was ALLOWED: %q, while %q is refused",
				spelling[1], spelling[0])
		}
	}
}

// The negative control, and the point of cleaning rather than dropping
// the exemption: system binaries and devices are invocation, not data. A
// check that refuses everything is not a check.
func TestSystemBinaryInvocationIsStillAllowed(t *testing.T) {
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

// A glob has to reach the file the SHELL would reach, or the floor holds
// only for the spelling nobody attacking it would use. Verified against a
// real providers.json: the refusal must name what the expansion found,
// because the offending token never carries the name itself.
func TestSubstrateFloorHoldsForMixedCaseGlobs(t *testing.T) {
	r, sandbox := relRegistry(t)
	if err := os.WriteFile(filepath.Join(sandbox, "providers.json"), []byte(`{"api_key":"sk-live"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"cat Provi*.json",
		"cat P?ovi*.json",
		"cat PROVI*.JSON",
		// Glued to an operator. Whitespace tokenizing handed the pipe to
		// filepath.Glob, which matched nothing — so the lowercase form
		// needed no nocaseglob to walk straight past the floor.
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

// The control, and the reason the lowered spelling is matched rather than
// the floor widened: a glob that reaches nothing protected is ordinary
// work, in either case.
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
