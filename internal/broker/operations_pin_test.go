package broker

import (
	"context"
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// pinnedClasses is which declared effect classes admit an operation.
type pinnedClasses int

const (
	// everyClass: any class, an unknown one and the undeclared "" included.
	everyClass pinnedClasses = iota + 1
	// localWrite: "", write.local, write.external, exec.
	localWrite
	// externalRead: "", read.external, write.external, exec.
	externalRead
	// externalWrite: "", write.external, exec.
	externalWrite
)

type pinnedOp struct {
	op string
	// passesSAFE: the dispatch gate lets it through while SAFE holds (its
	// handler may still refuse it there in its own words).
	passesSAFE bool
	// writes: refused under the operator's read-only grant.
	writes bool
	// storage: runs inside the plugin's storage admission.
	storage bool
	classes pinnedClasses
}

var pinnedOperations = []pinnedOp{
	{"kv.get", true, false, true, everyClass},
	{"kv.list", true, false, true, everyClass},
	{"kv.put", false, true, true, localWrite},
	{"kv.delete", false, true, true, localWrite},
	{"memory.recall", true, false, true, everyClass},
	{"memory.remember", false, true, true, localWrite},
	{"settings.get", true, false, false, everyClass},
	{"interaction.query", true, false, false, everyClass},
	{"interaction.read", true, false, false, everyClass},
	{"voice.observe", true, true, false, localWrite},
	{"embeddings.create", false, false, false, externalRead},
	{"http.get", false, false, false, externalRead},
	{"http.post", false, true, false, externalWrite},
	{"http.put", false, true, false, externalWrite},
	{"http.patch", false, true, false, externalWrite},
	{"http.delete", false, true, false, externalWrite},
	{"http.read", false, false, false, everyClass},
	{"http.close", false, false, false, everyClass},
	{"fs.list", true, false, true, everyClass},
	{"fs.read", true, false, true, everyClass},
	{"fs.write", true, true, true, localWrite},
	{"fs.delete", true, true, true, localWrite},
	{"fs.publish", true, true, true, localWrite},
	{"tools.publish", false, false, false, localWrite},
	{"tools.withdraw", false, false, false, localWrite},
}

// The two names no operation holds: an unknown name is refused by the
// registry's default, except that SAFE's gate answers first for any name
// outside the fs family — an unknown fs name passes SAFE's gate and is then
// refused as unknown (pinned as it stands).
const (
	unknownOp   = "kv.rename"
	unknownFSOp = "fs.rename"
)

var pinnedEffectClasses = []string{"", EffectsReadInternal, EffectsReadExternal, EffectsWriteLocal, EffectsWriteExternal, EffectsExec, "write"}

func (c pinnedClasses) admits(effects string) bool {
	switch c {
	case everyClass:
		return true
	case localWrite:
		return effects == "" || effects == "write.local" || effects == "write.external" || effects == "exec"
	case externalRead:
		return effects == "" || effects == "read.external" || effects == "write.external" || effects == "exec"
	case externalWrite:
		return effects == "" || effects == "write.external" || effects == "exec"
	}
	return false
}

func pinnedDenial(message, reason string) string {
	return fmt.Sprintf(`{"code":-32000,"message":%q,"data":{"reasonCode":%q,"denied_at":"capability_evaluation"}}`, message, reason)
}

func safeDenial(op string) string {
	return pinnedDenial("this identity is in SAFE; "+op+" is refused while it holds", "POLICY_DENY")
}

func readOnlyDenial(op string) string {
	return pinnedDenial("plugin p is granted read only; "+op+" writes", "POLICY_DENY")
}

func unknownDenial(op string) string {
	return pinnedDenial(fmt.Sprintf("operation %q is not allowed for any capability this broker serves", op), "OPERATION_NOT_ALLOWED_FOR_CAPABILITY")
}

const storageDenial = `{"code":-32000,"message":"this plugin's storage is retiring or being cleared","data":{"reasonCode":"POLICY_DENY","denied_at":"capability_evaluation"}}`

func rawDispatch(t *testing.T, b *Binding, op string) string {
	t.Helper()
	reply, err := b.Dispatch(context.Background(), methodInvokeCall, []byte(fmt.Sprintf(`{"operation":%q}`, op)))
	if err != nil {
		t.Fatalf("%s: Dispatch: %v", op, err)
	}
	return string(reply)
}

// While SAFE holds the dispatch gate refuses exactly the operations the
// table says, in the same bytes; the others reach their handlers.
func TestEveryOperationsSAFEAnswerIsPinned(t *testing.T) {
	safe := true
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {KV: true}}, InSAFE: func() bool { return safe }})
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	for _, row := range pinnedOperations {
		got := rawDispatch(t, b, row.op)
		if refused := got == safeDenial(row.op); refused == row.passesSAFE {
			t.Errorf("%s: refused by SAFE's gate = %v, pinned %v\n%s", row.op, refused, !row.passesSAFE, got)
		}
		if got == unknownDenial(row.op) {
			t.Errorf("%s reached the registry's unknown-operation refusal", row.op)
		}
	}
	if got := rawDispatch(t, b, unknownOp); got != safeDenial(unknownOp) {
		t.Errorf("an unknown name is refused by SAFE's gate first: %s", got)
	}
	if got := rawDispatch(t, b, unknownFSOp); got != unknownDenial(unknownFSOp) {
		t.Errorf("an unknown fs name passes SAFE's gate and is refused as unknown: %s", got)
	}

	// The control: lifted, SAFE's gate refuses nothing.
	safe = false
	for _, row := range pinnedOperations {
		if got := rawDispatch(t, b, row.op); got == safeDenial(row.op) {
			t.Errorf("%s refused by SAFE's gate outside SAFE", row.op)
		}
	}
	if got := rawDispatch(t, b, unknownOp); got != unknownDenial(unknownOp) {
		t.Errorf("outside SAFE an unknown name is refused as unknown: %s", got)
	}
}

