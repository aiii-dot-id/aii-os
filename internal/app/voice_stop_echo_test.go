package app

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// nativeFenceEngine is the native voice session's fence as the host meets
// it (voice plugin runtime/native/session/session.cpp interrupt, published
// by worker.cpp): the first stop or cancel of a synthesis not yet fenced
// is reported back as interruption_requested naming it, the control as
// its reason. atFence runs once, inside the host's fence of target,
// before the engine acknowledges it.
type nativeFenceEngine struct {
	*fakeEngineSession
	target  string
	once    sync.Once
	atFence func()
}

func (f *nativeFenceEngine) InterruptFor(ctx context.Context, sid, synth, why string) error {
	if synth == f.target && f.atFence != nil {
		f.once.Do(f.atFence)
	}
	return f.fakeEngineSession.InterruptFor(ctx, sid, synth, why)
}

// nativeEvent is one observation as the native worker publishes it.
func nativeEvent(typ, session string, fields map[string]any) pluginhost.Event {
	body := map[string]any{"type": typ, "session_id": session}
	for k, v := range fields {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	return pluginhost.Event{Type: typ, SessionID: session, Raw: raw}
}

func TestAReplacementSurvivesItsPredecessorsStopEchoButNotSpeech(t *testing.T) {
	const (
		beforeAck  = "before the fence acknowledgement"
		afterAck   = "between the acknowledgement and the enqueue"
		ackPending = "while the replacement's acknowledgement is pending"
	)
	interruption := func(synth, reason string) pluginhost.Event {
		fields := map[string]any{"synthesis_id": synth, "output_stream": 1}
		if reason != "" {
			fields["reason"] = reason
		}
		return nativeEvent("interruption_requested", "v", fields)
	}
	// next is the replacement's synthesis once it is enqueued, else "".
	cases := []struct {
		name  string
		event func(prior, next string) pluginhost.Event
		echo  bool
	}{
		{"the stop_playback echo", func(prior, _ string) pluginhost.Event { return interruption(prior, "stop_playback") }, true},
		{"the cancel_synthesis echo", func(prior, _ string) pluginhost.Event { return interruption(prior, "cancel_synthesis") }, true},
		{"speech_start", func(_, _ string) pluginhost.Event {
			return nativeEvent("speech_start", "v", map[string]any{"start_sample": 4800, "end_sample": 4800})
		}, false},
		{"the VAD fence", func(prior, next string) pluginhost.Event { return interruption(cmp.Or(next, prior), "vad_speech") }, false},
		{"an interruption naming no reason", func(prior, _ string) pluginhost.Event { return interruption(prior, "") }, false},
		{"a stop this host never sent", func(_, next string) pluginhost.Event {
			return interruption(cmp.Or(next, "v-syn-never-fenced"), "stop_playback")
		}, false},
	}
	for _, seam := range []string{beforeAck, afterAck, ackPending} {
		for _, c := range cases {
			t.Run(seam+"/"+c.name, func(t *testing.T) {
				a := &App{cfg: &Config{}}
				f := &nativeFenceEngine{fakeEngineSession: &fakeEngineSession{}}
				h := &voiceHandle{id: "v", v: f}
				var hushed atomic.Int32
				h.hush = func(string) { hushed.Add(1) }
				a.voiceSessions.Store(h.id, h)
				defer a.bgWG.Wait()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if a.synthesizeReply(ctx, h.id, h.gen.Load(), "first") != replyAdmitted {
					t.Fatal("first reply not admitted")
				}
				prior, gen := h.loadInflight().id, h.gen.Load()
				next := func() string {
					if s := f.synthed(); len(s) == 2 {
						return strings.SplitN(s[1], "|", 2)[0]
					}
					return ""
				}
				deliver := func() { a.voiceObserved(decodeVoiceFrame(c.event(prior, next()))) }
				f.target = prior
				switch seam {
				case beforeAck:
					f.atFence = deliver
				case afterAck:
					// The enqueue waits on admit; the observation lands once the
					// acknowledgement has released the predecessor's record.
					f.atFence = func() {
						h.admit.Lock()
						go func() {
							defer h.admit.Unlock()
							for deadline := time.Now().Add(5 * time.Second); h.loadInflight().id == prior && time.Now().Before(deadline); {
								time.Sleep(time.Millisecond)
							}
							deliver()
						}()
					}
				case ackPending:
					f.onSynthesize = deliver
				}
				got := a.synthesizeReply(ctx, h.id, gen, "replacement")
				a.bgWG.Wait()
				synths, ops := f.synthed(), f.opsSeen()
				if c.echo {
					if got != replyAdmitted || h.gen.Load() != gen || len(synths) != 2 || h.loadInflight().id != next() {
						t.Fatalf("the predecessor's stop echo superseded its own replacement: verdict=%d gen %d→%d synths=%v inflight=%q", got, gen, h.gen.Load(), synths, h.loadInflight().id)
					}
					if slices.Contains(ops, "interrupt:"+next()+":v") || hushed.Load() != 0 {
						t.Fatalf("the echo fenced the replacement or hushed as if the operator spoke: ops=%v hushed=%d", ops, hushed.Load())
					}
					return
				}
				if got != replySuperseded || h.gen.Load() == gen {
					t.Fatalf("speech did not supersede the replacement: verdict=%d gen %d→%d synths=%v", got, gen, h.gen.Load(), synths)
				}
				if seam == ackPending {
					if len(synths) != 2 || !slices.Contains(ops, "interrupt:"+next()+":v") {
						t.Fatalf("a replacement superseded after its enqueue was not fenced by its own id: synths=%v ops=%v", synths, ops)
					}
				} else if len(synths) != 1 {
					t.Fatalf("a superseded replacement was enqueued: %v", synths)
				}
			})
		}
	}
}
