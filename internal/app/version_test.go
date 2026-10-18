package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/version"
)

func TestBuildIdentityHonest(t *testing.T) {
	id := BuildIdentity()
	if id == "" {
		t.Fatal("BuildIdentity returned empty — the boot line would be blank")
	}
	if id == "unknown" {
		return // honest absence of VCS data
	}
	const dirty = " (dirty)"
	base := strings.TrimSuffix(id, dirty)
	if len(base) != 12 {
		t.Fatalf("commit shape %q: want 12 chars, got %d", base, len(base))
	}
	for _, c := range base {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("commit shape %q: non-hex character %q", base, c)
		}
	}
}

// A plain `go build` — which is what this test binary is, and what every
// build site does — must carry a version the updater will accept. This is
// the whole point of embedding VERSION: updates.go turns checking off for
// the life of any process whose version fails version.Valid, so a
// versionless dev build left the checker dark everywhere except CI-built
// releases.
func TestUninjectedBuildCarriesAnUpdaterAcceptableVersion(t *testing.T) {
	got := Current()
	if got == "" {
		t.Fatal("uninjected build reports an empty version — updates.go will refuse to check")
	}
	if !version.Valid(got) {
		t.Errorf("Current() = %q, which version.Valid rejects — updates.go turns checking off for such a build", got)
	}
	if got == "dev" {
		t.Error(`Current() still reports the old "dev" sentinel; that string is not a version and disables update checking`)
	}
}
