package broker

import (
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"reflect"
	"strings"
	"testing"
)

func TestInteractionReaderGrantScopeRevocationAndSAFE(t *testing.T) {
	st := newStore(t)
	if err := st.AddConversationTurn("operator", "question"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddOutboxMessage("secret", "peer", "peer", "private body", nil); err != nil {
		t.Fatal(err)
	}
	safe := false
	h := newHost(t, st, Config{Interactions: st, InSAFE: func() bool { return safe }, Grants: map[string]Grant{"p": {Interactions: true, ReadOnly: true}, "q": {Memory: true, KV: true}}})
	params := memParams(opInteractionQuery, `{"version":1}`)
	wantErrorReason(t, dispatch(t, h.Bind("p", packagefmt.TierT1, []string{"ring4.memory"}), params), reasonNotInEnvelope)
	wantErrorReason(t, dispatch(t, h.Bind("q", packagefmt.TierT1, []string{capInteractionRead}), params), reasonPolicyDeny)
	b := h.Bind("p", packagefmt.TierT1, []string{capInteractionRead})
	wantErrorReason(t, dispatch(t, b, params), reasonNotInEnvelope)
	b.BeginOperation(OperationScope{Operation: "legacy", Declared: false, Capabilities: []string{capInteractionRead}})
	wantErrorReason(t, dispatch(t, b, params), reasonNotInEnvelope)
	b.EndOperation()
	b.BeginOperation(OperationScope{Operation: "read", Declared: true, Capabilities: []string{"ring4.memory"}})
	wantErrorReason(t, dispatch(t, b, params), reasonNotInEnvelope)
	b.EndOperation()
	b.BeginOperation(OperationScope{Operation: "read", Declared: true, Effects: EffectsReadExternal, Capabilities: []string{capInteractionRead}})
	baseline, err := st.QueryInteractions(t.Context(), interaction.Query{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, frozen := range []bool{false, true} {
		safe = frozen
		st.SetFrozen(frozen, "test")
		m := dispatch(t, b, params)
		wantResult(t, m, statusSucceeded, "")
		var got interaction.Page
		if err = json.Unmarshal(m["operation_result"], &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, *baseline) || strings.Contains(string(m["operation_result"]), "private body") {
			t.Fatalf("reader changed audience/facts: %s", m["operation_result"])
		}
	}
	for _, args := range []string{`{"version":2}`, `{"version":1,"sql":"select secret"}`, `{"version":1,"limit":1000}`} {
		wantResult(t, dispatch(t, b, memParams(opInteractionQuery, args)), statusFailed, "")
	}
	h.ReplacePolicy(map[string]Grant{"p": {Memory: true}}, nil)
	wantErrorReason(t, dispatch(t, b, params), reasonPolicyDeny)
	wantErrorReason(t, dispatch(t, b, memParams(opInteractionRead, `{"version":1}`)), reasonPolicyDeny)
}

func TestInteractionReaderBoundsTheWholeEnvelope(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Interactions: st, Grants: map[string]Grant{"p": {Interactions: true}}})
	b := h.Bind("p", packagefmt.TierT1, []string{capInteractionRead})
	b.BeginOperation(OperationScope{Operation: strings.Repeat("x", 40000), Declared: true, Capabilities: []string{capInteractionRead}})
	wire, err := b.Dispatch(t.Context(), "invoke-call", []byte(memParams(opInteractionQuery, `{"version":1}`)))
	if err != nil || len(wire) > interaction.MaxPageBytes {
		t.Fatalf("unbounded envelope: %d %v", len(wire), err)
	}
	var reply map[string]json.RawMessage
	if err = json.Unmarshal(wire, &reply); err != nil {
		t.Fatal(err)
	}
	if string(reply["code"]) != "-32603" {
		t.Fatalf("oversized receipt did not refuse: %s", wire)
	}
}

// THE SIGNED ENVELOPE IS ASKED FIRST, AND IT IS THE ONLY ASKER HERE. The
// operator granted interaction history and the in-flight operation
// declares interaction.read — but the package's signed envelope does not
// carry it. Every later check passes, so only the envelope check refuses
// (an operation's declaration is not checked against the envelope when
// the package is loaded). Its control half: the same call with the
// envelope carrying the capability reads.
func TestAnOperationsDeclarationDoesNotStandInForTheEnvelope(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Interactions: st, Grants: map[string]Grant{"p": {Interactions: true}}})
	params := memParams(opInteractionQuery, `{"version":1}`)
	scope := OperationScope{Operation: "read", Declared: true, Effects: EffectsReadExternal, Capabilities: []string{capInteractionRead}}

	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.memory"})
	b.BeginOperation(scope)
	m := dispatch(t, b, params)
	wantErrorReason(t, m, reasonNotInEnvelope)
	if !strings.Contains(string(m["message"]), "not in the signed envelope") {
		t.Fatalf("the refusal is the envelope's: %s", m["message"])
	}

	b = h.Bind("p", packagefmt.TierT1, []string{capInteractionRead})
	b.BeginOperation(scope)
	wantResult(t, dispatch(t, b, params), statusSucceeded, "")
}
