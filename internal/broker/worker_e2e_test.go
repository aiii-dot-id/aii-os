package broker

// The broker behind the REAL wall: a wazero guest performs its effects
// through the aiii:bbb/bbb invoke-call import, the binding evaluates
// per call, and everything the guest sees came back through the
// canonical lowering. The caller guest forwards its request bytes
// verbatim as invoke-call params, so each Invoke drives one brokered
// call end to end.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

func TestBrokerThroughWazeroWall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"fetched":true}`)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)

	st := newStore(t)
	h := newHost(t, st, Config{
		Grants: map[string]Grant{"org.example.caller": {
			KV: true, Hosts: []string{fmt.Sprintf("%s:%d", host, port)},
		}},
		Guard: guardFor(ts),
	})
	b := h.Bind("org.example.caller", packagefmt.TierT1,
		[]string{fmt.Sprintf("net.outbound:%s:%d", host, port), "ring4.kv"})

	mod, err := pluginworker.Load(context.Background(), wasmgen.Caller(), pluginworker.Config{Dispatcher: b})
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(context.Background())

	invoke := func(params string) map[string]json.RawMessage {
		t.Helper()
		reply, err := mod.Invoke(context.Background(), []byte(params))
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(reply, &m); err != nil {
			t.Fatalf("guest relayed non-JSON: %v\n%s", err, reply)
		}
		return m
	}

	// kv round trip through linear memory.
	wantResult(t, invoke(kvParams("kv.put", "state", "warm")), statusSucceeded, "")
	m := invoke(kvParams("kv.get", "state", ""))
	wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(string(m["operation_result"]), `"warm"`) {
		t.Fatalf("kv value must survive the wall: %s", m["operation_result"])
	}

	// A real network fetch through the wall, receipt injected.
	m = invoke(netParams(ts.URL+"/wall", ""))
	rec := wantResult(t, m, statusSucceeded, "")
	if string(rec["plugin_id"]) != `"org.example.caller"` {
		t.Fatalf("the receipt names the bound plugin, got %s", rec["plugin_id"])
	}

	// And the lattice still bites through the wall: an unenveloped host
	// comes back as the audited error object, data for the guest.
	m = invoke(netParams("https://unenveloped.example.test/", ""))
	wantErrorReason(t, m, reasonNotInEnvelope)

	recs, err := st.PluginReceipts("org.example.caller")
	if err != nil || len(recs) != 3 {
		t.Fatalf("three brokered results → three receipts, got %d (%v)", len(recs), err)
	}
}
