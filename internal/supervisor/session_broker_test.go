package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A real broker denial must cross the resident lane as a JSON-RPC error,
// with its classification intact. The same lane must remain usable afterward.
func TestSessionPreservesBrokerDenialAndRecovery(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var safe atomic.Bool
	safe.Store(true)
	h, err := broker.New(broker.Config{
		Store: st, InSAFE: safe.Load,
		Grants: map[string]broker.Grant{"p": {KV: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	t.Cleanup(func() { _ = b.Close() })
	c, g, cleanup := newSessionPair(t, 8, b)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		cleanup()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("session reader did not exit")
		}
	})
	roundTrip := func(params string) map[string]json.RawMessage {
		t.Helper()
		// Five seconds for the exchange. A deadline cannot say it on every
		// platform — an os.Pipe on Windows takes none (os.ErrNoDeadline) —
		// so a stuck exchange is ended by closing the guest's ends, and
		// fails here instead of hanging the package.
		watchdog := time.AfterFunc(5*time.Second, func() {
			g.toHost.Close()
			g.fromH.Close()
		})
		defer watchdog.Stop()
		if err := bbb.WriteFrame(g.toHost, []byte(`{"jsonrpc":"2.0","id":"call-7","method":"invoke.call","params":`+params+`}`), bbb.MaxControlFrameBytes); err != nil {
			t.Fatal(err)
		}
		frame, err := bbb.ReadFrame(g.fromH, bbb.MaxControlFrameBytes)
		if err != nil {
			t.Fatal(err)
		}
		var reply map[string]json.RawMessage
		if err := json.Unmarshal(frame, &reply); err != nil {
			t.Fatal(err)
		}
		if string(reply["id"]) != `"call-7"` || string(reply["jsonrpc"]) != `"2.0"` {
			t.Fatalf("reply lost request correlation: %s", frame)
		}
		return reply
	}
	put := `{"operation":"kv.put","target":{"key":"note"},"arguments":{"value":"kept"}}`
	denial, err := b.Dispatch(ctx, "invoke-call", []byte(put))
	if err != nil || !isErrorObject(denial) {
		t.Fatalf("fixture must produce a broker denial: %s, %v", denial, err)
	}
	reply := roundTrip(put)
	if len(reply["result"]) != 0 || !bytes.Equal(reply["error"], denial) {
		t.Fatalf("session must preserve the broker denial in error, not result: %s; want %s", reply, denial)
	}

	// Recovery changes only the existing policy input, not the client or binding.
	safe.Store(false)
	reply = roundTrip(put)
	if len(reply["error"]) != 0 || !hasVal(reply["result"], "status", "succeeded") {
		t.Fatalf("same lane did not recover: %s", reply)
	}
	reply = roundTrip(`{"operation":"kv.get","target":{"key":"note"}}`)
	if len(reply["error"]) != 0 || !hasVal(rawField(reply["result"], "operation_result"), "value", "kept") {
		t.Fatalf("recovered write did not persist: %s", reply)
	}
	// An operation-level miss is a result, not a JSON-RPC denial.
	reply = roundTrip(`{"operation":"kv.get","target":{"key":"missing"}}`)
	if len(reply["error"]) != 0 || !hasVal(reply["result"], "status", "failed") {
		t.Fatalf("ordinary failed result was reclassified: %s", reply)
	}
}
