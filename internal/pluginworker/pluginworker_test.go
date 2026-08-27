package pluginworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v (run `go generate ./internal/pluginworker`)", name, err)
	}
	return b
}

func mustLoad(t *testing.T, name string, cfg Config) *Module {
	t.Helper()
	m, err := Load(context.Background(), loadFixture(t, name), cfg)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	return m
}

// TestFixturesInSync enforces the reproducibility rule: the checked-in
// .wasm fixtures must be byte-identical to their wasmgen source.
func TestFixturesInSync(t *testing.T) {
	for name, want := range wasmgen.Fixtures() {
		got, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("fixture %s missing: %v (run `go generate ./internal/pluginworker`)", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("fixture %s drifted from wasmgen source (run `go generate ./internal/pluginworker`)", name)
		}
	}
}

func TestEchoRoundTrip(t *testing.T) {
	m := mustLoad(t, "echo.wasm", Config{})
	frame := []byte(`{"jsonrpc":"2.0","id":"1","method":"plugin.invoke","params":{"operation":"echo"}}`)
	got, err := m.Invoke(context.Background(), frame)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatalf("echo mismatch:\n got %q\nwant %q", got, frame)
	}
	// The optional post-return hook must have been honored exactly once.
	g := m.mod.ExportedGlobal("post-calls")
	if g == nil {
		t.Fatal("echo fixture must export the post-calls global")
	}
	if n := uint32(g.Get()); n != 1 {
		t.Fatalf("cabi_post_plugin-invoke called %d times, want 1", n)
	}
	// A second frame still round-trips: the instance carries state
	// across invokes (connection lifetime = process lifetime).
	frame2 := []byte(`{"jsonrpc":"2.0","id":"2","method":"plugin.invoke","params":{}}`)
	got, err = m.Invoke(context.Background(), frame2)
	if err != nil {
		t.Fatalf("second Invoke: %v", err)
	}
	if !bytes.Equal(got, frame2) {
		t.Fatalf("second echo mismatch: got %q", got)
	}
}

// TestStubDenialShape proves the fail-closed default: with no broker
// attached, a guest-outgoing invoke-call receives the audited denial —
// the JSON-RPC error object, code -32000, camelCase reasonCode
// (BBB_V2_AUDIT §8; DELTA_D1 §3: never -32001).
func TestStubDenialShape(t *testing.T) {
	m := mustLoad(t, "caller.wasm", Config{})
	got, err := m.Invoke(context.Background(), []byte(`{"params":true}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var denial struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			ReasonCode string `json:"reasonCode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got, &denial); err != nil {
		t.Fatalf("stub reply is not a JSON error object: %v (%q)", err, got)
	}
	if denial.Code != -32000 {
		t.Errorf("denial code %d, want -32000 (FORBIDDEN; F-2b forbids -32001)", denial.Code)
	}
	if denial.Data.ReasonCode != "POLICY_DENY" {
		t.Errorf("reasonCode %q, want POLICY_DENY", denial.Data.ReasonCode)
	}
	if denial.Message == "" {
		t.Error("denial must name the requirement in message (R39)")
	}
	// camelCase on the wire, not snake (AUDIT §8).
	if bytes.Contains(got, []byte("reason_code")) {
		t.Error("denial must spell reasonCode camelCase on this path")
	}
}

// TestDispatcherSeam proves step 4's replacement point: a custom
// dispatcher sees the WIT method name and raw params, and its reply
// reaches the guest byte-for-byte.
func TestDispatcherSeam(t *testing.T) {
	var gotMethod string
	var gotParams []byte
	reply := []byte(`{"ok":true,"status":"succeeded"}`)
	m := mustLoad(t, "caller.wasm", Config{Dispatcher: dispatcherFunc(func(_ context.Context, method string, params []byte) ([]byte, error) {
		gotMethod, gotParams = method, append([]byte(nil), params...)
		return reply, nil
	})})
	params := []byte(`{"operation":"http.get"}`)
	got, err := m.Invoke(context.Background(), params)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if gotMethod != "invoke-call" {
		t.Errorf("dispatcher saw method %q, want invoke-call", gotMethod)
	}
	if !bytes.Equal(gotParams, params) {
		t.Errorf("dispatcher saw params %q, want %q", gotParams, params)
	}
	if !bytes.Equal(got, reply) {
		t.Errorf("guest received %q, want dispatcher reply %q", got, reply)
	}
}

// TestDispatcherOversizeReply proves the outbound host-call ceiling:
// a broker reply above 1 MiB fails the guest call (C parity
// wasm_host.c:2251), it is never truncated or silently delivered.
func TestDispatcherOversizeReply(t *testing.T) {
	m := mustLoad(t, "caller.wasm", Config{Dispatcher: dispatcherFunc(func(context.Context, string, []byte) ([]byte, error) {
		return make([]byte, bbb.MaxControlFrameBytes+1), nil
	})})
	_, err := m.Invoke(context.Background(), []byte(`{}`))
	var fe *FrameTooLargeError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want FrameTooLargeError", err)
	}
	if fe.Direction != "host-to-guest" {
		t.Errorf("direction %q, want host-to-guest", fe.Direction)
	}
}

