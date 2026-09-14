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

// .
// .
type mockEngine struct {
	mu        sync.Mutex
	seen      []string
	statusSeq atomic.Int64
	playback  atomic.Value
	wmu       sync.Mutex
	toHost    *os.File
}

// .
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

	// .
	snap, err := v.Status(ctx)
	if err != nil || snap.StateSequence != 10 {
		t.Fatalf("status: %v %+v", err, snap)
	}
	if got := v.Label(); got != "Speaking" {
		t.Fatalf("label with playback playing = %q, want Speaking", got)
	}

	// .
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

	// .
	eng.statusSeq.Store(5)
	snap, _ = v.Status(ctx)
	if snap.StateSequence != 10 {
		t.Fatalf("a stale status (seq 5) must be discarded; kept %d", snap.StateSequence)
	}

	// .
	before := len(eng.ops())
	if err := v.Close(ctx, "drain", "done"); err == nil {
		t.Fatal("drain close without finish_input must be refused")
	}
	if len(eng.ops()) != before {
		t.Fatal("a refused drain close must not reach the engine")
	}
	// .
	if err := v.FinishInput(ctx, "microphone", 48000); err != nil {
		t.Fatalf("finish_input: %v", err)
	}
	if err := v.Close(ctx, "drain", "done"); err != nil {
		t.Fatalf("drain close after cutoff: %v", err)
	}
	// .
	// .
	// .
	if _, err := v.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if got := v.Label(); got != "Draining" {
		t.Fatalf("label after close ADMISSION with playback playing = %q, want Draining", got)
	}
	if !v.IsOpen() {
		t.Fatal("a closing session is still open until the engine reports terminal closure")
	}
	// .
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
