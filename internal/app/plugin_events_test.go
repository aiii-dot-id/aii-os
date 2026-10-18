package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// eventSink is a subscribed operation: it records what it was handed.
type eventSink struct {
	name  string
	mu    sync.Mutex
	got   []map[string]any
	block chan struct{}
}

func (s *eventSink) Name() string        { return s.name }
func (s *eventSink) Description() string { return "records events" }
func (s *eventSink) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s *eventSink) Execute(_ context.Context, args map[string]any) (tools.Result, error) {
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

// Events reach a subscriber as invocations of its declared operation,
// filtered exactly, from a bounded queue that drops its oldest and says
// so; a plugin without subscriptions has no queue; a stopped subscriber
// receives nothing more.
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
	defer a.stopSubscriberOf(ap)
	a.startSubscriber(&pluginhost.ActivePlugin{ID: "org.example.quiet"})
	if _, ok := a.subscribers["org.example.quiet"]; ok {
		t.Fatal("a plugin without subscriptions has no queue")
	}

	a.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "read", "failed": false})
	a.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "ls", "failed": false, "duration_ms": int64(3)})
	a.emitPluginEvent(pluginhost.TopicTurnEnded, nil)
	a.emitPluginEvent(pluginhost.TopicLedgerAppended, map[string]any{"event_type": "x"})
	a.emitPluginEvent(pluginhost.TopicAlarmFired, map[string]any{"owner": "DREAM", "alarm_id": "d1", "accepted": true})
	awaitEvents(t, "three deliveries", func() bool { return sink.count() == 3 })
	if got := strings.Join(sink.topics(), ","); got != "tool.called,turn.ended,alarm.fired" {
		t.Fatalf("delivered in order, filtered exactly: %s", got)
	}
	first := sink.got[0]
	payload, _ := first["payload"].(map[string]any)
	if payload["tool"] != "ls" || first["at"] == nil {
		t.Fatalf("the invocation carries the topic, the time and the payload: %v", first)
	}
	raw, _ := json.Marshal(first)
	if !strings.Contains(string(raw), `"topic":"tool.called"`) {
		t.Fatalf("the shape is the host's: %s", raw)
	}

	// The queue is bounded: a blocked subscriber loses its oldest, not
	// the identity's time.
	blocked := &eventSink{name: pluginhost.ToolNameFor("org.example.slow", "log.event"), block: make(chan struct{})}
	if err := a.toolReg.RegisterHostOp(blocked, "org.example.slow"); err != nil {
		t.Fatal(err)
	}
	slow := &pluginhost.ActivePlugin{ID: "org.example.slow", Subscriptions: []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicTurnEnded, Operation: "log.event"}}}
	a.startSubscriber(slow)
	defer a.stopSubscriberOf(slow)
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

	// The real hook: a tool call through the identity's dispatch reaches
	// a subscriber with the tool's name and outcome, and a stopped
	// subscriber receives nothing more.
	hooks := &eventSink{name: pluginhost.ToolNameFor("org.example.hooks", "log.event")}
	if err := a.toolReg.RegisterHostOp(hooks, "org.example.hooks"); err != nil {
		t.Fatal(err)
	}
	hooked := &pluginhost.ActivePlugin{ID: "org.example.hooks", Subscriptions: []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicToolCalled, Operation: "log.event", Filter: map[string]string{"tool": "ls"}}}}
	a.startSubscriber(hooked)
	a.executeToolCall(context.Background(), toolCallFor("ls"))
	awaitEvents(t, "the tool event", func() bool { return hooks.count() == 1 })
	hp, _ := hooks.got[0]["payload"].(map[string]any)
	if hp["tool"] != "ls" || hp["failed"] != false {
		t.Fatalf("the tool event carries the name and the outcome: %v", hp)
	}
	a.stopSubscriberOf(hooked)
	a.executeToolCall(context.Background(), toolCallFor("ls"))
	time.Sleep(50 * time.Millisecond)
	if hooks.count() != 1 {
		t.Fatal("a stopped subscriber receives nothing more")
	}
}

// cancelledOnTaking is a caller's context that is live when the wait
// begins and cancelled by the time the gate is taken.
type cancelledOnTaking struct {
	context.Context
	checks int
}

func (c *cancelledOnTaking) Done() <-chan struct{} { return nil }
func (c *cancelledOnTaking) Err() error {
	if c.checks++; c.checks > 1 {
		return context.Canceled
	}
	return nil
}