type dispatcherFunc func(context.Context, string, []byte) ([]byte, error)

func (f dispatcherFunc) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	return f(ctx, method, params)
}

func TestTrapTypedErrorAndPoisoning(t *testing.T) {
	m := mustLoad(t, "trap.wasm", Config{})
	_, err := m.Invoke(context.Background(), []byte(`{}`))
	var trap *TrapError
	if !errors.As(err, &trap) {
		t.Fatalf("err = %v, want TrapError", err)
	}
	if trap.Reason == "" {
		t.Error("TrapError must carry the trap reason")
	}
	// The module is retired: guest invariants are untrusted after a
	// fault (ADR-033:197).
	_, err = m.Invoke(context.Background(), []byte(`{}`))
	var unusable *ModuleUnusableError
	if !errors.As(err, &unusable) {
		t.Fatalf("post-trap err = %v, want ModuleUnusableError", err)
	}
}

func TestDeadlineKillsLoopGuest(t *testing.T) {
	m := mustLoad(t, "loop.wasm", Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.Invoke(ctx, []byte(`{}`))
	elapsed := time.Since(start)
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want TimeoutError", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("TimeoutError must unwrap to context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("deadline kill took %v; the loop guest must die promptly", elapsed)
	}
}

func TestMemoryHogTrapsAtCap(t *testing.T) {
	// A small cap keeps the one-page-at-a-time growth cheap; the
	// classification logic is cap-size independent.
	m := mustLoad(t, "memhog.wasm", Config{MemoryMaxBytes: 4 << 20})
	_, err := m.Invoke(context.Background(), []byte(`{}`))
	var re *ResourceLimitError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want ResourceLimitError", err)
	}
	if re.LimitBytes != 4<<20 {
		t.Errorf("LimitBytes = %d, want %d", re.LimitBytes, 4<<20)
	}
	var trap *TrapError
	if !errors.As(re.Cause, &trap) {
		t.Errorf("ResourceLimitError must keep the underlying trap, got %v", re.Cause)
	}
}

func TestForbiddenImportNamed(t *testing.T) {
	_, err := Load(context.Background(), loadFixture(t, "wasi.wasm"), Config{})
	var fi *ForbiddenImportError
	if !errors.As(err, &fi) {
		t.Fatalf("err = %v, want ForbiddenImportError", err)
	}
	if fi.Module != "wasi_snapshot_preview1" || fi.Name != "fd_write" {
		t.Errorf("rejection names %q.%q, want wasi_snapshot_preview1.fd_write", fi.Module, fi.Name)
	}
}

func TestWrongProtocolVersionRejected(t *testing.T) {
	_, err := Load(context.Background(), loadFixture(t, "wrongver.wasm"), Config{})
	var pv *ProtocolVersionError
	if !errors.As(err, &pv) {
		t.Fatalf("err = %v, want ProtocolVersionError", err)
	}
	if pv.Got != 1 {
		t.Errorf("Got = %d, want 1 (the fixture reports the RPC envelope version, the wrong number)", pv.Got)
	}
}

func TestOversizeRequestRejected(t *testing.T) {
	m := mustLoad(t, "echo.wasm", Config{})
	_, err := m.Invoke(context.Background(), make([]byte, bbb.MaxControlFrameBytes+1))
	var fe *FrameTooLargeError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want FrameTooLargeError", err)
	}
	if fe.Direction != "host-to-guest" {
		t.Errorf("direction %q, want host-to-guest", fe.Direction)
	}
	// Nothing entered the guest, so the module is NOT poisoned.
	if _, err := m.Invoke(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("module must survive an oversize request rejection, got %v", err)
	}
}

