package install

import (
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestStopChannelNameIsStableAcrossSpellings(t *testing.T) {
	base := filepath.Join("C:", "Users", "dev", ".aii", "identity-0")
	same := []string{
		base,
		base + string(filepath.Separator),
		filepath.Join(base, "sub", ".."),
		strings.ToUpper(base),
	}
	want := StopChannelNames(base)
	if len(want) < 2 {
		t.Fatalf("expected a preferred name and a fallback, got %v", want)
	}
	for _, spelling := range same {
		got := StopChannelNames(spelling)
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("spelling %q produced %v, want %v — the two ends would not meet", spelling, got, want)
		}
	}
	// .
	other := StopChannelNames(filepath.Join("C:", "Users", "dev", ".aii", "identity-1"))
	if other[0] == want[0] {
		t.Errorf("two identities share a stop channel: %q", other[0])
	}
}
