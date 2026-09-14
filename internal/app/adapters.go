// .
package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"reflect"
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

// .
type ringAuthority struct {
	gate *prompt.Gate
	st   *store.Store
}

// .
func (r ringAuthority) AuthorityPreamble() (string, error) {
	text, _, err := r.AuthorityPrefix()
	return text, err
}

func (r ringAuthority) AuthorityPrefix() (string, int, error) {
	if r.gate == nil || r.st == nil {
		return "", 0, fmt.Errorf("identity prompt source is not wired")
	}
	identity, err := r.st.PromptIdentity()
	if err != nil {
		return "", 0, fmt.Errorf("load identity projection: %w", err)
	}
	ring2Text := prompt.RenderRing2(identity.Ring2)
	parts := []string{"# Facility prompt"}
	if rendered := prompt.RenderSelfModel(identity.SelfModel); rendered != "" {
		parts = append(parts, rendered)
	}
	text, seam := r.gate.SystemWithIdentitySeam(strings.Join(parts, "\n\n"), identity.Charter, ring2Text)
	return text, seam, nil
}

// .
// .
type appRingSource struct {
	rm         *ring.Manager
	priorities prioritiesSource
}

// .
type prioritiesSource interface {
	ActivePriorities() ([]string, error)
}

func (r appRingSource) Ring0() string { return r.rm.GetContent(ring.Ring0) }
func (r appRingSource) Ring5() string { return r.rm.GetContent(ring.Ring5) }

// .
// .
// .
// .
func (r appRingSource) Ring3() string {
	return prompt.RenderRing3ForFacility(r.rm.Sections(ring.Ring3))
}
func (r appRingSource) Ring4() string {
	if r.priorities == nil {
		return ""
	}
	active, err := r.priorities.ActivePriorities()
	if err != nil {
		return ""
	}
	return prompt.RenderRing4ForFacility(active)
}

// .
// .
// .
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
func (c selfModelCommitter) Definition() llm.ToolDefinition {
	for _, verb := range identity.Verbs() {
		if verb.Name == "commit" {
			return llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{
				Name:        verb.Name,
				Description: verb.Description,
				Parameters:  narrowToSelfModel(verb.Params),
			}}
		}
	}
	panic("identity registry has no commit verb")
}

// .
// .
func selfModelKeys() map[string]bool {
	keys := map[string]bool{"variant": true}
	t := reflect.TypeOf(ledger.SelfModelSynthesisPayload{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			tag = tag[:comma]
		}
		// .
		// .
		// .
		if tag == "" || tag == "model_id" {
			continue
		}
		keys[tag] = true
	}
	return keys
}

// .
// .
// .
func narrowToSelfModel(params map[string]interface{}) map[string]interface{} {
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		return params
	}
	allowed := selfModelKeys()
	kept := make(map[string]interface{}, len(allowed))
	for name, spec := range props {
		if allowed[name] {
			kept[name] = spec
		}
	}
	kept["variant"] = map[string]interface{}{
		"type":        "string",
		"enum":        []string{"self_model.synthesize"},
		"description": "The self-authorship act. This facility commits exactly one.",
	}
	out := make(map[string]interface{}, len(params))
	for k, v := range params {
		out[k] = v
	}
	out["properties"] = kept
	out["required"] = []string{"variant", "synthesis_text", "continuity_thread", "source_entity_refs"}
	return out
}

func (c selfModelCommitter) Commit(ctx context.Context, args map[string]interface{}) (string, error) {
	return c.engine.ExecuteAction(ctx, "verb", "commit", args)
}

// .
type ledgerAdapter struct {
	*ledger.Ledger
	kp          *crypto.KeyPair
	st          eventProjection
	mu          sync.Mutex
	onIntegrity func(error)
	// .
	// .
	onAppend func(*ledger.Event)
}

type eventProjection interface {
	ValidateEvent(ledger.EventType, int, []byte) error
	Materialize(*ledger.Event) error
}

func (l *ledgerAdapter) Append(eventType ledger.EventType, ring int, payload interface{}, modelID string) (*ledger.Event, error) {
	// .
	// .
	if l.Ledger == nil {
		return nil, fmt.Errorf("refused before append: no ledger is open — the record could not be read at startup")
	}
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
	if l.onAppend != nil {
		l.onAppend(evt)
	}
	return evt, nil
}

// .

// .
// .
// .
// .
type appTranscript struct {
	st *store.Store
	// .
	// .
	// .
	// .
	actor string
}

func (t appTranscript) RecordToolStart(turnID string, ordinal int, callID, tool, args, model string) error {
	actor := t.actor
	if actor == "" {
		actor = "main"
	}
	return t.st.RecordToolStart(turnID, ordinal, actor, model, callID, tool, args)
}