// Under the operator's read-only grant exactly the writing operations are
// refused, in the same bytes.
func TestEveryOperationsReadOnlyAnswerIsPinned(t *testing.T) {
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {KV: true, ReadOnly: true}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	for _, row := range pinnedOperations {
		got := rawDispatch(t, b, row.op)
		if refused := got == readOnlyDenial(row.op); refused != row.writes {
			t.Errorf("%s: refused under read only = %v, pinned %v\n%s", row.op, refused, row.writes, got)
		}
	}
	for _, op := range []string{unknownOp, unknownFSOp} {
		if got := rawDispatch(t, b, op); got != unknownDenial(op) {
			t.Errorf("%s: an unknown name is refused as unknown under read only: %s", op, got)
		}
	}
	// The control: without the restriction no operation reads as a write.
	h2 := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {KV: true}}})
	b2 := h2.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	for _, row := range pinnedOperations {
		if got := rawDispatch(t, b2, row.op); got == readOnlyDenial(row.op) {
			t.Errorf("%s refused as a write without read only", row.op)
		}
	}
}

// While the plugin's storage is being cleared exactly the storage
// operations are refused, in the same bytes.
func TestEveryOperationsStorageAdmissionIsPinned(t *testing.T) {
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {KV: true}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	b.lifecycleMu.Lock()
	b.cleaning = make(chan struct{})
	b.lifecycleMu.Unlock()
	t.Cleanup(func() {
		b.lifecycleMu.Lock()
		b.cleaning = nil
		b.lifecycleMu.Unlock()
	})
	for _, row := range pinnedOperations {
		got := rawDispatch(t, b, row.op)
		if refused := got == storageDenial; refused != row.storage {
			t.Errorf("%s: refused while storage clears = %v, pinned %v\n%s", row.op, refused, row.storage, got)
		}
	}
	for _, op := range []string{unknownOp, unknownFSOp} {
		if got := rawDispatch(t, b, op); got != unknownDenial(op) {
			t.Errorf("%s: an unknown name is refused as unknown while storage clears: %s", op, got)
		}
	}
}

// Which declared effect classes admit each operation, the undeclared ""
// and a class no author contract names included; an unknown operation is
// admitted by none.
func TestEveryOperationsEffectClassAnswerIsPinned(t *testing.T) {
	for _, row := range pinnedOperations {
		for _, effects := range pinnedEffectClasses {
			if got, want := effectsAllow(effects, row.op), row.classes.admits(effects); got != want {
				t.Errorf("%s under %q: admitted = %v, pinned %v", row.op, effects, got, want)
			}
		}
	}
	for _, op := range []string{unknownOp, unknownFSOp, ""} {
		for _, effects := range pinnedEffectClasses {
			if effectsAllow(effects, op) {
				t.Errorf("unknown %q admitted under %q", op, effects)
			}
		}
	}

	// The class's refusal, in its bytes, where a call meets it.
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {KV: true}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	b.BeginOperation(OperationScope{Operation: "notes.look", Effects: EffectsReadInternal})
	defer b.EndOperation()
	want := pinnedDenial("operation notes.look declares effects read.internal; kv.put is outside that class", "POLICY_DENY")
	reply, err := b.Dispatch(context.Background(), methodInvokeCall, []byte(`{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`))
	if err != nil || string(reply) != want {
		t.Fatalf("the class's refusal changed:\n got %s %v\nwant %s", reply, err, want)
	}
}

// The operations table holds exactly the pinned operations: one added to
// the table without its row here, or pinned here and missing there, fails.
func TestTheOperationsTableHoldsExactlyThePinnedOperations(t *testing.T) {
	pinned := map[string]bool{}
	for _, row := range pinnedOperations {
		pinned[row.op] = true
		if _, ok := operations[row.op]; !ok {
			t.Errorf("%s is pinned and has no row in operations", row.op)
		}
	}
	for op := range operations {
		if !pinned[op] {
			t.Errorf("%s has a row in operations and none in the pin", op)
		}
	}
}
