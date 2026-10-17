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

// guestEnd is the test playing the native engine: it reads control
// requests the host sends and writes admission responses, events and
// upstream hostcalls back.
type guestEnd struct {
	t      *testing.T
	toHost *os.File // guest -> host (client.in reads this)
	fromH  *os.File // host -> guest (client.out wrote this)
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

// Control returns on admission, an event reaches the observer, and a
// guest upstream hostcall is dispatched and answered (V-B).
func TestSessionClientAdmitsObservesAndDispatches(t *testing.T) {
	disp := &mockDispatcher{called: make(chan string, 1)}
	c, g, cleanup := newSessionPair(t, 8, disp)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// Admission.
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

	// Observation.
	g.event(`{"type":"transcript_final","sequence":3}`)
	select {
	case ev := <-c.Events():
		if !hasVal(ev, "type", "transcript_final") {
			t.Fatalf("event: %s", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event never observed")
	}

	// Guest upstream hostcall, dispatched and answered.
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

// A slow observer that never drains Events() must not stall the control
// reader: events are shed (Dropped counts them) and control still flows.
func TestSessionClientSlowObserverDoesNotBlockControl(t *testing.T) {
	c, g, cleanup := newSessionPair(t, 2, &mockDispatcher{called: make(chan string, 1)})
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// Flood events without reading them: 2 buffer, the rest are shed.
	for i := 0; i < 6; i++ {
		g.event(fmt.Sprintf(`{"type":"vad_probability","sequence":%d}`, i))
	}
	// Control still answers — the reader was never blocked by the flood.
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

func TestSessionClientForwardsAnUndecodableEventToTheDriver(t *testing.T) {
	c, g, cleanup := newSessionPair(t, 2, &mockDispatcher{called: make(chan string, 1)})
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	g.event(`{"type":"vad_probability","sequence":1}`)
	g.event(`{"type":"vad_probability","sequence":2}`)
	g.event(`{"type":5,"sequence":3}`)
	deadline := time.Now().Add(5 * time.Second)
	for c.Dropped() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Dropped() != 1 {
		t.Fatalf("dropped %d, want the one telemetry event the undecodable displaced", c.Dropped())
	}
	next := func() string {
		t.Helper()
		select {
		case ev := <-c.Events():
			return string(ev)
		case <-time.After(5 * time.Second):
			t.Fatal("an event never reached the driver")
			return ""
		}
	}
	if got := next(); got != `{"type":"vad_probability","sequence":2}` {
		t.Fatalf("first = %s", got)
	}
	if got := next(); got != `{"type":5,"sequence":3}` {
		t.Fatalf("the undecodable event = %s, want it forwarded as sent", got)
	}
	for _, params := range []string{`[1]`, `null`, `"transcript_final"`, `{"type":"transcript_partial","text":5}`} {
		g.event(params)
		if got := next(); got != params {
			t.Fatalf("forwarded %s, want %s", got, params)
		}
	}
	if c.Faulted() {
		t.Fatalf("an undecodable event faulted the transport: %s", c.FaultReason())
	}
}

func TestSessionControlParamsRejectsMalformedEnvelopeBeforeAdmission(t *testing.T) {
	c := &SessionClient{}
	for _, raw := range []string{"", "null", "[]", `{}`, `{"operation":null}`, `{"operation":12}`} {
		if _, err := c.ControlParams(t.Context(), json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid operation admitted: %s", raw)
		}
	}
}
