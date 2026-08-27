// Package genesistest is the ONE test seam for consuming genuinely-signed
// RING0 bundles and birthing test identities.
//
// Immutable public vectors replace repeated SLH-DSA signing; Birth still
// verifies them through the production path. The package's dedicated fresh
// ceremony test generates new keys and signatures. Tests have no verifier
// bypass.
package genesistest

import (
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesisvectors"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// Root is an immutable signed-vector authority plus its public envelope — the
// test-side stand-in for the shipped pinned root.
type Root struct {
	vectors *genesisvectors.Set
	Env     *sigenvelope.PublicKeyEnvelope
}

// NewRoot loads the shared dual-PQ verifier root. The returned material is
// immutable and contains no private keys.
func NewRoot(t *testing.T) *Root {
	t.Helper()
	v, err := genesisvectors.Load()
	if err != nil {
		t.Fatalf("genesistest: load verifier vectors: %v", err)
	}
	return &Root{vectors: v, Env: v.Root}
}

// MintRing0Bundle returns the immutable signed payload shape served by the live
// genesis server.
func (r *Root) MintRing0Bundle(t *testing.T, constitution string) []byte {
	t.Helper()
	bundle, ok := r.vectors.Ring0[constitution]
	if !ok {
		t.Fatal(fmt.Errorf("genesistest: no signed Ring 0 vector for constitution %q", constitution))
	}
	return bundle
}

// MintBootstrapArtifacts creates the complete test trust chain used by the
// bootstrap server: Ring 0 root -> bootstrap key -> bootstrap packet.
func (r *Root) MintBootstrapArtifacts(t *testing.T, prompt string) (keyBundle, packet []byte) {
	t.Helper()
	packet, ok := r.vectors.BootstrapPackets[prompt]
	if !ok {
		t.Fatal(fmt.Errorf("genesistest: no signed bootstrap vector for prompt %q", prompt))
	}
	return r.vectors.BootstrapKeyBundle, packet
}

// MintForeignRing0Bundle signs a ring0.bundle under a DIFFERENT root —
// the adversary's constitution, valid in the adversary's domain, that
// the shipped root must refuse. Returns the bundle and the foreign root
// envelope (for a client the test points at the attacker's server).
func MintForeignRing0Bundle(t *testing.T, constitution string) ([]byte, *sigenvelope.PublicKeyEnvelope) {
	t.Helper()
	v, err := genesisvectors.Load()
	if err != nil {
		t.Fatalf("genesistest: load verifier vectors: %v", err)
	}
	bundle, ok := v.ForeignRing0[constitution]
	if !ok {
		t.Fatal(fmt.Errorf("genesistest: no signed foreign Ring 0 vector for constitution %q", constitution))
	}
	return bundle, v.ForeignRoot
}

// Birth mints a test identity at the given paths under this root — the
// standard fixture the 13 former hand-rolled births now share. Returns
// the BirthResult with its ledger still open (caller closes, matching
// genesis.Birth's contract).
func (r *Root) Birth(t *testing.T, cfg genesis.BirthConfig) *genesis.BirthResult {
	t.Helper()
	if len(cfg.Ring0Bundle) == 0 {
		cfg.Ring0Bundle = r.MintRing0Bundle(t, defaultConstitution)
	}
	if cfg.Root == nil {
		cfg.Root = r.Env
	}
	res, err := genesis.Birth(&cfg)
	if err != nil {
		t.Fatalf("genesistest: birth: %v", err)
	}
	return res
}

const defaultConstitution = "# Constitution\n\nHonesty. Care. Continuity."
