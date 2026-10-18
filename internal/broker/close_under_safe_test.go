package broker

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// AN ACTIVATION THAT ENDS UNDER SAFE CLOSES CLEANLY. Its temporary rows
// cannot be deleted while SAFE freezes the projection, and they need not
// be: they belong to an activation that never runs again, and cold start
// clears every expired one. The refusal failed the close, so a SAFE
// shutdown with a plugin up read as a teardown that did not complete.
func TestAnActivationClosedUnderSAFELeavesItsTemporaryRowsAndClosesCleanly(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"t0": {KV: true}}})
	b := h.Bind("t0", packagefmt.TierT0, []string{"ring4.kv"})
	dispatch(t, b, kvParams("kv.put", "k", "ephemeral"))
	scope := b.storageScope()

	st.SetFrozen(true, "test: SAFE")
	if err := b.Close(); err != nil {
		t.Fatalf("closing an activation under SAFE: %v", err)
	}
	st.SetFrozen(false, "")
	if _, found, err := st.PluginKVGet(scope, "k"); err != nil || !found {
		t.Fatalf("SAFE wrote: the temporary row is gone (%v, %v)", found, err)
	}
}
