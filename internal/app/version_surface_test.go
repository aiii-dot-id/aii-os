package app

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// Binary identity has to be ASKABLE, not just stated once into a log.
//
// BuildIdentity and VersionString have existed and were correct; the only
// place either reached was a boot line. Answering "which build is this?"
// therefore meant finding a log from a boot that might be long gone —
// exactly what the 2026-08-21 emergency-swap forensics could not do, and
// why a resident later resorted to hashing the assets it was serving to
// work out what it was running. AGENTS.md 9 requires commit,
// configuration, executable and process to be bound independently before
// an artifact may be called current; unaskable identity makes that
// impossible rather than merely awkward.

// The flag must answer without starting an identity, and from anywhere:
// a binary that needs a config directory to say what it is cannot be
// interrogated on the machine it was deployed to.
func TestVersionFlagAnswersWithoutAnIdentity(t *testing.T) {
	bin := t.TempDir() + "/aii"
	build := exec.Command("go", "build", "-o", bin, "../../cmd/aii")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
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

// The honesty half, stated by the version comment and never pinned: a
// build with no VCS data says so rather than inventing one.
func TestBuildIdentityNeverFabricates(t *testing.T) {
	got := BuildIdentity()
	if got == "" {
		t.Fatal("BuildIdentity returned empty — callers would render nothing at all")
	}
	if got == "unknown" {
		return // honest absence
	}
	base := strings.TrimSuffix(got, " (dirty)")
	if !regexp.MustCompile(`^[0-9a-f]{7,12}$`).MatchString(base) {
		t.Fatalf("BuildIdentity returned %q — not a commit and not \"unknown\"", got)
	}
}

func TestVersionStringIsHonestWhenUninjected(t *testing.T) {
	saved := Version
	t.Cleanup(func() { Version = saved })

	Version = ""
	if got := VersionString(); got != "dev" {
		t.Fatalf("an uninjected build reported %q, want dev — a fabricated version is worse than none", got)
	}
	Version = "0.1.0"
	if got := VersionString(); got != "0.1.0" {
		t.Fatalf("an injected version was not reported: %q", got)
	}
}
