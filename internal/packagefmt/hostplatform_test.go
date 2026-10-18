package packagefmt

import (
	"runtime"
	"testing"
)

// The platform words are the shared contract's and the GOOS words are
// Go's; the two differ for exactly one platform. Both directions are
// pinned, so a release target is built for the platform it is named for.
func TestPlatformAndGOOSAreOneMappingBothWays(t *testing.T) {
	for goos, platform := range map[string]string{
		"linux": "linux", "darwin": "macos", "windows": "windows", "android": "android", "ios": "ios",
	} {
		if got := platformOf(goos); got != platform {
			t.Errorf("platformOf(%q) = %q, want %q", goos, got, platform)
		}
		if got := GOOSFor(platform); got != goos {
			t.Errorf("GOOSFor(%q) = %q, want %q", platform, got, goos)
		}
	}
	for _, word := range []string{"", "freebsd", "darwin", "x86_64", "MacOS"} {
		if got := GOOSFor(word); got != "" {
			t.Errorf("GOOSFor(%q) = %q, want \"\" (not a platform word)", word, got)
		}
	}
	if got := platformOf("plan9"); got != "" {
		t.Errorf("platformOf(plan9) = %q, want \"\"", got)
	}
	if p := HostPlatform(); p != "" && GOOSFor(p) != runtime.GOOS {
		t.Errorf("GOOSFor(HostPlatform()) = %q on %s", GOOSFor(p), runtime.GOOS)
	}
}
