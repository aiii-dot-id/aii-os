package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

type mockDispatcher struct{ called chan string }

func (m *mockDispatcher) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	select {
	case m.called <- method:
	default:
	}
	return []byte(`{"ok":true}`), nil
}

// .
// .
// .
type guestEnd struct {
	t      *testing.T
	toHost *os.File
	fromH  *os.File
}

func (g *guestEnd) readControl() (id json.RawMessage, op string) {
	g.t.Helper()
	g.fromH.SetReadDeadline(time.Now().Add(5 * time.Second))
	f, err := bbb.ReadFrame(g.fromH, bbb.MaxControlFrameBytes)
	if err != nil {
		g.t.Fatalf("guest read control: %v", err)
	}
	var m struct {
		ID     json.RawMessage `json:"id"`
		Params struct {
			Operation string `json:"operation"`
		} `json:"params"`
	}
	json.Unmarshal(f, &m)
	return m.ID, m.Params.Operation
}
func (g *guestEnd) admit(id json.RawMessage, result string) {
	g.t.Helper()
	f := append([]byte(`{"jsonrpc":"2.0","id":`), id...)
	f = append(f, []byte(`,"result":`+result+`}`)...)
	if err := bbb.WriteFrame(g.toHost, f, bbb.MaxControlFrameBytes); err != nil {
		g.t.Fatalf("guest admit: %v", err)
	}
}
func (g *guestEnd) event(params string) {
	g.t.Helper()
	f := []byte(`{"jsonrpc":"2.0","method":"session.event","params":` + params + `}`)
	if err := bbb.WriteFrame(g.toHost, f, bbb.MaxControlFrameBytes); err != nil {
		g.t.Fatalf("guest event: %v", err)
	}
}

func newSessionPair(t *testing.T, eventBuf int, disp Dispatcher) (*SessionClient, *guestEnd, func()) {
	h2g_r, h2g_w, _ := os.Pipe()
	g2h_r, g2h_w, _ := os.Pipe()
	c := NewSessionClient(g2h_r, h2g_w, disp, eventBuf)
	g := &guestEnd{t: t, toHost: g2h_w, fromH: h2g_r}
	return c, g, func() { h2g_w.Close(); g2h_w.Close(); h2g_r.Close(); g2h_r.Close() }
}

// .
// .
func TestSessionClientAdmitsObservesAndDispatches(t *testing.T) {
	disp := &mockDispatcher{called: make(chan string, 1)}
	c, g, cleanup := newSessionPair(t, 8, disp)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// .
	type res struct {
		r   json.RawMessage
		err error
	}
	rc := make(chan res, 1)
	go func() {
		r, err := c.Control(ctx, "speech.session.open", map[string]any{"session_id": "s1"})
		rc <- res{r, err}
	}()
	id, op := g.readControl()
	if op != "speech.session.open" {
		t.Fatalf("guest saw op %q", op)
	}
	g.admit(id, `{"accepted":true,"state":"opening"}`)
	select {
	case got := <-rc:
		if got.err != nil || !hasKey(got.r, "accepted") {
			t.Fatalf("admission: %v %s", got.err, got.r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Control never returned on admission")
	}

	// .
	g.event(`{"type":"transcript_final","sequence":3}`)
	select {
	case ev := <-c.Events():
		if !hasVal(ev, "type", "transcript_final") {
			t.Fatalf("event: %s", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event never observed")
	}

	// .
	up := []byte(`{"jsonrpc":"2.0","id":77,"method":"invoke.call","params":{"operation":"kv.get","arguments":{}}}`)
	bbb.WriteFrame(g.toHost, up, bbb.MaxControlFrameBytes)
	select {
	case <-disp.called:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream hostcall was never dispatched")
	}
	g.fromH.SetReadDeadline(time.Now().Add(5 * time.Second))
	rf, err := bbb.ReadFrame(g.fromH, bbb.MaxControlFrameBytes)
	if err != nil || !hasKey(rawField(rf, "result"), "ok") {
		t.Fatalf("the host answered the upstream call: %v %s", err, rf)
	}
}

// .
// .
func TestSessionClientSlowObserverDoesNotBlockControl(t *testing.T) {
	c, g, cleanup := newSessionPair(t, 2, &mockDispatcher{called: make(chan string, 1)})
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// .
	for i := 0; i < 6; i++ {
		g.event(fmt.Sprintf(`{"type":"vad_probability","sequence":%d}`, i))
	}
	// .
	rc := make(chan error, 1)
	go func() { _, err := c.Control(ctx, "speech.session.status", nil); rc <- err }()
	id, _ := g.readControl()
	g.admit(id, `{"lifecycle":"open"}`)
	select {
	case err := <-rc:
		if err != nil {
			t.Fatalf("control blocked behind a slow observer: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("control never returned — the flood blocked the reader")
	}
	if c.Dropped() == 0 {
		t.Fatal("a full observer buffer must shed events and count them")
	}
}

func hasKey(raw json.RawMessage, key string) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && len(m[key]) != 0
}
func hasVal(raw json.RawMessage, key, val string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	var s string
	return json.Unmarshal(m[key], &s) == nil && s == val
}
func rawField(raw json.RawMessage, key string) json.RawMessage {
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)
	return m[key]
}
