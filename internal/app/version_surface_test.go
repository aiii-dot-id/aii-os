package app

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/version"
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
func TestVersionFlagAnswersWithoutAnIdentity(t *testing.T) {
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
	cmd := exec.Command(bin, "-version")
	cmd.Dir = t.TempDir()
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
	// .
	if !regexp.MustCompile(`build (unknown|[0-9a-f]{7,12})`).MatchString(got) {
		t.Fatalf("build identity is neither a commit nor \"unknown\": %q", got)
	}
}

// .
// .
func TestBuildIdentityNeverFabricates(t *testing.T) {
	got := BuildIdentity()
	if got == "" {
		t.Fatal("BuildIdentity returned empty — callers would render nothing at all")
	}
	if got == "unknown" {
		return
	}
	base := strings.TrimSuffix(got, " (dirty)")
	if !regexp.MustCompile(`^[0-9a-f]{7,12}$`).MatchString(base) {
		t.Fatalf("BuildIdentity returned %q — not a commit and not \"unknown\"", got)
	}
}

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
func TestUninjectedBuildReportsTheAuthoredVersion(t *testing.T) {
	saved := Version
	t.Cleanup(func() { Version = saved })

	Version = ""
	got := VersionString()
	if got == "dev" {
		t.Fatal(`uninjected build still reports the "dev" sentinel — it fails version.Valid, which disables update checking for the process`)
	}
	if got != version.Authored() {
		t.Fatalf("uninjected build reported %q, want the authored VERSION %q", got, version.Authored())
	}
	if !version.Valid(got) {
		t.Fatalf("uninjected build reported %q, which version.Valid rejects — updates.go would refuse to check", got)
	}

	Version = "0.1.0"
	if got := VersionString(); got != "0.1.0" {
		t.Fatalf("an injected version was not reported: %q", got)
	}
}
