package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

type nopDispatcher struct{}

func (nopDispatcher) Dispatch(context.Context, string, []byte) ([]byte, error) {
	return []byte(`{}`), nil
}

// mockEngine answers every control op with an admission and records the
// ops it saw; status returns a snapshot at the current sequence.
type mockEngine struct {
	mu        sync.Mutex
	seen      []string
	statusSeq atomic.Int64
	// completion, when set, is the input_completion status carries (JSON).
	completion atomic.Value // string
	playback   atomic.Value // string: playing|idle
	wmu        sync.Mutex
	toHost     *os.File
}

// emit sends a notification (an observation) to the host.
func (m *mockEngine) emit(params string) {
	m.wmu.Lock()
	defer m.wmu.Unlock()
	_ = bbb.WriteFrame(m.toHost, []byte(`{"jsonrpc":"2.0","method":"session.event","params":`+params+`}`), bbb.MaxControlFrameBytes)
}

func (m *mockEngine) ops() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.seen...)
}

func (m *mockEngine) serve(fromHost, toHost *os.File) {
	m.wmu.Lock()
	m.toHost = toHost
	m.wmu.Unlock()
	for {
		f, err := bbb.ReadFrame(fromHost, bbb.MaxControlFrameBytes)
		if err != nil {
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Params struct {
				Operation string `json:"operation"`
			} `json:"params"`
		}
		_ = json.Unmarshal(f, &req)
		m.mu.Lock()
		m.seen = append(m.seen, req.Params.Operation)
		m.mu.Unlock()
		result := `{"accepted":true}`
		if req.Params.Operation == "speech.session.status" {
			pb, _ := m.playback.Load().(string)
			result = fmt.Sprintf(`{"session_id":"s1","state_sequence":%d,"lifecycle":"open","input":{"state":"accepting"},"recognition":{"utterance_open":true},"playback":{"state":%q,"synthesis_id":"x"}}`, m.statusSeq.Load(), pb)
			if c, _ := m.completion.Load().(string); c != "" {
				result = result[:len(result)-1] + `,"input_completion":` + c + `}`
			}
		}
		reply := append([]byte(`{"jsonrpc":"2.0","id":`), req.ID...)
		reply = append(reply, []byte(`,"result":`+result+`}`)...)
		m.wmu.Lock()
		_ = bbb.WriteFrame(toHost, reply, bbb.MaxControlFrameBytes)
		m.wmu.Unlock()
	}
}

