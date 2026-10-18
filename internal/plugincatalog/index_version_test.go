package plugincatalog

import "testing"

// A catalog offers an update only for a release the host can rank above
// the installed one: dotted numbers, a leading v ignored, a pre-release
// below its release — and anything unrankable is never "newer".
func TestNewerVersionRanksReleasesAndRefusesTheUnrankable(t *testing.T) {
	for _, tc := range []struct {
		candidate, installed string
		newer                bool
	}{
		{"0.2.0", "0.1.0", true}, {"0.1.0", "0.2.0", false}, {"0.1.0", "0.1.0", false},
		{"1.0.0", "0.9.9", true}, {"0.1.10", "0.1.9", true}, {"v0.2.0", "0.1.0", true},
		{"0.1.0.1", "0.1.0", true}, {"0.1", "0.1.0", false},
		{"1.0.0", "1.0.0-rc1", true}, {"1.0.0-rc1", "1.0.0", false},
		// rc2 IS above rc1. This line asserted the opposite, which is
		// how a boolean-only prerelease comparison survived: the test
		// agreed with the defect.
		{"1.0.0-rc2", "1.0.0-rc1", true},
		{"latest", "0.1.0", false}, {"0.2.0", "unknown", false}, {"", "0.1.0", false}, {"0.-1.0", "0.1.0", false},
	} {
		if got := NewerVersion(tc.candidate, tc.installed); got != tc.newer {
			t.Errorf("NewerVersion(%q, %q) = %v, want %v", tc.candidate, tc.installed, got, tc.newer)
		}
	}
}
