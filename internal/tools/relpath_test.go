package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sandbox boundary must not depend on how a path is SPELLED.
//
// Before this, the containment scan skipped every relative token, so
// "cat /etc/passwd" was refused and "cat ../../../etc/passwd" was
// allowed — the same file, and the escape was the cheaper one to write.
// The scrub had the mirror defect: a granted root was exempt when named
// absolutely and matched on the bare name when named relatively.
//
// This is a best-effort check by design (the honest boundary is a
// container), so these do not claim adversarial completeness. They claim
// that the two spellings agree.

func relRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	sandbox := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(sandbox); err == nil {
		sandbox = resolved // NewRegistry does this; the assertions must match
	}
	return NewRegistry(sandbox, nil, Timeouts{}), sandbox
}

func TestRelativeEscapesAreRefused(t *testing.T) {
	r, _ := relRegistry(t)
	for _, cmd := range []string{
		"cat ../../../etc/passwd",
		"cat ./../../../etc/passwd",
		"cat ../../../../../../../../etc/passwd",
		"cd ..",
		"cp notes.txt ../stolen.txt",
	} {
		if why := r.shellRefusal(cmd); why == "" {
			t.Errorf("a relative walk out of the sandbox was ALLOWED: %q", cmd)
		}
	}
}

// The absolute spelling was always refused. Both must now agree.
func TestBothSpellingsOfAnEscapeAgree(t *testing.T) {
	r, _ := relRegistry(t)
	rel := r.shellRefusal("cat ../../../etc/passwd")
	abs := r.shellRefusal("cat /etc/passwd")
	if (rel == "") != (abs == "") {
		t.Fatalf("the boundary depends on spelling: relative=%q absolute=%q", rel, abs)
	}
}

// A symlink pointing out of the sandbox, followed by a RELATIVE path.
// resolveForContainment owns this; before the fix the relative spelling
// never reached it.
func TestRelativePathThroughAnEscapingSymlinkIsRefused(t *testing.T) {
	r, sandbox := relRegistry(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sandbox, "way-out")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if why := r.shellRefusal("cat way-out/secret.txt"); why == "" {
		t.Fatal("a relative path through a symlink leaving the sandbox was ALLOWED")
	}
}

// The change must not start refusing ordinary work. Flags and search
// literals are not paths; joined onto the sandbox they land inside it.
func TestOrdinaryCommandsAreStillAllowed(t *testing.T) {
	r, sandbox := relRegistry(t)
	if err := os.MkdirAll(filepath.Join(sandbox, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		"ls -la",
		"cat notes.txt",
		"grep -n Broadcast src/server.go",
		"grep -rn --include=*.go Handler src/",
		"echo hello && pwd",
		"cat ./src/server.go",
		"sed -n '1,40p' src/server.go",
		"printf '%s\\n' done",
	} {
		if why := r.shellRefusal(cmd); why != "" {
			t.Errorf("ordinary work was refused: %q -> %s", cmd, why)
		}
	}
}

// Unchanged refusals: the substrate floor and $HOME.
func TestExistingRefusalsAreUnchanged(t *testing.T) {
	r, _ := relRegistry(t)
	for _, cmd := range []string{
		"cat data/ledger.jsonl",
		"cat /etc/passwd",
		"cat ~/somewhere",
		"cat $HOME/somewhere",
	} {
		if why := r.shellRefusal(cmd); why == "" {
			t.Errorf("a refusal that must hold was dropped: %q", cmd)
		}
	}
}

// The approved consistency: inside a granted root, both spellings of the
// same directory give the same answer. This is the half that gets MORE
// permissive, and it is the point — a grant that depends on spelling is
// a grant that is silently inert half the time.
func TestGrantedRootAgreesInBothSpellings(t *testing.T) {
	r, sandbox := relRegistry(t)
	granted := filepath.Join(sandbox, "work", "checkout")
	if err := os.MkdirAll(granted, 0o755); err != nil {
		t.Fatal(err)
	}
	r.SetExtraRoots([]string{filepath.Join(sandbox, "work")})

	rel := r.shellRefusal("cd work/checkout && git status")
	abs := r.shellRefusal("cd " + granted + " && git status")
	if rel != abs {
		t.Fatalf("a grant answers differently by spelling:\n  relative: %q\n  absolute: %q", rel, abs)
	}
	if rel != "" {
		t.Fatalf("a granted root was refused in both spellings: %q", rel)
	}
}

// The refusal still names what it refused, in the relative case too.
func TestRelativeRefusalNamesTheOffender(t *testing.T) {
	r, sandbox := relRegistry(t)
	why := r.shellRefusal("cat ../../../etc/passwd")
	if !strings.Contains(why, "../../../etc/passwd") {
		t.Fatalf("the refusal did not name the offending token: %q", why)
	}
	if !strings.Contains(why, sandbox) {
		t.Fatalf("the refusal did not say where the sandbox is: %q", why)
	}
}
