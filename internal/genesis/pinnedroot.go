package genesis

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

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
// .
// .
// .

//go:embed ring0_root_pubkey.json
var pinnedRootJSON []byte

// .
// .
// .
// .
func PinnedRoot() *publicKeyEnvelope { return pinnedRoot() }

// .
// .
func pinnedRoot() *publicKeyEnvelope {
	var env publicKeyEnvelope
	if err := json.Unmarshal(pinnedRootJSON, &env); err != nil {
		panic(fmt.Sprintf("genesis: embedded pinned root unparseable — broken build: %v", err))
	}
	if err := sigenvelope.ValidatePublicKeyEnvelope(&env, crypto.ProfileRoot); err != nil {
		panic(fmt.Sprintf("genesis: embedded pinned root invalid — broken build: %v", err))
	}
	return &env
}
