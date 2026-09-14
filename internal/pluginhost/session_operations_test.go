package pluginhost

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestAResidentEnginesNonSessionOperationsGoDownItsLane(t *testing.T) {
	p := newEpochProbe(t)
	inv := sessionInvoker{v: p.v}

	// .
	// .
	frame, err := json.Marshal(invokeRequest{JSONRPC: "2.0", ID: harnessRequestID, Method: "invoke.call",
		Params: invokeParams{Operation: "enroll", Arguments: map[string]interface{}{"label": "Sam", "finals": []interface{}{1, 2, 3}}}})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		reply []byte
		err   error
	}
	done := make(chan outcome, 1)
	go func() { r, e := inv.Invoke(p.ctx, frame); done <- outcome{r, e} }()

	// .
	raw, err := bbb.ReadFrame(p.reader, bbb.MaxControlFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Operation string                 `json:"operation"`
			Arguments map[string]interface{} `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Method != "invoke.call" || got.Params.Operation != "enroll" || got.Params.Arguments["label"] != "Sam" {
		t.Fatalf("the engine gets the operation it was asked for: %s", raw)
	}
	p.reply(got.ID, `{"enrolled":"Sam","speakers":1}`)

	out := <-done
	if out.err != nil {
		t.Fatalf("a resident operation is not a refusal: %v", out.err)
	}
	res, err := decodeReply(out.reply)
	if err != nil || res.Error != "" {
		t.Fatalf("the engine's result comes back as a tool reply: %v %q", err, res.Error)
	}
	if !strings.Contains(res.Text(), `"enrolled":"Sam"`) {
		t.Fatalf("the engine's own words: %q", res.Text())
	}
}

// .
// .
// .
func TestAResidentEnginesRefusalIsTheOperationsNotTheLanes(t *testing.T) {
	p := newEpochProbe(t)
	inv := sessionInvoker{v: p.v}
	frame, _ := json.Marshal(invokeRequest{JSONRPC: "2.0", ID: harnessRequestID, Method: "invoke.call",
		Params: invokeParams{Operation: "remove", Arguments: map[string]interface{}{"label": "nobody"}}})
	type outcome struct {
		reply []byte
		err   error
	}
	done := make(chan outcome, 1)
	go func() { r, e := inv.Invoke(p.ctx, frame); done <- outcome{r, e} }()
	id := p.read("remove")
	p.frames <- []byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"error":{"code":-32000,"message":"no speaker is enrolled under that label"}}`)

	out := <-done
	if out.err != nil {
		t.Fatalf("an engine that answered is not a transport failure: %v", out.err)
	}
	res, _ := decodeReply(out.reply)
	if !strings.Contains(res.Error, "no speaker is enrolled under that label") {
		t.Fatalf("the engine's own refusal reaches the caller: %+v", res)
	}
}

