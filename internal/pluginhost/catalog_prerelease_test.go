package pluginhost

import "testing"

// .
// .
func TestVoiceBetaCatalogUpgrade(t *testing.T) {
	for _, tc := range []struct {
		candidate, installed string
		want                 bool
	}{
		{"0.1.0-beta.3", "0.1.0-beta.1", true},
		{"0.1.0-beta.10", "0.1.0-beta.9", true},
		{"0.1.0-beta.1", "0.1.0-beta.3", false},
		{"0.1.0-beta.3", "0.1.0-beta.3", false},
		{"0.1.0-beta.3+new", "0.1.0-beta.3+old", false},
		{"0.1.0-rc.1", "0.1.0-beta.3", true},
		{"0.1.0", "0.1.0-beta.3", true},
		{"0.1.0-beta.3", "0.1.0", false},
		{"v0.1.0-beta.3", "v0.1.0-beta.1", true},
	} {
		t.Run(tc.candidate+"_from_"+tc.installed, func(t *testing.T) {
			if got := NewerVersion(tc.candidate, tc.installed); got != tc.want {
				t.Fatalf("catalog upgrade %q from %q = %v, want %v", tc.candidate, tc.installed, got, tc.want)
			}
		})
	}
}
