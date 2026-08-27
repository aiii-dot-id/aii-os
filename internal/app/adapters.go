// Package app contains the runtime adapters.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// ringAuthority builds the identity context every LLM facility sees.
type ringAuthority struct {
	gate *prompt.Gate
	st   *store.Store
}

// AuthorityPreamble passes the derived identity context through the prompt gate.
func (r ringAuthority) AuthorityPreamble() (string, error) {
	if r.gate == nil || r.st == nil {
		return "", fmt.Errorf("identity prompt source is not wired")
	}
	identity, err := r.st.PromptIdentity()
	if err != nil {
		return "", fmt.Errorf("load identity projection: %w", err)
	}
	ring2Text := prompt.RenderRing2(identity.Ring2)
	parts := []string{"# Facility prompt"}
	if rendered := prompt.RenderSelfModel(identity.SelfModel); rendered != "" {
		parts = append(parts, rendered)
	}
	return r.gate.SystemWithIdentity(strings.Join(parts, "\n\n"), identity.Charter, ring2Text), nil
}

// appRingSource adapts the ring.Manager to prompt.RingSource — the gate's
// view of the rings.
type appRingSource struct{ rm *ring.Manager }

func (r appRingSource) Ring0() string { return r.rm.GetContent(ring.Ring0) }
func (r appRingSource) Ring5() string { return r.rm.GetContent(ring.Ring5) }
func (r appRingSource) Ring3() string { return r.rm.GetContent(ring.Ring3) }
func (r appRingSource) Ring4() string { return r.rm.GetContent(ring.Ring4) }

// appTimers keeps TIME as the sole alarm mutation boundary so each change
// immediately re-arms the scheduler. The store adapter owns only the read
// projection used by timer list/query.
type appTimers struct {
	time *cognitive.TIME
	read identity.TimerSetter
}

func (t appTimers) SetTimer(id, payload string, deadline int64) error {
	return t.time.SetAlarm(id, "timers", "wall", deadline, nil, payload)
}

func (t appTimers) SetRepeating(id, payload string, deadline, every int64) error {
	return t.time.SetAlarm(id, "timers", "wall", deadline, &every, payload)
}

func (t appTimers) CancelTimer(id string) error {
	return t.time.CancelAlarm("timers", id)
}

func (t appTimers) ListTimers() ([]identity.TimerInfo, error) {
	return t.read.ListTimers()
}

type selfModelCommitter struct{ engine *identity.Engine }

func (c selfModelCommitter) Definition() llm.ToolDefinition {
	for _, verb := range identity.Verbs() {
		if verb.Name == "commit" {
			return llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{
				Name: verb.Name, Description: verb.Description, Parameters: verb.Params,
			}}
		}
	}
	panic("identity registry has no commit verb")
}

func (c selfModelCommitter) Commit(ctx context.Context, args map[string]interface{}) (string, error) {
	return c.engine.ExecuteAction(ctx, "verb", "commit", args)
}

// ledgerAdapter is the single live admission path.
type ledgerAdapter struct {
	*ledger.Ledger
	kp          *crypto.KeyPair
	st          eventProjection
	mu          sync.Mutex
	onIntegrity func(error)
}

type eventProjection interface {
	ValidateEvent(ledger.EventType, int, []byte) error
	Materialize(*ledger.Event) error
}

func (l *ledgerAdapter) Append(eventType ledger.EventType, ring int, payload interface{}, modelID string) (*ledger.Event, error) {
	l.mu.Lock()
	var prepared ledger.PreparedPayload
	var err error
	if modelID == "" {
		prepared, err = l.Ledger.PreparePayload(payload)
	} else {
		prepared, err = l.Ledger.PreparePayloadWithModel(payload, modelID)
	}
	if err == nil {
		err = l.st.ValidateEvent(eventType, ring, prepared.Bytes())
	}
	if err != nil {
		l.mu.Unlock()
		return nil, fmt.Errorf("refused before append: %w", err)
	}
	evt, err := l.Ledger.AppendPrepared(eventType, l.kp.Fingerprint(), ring, prepared, l.kp)
	if err != nil {
		integrity := errors.Is(err, ledger.ErrTailIntegrity) || errors.Is(err, ledger.ErrAppendUncertain)
		l.mu.Unlock()
		if integrity && l.onIntegrity != nil {
			l.onIntegrity(err)
		}
		return nil, err
	}
	if err := l.st.Materialize(evt); err != nil {
		err = fmt.Errorf("event %d (%s) is durable but did not materialize: %w", evt.Seq, evt.Type, err)
		l.Ledger.SetFrozen(err.Error())
		l.mu.Unlock()
		if l.onIntegrity != nil {
			l.onIntegrity(err)
		}
		return evt, err
	}
	l.mu.Unlock()
	return evt, nil
}

