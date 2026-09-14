// .
// .
// .
// .
// .
// .
// .
package genesistest

import (
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesisvectors"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
type Root struct {
	vectors *genesisvectors.Set
	Env     *sigenvelope.PublicKeyEnvelope
}

// .
// .
func NewRoot(t *testing.T) *Root {
	t.Helper()
	v, err := genesisvectors.Load()
	if err != nil {
		t.Fatalf("genesistest: load verifier vectors: %v", err)
	}
	return &Root{vectors: v, Env: v.Root}
}

// .
// .
func (r *Root) MintRing0Bundle(t *testing.T, constitution string) []byte {
	t.Helper()
	bundle, ok := r.vectors.Ring0[constitution]
	if !ok {
		t.Fatal(fmt.Errorf("genesistest: no signed Ring 0 vector for constitution %q", constitution))
	}
	return bundle
}

// .
// .
func (r *Root) MintBootstrapArtifacts(t *testing.T, prompt string) (keyBundle, packet []byte) {
	t.Helper()
	packet, ok := r.vectors.BootstrapPackets[prompt]
	if !ok {
		t.Fatal(fmt.Errorf("genesistest: no signed bootstrap vector for prompt %q", prompt))
	}
	return r.vectors.BootstrapKeyBundle, packet
}

// .
// .
// .
// .
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

// .
// .
// .
// .
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
