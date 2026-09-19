// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesislive"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
type Root struct {
	Env *sigenvelope.PublicKeyEnvelope

	ring0        []byte
	constitution string
	token        string

	bootstrapKeyBundle []byte
	bootstrapPacket    []byte
	bootstrapPrompt    string
}

var (
	once     sync.Once
	shared   *Root
	fetchErr error
)

// .
func NewRoot(t *testing.T) *Root {
	t.Helper()
	once.Do(func() { shared, fetchErr = fetchLive() })
	if fetchErr != nil {
		t.Fatalf("genesistest: RING0 comes from the real servers and nowhere else "+
			"(operator ruling 2026-09-19); this test binary could not reach them: %v", fetchErr)
	}
	return shared
}

func fetchLive() (*Root, error) {
	live, err := genesislive.Fetch()
	if err != nil {
		return nil, err
	}
	root := genesis.PinnedRoot()
	laws, err := genesis.VerifyArtifact(live.Ring0, root, "ring0.bundle")
	if err != nil {
		return nil, fmt.Errorf("live RING0 does not verify against the shipped pin: %w", err)
	}
	bsKey, err := genesis.DomainKeyFromBundle(live.BootstrapKey, "bootstrap.pubkey")
	if err != nil {
		return nil, fmt.Errorf("live bootstrap domain key: %w", err)
	}
	prompt, err := genesis.VerifyArtifact(live.BootstrapPacket, bsKey, "bootstrap.packet")
	if err != nil {
		return nil, fmt.Errorf("live bootstrap packet: %w", err)
	}
	return &Root{
		Env: root, ring0: live.Ring0, constitution: laws, token: live.Token,
		bootstrapKeyBundle: live.BootstrapKey, bootstrapPacket: live.BootstrapPacket, bootstrapPrompt: prompt,
	}, nil
}

// .
func (r *Root) Ring0Bundle(t *testing.T) []byte {
	t.Helper()
	return r.ring0
}

// .
// .
func (r *Root) Constitution(t *testing.T) string {
	t.Helper()
	return r.constitution
}

// .
func (r *Root) Token(t *testing.T) string {
	t.Helper()
	return r.token
}

// .
// .
func (r *Root) BootstrapArtifacts(t *testing.T) (keyBundle, packet []byte, prompt string) {
	t.Helper()
	return r.bootstrapKeyBundle, r.bootstrapPacket, r.bootstrapPrompt
}

// .
func (r *Root) BootstrapPrompt(t *testing.T) string {
	t.Helper()
	return r.bootstrapPrompt
}

// .
// .
// .
// .
// .
func NotTheSigner(t *testing.T) *sigenvelope.PublicKeyEnvelope {
	t.Helper()
	live, err := genesislive.Fetch()
	if err != nil {
		t.Fatalf("genesistest: %v", err)
	}
	env, err := genesis.DomainKeyFromBundle(live.Ring5PubkeyBundle, "ring5.pubkey")
	if err != nil {
		t.Fatalf("genesistest: read the Ring 5 domain key: %v", err)
	}
	return env
}

// .
// .
func (r *Root) Birth(t *testing.T, cfg genesis.BirthConfig) *genesis.BirthResult {
	t.Helper()
	if len(cfg.Ring0Bundle) == 0 {
		cfg.Ring0Bundle = r.ring0
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