func TestVoiceSessionInterruptIsBothAndDrainNeedsCutoffAndWatermark(t *testing.T) {
	h2g_r, h2g_w, _ := os.Pipe()
	g2h_r, g2h_w, _ := os.Pipe()
	t.Cleanup(func() { h2g_w.Close(); g2h_w.Close(); h2g_r.Close(); g2h_r.Close() })
	c := supervisor.NewSessionClient(g2h_r, h2g_w, nopDispatcher{}, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go c.Run(ctx)
	eng := &mockEngine{}
	eng.statusSeq.Store(10)
	eng.playback.Store("playing")
	go eng.serve(h2g_r, g2h_w)

	v := NewVoiceSession(c)
	if err := v.Open(ctx, "s1", nil); err != nil {
		t.Fatalf("open: %v", err)
	}

	// Speaking is derived from playback that is actually playing.
	snap, err := v.Status(ctx)
	if err != nil || snap.StateSequence != 10 {
		t.Fatalf("status: %v %+v", err, snap)
	}
	if got := v.Label(); got != "Speaking" {
		t.Fatalf("label with playback playing = %q, want Speaking", got)
	}

	// Interruption is BOTH operations, each independently admitted.
	if err := v.Interrupt(ctx, "x", "user_interruption"); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	ops := eng.ops()
	var sawStop, sawCancel bool
	for _, o := range ops {
		sawStop = sawStop || o == "speech.session.stop_playback"
		sawCancel = sawCancel || o == "speech.session.cancel_synthesis"
	}
	if !sawStop || !sawCancel {
		t.Fatalf("interrupt must dispatch both stop_playback and cancel_synthesis; engine saw %v", ops)
	}

	// A stale snapshot (lower sequence) never moves state backward.
	eng.statusSeq.Store(5)
	snap, _ = v.Status(ctx)
	if snap.StateSequence != 10 {
		t.Fatalf("a stale status (seq 5) must be discarded; kept %d", snap.StateSequence)
	}

	// A drain close is refused without a prior finish_input cutoff.
	before := len(eng.ops())
	if err := v.Close(ctx, "drain", "done"); err == nil {
		t.Fatal("drain close without finish_input must be refused")
	}
	if len(eng.ops()) != before {
		t.Fatal("a refused drain close must not reach the engine")
	}
	// With the cutoff recorded, the drain close is admitted.
	if err := v.FinishInput(ctx, "microphone", 48000); err != nil {
		t.Fatalf("finish_input: %v", err)
	}
	if err := v.Close(ctx, "drain", "done"); err != nil {
		t.Fatalf("drain close after cutoff: %v", err)
	}
	// Close ADMISSION is not closure: the engine still reports playback,
	// so the session is Draining and still open — neither the terminal
	// label nor the pin may advance on an acknowledgement.
	if _, err := v.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if got := v.Label(); got != "Draining" {
		t.Fatalf("label after close ADMISSION with playback playing = %q, want Draining", got)
	}
	if !v.IsOpen() {
		t.Fatal("a closing session is still open until the engine reports terminal closure")
	}
	// The engine's terminal word closes it.
	eng.emit(`{"type":"session_end","session_id":"s1","sequence":9}`)
	select {
	case <-v.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal event never closed the session")
	}
	if v.IsOpen() || v.Label() != "Closed" {
		t.Fatalf("after session_end: open=%v label=%q", v.IsOpen(), v.Label())
	}
}

// ONE PREDICATE PER VERDICT. A control's error is the engine's own refusal
// — which survives wrapping, as every caller wraps it — or an admission
// that is unknown, or a frame that never reached the engine; only the
// first proves nothing is happening there, and neither predicate reads
// another's verdict as its own.
func TestSessionRefusedIsTheEnginesOwnNoAndNothingElse(t *testing.T) {
	refusal := &supervisor.SessionRefusedError{Op: "speech.session.open", Err: json.RawMessage(`{"code":-32000,"message":"busy"}`)}
	cases := []struct {
		name             string
		err              error
		refused, unknown bool
	}{
		{"the engine's refusal", refusal, true, false},
		{"the engine's refusal, wrapped", fmt.Errorf("open: %w", refusal), true, false},
		{"an unknown admission", fmt.Errorf("open: %w", supervisor.ErrAdmissionUnknown), false, true},
		{"a frame never sent", fmt.Errorf("write: broken pipe"), false, false},
		{"no error", nil, false, false},
	}
	for _, c := range cases {
		if got := IsSessionRefused(c.err); got != c.refused {
			t.Errorf("%s: IsSessionRefused = %t, want %t", c.name, got, c.refused)
		}
		if got := IsAdmissionUnknown(c.err); got != c.unknown {
			t.Errorf("%s: IsAdmissionUnknown = %t, want %t", c.name, got, c.unknown)
		}
	}
}

// THE ENGINE'S NO CLOSES WHAT IT REFUSED. A refused open is a session
// that never existed at the engine; a status the engine refuses resolves
// an open whose admission was unknown — there is nothing to own. Both
// read the refusal through IsSessionRefused.
func TestARefusedOpenAndARefusedStatusCloseTheSession(t *testing.T) {
	refuse := func(p *epochProbe, id json.RawMessage) {
		p.frames <- []byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"error":{"code":-32000,"message":"no such session"}}`)
	}
	state := func(v *VoiceSession) (string, bool, string) {
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.admission, v.closed, v.closeWhy
	}
	t.Run("the open", func(t *testing.T) {
		p := newEpochProbe(t)
		done := make(chan error, 1)
		go func() { done <- p.v.Open(p.ctx, "s1", nil) }()
		refuse(p, p.read("speech.session.open"))
		if err := <-done; !IsSessionRefused(err) {
			t.Fatalf("the engine's no reaches the caller as its refusal: %v", err)
		}
		if admission, closed, why := state(p.v); admission != "refused" || !closed || why != "the engine refused the open" {
			t.Fatalf("a refused open is closed as refused: %q closed=%t %q", admission, closed, why)
		}
	})
	t.Run("a status after an unknown open", func(t *testing.T) {
		p := newEpochProbe(t)
		octx, cancel := context.WithCancel(p.ctx)
		done := make(chan error, 1)
		go func() { done <- p.v.Open(octx, "s1", nil) }()
		p.read("speech.session.open") // sent, never answered
		cancel()
		if err := <-done; !IsAdmissionUnknown(err) {
			t.Fatalf("an open sent and never answered is unknown: %v", err)
		}
		if admission, closed, _ := state(p.v); admission != "unknown" || closed {
			t.Fatalf("an unknown open stays open until resolved: %q closed=%t", admission, closed)
		}
		sdone := make(chan error, 1)
		go func() { _, err := p.v.Status(p.ctx); sdone <- err }()
		refuse(p, p.read("speech.session.status"))
		if err := <-sdone; !IsSessionRefused(err) {
			t.Fatalf("the engine's no to a status reaches the caller as its refusal: %v", err)
		}
		if _, closed, why := state(p.v); !closed || why != "the engine does not know the session (status refused)" {
			t.Fatalf("a refused status resolves the unknown open as closed: closed=%t %q", closed, why)
		}
	})
}
