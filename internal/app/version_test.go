package app

import (
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
func TestBuildIdentityHonest(t *testing.T) {
	id := BuildIdentity()
	if id == "" {
		t.Fatal("BuildIdentity returned empty — the boot line would be blank")
	}
	if id == "unknown" {
		return
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

// .
// .
// .
// .
// .
// .
// .
// .
func TestUninjectedBuildCarriesAnUpdaterAcceptableVersion(t *testing.T) {
	if Version != "" {
		t.Skipf("this binary was built with -ldflags Version=%q; the fallback is not under test", Version)
	}
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

// .
// .
// .
func TestLdflagsInjectionWinsOverEmbedded(t *testing.T) {
	saved := Version
	t.Cleanup(func() { Version = saved })
	Version = "9.9.9"
	if got := Current(); got != "9.9.9" {
		t.Errorf("Current() = %q, want the injected 9.9.9 to win over the embedded file", got)
	}
}
