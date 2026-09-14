package broker

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestReadOnlyGrantRefusesAWriteFromAnyLane(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {KV: true, ReadOnly: true}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})

	// .
	m := dispatch(t, b, `{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`)
	wantErrorReason(t, m, reasonPolicyDeny)

	// .
	got := dispatch(t, b, `{"operation":"kv.get","target":{"key":"k"}}`)
	if _, isErr := got["error"]; isErr {
		t.Fatalf("read only must still read: %v", got)
	}

	// .
	// .
	h2 := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {KV: true}}})
	b2 := h2.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	if m2 := dispatch(t, b2, `{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`); func() bool { _, e := m2["error"]; return e }() {
		t.Fatalf("without read only the write stands: %v", m2)
	}
}

// .
// .
func TestEveryMutatingOperationIsNamedAsMutating(t *testing.T) {
	for _, op := range []string{opKVPut, opKVDelete, opMemoryRemember, opVoiceObserve,
		opFSWrite, opFSDelete, opFSPublish, opHTTPPost, opHTTPPut, opHTTPPatch, opHTTPDelete} {
		if !opMutates(op) {
			t.Errorf("%s writes and must be refused under a read-only grant", op)
		}
	}
	for _, op := range []string{opKVGet, opKVList, opMemoryRecall, opSettingsGet,
		opFSList, opFSRead, opHTTPGet, opHTTPRead, opHTTPClose, opEmbeddingsCreate} {
		if opMutates(op) {
			t.Errorf("%s reads and must not be refused under a read-only grant", op)
		}
	}
}
