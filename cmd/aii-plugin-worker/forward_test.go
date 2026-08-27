package main

// Forward-mode wire tests: this suite PLAYS THE HOST against the real
// worker binary over real pipes — the other half of the supervised
// channel, proven without a supervisor so the worker's own framing
// obligations stand alone. The caller.wasm fixture forwards its whole
// request frame as invoke-call params and answers with whatever reply
// bytes came back, making every layer visible on the wire.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

func writeHostFrame(t *testing.T, w *workerProc, frame string) {
	t.Helper()
	if err := bbb.WriteFrame(w.stdin, []byte(frame), bbb.MaxControlFrameBytes); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func readChildFrame(t *testing.T, w *workerProc) []byte {
	t.Helper()
	frame, err := bbb.ReadFrame(w.stdout, bbb.MaxControlFrameBytes)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return frame
}

func TestForwardModeRoundTripsGuestHostcalls(t *testing.T) {
	w := startWorker(t, "-forward", fixture("caller.wasm"))

	for i, want := range []struct{ id, reply, final string }{
		{`"w1"`, `{"jsonrpc":"2.0","id":"w1","result":{"relayed":"first"}}`, `{"relayed":"first"}`},
		// Ids increment per hostcall; a denial error object relays to
		// the guest exactly like a result — the ADR-033 mapping.
		{`"w2"`, `{"jsonrpc":"2.0","id":"w2","error":{"code":-32000,"message":"denied","data":{"reasonCode":"POLICY_DENY"}}}`,
			`{"code":-32000,"message":"denied","data":{"reasonCode":"POLICY_DENY"}}`},
	} {
		req := `{"jsonrpc":"2.0","id":"h1","method":"invoke.call","params":{"round":` + want.id + `}}`
		writeHostFrame(t, w, req)

		// The worker must serialize the guest's hostcall as a REQUEST
		// frame: wire method spelling, minted string id, the guest's
		// params (here: the whole original frame) embedded verbatim.
		upstream := readChildFrame(t, w)
		var env struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(upstream, &env); err != nil {
			t.Fatalf("round %d: upstream not JSON: %v", i, err)
		}
		if env.JSONRPC != "2.0" || env.Method != "invoke.call" {
			t.Fatalf("round %d: upstream must be an invoke.call request, got %s", i, upstream)
		}
		if string(env.ID) != want.id {
			t.Fatalf("round %d: minted id = %s, want %s (string ids, incrementing — the C SDK client precedent)", i, env.ID, want.id)
		}
		if !bytes.Equal(env.Params, []byte(req)) {
			t.Fatalf("round %d: params must embed the guest's bytes verbatim:\n%s\n%s", i, env.Params, req)
		}

		// Answer; the guest returns the member bytes as its response.
		writeHostFrame(t, w, want.reply)
		final := readChildFrame(t, w)
		if string(final) != want.final {
			t.Fatalf("round %d: guest must receive the %s member bytes verbatim, got %s", i,
				map[bool]string{true: "error", false: "result"}[strings.Contains(want.reply, "error")], final)
		}
	}

	if err := w.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if code := w.exitCode(t); code != 0 {
		t.Fatalf("clean EOF after forwarded rounds must exit 0, got %d\nstderr:\n%s", code, w.stderr)
	}
	if !strings.Contains(w.stderr.String(), "forward=true") {
		t.Errorf("the ready banner must record forward mode:\n%s", w.stderr)
	}
}

func TestForwardModeHostIDMismatchIsFatal(t *testing.T) {
	w := startWorker(t, "-forward", fixture("caller.wasm"))
	writeHostFrame(t, w, `{"jsonrpc":"2.0","id":"h1","method":"invoke.call","params":{}}`)
	_ = readChildFrame(t, w) // the upstream request
	// Echo the WRONG id: the guest's hostcall must fail hard — a
	// lawless stream has no resync; the worker dies for restart.
	writeHostFrame(t, w, `{"jsonrpc":"2.0","id":"w999","result":{}}`)
	if code := w.exitCode(t); code != 3 {
		t.Fatalf("id mismatch must be fatal (exit 3), got %d\nstderr:\n%s", code, w.stderr)
	}
	if !strings.Contains(w.stderr.String(), "byte-form verbatim") {
		t.Errorf("the fatal line must name the id rule:\n%s", w.stderr)
	}
}

func TestWithoutForwardFlagHostcallsStayDenied(t *testing.T) {
	// The deny-all DEFAULT survives the feature: no -forward, no
	// upstream frame ever appears — the caller guest's hostcall is
	// answered in-process with the audited denial, which the guest
	// relays as its response.
	w := startWorker(t, fixture("caller.wasm"))
	writeHostFrame(t, w, `{"jsonrpc":"2.0","id":"h1","method":"invoke.call","params":{}}`)
	final := readChildFrame(t, w)
	for _, want := range []string{"POLICY_DENY", "-32000"} {
		if !strings.Contains(string(final), want) {
			t.Fatalf("without -forward the audited denial must relay (%s): %s", want, final)
		}
	}
	if err := w.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if code := w.exitCode(t); code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
}