// .
// .
// .
// .
// .
func TestASlowResidentOperationDoesNotHoldTheSpeechControls(t *testing.T) {
	p := newEpochProbe(t)
	inv := sessionInvoker{v: p.v}
	frame, _ := json.Marshal(invokeRequest{JSONRPC: "2.0", ID: harnessRequestID, Method: "invoke.call",
		Params: invokeParams{Operation: "enroll", Arguments: map[string]interface{}{"label": "Sam"}}})
	slow := make(chan []byte, 1)
	go func() { r, _ := inv.Invoke(p.ctx, frame); slow <- r }()
	enrollID := p.read("enroll")

	// .
	opened := make(chan error, 1)
	go func() { opened <- p.v.open(p.ctx, "s1", map[string]any{}, nil) }()
	openID := p.read("speech.session.open")
	p.reply(openID, `{"accepted":true}`)
	if err := <-opened; err != nil {
		t.Fatalf("a speech control is admitted with an enrollment still outstanding: %v", err)
	}
	select {
	case <-slow:
		t.Fatal("the enroll returned before it was answered")
	default:
	}
	p.reply(enrollID, `{"enrolled":"Sam"}`)
	select {
	case reply := <-slow:
		res, _ := decodeReply(reply)
		if !strings.Contains(res.Text(), "Sam") {
			t.Fatalf("the enroll's own answer, after the control: %q", res.Text())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the enroll never completed")
	}
}

// .
// .
func TestAResidentOperationIsBoundedWhenTheCallerBroughtNoDeadline(t *testing.T) {
	p := newEpochProbe(t)
	inv := sessionInvoker{v: p.v}
	frame, _ := json.Marshal(invokeRequest{JSONRPC: "2.0", ID: harnessRequestID, Method: "invoke.call",
		Params: invokeParams{Operation: "list", Arguments: map[string]interface{}{}}})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := inv.Invoke(ctx, frame)
	if err == nil {
		t.Fatal("an unanswered operation must not succeed")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("the caller's own deadline was not honoured: %v", time.Since(start))
	}
	if SessionOperationCeiling <= 0 || SessionOperationCeiling > 5*time.Minute {
		t.Fatalf("the ceiling for a caller with no deadline is a bound, not a hang: %v", SessionOperationCeiling)
	}
	p.read("list")
}

// .
// .
// .
// .
// .
func TestOnlyTheSessionInterfacesControlsAreWithheldFromTheIdentity(t *testing.T) {
	if (&ActivePlugin{Voice: &VoiceSession{}}).offersOperationsFor(SessionInterfaceID) {
		t.Fatal("a resident engine's session controls are the host's — no tool is built for them")
	}
	if (&ActivePlugin{}).offersOperationsFor(SessionInterfaceID) {
		t.Fatal("the session interface says so, not the runtime that carried it")
	}
	if !(&ActivePlugin{Voice: &VoiceSession{}}).offersOperationsFor("speaker.uid") {
		t.Fatal("a voice package's OTHER interface is operations — enrollment had no path when this was false")
	}
	if !(&ActivePlugin{}).offersOperationsFor("aii.channel") {
		t.Fatal("every other interface offers its operations")
	}
}

// .
func TestAnOperationWithNoResidentLaneIsRefused(t *testing.T) {
	var inv sessionInvoker
	frame, _ := json.Marshal(invokeRequest{JSONRPC: "2.0", ID: harnessRequestID, Method: "invoke.call",
		Params: invokeParams{Operation: "list"}})
	if _, err := inv.Invoke(context.Background(), frame); err == nil || !strings.Contains(err.Error(), "no resident session lane") {
		t.Fatalf("no lane, no call: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
func TestAQueuedToolCallLeavesOnItsOwnDeadline(t *testing.T) {
	var gate opGate
	desc := &opDescriptor{effects: "read.internal"}
	held := make(chan struct{})
	blocking := &operationTool{name: "pl_slow", operation: "slow", plugin: "org.example.slow",
		inv: blockingInvoker(held), desc: desc, inFlight: &gate}
	go func() { _, _ = blocking.Execute(context.Background(), map[string]interface{}{}) }()

	// .
	for i := 0; i < 200 && gate.tryAcquire(); i++ {
		gate.release()
		time.Sleep(2 * time.Millisecond)
	}

	queued := &operationTool{name: "pl_next", operation: "next", plugin: "org.example.slow",
		inv: staticInvoker(`{"ok":true}`), desc: desc, inFlight: &gate}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := queued.Execute(ctx, map[string]interface{}{})
	elapsed := time.Since(start)
	close(held)
	if err != nil {
		t.Fatalf("a cancelled wait is a refusal, not a transport error: %v", err)
	}
	if !strings.Contains(res.Error, "nothing ran") {
		t.Fatalf("and it says nothing ran: %+v", res)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the queued call must leave on its own deadline: %v", elapsed)
	}
}

// .
type blockingInvoker chan struct{}

func (b blockingInvoker) Invoke(ctx context.Context, _ []byte) ([]byte, error) {
	select {
	case <-b:
	case <-ctx.Done():
	}
	return json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": harnessRequestID, "result": json.RawMessage(`{}`)})
}

// .
type staticInvoker string

func (s staticInvoker) Invoke(context.Context, []byte) ([]byte, error) {
	return json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": harnessRequestID, "result": json.RawMessage(s)})
}