func (t appTranscript) RecordToolDone(turnID string, ordinal int, tool, args, result string, failed, truncated bool) error {
	return t.st.RecordToolDone(turnID, ordinal, tool, args, result, failed, truncated)
}
func (t appTranscript) TranscriptResultExcerptLimit() int { return store.TranscriptResultLimit }

// .
// .
type appToolExecutor struct{ a *App }

func (x appToolExecutor) Execute(ctx context.Context, call llm.ToolCall) conversation.Observation {
	return x.a.executeToolCall(ctx, call)
}

// .
// .
func (x appToolExecutor) ParallelSafe(call llm.ToolCall) bool {
	for _, v := range identity.Verbs() {
		if v.Name == call.Function.Name {
			return false
		}
	}
	if x.a.toolReg == nil {
		return false
	}
	return x.a.toolReg.ParallelSafe(call.Function.Name)
}

// .
type appToolDefiner struct{ a *App }

func (d appToolDefiner) ToolDefinitions() []llm.ToolDefinition {
	return d.a.buildToolDefinitions()
}

// .
// .
type appEmitter struct {
	a *App
	// .
	// .
	// .
	// .
	// .
	// .
	actor string
}

func (e appEmitter) EmitToolEvent(kind, name, args string) {
	e.a.toolEmitMu.Lock()
	emit := e.a.toolEmit
	e.a.toolEmitMu.Unlock()
	if emit != nil {
		if e.actor != "" {
			args = "[" + e.actor + "] " + args
		}
		emit(kind, name, args)
	}
}

// .
// .
type toolDiscovererAdapter struct{ reg *tools.Registry }

func (t toolDiscovererAdapter) Discover(depth int) []identity.ToolInfo {
	infos := t.reg.Discover(depth)
	out := make([]identity.ToolInfo, 0, len(infos))
	for _, i := range infos {
		out = append(out, identity.ToolInfo{Name: i.Name, Description: i.Description})
	}
	return out
}

// .
// .
func (t toolDiscovererAdapter) Brief() identity.ToolBrief {
	b := t.reg.Brief()
	out := identity.ToolBrief{Total: b.Total, Offered: b.Offered, Unavailable: b.Unavailable, MoreFamilies: b.MoreFamilies, OfferedNames: b.OfferedNames}
	for _, f := range b.Families {
		out.Families = append(out.Families, identity.ToolFamily{Name: f.Name, Count: f.Count, Names: f.Names, More: f.More})
	}
	return out
}

func (t toolDiscovererAdapter) Search(query string, limit int) []identity.ToolHit {
	hits := t.reg.Search(query, limit)
	out := make([]identity.ToolHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, identity.ToolHit{Name: h.Name, Operation: h.Operation, Plugin: h.Plugin, Summary: h.Summary, Effects: h.Effects, State: string(h.State)})
	}
	return out
}

func (t toolDiscovererAdapter) Show(ref string) (identity.ToolCard, error) {
	c, err := t.reg.Show(ref)
	if err != nil {
		return identity.ToolCard{}, err
	}
	return identity.ToolCard{Name: c.Name, Operation: c.Operation, Plugin: c.Plugin, Version: c.Version, Tier: c.Tier,
		Family: c.Family, Summary: c.Summary, Effects: c.Effects, Capabilities: c.Capabilities, MaxResultBytes: c.MaxResultBytes,
		Examples: c.Examples, State: string(c.State), Reason: c.Reason, Receipt: c.Receipt, Parameters: c.Parameters}, nil
}

func (t toolDiscovererAdapter) Offer(ref string) (string, error)   { return t.reg.Offer(ref) }
func (t toolDiscovererAdapter) Release(ref string) (string, error) { return t.reg.Release(ref) }

// .
// .
// .
type witnessMinter struct {
	door *ledgerAdapter
}

func (m witnessMinter) MintWitnessed(receipt witness.WitnessReceipt, witnessKeyID string) (*ledger.Event, error) {
	payload := map[string]interface{}{
		"receipt": store.WitnessReceiptPayload{
			IdentityID:                     receipt.IdentityID,
			PreviousWitnessedLedgerOrdinal: receipt.PreviousWitnessedLedgerOrdinal,
			PreviousWitnessedLedgerHash:    receipt.PreviousWitnessedLedgerHash,
			LedgerOrdinal:                  receipt.LedgerOrdinal,
			LedgerHash:                     receipt.LedgerHash,
			WitnessedAt:                    receipt.WitnessedAt,
			WitnessKeyID:                   witnessKeyID,
			WitnessSigB64:                  receipt.WitnessSignature.SigB64,
		},
	}
	return m.door.Append(ledger.EventSystemWitnessed, 0, payload, "")
}

// .
// .
// .
// .
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

// .
// .
// .
func (a *App) ledgerAppended(evt *ledger.Event) {
	if evt == nil {
		return
	}
	a.emitPluginEvent(pluginhost.TopicLedgerAppended, map[string]interface{}{"event_type": string(evt.Type), "ring": evt.Ring, "seq": evt.Seq})
}