func TestOversizeResponseRejected(t *testing.T) {
	m := mustLoad(t, "bloat.wasm", Config{})
	_, err := m.Invoke(context.Background(), []byte(`{}`))
	var fe *FrameTooLargeError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want FrameTooLargeError", err)
	}
	if fe.Direction != "guest-to-host" {
		t.Errorf("direction %q, want guest-to-host", fe.Direction)
	}
	if fe.Size != bbb.MaxControlFrameBytes+1 {
		t.Errorf("Size = %d, want %d", fe.Size, bbb.MaxControlFrameBytes+1)
	}
}

func TestEmptyFrameRefused(t *testing.T) {
	m := mustLoad(t, "echo.wasm", Config{})
	if _, err := m.Invoke(context.Background(), nil); err == nil {
		t.Fatal("empty frame must be refused")
	}
}

func TestOnEventDeliveryAndSerialization(t *testing.T) {
	m := mustLoad(t, "event.wasm", Config{})
	if !m.HasOnEvent() {
		t.Fatal("event fixture must export on_event")
	}
	topic, payload := "observe.progress", []byte(`{"step":3}`)
	if err := m.DeliverEvent(context.Background(), topic, payload); err != nil {
		t.Fatalf("DeliverEvent: %v", err)
	}
	got, err := m.Invoke(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	want := append(append([]byte(topic), '\n'), payload...)
	if !bytes.Equal(got, want) {
		t.Fatalf("stash = %q, want %q", got, want)
	}

	// Bounded reentrancy under contention: the guest traps if the host
	// ever overlaps on_event with plugin-invoke (its guard global).
	// Hammering both from separate goroutines under -race proves the
	// invocation lock holds.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if err := m.DeliverEvent(context.Background(), "t", []byte(fmt.Sprintf("%d", i))); err != nil {
				errs <- fmt.Errorf("DeliverEvent #%d: %w", i, err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := m.Invoke(context.Background(), []byte(`{}`)); err != nil {
				errs <- fmt.Errorf("Invoke #%d: %w", i, err)
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("serialization violated: %v", err)
	}
}

func TestDeliverEventWithoutExport(t *testing.T) {
	m := mustLoad(t, "echo.wasm", Config{})
	if m.HasOnEvent() {
		t.Fatal("echo fixture must not export on_event")
	}
	if err := m.DeliverEvent(context.Background(), "t", nil); !errors.Is(err, ErrNoOnEvent) {
		t.Fatalf("err = %v, want ErrNoOnEvent", err)
	}
}

func TestCloseRetiresModule(t *testing.T) {
	m := mustLoad(t, "echo.wasm", Config{})
	if err := m.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, err := m.Invoke(context.Background(), []byte(`{}`))
	var unusable *ModuleUnusableError
	if !errors.As(err, &unusable) {
		t.Fatalf("post-Close err = %v, want ModuleUnusableError", err)
	}
}

// TestDispatcherErrorAnswers32603AndSurvives pins the blast-radius
// unification (design pass 2026-08-19): a dispatcher INTERNAL error is
// answered with the supervised bridge's exact -32603 object and the
// module keeps working — previously it trapped the guest and poisoned
// the module, so the same broker failure killed an in-process plugin
// but not a supervised one. Poison stays reserved for guest and
// containment faults.
func TestDispatcherErrorAnswers32603AndSurvives(t *testing.T) {
	calls := 0
	m := mustLoad(t, "caller.wasm", Config{Dispatcher: dispatcherFunc(func(context.Context, string, []byte) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("transient store failure")
		}
		return []byte(`{"ok":true,"status":"succeeded"}`), nil
	})})

	// First invoke: the guest's hostcall gets the -32603 object as its
	// reply; the invoke itself SUCCEEDS (caller.wasm embeds the reply).
	got, err := m.Invoke(context.Background(), []byte(`{"operation":"kv.get"}`))
	if err != nil {
		t.Fatalf("invoke during dispatcher failure must survive, got %v", err)
	}
	if !bytes.Contains(got, []byte(`"code":-32603`)) || !bytes.Contains(got, []byte(`"host dispatch failed"`)) {
		t.Fatalf("guest reply must carry the supervised bridge's -32603 object, got %s", got)
	}

	// Second invoke: the module was never poisoned.
	got, err = m.Invoke(context.Background(), []byte(`{"operation":"kv.get"}`))
	if err != nil {
		t.Fatalf("module must survive a dispatcher failure, got %v", err)
	}
	if !bytes.Contains(got, []byte(`"status":"succeeded"`)) {
		t.Fatalf("second invoke must reach the recovered dispatcher, got %s", got)
	}
}
