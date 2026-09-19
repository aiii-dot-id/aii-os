package app

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
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

// .
// .
// .
const pluginEventQueue = 64

// .
const pluginEventTimeout = 30 * time.Second

type pluginEvent struct {
	Topic   string
	At      time.Time
	Payload map[string]interface{}
}

type pluginSubscriber struct {
	id string
	// .
	// .
	owner   *pluginhost.ActivePlugin
	subs    []pluginhost.SubscriptionDecl
	queue   chan pluginEvent
	stop    context.CancelFunc
	dropped atomic.Int64
}

// .
// .
// .
// .
// .
func (a *App) startSubscriber(ap *pluginhost.ActivePlugin) {
	var s *pluginSubscriber
	var ctx context.Context
	if len(ap.Subscriptions) > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithCancel(context.Background())
		s = &pluginSubscriber{id: ap.ID, owner: ap, subs: append([]pluginhost.SubscriptionDecl(nil), ap.Subscriptions...), queue: make(chan pluginEvent, pluginEventQueue), stop: stop}
	}
	a.subMu.Lock()
	if prior, ok := a.subscribers[ap.ID]; ok {
		prior.stop()
		delete(a.subscribers, ap.ID)
	}
	if s != nil {
		if a.subscribers == nil {
			a.subscribers = map[string]*pluginSubscriber{}
		}
		a.subscribers[ap.ID] = s
	}
	a.subMu.Unlock()
	if s == nil {
		return
	}
	go a.deliverEvents(ctx, s)
	log.Printf("plugin %s: subscribed to %d event topic(s)", ap.ID, len(s.subs))
}

// .
// .
// .
// .
// .
// .
// .
func (a *App) stopSubscriberOf(ap *pluginhost.ActivePlugin) {
	if ap == nil {
		return
	}
	a.subMu.Lock()
	s, ok := a.subscribers[ap.ID]
	if ok && s.owner == ap {
		delete(a.subscribers, ap.ID)
	} else {
		ok = false
	}
	a.subMu.Unlock()
	if ok {
		s.stop()
	}
}

// .
// .
func (a *App) stopSubscriber(id string) {
	a.subMu.Lock()
	s, ok := a.subscribers[id]
	if ok {
		delete(a.subscribers, id)
	}
	a.subMu.Unlock()
	if ok {
		s.stop()
	}
}

// .
// .
func (a *App) emitPluginEvent(topic string, payload map[string]interface{}) {
	a.subMu.RLock()
	subs := make([]*pluginSubscriber, 0, len(a.subscribers))
	for _, s := range a.subscribers {
		subs = append(subs, s)
	}
	a.subMu.RUnlock()
	if len(subs) == 0 {
		return
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	ev := pluginEvent{Topic: topic, At: time.Now().UTC(), Payload: payload}
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
		select {
		case s.queue <- ev:
		default:
			// .
			select {
			case <-s.queue:
			default:
			}
			select {
			case s.queue <- ev:
			default:
			}
			if n := s.dropped.Add(1); n == 1 || n%100 == 0 {
				log.Printf("plugin %s: event queue full — %d event(s) dropped so far (the subscriber is falling behind)", s.id, n)
			}
		}
	}
}

// .
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
				res, err := a.toolReg.Execute(dctx, pluginhost.ToolNameFor(s.id, d.Operation), map[string]interface{}{
					"topic": ev.Topic, "at": ev.At.Format(time.RFC3339Nano), "payload": ev.Payload,
				})
				cancel()
				if err != nil || res.Error != "" {
					log.Printf("plugin %s: %s delivery of %s failed (%v %s)", s.id, d.Operation, ev.Topic, err, res.Error)
				}
			}
		}
	}
}

// .
func (a *App) subscriberDropped(id string) int64 {
	a.subMu.RLock()
	defer a.subMu.RUnlock()
	if s, ok := a.subscribers[id]; ok {
		return s.dropped.Load()
	}
	return 0
}

var _ = sync.Mutex{}
