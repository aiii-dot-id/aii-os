package genesis

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// The pinned AIII Ring 0 root — the firstboot trust anchor.
//
// WHY THIS SHIPS IN THE BINARY (canon GENESIS_AND_FIRSTBOOT §Step 4b;
// review docs/claude/BIRTH_CANON_REVIEW_2026-08-20.md): before an
// identity is born, bytes claiming to be Ring 0 must be authenticated,
// and authentication needs a first key. There are only three places a
// first key can come from — inside the artifact, over the network, or
// typed by a human. Over-the-network makes the delivery host the root
// of trust (a compromised genesis server self-endorses a forged key and
// the identity is born under a forged constitution). Typed-by-human
// fails "anyone anywhere". So the first key ships inside the artifact,
// forced not chosen.
//
// This is not a ceremony output and needs none: aii-os is open source,
// so this file is code — one public artifact in a public repo, exactly
// as trustworthy as every other line, and anyone anywhere verifies its
// authenticity in seconds:
//
//	curl -s https://genesis.aiii.id/genesis/pubkey | \
//	  diff - internal/genesis/ring0_pubkey.json
//
// PROVENANCE (a public claim, not a private vouching):
//   - source:   https://genesis.aiii.id/genesis/pubkey
//   - sha256:   9ee375d3fc7f7f3962b254669d26a47f3e0f64b07c2f917d020e7cb68165d506
//   - key_id:   aiii_ring0_20260602_k14
//   - fetched:  2026-08-20 (byte-identical to the 2026-08-18 interop
//     capture in testdata/ring0_pubkey.json — two independent
//     fetches, days apart, agree)
//
// ROTATION is a release event: a new root ships in a new binary, and the
// replacement must be cross-signed by this root (continuity), reviewed
// as an ordinary public PR. A live /genesis/pubkey response has no boot
// authority — it is a diagnostic cross-check only (FetchRing0).
//
//go:embed ring0_pubkey.json
var pinnedRootJSON []byte

// pinnedRoot parses and validates the embedded root once. A broken embed
// is a broken build — panic, never boot without a trust anchor.
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
