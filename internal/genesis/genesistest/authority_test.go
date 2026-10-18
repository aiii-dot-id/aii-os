package genesistest

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis/genesislive"
)

// THE COPY HAS NO AUTHORITY. What the machine keeps of the platform's
// artifacts, and what this package serves to an app a test boots, is
// judged by the production rule against the shipped pin every time it is
// used. So a copy that someone altered is not a way in: each artifact,
// with one byte changed, is refused here, and so is one artifact put in
// another's place.
func TestAnAlteredCopyIsRefused(t *testing.T) {
	NewRoot(t)
	live, err := genesislive.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rootFrom(live); err != nil {
		t.Fatalf("rig: the copy as fetched does not verify: %v", err)
	}
	flip := func(b []byte) []byte {
		c := append([]byte(nil), b...)
		c[len(c)/2] ^= 0x01
		return c
	}
	for name, alter := range map[string]func(a *genesislive.Artifacts){
		"the constitution bundle":         func(a *genesislive.Artifacts) { a.Ring0 = flip(a.Ring0) },
		"the Ring 5 bundle":               func(a *genesislive.Artifacts) { a.Ring5Bundle = flip(a.Ring5Bundle) },
		"the Ring 5 manifest":             func(a *genesislive.Artifacts) { a.Ring5Manifest = flip(a.Ring5Manifest) },
		"the Ring 5 key chain":            func(a *genesislive.Artifacts) { a.Ring5PubkeyBundle = flip(a.Ring5PubkeyBundle) },
		"the bootstrap key chain":         func(a *genesislive.Artifacts) { a.BootstrapKey = flip(a.BootstrapKey) },
		"the bootstrap packet":            func(a *genesislive.Artifacts) { a.BootstrapPacket = flip(a.BootstrapPacket) },
		"a bundle in a manifest's place":  func(a *genesislive.Artifacts) { a.Ring5Manifest = a.Ring5Bundle },
		"the bootstrap chain as Ring 5's": func(a *genesislive.Artifacts) { a.Ring5PubkeyBundle = a.BootstrapKey },
	} {
		altered := *live
		alter(&altered)
		if _, err := rootFrom(&altered); err == nil {
			t.Errorf("%s was altered, and the copy was accepted", name)
		}
	}
}
