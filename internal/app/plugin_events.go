package app

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

const pluginEventQueue = 64

const pluginEventTimeout = 30 * time.Second

type pluginEvent struct {
	Generation, Sequence uint64
	Stream               string
	Topic                string
	At                   time.Time
	Payload              map[string]interface{}
}

type pluginSubscriber struct {
	mu                                 sync.Mutex
	generation, emitted, settled, loss uint64
	stream                             string
	exhausted, stopped                 bool
	calibration                        string
	id                                 string

	owner   *pluginhost.ActivePlugin
	subs    []pluginhost.SubscriptionDecl
	queue   chan pluginEvent
	stop    context.CancelFunc
	dropped atomic.Int64
}

func (a *App) startSubscriber(ap *pluginhost.ActivePlugin) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	a.startSubscriberLocked(ap)
}

func (a *App) startSubscriberLocked(ap *pluginhost.ActivePlugin) {
	var s *pluginSubscriber
	var ctx context.Context
	if len(ap.Subscriptions) > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithCancel(a.lifetime())
		s = &pluginSubscriber{calibration: ap.CalibrationOperation(), id: ap.ID, owner: ap, subs: append([]pluginhost.SubscriptionDecl(nil), ap.Subscriptions...), queue: make(chan pluginEvent, pluginEventQueue), stop: stop}
	}
	if s != nil {
		cancel := s.stop
		s.stop = func() { s.mu.Lock(); s.stopped = true; s.mu.Unlock(); cancel() }
		if s.calibration != "" {
			ap.SetCalibrationSource(s)
		}
	}
	if prior, ok := a.subscribers[ap.ID]; ok {
		prior.stop()
		delete(a.subscribers, ap.ID)
	}
	if s == nil {
		return
	}
	if !a.runBackground(func() { a.deliverEvents(ctx, s) }) {
		s.stop()
		logsink.Info("plugins.refusal", "plugin %s: its events are not delivered: %v", ap.ID, errStopping)
		return
	}
	if a.subscribers == nil {
		a.subscribers = map[string]*pluginSubscriber{}
	}
	a.subscribers[ap.ID] = s
	logsink.Info("plugins.start", "plugin %s: subscribed to %d event topic(s)", ap.ID, len(s.subs))
}

func (a *App) stopSubscriberOf(ap *pluginhost.ActivePlugin) {
	if ap == nil {
		return
	}
	a.pluginMu.Lock()
	s, ok := a.subscribers[ap.ID]
	if ok && s.owner == ap {
		delete(a.subscribers, ap.ID)
	} else {
		ok = false
	}
	a.pluginMu.Unlock()
	if ok {
		s.stop()
	}
}

func (a *App) emitPluginEvent(topic string, payload map[string]interface{}) {
	a.emitPluginEventAttributed(topic, payload, false)
}

func (a *App) emitPluginEventAttributed(topic string, payload map[string]interface{}, attributionLost bool) {
	a.pluginMu.RLock()
	subs := make([]*pluginSubscriber, 0, len(a.subscribers))
	for _, s := range a.subscribers {
		subs = append(subs, s)
	}
	a.pluginMu.RUnlock()
	if len(subs) == 0 {
		return
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	immutable := make(map[string]interface{}, len(payload))
	for k, v := range payload {
		immutable[k] = v
	}
	ev := pluginEvent{Topic: topic, At: time.Now().UTC(), Payload: immutable}
	for _, s := range subs {
		wanted := false
		for _, d := range s.subs {
			if d.Topic == topic && d.Matches(payload) {
				wanted = true
				break
			}
		}
		if !wanted {
			continue
		}
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			continue
		}
		item := ev
		if s.calibration != "" && topic == pluginhost.TopicToolCalled {
			current := s.generationLocked(s.owner.RuntimeGeneration())
			if !current {
				s.loseLocked()
				s.mu.Unlock()
				continue
			}
			if attributionLost {
				s.loseLocked()
			}
			if s.emitted == pluginhost.MaxCalibrationCounter {
				s.exhausted = true
				s.mu.Unlock()
				continue
			}
			s.emitted++
			item.Generation = s.generation
			item.Sequence = s.emitted
			item.Stream = s.stream
		}
		var dropped int64
		select {
		case s.queue <- item:
		default:
			select {
			case old := <-s.queue:
				if old.Sequence != 0 && old.Generation == s.generation {
					s.loseLocked()
				}
			default:
			}
			select {
			case s.queue <- item:
			default:
				if item.Sequence != 0 {
					s.loseLocked()
					s.exhausted = true
				}
			}
			dropped = s.dropped.Add(1)
		}
		s.mu.Unlock()
		if dropped == 1 || dropped > 0 && dropped%100 == 0 {
			logsink.Warn("plugins.budget", "plugin %s: event queue full — %d event(s) dropped", s.id, dropped)
		}
	}
}

func (a *App) deliverEvents(ctx context.Context, s *pluginSubscriber) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-s.queue:
			for _, d := range s.subs {
				if d.Topic != ev.Topic || !d.Matches(ev.Payload) {
					continue
				}
				dctx, cancel := context.WithTimeout(ctx, pluginEventTimeout)
				args := map[string]interface{}{"topic": ev.Topic, "at": ev.At.Format(time.RFC3339Nano), "payload": ev.Payload}
				var delivery *pluginhost.CalibrationDelivery
				if d.Delivery == pluginhost.CalibrationV1 {
					args["stream_id"] = ev.Stream
					args["sequence"] = ev.Sequence
					delivery = &pluginhost.CalibrationDelivery{Owner: s.owner, Generation: ev.Generation, Sequence: ev.Sequence, Stream: ev.Stream, Source: s}
					dctx = pluginhost.WithCalibrationDelivery(dctx, delivery)
				}
				res, err := a.toolReg.Execute(dctx, pluginhost.ToolNameFor(s.id, d.Operation), args)
				if delivery != nil {
					delivery.Finish(false)
				}
				cancel()
				if err != nil || res.Error != "" {
					logsink.Warn("plugins.error", "plugin %s: %s delivery of %s failed (%v %s)", s.id, d.Operation, ev.Topic, err, res.Error)
				}
			}
		}
	}
}
