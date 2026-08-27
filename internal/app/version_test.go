package app

import (
	"strings"
	"testing"
)

// TestBuildIdentityHonest pins the boot-identity contract (operator
// directive 2026-08-22): the line is either an honest "unknown" (no VCS
// data — go run, some CI) or a commit-shaped value — 12 hex chars,
// optionally suffixed " (dirty)". Never empty, never fabricated length.
// The test binary itself decides which branch runs: built from the
// repo it carries vcs.revision; built otherwise it does not. Both are
// correct answers; only dishonesty would fail.
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
