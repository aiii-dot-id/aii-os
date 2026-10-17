package app

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func calibrationFixture() (*App, *pluginSubscriber) {
	ap := &pluginhost.ActivePlugin{ID: "test.cal"}
	s := &pluginSubscriber{id: ap.ID, owner: ap, calibration: "observe", queue: make(chan pluginEvent, pluginEventQueue), subs: []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicToolCalled, Operation: "observe", Delivery: pluginhost.CalibrationV1}, {Topic: pluginhost.TopicTurnEnded, Operation: "log"}}}
	a := &App{pluginState: pluginState{subscribers: map[string]*pluginSubscriber{ap.ID: s}}}
	return a, s
}
func TestCalibrationConcurrentQueueLossAndTail(t *testing.T) {
	a, s := calibrationFixture()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"session": "ws"})
		}()
	}
	wg.Wait()
	snap := s.Snapshot(1, pluginhost.ActingSession{ID: "ws"})
	if snap["loss_count"] != uint64(0) || snap["emitted"] != uint64(64) {
		t.Fatal(snap)
	}
	a.emitPluginEvent(pluginhost.TopicToolCalled, nil)
	snap = s.Snapshot(1, pluginhost.ActingSession{ID: "ws"})
	if snap["loss_count"] != uint64(1) {
		t.Fatal(snap)
	}
	for want := uint64(2); want <= 65; want++ {
		ev := <-s.queue
		if ev.Sequence != want {
			t.Fatal(ev.Sequence, want)
		}
		s.Finish(1, ev.Sequence, want != 65)
	}
	snap = s.Snapshot(1, pluginhost.ActingSession{ID: "ws"})
	if snap["loss_count"] != uint64(2) || snap["settled"] != uint64(65) {
		t.Fatal(snap)
	}
}
func TestCalibrationAdvisoryDropAndSourceLoss(t *testing.T) {
	a, s := calibrationFixture()
	a.emitPluginEvent(pluginhost.TopicTurnEnded, nil)
	for i := 0; i < 64; i++ {
		a.emitPluginEvent(pluginhost.TopicToolCalled, nil)
	}
	snap := s.Snapshot(1, pluginhost.ActingSession{ID: "ws"})
	if snap["loss_count"] != uint64(0) {
		t.Fatal(snap)
	}
	<-s.queue
	a.emitPluginEventAttributed(pluginhost.TopicToolCalled, nil, true)
	snap = s.Snapshot(1, pluginhost.ActingSession{ID: "ws"})
	if snap["loss_count"] != uint64(1) {
		t.Fatal(snap)
	}
}
func TestCalibrationGenerationAndUnavailableSession(t *testing.T) {
	a, s := calibrationFixture()
	a.emitPluginEvent(pluginhost.TopicToolCalled, nil)
	old := s.Snapshot(1, pluginhost.ActingSession{ID: "ws"})
	fresh := s.Snapshot(2, pluginhost.ActingSession{ID: "ws"})
	s.Finish(1, 1, true)
	now := s.Snapshot(2, pluginhost.ActingSession{ID: "ws"})
	if old["stream_id"] == fresh["stream_id"] || now["settled"] != uint64(0) {
		t.Fatal(old, now)
	}
	absent := s.Snapshot(0, pluginhost.ActingSession{ID: "ws"})
	if absent["available"] != false || absent["session_id"] != "ws" {
		t.Fatal(absent)
	}
	d := &pluginhost.CalibrationDelivery{Owner: s.owner, Generation: 2, Sequence: 1, Source: s}
	_ = pluginhost.WithCalibrationDelivery(context.Background(), d)
}

func TestCalibrationStaleSnapshotCannotRewindGeneration(t *testing.T) {
	_, s := calibrationFixture()
	fresh := s.Snapshot(2, pluginhost.ActingSession{ID: "ws"})
	old := s.Snapshot(1, pluginhost.ActingSession{ID: "ws"})
	now := s.Snapshot(2, pluginhost.ActingSession{ID: "ws"})
	if old["available"] != false || fresh["stream_id"] != now["stream_id"] {
		t.Fatal(fresh, old, now)
	}
}

func TestCalibrationPreProxyRefusalSettles(t *testing.T) {
	a, s := calibrationFixture()
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); a.deliverEvents(ctx, s) }()
	defer func() { cancel(); <-done }()
	a.emitPluginEvent(pluginhost.TopicToolCalled, nil)
	awaitEvents(t, "refusal settlement", func() bool {
		snap := s.Snapshot(1, pluginhost.ActingSession{ID: "ws"})
		return snap["settled"] == uint64(1)
	})
	snap := s.Snapshot(1, pluginhost.ActingSession{ID: "ws"})
	if snap["loss_count"] != uint64(1) {
		t.Fatal(snap)
	}
}

type calibrationBlockingTool struct {
	entered chan pluginhost.ActingSession
	release chan struct{}
}

func (t *calibrationBlockingTool) Name() string        { return "calibration_probe" }
func (t *calibrationBlockingTool) Description() string { return "test captured acting context" }
func (t *calibrationBlockingTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *calibrationBlockingTool) Execute(ctx context.Context, _ map[string]any) (tools.Result, error) {
	t.entered <- pluginhost.ActingSessionFrom(ctx)
	select {
	case <-t.release:
		return tools.Result{Output: "done"}, nil
	case <-ctx.Done():
		return tools.Result{}, ctx.Err()
	}
}
func TestCalibrationAttributionSurvivesSessionSwitch(t *testing.T) {
	a := liveApp(t)
	tool := &calibrationBlockingTool{entered: make(chan pluginhost.ActingSession, 1), release: make(chan struct{})}
	a.toolReg.Register(tool)
	id := "org.example.sessioncapture"
	sink := &eventSink{name: pluginhost.ToolNameFor(id, "observe")}
	if err := a.toolReg.RegisterHostOp(sink, id); err != nil {
		t.Fatal(err)
	}
	ap := &pluginhost.ActivePlugin{ID: id, Subscriptions: []pluginhost.SubscriptionDecl{{Topic: pluginhost.TopicToolCalled, Operation: "observe"}}}
	a.startSubscriber(ap)
	defer a.stopSubscriberOf(ap)
	if err := a.store.StartWorkSession(t.Context(), "ws_before", "test"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); a.executeToolCall(t.Context(), toolCallFor(tool.Name())) }()
	select {
	case captured := <-tool.entered:
		if captured.ID != "ws_before" {
			close(tool.release)
			<-done
			t.Fatal(captured)
		}
	case <-time.After(3 * time.Second):
		close(tool.release)
		<-done
		t.Fatal("tool did not dispatch")
	}
	if err := a.store.DeliverWorkSession(t.Context(), "ws_before", "served: test", store.EvidenceCompletedLocally, ""); err != nil {
		close(tool.release)
		<-done
		t.Fatal(err)
	}
	if err := a.store.StartWorkSession(t.Context(), "ws_after", "test"); err != nil {
		close(tool.release)
		<-done
		t.Fatal(err)
	}
	close(tool.release)
	<-done
	awaitEvents(t, "captured session event", func() bool { return sink.count() == 1 })
	sink.mu.Lock()
	defer sink.mu.Unlock()
	payload := sink.got[0]["payload"].(map[string]any)
	if payload["session"] != "ws_before" {
		t.Fatal(payload)
	}
}
