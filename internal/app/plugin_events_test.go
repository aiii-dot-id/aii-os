package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
type eventSink struct {
	name  string
	mu    sync.Mutex
	got   []map[string]interface{}
	block chan struct{}
}

func (s *eventSink) Name() string        { return s.name }
func (s *eventSink) Description() string { return "records events" }
func (s *eventSink) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (s *eventSink) Execute(_ context.Context, args map[string]interface{}) (tools.Result, error) {
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	s.got = append(s.got, args)
	s.mu.Unlock()
	return tools.Result{Output: "{}"}, nil
}
func (s *eventSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}
func (s *eventSink) topics() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, g := range s.got {
		t, _ := g["topic"].(string)
		out = append(out, t)
	}
	return out
}

func awaitEvents(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// .
// .
// .
// .
func TestEventsReachSubscribersAsInvocations(t *testing.T) {
	a := liveApp(t)
	const id = "org.example.logger"
	sink := &eventSink{name: pluginhost.ToolNameFor(id, "log.event")}
	if err := a.toolReg.RegisterHostOp(sink, id); err != nil {
		t.Fatal(err)
	}
	ap := &pluginhost.ActivePlugin{ID: id, Subscriptions: []pluginhost.SubscriptionDecl{
		{Topic: pluginhost.TopicToolCalled, Operation: "log.event", Filter: map[string]string{"tool": "ls"}},
		{Topic: pluginhost.TopicTurnEnded, Operation: "log.event"},
		{Topic: pluginhost.TopicAlarmFired, Operation: "log.event"},
	}}
	a.startSubscriber(ap)
	defer a.stopSubscriber(id)
	a.startSubscriber(&pluginhost.ActivePlugin{ID: "org.example.quiet"})
	if _, ok := a.subscribers["org.example.quiet"]; ok {
		t.Fatal("a plugin without subscriptions has no queue")
	}

	a.emitPluginEvent(pluginhost.TopicToolCalled, map[string]interface{}{"tool": "read", "failed": false})
	a.emitPluginEvent(pluginhost.TopicToolCalled, map[string]interface{}{"tool": "ls", "failed": false, "duration_ms": int64(3)})
	a.emitPluginEvent(pluginhost.TopicTurnEnded, nil)
	a.emitPluginEvent(pluginhost.TopicLedgerAppended, map[string]interface{}{"event_type": "x"})
	a.emitPluginEvent(pluginhost.TopicAlarmFired, map[string]interface{}{"owner": "DREAM", "alarm_id": "d1", "accepted": true})
	awaitEvents(t, "three deliveries", func() bool { return sink.count() == 3 })
	if got := strings.Join(sink.topics(), ","); got != "tool.called,turn.ended,alarm.fired" {
		t.Fatalf("delivered in order, filtered exactly: %s", got)
	}
	first := sink.got[0]
	payload, _ := first["payload"].(map[string]interface{})
	if payload["tool"] != "ls" || first["at"] == nil {
		t.Fatalf("the invocation carries the topic, the time and the payload: %v", first)
	}
	raw, _ := json.Marshal(first)
	if !strings.Contains(string(raw), `"topic":"tool.called"`) {
		t.Fatalf("the shape is the host's: %s", raw)
	}

	// .
	// .
	blocked := &eventSink{name: pluginhost.ToolNameFor("org.example.slow", "log.event"), block: make(chan struct{})}
	if err := a.toolReg.RegisterHostOp(blocked, "org.example.slow"); err != nil {
		t.Fatal(err)
	}
	a.startSubscriber(&pluginhost.ActivePlugin{ID: "org.example.slow", Subscriptions: []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicTurnEnded, Operation: "log.event"}}})
	defer a.stopSubscriber("org.example.slow")
	done := make(chan struct{})
	go func() {
		for i := 0; i < pluginEventQueue+10; i++ {
			a.emitPluginEvent(pluginhost.TopicTurnEnded, nil)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitting must never block the identity")
	}
	awaitEvents(t, "drops counted", func() bool { return a.subscriberDropped("org.example.slow") >= 9 })
	close(blocked.block)

	// .
	// .
	// .
	hooks := &eventSink{name: pluginhost.ToolNameFor("org.example.hooks", "log.event")}
	if err := a.toolReg.RegisterHostOp(hooks, "org.example.hooks"); err != nil {
		t.Fatal(err)
	}
	a.startSubscriber(&pluginhost.ActivePlugin{ID: "org.example.hooks", Subscriptions: []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicToolCalled, Operation: "log.event", Filter: map[string]string{"tool": "ls"}}}})
	a.executeToolCall(context.Background(), toolCallFor("ls"))
	awaitEvents(t, "the tool event", func() bool { return hooks.count() == 1 })
	hp, _ := hooks.got[0]["payload"].(map[string]interface{})
	if hp["tool"] != "ls" || hp["failed"] != false {
		t.Fatalf("the tool event carries the name and the outcome: %v", hp)
	}
	a.stopSubscriber("org.example.hooks")
	a.executeToolCall(context.Background(), toolCallFor("ls"))
	time.Sleep(50 * time.Millisecond)
	if hooks.count() != 1 {
		t.Fatal("a stopped subscriber receives nothing more")
	}
}
