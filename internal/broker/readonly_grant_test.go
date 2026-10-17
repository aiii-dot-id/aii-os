package broker

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func TestReadOnlyGrantRefusesAWriteFromAnyLane(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {KV: true, ReadOnly: true}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})

	// No operation is in flight — this is the resident lane's own call.
	m := dispatch(t, b, `{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`)
	wantErrorReason(t, m, reasonPolicyDeny)

	// A read is exactly what read-only means, and it still works.
	got := dispatch(t, b, `{"operation":"kv.get","target":{"key":"k"}}`)
	if _, isErr := got["error"]; isErr {
		t.Fatalf("read only must still read: %v", got)
	}

	// The same plugin without the restriction writes as before, so the
	// refusal is the operator's word and not a broken path.
	h2 := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {KV: true}}})
	b2 := h2.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	if m2 := dispatch(t, b2, `{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`); func() bool { _, e := m2["error"]; return e }() {
		t.Fatalf("without read only the write stands: %v", m2)
	}
}
