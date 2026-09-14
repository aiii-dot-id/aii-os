package updates

import (
	"runtime"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestTheUpdaterRequestsANameTheProducerMakes(t *testing.T) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	platform, arch := packagefmt.HostPlatform(), runtime.GOARCH
	inContract := false
	for _, tgt := range SupportedTargets() {
		if tgt.Platform == platform && tgt.Arch == arch {
			inContract = true
		}
	}
	if !inContract {
		t.Skipf("host %s/%s is not a release target; nothing to request", platform, arch)
	}
	want := AssetName("1.2.3", platform, arch)
	if got := assetName("1.2.3"); got != want {
		t.Fatalf("this host requests %q but the producer makes %q — self-update can never find its asset here", got, want)
	}
	for _, tgt := range BundleTargets() {
		if tgt.Platform == platform && tgt.Arch == arch {
			if got, want := bundleAssetName("1.2.3"), BundleAssetName("1.2.3", platform, arch); got != want {
				t.Fatalf("this host requests bundle %q but the producer makes %q", got, want)
			}
		}
	}
}
