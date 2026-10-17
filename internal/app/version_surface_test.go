package app

import (
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/version"
)

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

func TestUninjectedBuildReportsTheAuthoredVersion(t *testing.T) {
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
}