// EVERY TURN THAT ENDS STARTED. Only the blocking acquire told plugins a
// turn had started, while every holder's release said one ended: typed,
// channel, voice and facility turns sent an end with no start, and an
// acquire whose caller gave up sent an end for a turn it never began.
func TestEveryTurnTakerPairsStartedWithEnded(t *testing.T) {
	a := liveApp(t)
	const id = "org.example.turns"
	sink := &eventSink{name: pluginhost.ToolNameFor(id, "log.event")}
	if err := a.toolReg.RegisterHostOp(sink, id); err != nil {
		t.Fatal(err)
	}
	ap := &pluginhost.ActivePlugin{ID: id, Subscriptions: []pluginhost.SubscriptionDecl{
		{Topic: pluginhost.TopicTurnStarted, Operation: "log.event"},
		{Topic: pluginhost.TopicTurnEnded, Operation: "log.event"},
	}}
	a.startSubscriber(ap)
	defer a.stopSubscriberOf(ap)

	// The admission taker: typed, channel and voice turns.
	if !a.TryBeginTurn() {
		t.Fatal("the gate is busy")
	}
	a.EndTurn()
	// A facility pass.
	if g := (facilityGate{a}); !g.TryBeginTurn() {
		t.Fatal("the gate is busy")
	} else {
		g.EndTurn()
	}
	// Spoken words waiting for a held turn.
	if !a.TryBeginTurn() {
		t.Fatal("the gate is busy")
	}
	spoken := make(chan bool)
	go func() { spoken <- a.awaitTurnGate(context.Background(), nil) }()
	a.EndTurn()
	if !<-spoken {
		t.Fatal("the spoken words did not get the turn")
	}
	a.EndTurn()
	// The blocking acquire.
	if err := a.acquireTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.releaseTurn()
	// A blocking acquire whose caller gave up as it took the gate.
	if took, err := a.acquireTurnOr(&cancelledOnTaking{Context: context.Background()}, nil); took || err == nil {
		t.Fatalf("a caller that gave up was handed the turn: %t %v", took, err)
	}

	want := strings.Repeat("turn.started,turn.ended,", 6)
	want = want[:len(want)-1]
	deadline := time.Now().Add(5 * time.Second)
	for sink.count() < 12 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // nothing more arrives
	if got := strings.Join(sink.topics(), ","); got != want {
		t.Fatalf("six turns taken and returned told plugins\n  %s\nwant\n  %s", got, want)
	}
}

// ONE LOCK FOR WHAT IS ACTIVE AND WHO HEARS ITS EVENTS, AND NO EVENT
// UNDER IT (T23). The commit point changes the plugin list, its metadata
// and the event subscriber in one pluginMu section, and every event takes
// that lock's read half to copy the subscribers. An event emitted from
// inside a pluginMu section waits on itself under the write half, and
// under the read half on any writer queued since. Here events are emitted
// from eight goroutines while the sweep's own sequence — commit a
// successor, take the predecessor out of the view, stop its delivery —
// runs as fast as it can; all of it returns within ten seconds, under
// -race, and the last activation is the one that delivers.
func TestEventsAndActivationsShareThePluginLockWithoutDeadlock(t *testing.T) {
	a := liveApp(t)
	logsink.CaptureForTest(t) // a full queue says so once per subscriber; not here
	const id = "org.example.swapped"
	sink := &eventSink{name: pluginhost.ToolNameFor(id, "log.event")}
	if err := a.toolReg.RegisterHostOp(sink, id); err != nil {
		t.Fatal(err)
	}
	subs := []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicToolCalled, Operation: "log.event"}}
	const emitters, emits, swaps = 8, 400, 200
	var last *pluginhost.Activation
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for e := 0; e < emitters; e++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < emits; i++ {
					a.emitPluginEventAttributed(pluginhost.TopicToolCalled, map[string]any{"tool": "read", "failed": false}, i%7 == 0)
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var prev *pluginhost.Activation
			for i := 0; i < swaps; i++ {
				next := &pluginhost.Activation{ID: id, Version: fmt.Sprintf("1.0.%d", i), Plugin: &pluginhost.ActivePlugin{ID: id, Version: fmt.Sprintf("1.0.%d", i), Subscriptions: subs}}
				if prev == nil {
					a.adoptPlugin(next, nil)
				} else {
					a.adoptPlugin(next, prev.Plugin)
					a.releaseActivation(prev)
					a.stopSubscriberOf(prev.Plugin)
				}
				prev = next
			}
			last = prev
		}()
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("events and activations deadlocked on pluginMu: nothing came back within 10 s")
	}
	if got := a.activePlugin(id); got != last.Plugin {
		t.Fatal("the last activation committed is not the one active")
	}
	a.pluginMu.RLock()
	s := a.subscribers[id]
	a.pluginMu.RUnlock()
	if s == nil || s.owner != last.Plugin {
		t.Fatal("the last activation committed is not the one delivering")
	}
	a.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "ls", "failed": false})
	awaitEvents(t, "a delivery through the last activation", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		for _, g := range sink.got {
			if p, _ := g["payload"].(map[string]any); p["tool"] == "ls" {
				return true
			}
		}
		return false
	})
	a.stopSubscriberOf(last.Plugin)
}