// --- conversation-loop adapters ---

// appTranscript adapts the store to the conversation Transcript port.
// The args truncation lives here (operator's view); the result excerpt
// limit is the store's own property — the loop's banner states what this
// recorder actually keeps.
type appTranscript struct{ st *store.Store }

func (t appTranscript) RecordToolEvent(tool, args, result string) error {
	runes := []rune(args)
	if len(runes) > 200 {
		args = string(runes[:197]) + "..."
	}
	return t.st.RecordToolEvent(tool, args, result)
}
func (t appTranscript) TranscriptResultExcerptLimit() int { return store.TranscriptResultLimit }

// appToolExecutor adapts the app's tool dispatch (physical tools +
// identity verbs) to the conversation ToolExecutor port.
type appToolExecutor struct{ a *App }

func (x appToolExecutor) Execute(ctx context.Context, call llm.ToolCall) string {
	return x.a.executeToolCall(ctx, call)
}

// appToolDefiner adapts buildToolDefinitions to the ToolDefiner port.
type appToolDefiner struct{ a *App }

func (d appToolDefiner) ToolDefinitions() []llm.ToolDefinition {
	return d.a.buildToolDefinitions()
}

// appEmitter streams tool calls to the live dashboard observer, if one
// is attached (observeChat sets/clears it per connection).
type appEmitter struct{ a *App }

func (e appEmitter) EmitToolEvent(kind, name, args string) {
	e.a.toolEmitMu.Lock()
	emit := e.a.toolEmit
	e.a.toolEmitMu.Unlock()
	if emit != nil {
		emit(kind, name, args)
	}
}

// toolDiscovererAdapter adapts tools.Registry to identity.ToolDiscoverer —
// the identity domain sees its own ToolInfo type, not the tool layer's.
type toolDiscovererAdapter struct{ reg *tools.Registry }

func (t toolDiscovererAdapter) Discover(depth int) []identity.ToolInfo {
	infos := t.reg.Discover(depth)
	out := make([]identity.ToolInfo, 0, len(infos))
	for _, i := range infos {
		out = append(out, identity.ToolInfo{Name: i.Name, Description: i.Description})
	}
	return out
}

// witnessMinter adapts ledger + store + key to the witness EventMinter
// port: the verified receipt is minted as a signed system.witnessed event
// and materialized into the receipt projection in one step.
type witnessMinter struct {
	door *ledgerAdapter
}

func (m witnessMinter) MintWitnessed(receipt witness.WitnessReceipt, witnessKeyID string) (*ledger.Event, error) {
	payload := map[string]interface{}{
		"receipt": store.WitnessReceiptPayload{
			WitnessVersion:                 receipt.WitnessVersion,
			IdentityID:                     receipt.IdentityID,
			PreviousWitnessedLedgerOrdinal: receipt.PreviousWitnessedLedgerOrdinal,
			PreviousWitnessedLedgerHash:    receipt.PreviousWitnessedLedgerHash,
			LedgerOrdinal:                  receipt.LedgerOrdinal,
			LedgerHash:                     receipt.LedgerHash,
			RangeStartOrdinal:              receipt.RangeStartOrdinal,
			RangeHash:                      receipt.RangeHash,
			WitnessedAt:                    receipt.WitnessedAt,
			WitnessKeyID:                   witnessKeyID,
			WitnessSigB64:                  receipt.WitnessSignature.SigB64,
		},
	}
	return m.door.Append(ledger.EventSystemWitnessed, 0, payload, "")
}

// trustEpochGuard adapts ledger + store + key to packagefmt.EpochGuard:
// accepted snapshot epochs are ledgered facts (trust.epoch_accepted),
// read back from the trust_epochs projection — the witness-receipt
// pattern applied to trust state.
type trustEpochGuard struct {
	door *ledgerAdapter
	st   *store.Store
}

func (g trustEpochGuard) TrustEpochHighWater(root string) (int64, string, bool, error) {
	return g.st.TrustEpochHighWater(root)
}

func (g trustEpochGuard) AcceptTrustEpoch(root string, epoch int64, payloadSHA256 string) error {
	_, err := g.door.Append(ledger.EventTrustEpochAccepted, 0,
		store.TrustEpochPayload{Root: root, TrustEpoch: epoch, PayloadSHA256: payloadSHA256}, "")
	return err
}
