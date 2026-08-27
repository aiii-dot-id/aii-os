// Package aiiosexport exposes internal enforcement surfaces to external
// verification tools (red-team battery, auditors, strangers).
//
// GOVERNANCE: fixtures serve the runtime; the runtime serves humans. This
// seam is NOT fixture convenience — it is condition 1's strong form as
// code: "verifiable by implementations that don't share code with the
// original." External verification is a runtime property the product
// promises; this package is where that promise is kept honest. The red
// team attacks through it precisely so no test ever needs to copy (and
// thereby stop testing) enforcement code.
//
// Rules:
//   - Re-exports only. Any behavior added here is corruption.
//   - Minimal surface. Anything exported must be justified by
//     verification-from-outside, not by test convenience.
//   - If the battery "needs" more than verification requires, the
//     battery adapts — never the runtime.
package aiiosexport

import (
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// Types
type (
	Result      = tools.Result
	Registry    = tools.Registry
	Engine      = identity.Engine
	Store       = store.Store
	Ledger      = ledger.Ledger
	LedgerEvent = ledger.Event
	EventType   = ledger.EventType
	KeyPair     = crypto.KeyPair
	RingManager = ring.Manager
)

// Constructors / functions
var (
	NewRegistry     = tools.NewRegistry
	NewEngine       = identity.NewEngine
	StoreNew        = store.New
	LedgerNew       = ledger.New
	GenerateKeyPair = crypto.GenerateKeyPair
	NewRingManager  = ring.NewManager
)

// Event constants the battery needs
const (
	EventRing0Genesis       = ledger.EventRing0Genesis
	EventBeliefUpsert       = ledger.EventBeliefUpsert
	EventRelationshipUpsert = ledger.EventRelationshipUpsert
)

// VerifyChain is the stranger's path.
var VerifyChain = ledger.VerifyChain