// subscriberDropped reports a subscriber's drop count.
func (a *App) subscriberDropped(id string) int64 {
	a.pluginMu.RLock()
	defer a.pluginMu.RUnlock()
	if s, ok := a.subscribers[id]; ok {
		return s.dropped.Load()
	}
	return 0
}

// inFlightSink is a subscribed operation that holds its call until the
// call's context ends, then takes a moment to answer — as a plugin
// answering a cancelled call does.
type inFlightSink struct {
	name      string
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
	cancelled atomic.Bool
	returned  atomic.Bool
}

func (s *inFlightSink) Name() string        { return s.name }
func (s *inFlightSink) Description() string { return "holds a delivery until it is cancelled" }
func (s *inFlightSink) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s *inFlightSink) Execute(ctx context.Context, _ map[string]any) (tools.Result, error) {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-ctx.Done():
		s.cancelled.Store(true)
		time.Sleep(500 * time.Millisecond)
	case <-s.release:
	}
	s.returned.Store(true)
	return tools.Result{Output: "{}"}, nil
}

// EVENT DELIVERY IS THE APPLICATION'S, AND SHUTDOWN ENDS IT (T21). A
// delivery rooted on Background, started on a bare goroutine, was ended
// only by its activation's stop, and joined by nothing: Shutdown returned
// with a call to a plugin still in flight, beside the teardown closing
// that plugin. Rooted on the lifetime and started through runBackground,
// Shutdown cancels the call and joins the delivery before it returns —
// and a subscriber made once the identity is stopping starts none.
func TestShutdownCancelsAndJoinsEventDelivery(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.toolReg = tools.NewRegistry(dir, nil, tools.Timeouts{})
	const id = "org.example.inflight"
	sink := &inFlightSink{name: pluginhost.ToolNameFor(id, "log.event"), entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(sink.release) }) // a delivery nothing cancelled must not outlive the test
	if err := a.toolReg.RegisterHostOp(sink, id); err != nil {
		t.Fatal(err)
	}
	subs := []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicTurnEnded, Operation: "log.event"}}
	a.startSubscriber(&pluginhost.ActivePlugin{ID: id, Subscriptions: subs})
	a.emitPluginEvent(pluginhost.TopicTurnEnded, nil)
	select {
	case <-sink.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the event was never delivered")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with a delivery in flight: %v", err)
	}
	if !sink.cancelled.Load() {
		t.Fatal("Shutdown returned and the delivery's call was never cancelled")
	}
	if !sink.returned.Load() {
		t.Fatal("Shutdown returned before the delivery's call did: nothing joined it")
	}

	const late = "org.example.late"
	lateSink := &eventSink{name: pluginhost.ToolNameFor(late, "log.event")}
	if err := a.toolReg.RegisterHostOp(lateSink, late); err != nil {
		t.Fatal(err)
	}
	a.startSubscriber(&pluginhost.ActivePlugin{ID: late, Subscriptions: subs})
	a.pluginMu.RLock()
	_, registered := a.subscribers[late]
	a.pluginMu.RUnlock()
	if registered {
		t.Fatal("a subscriber made once the identity is stopping was registered: nothing delivers to it")
	}
	a.emitPluginEvent(pluginhost.TopicTurnEnded, nil)
	time.Sleep(100 * time.Millisecond)
	if lateSink.count() != 0 {
		t.Fatal("an event reached a plugin after Shutdown")
	}
}
