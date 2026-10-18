package updates

import (
	"runtime"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// THE UPDATER MUST ASK FOR A NAME THE PRODUCER MAKES, ON THIS HOST.
//
// The contract tests exercised AssetName with the contract's own
// (platform, arch) pairs and never the host-derived assetName(), so a
// vocabulary mismatch in the host half was invisible: amd64 hosts asked
// for "_x86_64" while every producer wrote "_amd64". This test fails on
// any amd64 host with the old code and passes only when the name the
// running binary requests is one SupportedTargets/BundleTargets produce.
func TestTheUpdaterRequestsANameTheProducerMakes(t *testing.T) {
	// The expected pair is derived here WITHOUT hostTarget(): a first
	// version used the function under test for its own guard, so the
	// mutant that spells amd64 "x86_64" made the test SKIP ("not a
	// release target") instead of fail — the fixture prevented the very
	// behaviour it exists to catch, and a grep for "x86_64" in the skip
	// text reported a kill. The contract's vocabulary is GOOS→platform
	// via packagefmt.HostPlatform (already contract words) and GOARCH
	// verbatim.
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
