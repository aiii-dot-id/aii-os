package broker

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func TestAHostReceiptThatDidNotLandIsSaid(t *testing.T) {
	lines := logsink.CaptureForTest(t)
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"t0": {KV: true}}})
	b := h.Bind("t0", packagefmt.TierT0, []string{"ring4.kv"})
	st.SetFrozen(true, "test: SAFE")
	b.InvokeRefused("summarize", "the answer was refused", "")
	st.SetFrozen(false, "")
	if !lines.Contains("broker.error") || !lines.Contains("receipt of a refused summarize answer was not recorded") {
		t.Fatalf("a receipt that did not land left no trace:\n%s", lines.String())
	}
}
