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
package aiiosexport

import (
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
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

// .
var (
	NewRegistry     = tools.NewRegistry
	NewEngine       = identity.NewEngine
	StoreNew        = store.New
	LedgerNew       = ledger.New
	GenerateKeyPair = crypto.GenerateKeyPair
	NewRingManager  = ring.NewManager
)

// .
const (
	EventRing0Genesis       = ledger.EventRing0Genesis
	EventBeliefUpsert       = ledger.EventBeliefUpsert
	EventRelationshipUpsert = ledger.EventRelationshipUpsert
)

// .
var VerifyChain = ledger.VerifyChain
