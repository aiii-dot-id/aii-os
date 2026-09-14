package pluginhost

import "testing"

// .
// .
// .
func TestNewerVersionRanksReleasesAndRefusesTheUnrankable(t *testing.T) {
	for _, tc := range []struct {
		candidate, installed string
		newer                bool
	}{
		{"0.2.0", "0.1.0", true}, {"0.1.0", "0.2.0", false}, {"0.1.0", "0.1.0", false},
		{"1.0.0", "0.9.9", true}, {"0.1.10", "0.1.9", true}, {"v0.2.0", "0.1.0", true},
		{"0.1.0.1", "0.1.0", true}, {"0.1", "0.1.0", false},
		{"1.0.0", "1.0.0-rc1", true}, {"1.0.0-rc1", "1.0.0", false}, {"1.0.0-rc2", "1.0.0-rc1", false},
		{"latest", "0.1.0", false}, {"0.2.0", "unknown", false}, {"", "0.1.0", false}, {"0.-1.0", "0.1.0", false},
	} {
		if got := NewerVersion(tc.candidate, tc.installed); got != tc.newer {
			t.Errorf("NewerVersion(%q, %q) = %v, want %v", tc.candidate, tc.installed, got, tc.newer)
		}
	}
}
