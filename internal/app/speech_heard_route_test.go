package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
)

// heardRoute is an application with engines A and B running, the speaking
// pointer given, a conversation hearing on A and — when asked — an output
// session open on B.
type heardRoute struct {
	t      *testing.T
	a      *App
	engA   *pluginhost.ActivePlugin
	engB   *pluginhost.ActivePlugin
	hear   *voiceHandle
	hearOn *fakeEngineSession
	out    *voiceHandle
	outOn  *fakeEngineSession
	mintID string // what the service mint answers; "" is a mint that failed

	mu     sync.Mutex
	minted []string
	refs   []dashboard.VoiceReplyRef
	events []dashboard.VoiceEvent
	hushes []dashboard.VoiceHush
}

func newHeardRoute(t *testing.T, tts string, outputOnB bool, installed ...string) *heardRoute {
	t.Helper()
	a := newVoiceApp(t)
	for _, id := range installed {
		engineInstalledHere(t, a, id, "voice_interface")
	}
	r := &heardRoute{t: t, a: a, mintID: "svc-1"}
	r.engA, _ = runningEngine(t, a, "id.engine.a")
	r.engB, _ = runningEngine(t, a, "id.engine.b")
	chooseSpeech(a, "", tts)
	r.hearOn = &fakeEngineSession{}
	// Opened under the choice given, as OpenVoiceSession records it.
	r.hear = &voiceHandle{id: "vs-hear", v: r.hearOn, engine: r.engA, speaks: tts,
		b:    &audio.Binding{InputHandle: "in-hear", Source: &liveSource{f: mono16k}, Contained: true},
		done: make(chan struct{}), drained: make(chan struct{})}
	r.hear.hush = func(why string) { a.hushFallback(r.hear, why) }
	a.voiceSessions.Store(r.hear.id, r.hear)
	a.voiceModes.Store(r.hear.id, true)
	if outputOnB {
		r.outOn = &fakeEngineSession{}
		r.out = &voiceHandle{id: "vs-speak", v: r.outOn, engine: r.engB, b: &audio.Binding{Contained: true},
			done: make(chan struct{}), drained: make(chan struct{})}
		r.out.hush = func(why string) { a.hushFallback(r.out, why) }
		a.voiceSessions.Store(r.out.id, r.out)
		a.voiceModes.Store(r.out.id, false)
	}
	prev := voiceFallbackMint
	voiceFallbackMint = func(_ *App, text string) string {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.minted = append(r.minted, text)
		return r.mintID
	}
	t.Cleanup(func() { voiceFallbackMint = prev })
	a.voiceReplySink = func(ref dashboard.VoiceReplyRef, _ string) { r.mu.Lock(); r.refs = append(r.refs, ref); r.mu.Unlock() }
	a.voiceEventSink = func(ev dashboard.VoiceEvent) { r.mu.Lock(); r.events = append(r.events, ev); r.mu.Unlock() }
	a.voiceHushSink = func(h dashboard.VoiceHush) { r.mu.Lock(); r.hushes = append(r.hushes, h); r.mu.Unlock() }
	t.Cleanup(func() { awaitAppWork(t, a) })
	return r
}

// hears is one utterance heard on A, through the real path: the engine's
// final, the operator's turn, the settlement of its reply.
func (r *heardRoute) hears(answer func() (string, error)) {
	r.t.Helper()
	stubWake(r.t, answer)
	if err := r.a.observeVoice(context.Background(), heardUtterance{Text: "a question", Answer: true, Operator: true,
		SessionID: r.hear.id, Gen: r.hear.gen.Load(), Source: "speech engine id.engine.a"}); err != nil {
		r.t.Fatal(err)
	}
	awaitAppWork(r.t, r.a)
}

func answer() (string, error) { return "the answer", nil }

// spokeBy names who actually synthesized the reply.
func (r *heardRoute) spokeBy() string {
	var who []string
	if n := len(r.hearOn.synthed()); n > 0 {
		who = append(who, fmt.Sprintf("%s×%d", r.engA.ID, n))
	}
	if r.outOn != nil {
		if n := len(r.outOn.synthed()); n > 0 {
			who = append(who, fmt.Sprintf("%s×%d", r.engB.ID, n))
		}
	}
	r.mu.Lock()
	if n := len(r.minted); n > 0 {
		who = append(who, fmt.Sprintf("service×%d", n))
	}
	r.mu.Unlock()
	if len(who) == 0 {
		return "nobody"
	}
	return strings.Join(who, "+")
}

func (r *heardRoute) replyRefs() []dashboard.VoiceReplyRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]dashboard.VoiceReplyRef(nil), r.refs...)
}

func (r *heardRoute) hushed() []dashboard.VoiceHush {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]dashboard.VoiceHush(nil), r.hushes...)
}

func (r *heardRoute) refused(session string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, ev := range r.events {
		if ev.Type == "reply_refused" && ev.SessionID == session {
			out = append(out, ev.Reason)
		}
	}
	return out
}

// synthOn is the synthesis id an engine double was given, or "".
func synthOn(f *fakeEngineSession) string {
	if s := f.synthed(); len(s) > 0 {
		return strings.SplitN(s[len(s)-1], "|", 2)[0]
	}
	return ""
}

func outcomeOf(h *voiceHandle) string { s, _ := h.replyOutcome.Load().(string); return s }

// THE CHOSEN VOICE MAKES THE REPLY TO WHAT WAS HEARD, and nothing else
// makes it: not the hearing engine beside a chosen voice, not a second
// voice behind the chosen one.
func TestTheVoiceChosenToSpeakAnswersWhatWasHeard(t *testing.T) {
	cases := []struct {
		name, tts, spoke string
		ref              func(r *heardRoute, ref dashboard.VoiceReplyRef) bool
	}{
		{"nothing chosen: the engine that hears speaks", "", "id.engine.a×1",
			func(r *heardRoute, ref dashboard.VoiceReplyRef) bool {
				return ref.Route == "plugin" && ref.SessionID == r.hear.id && ref.SynthesisID == synthOn(r.hearOn) && !ref.TextOnly
			}},
		{"the hearing engine chosen by name", "id.engine.a", "id.engine.a×1",
			func(r *heardRoute, ref dashboard.VoiceReplyRef) bool {
				return ref.Route == "plugin" && ref.SessionID == r.hear.id && ref.SynthesisID == synthOn(r.hearOn) && !ref.TextOnly
			}},
		{"a service chosen", "ElevenLabs", "service×1",
			func(r *heardRoute, ref dashboard.VoiceReplyRef) bool {
				return ref.Route == "cloud" && ref.SessionID == r.hear.id && ref.SynthesisID == "svc-1" && !ref.Fallback && !ref.TextOnly
			}},
		{"another engine chosen", "id.engine.b", "id.engine.b×1",
			func(r *heardRoute, ref dashboard.VoiceReplyRef) bool {
				return ref.Route == "plugin" && ref.SessionID == r.out.id && ref.SynthesisID == synthOn(r.outOn) && ref.SynthesisID != "" && !ref.TextOnly
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newHeardRoute(t, c.tts, true)
			r.hears(answer)
			if got := r.spokeBy(); got != c.spoke {
				t.Fatalf("the reply to what A heard was synthesized by %s, want %s", got, c.spoke)
			}
			refs := r.replyRefs()
			if len(refs) != 1 || !c.ref(r, refs[0]) {
				t.Fatalf("the page was told %+v", refs)
			}
		})
	}
}

func TestASpeakingChoiceThatCannotSpeakLeavesTheReplyAsTextAndSaysWhy(t *testing.T) {
	cases := []struct {
		name, tts string
		output    bool
		installed []string
	}{
		{"the chosen engine holds no session", "id.engine.b", false, nil},
		{"the chosen engine is installed and not running", "id.engine.c", true, []string{"id.engine.c"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newHeardRoute(t, c.tts, c.output, c.installed...)
			r.hears(answer)
			if got := r.spokeBy(); got != "nobody" {
				t.Fatalf("the host asked %s to speak for %s, which cannot", got, c.tts)
			}
			refs := r.replyRefs()
			if len(refs) != 1 || refs[0].Route != "plugin" || !refs[0].TextOnly || refs[0].Fallback || refs[0].SessionID != r.hear.id || refs[0].SynthesisID != "" {
				t.Fatalf("the reply did not stay text under the session that heard it, or another voice was handed it: %+v", refs)
			}
			why := r.refused(r.hear.id)
			if len(why) != 1 || !strings.Contains(why[0], c.tts) {
				t.Fatalf("the refusal does not name the voice chosen to speak (%s): %q", c.tts, why)
			}
			if out := outcomeOf(r.hear); !strings.Contains(out, c.tts) || !strings.Contains(out, "not spoken") || strings.Contains(out, "browser") {
				t.Fatalf("the session's drain reason does not say the reply was not spoken and which voice could not: %q", out)
			}
			if h := r.hushed(); len(h) != 0 {
				t.Fatalf("a reply no voice speaks was hushed: %+v", h)
			}
		})
	}
}

// A BARGE-IN STOPS THE REPLY WHICHEVER VOICE SPEAKS IT. The heard session
// keeps custody of its reply on every route: the engine hearing the
// operator, the operator's stop and an abort each reach the voice that is
// speaking; and the operator speaking before the reply is settled — or
// while another engine admits it — leaves nothing spoken.
func TestABargeInStopsTheReplyWhicheverVoiceSpeaksIt(t *testing.T) {
	routes := []struct {
		name, tts string
		out       bool
	}{{"the hearing engine", "", true}, {"a service", "ElevenLabs", true}, {"another engine", "id.engine.b", true},
		{"a voice that cannot speak, whose reply stays text", "id.engine.b", false}}
	fences := []string{"the engine heard the operator", "the operator's stop", "an abort"}
	for _, route := range routes {
		for _, fence := range fences {
			t.Run(route.name+"/"+fence, func(t *testing.T) {
				r := newHeardRoute(t, route.tts, route.out)
				r.hears(answer)
				if r.spokeBy() == "nobody" && len(r.replyRefs()) == 0 {
					t.Fatal("fixture: nothing was admitted to stop")
				}
				var synth string
				switch {
				case route.tts == "":
					synth = synthOn(r.hearOn)
				case route.out && route.tts == "id.engine.b":
					synth = synthOn(r.outOn)
				}
				switch fence {
				case "the engine heard the operator":
					r.a.voiceObserved(decodeVoiceFrame(pluginhost.Event{Type: "speech_start", SessionID: r.hear.id}))
				case "the operator's stop":
					if err := r.hear.Interrupt(context.Background()); err != nil {
						t.Fatal(err)
					}
				case "an abort":
					if err := r.hear.Close(context.Background(), "abort"); err != nil {
						t.Fatal(err)
					}
				}
				awaitAppWork(t, r.a)
				switch {
				case route.tts == "":
					stopped := slices.Contains(r.hearOn.opsSeen(), "interrupt:"+synth+":"+r.hear.id)
					if fence == "an abort" {
						stopped = slices.Contains(r.hearOn.closes(), "abort: operator")
					}
					if !stopped {
						t.Fatalf("%s did not reach A's own reply: ops=%v closes=%v", fence, r.hearOn.opsSeen(), r.hearOn.closes())
					}
				case route.tts == "ElevenLabs":
					h := r.hushed()
					if len(h) != 1 || h[0].SessionID != r.hear.id || h[0].SynthesisID != "svc-1" || h[0].Route != "cloud" {
						t.Fatalf("%s did not hush the service's reply: %+v", fence, h)
					}
				case !route.out:

					if h := r.hushed(); len(h) != 0 {
						t.Fatalf("%s hushed a reply no voice was speaking: %+v", fence, h)
					}
					if refs := r.replyRefs(); len(refs) != 1 || !refs[0].TextOnly || refs[0].Route != "plugin" {
						t.Fatalf("the reply a voice could not speak did not stay text: %+v", refs)
					}
				default:
					if !slices.Contains(r.outOn.opsSeen(), "interrupt:"+synth+":"+r.out.id) {
						t.Fatalf("%s did not fence the reply B was speaking (%s): %v", fence, synth, r.outOn.opsSeen())
					}
					if r.out.closing.Load() {
						t.Fatal("the barge-in closed B's output session; it should only have stopped the reply")
					}
				}
			})
		}
		t.Run(route.name+"/the operator spoke before the reply was settled", func(t *testing.T) {
			r := newHeardRoute(t, route.tts, route.out)
			r.hears(func() (string, error) {
				r.a.voiceObserved(decodeVoiceFrame(pluginhost.Event{Type: "speech_start", SessionID: r.hear.id}))
				return "a superseded answer", nil
			})
			if got := r.spokeBy(); got != "nobody" {
				t.Fatalf("a reply the operator spoke over was synthesized by %s", got)
			}
			if refs := r.replyRefs(); len(refs) != 1 || !refs[0].TextOnly {
				t.Fatalf("the superseded reply did not stay text: %+v", refs)
			}
		})
	}
	t.Run("another engine/the operator spoke while B admitted it", func(t *testing.T) {
		r := newHeardRoute(t, "id.engine.b", true)
		r.outOn.onSynthesize = func() {
			r.a.voiceObserved(decodeVoiceFrame(pluginhost.Event{Type: "speech_start", SessionID: r.hear.id}))
		}
		r.hears(answer)
		synth := synthOn(r.outOn)
		if synth == "" {
			t.Fatal("fixture: B was not asked to synthesize")
		}
		if !slices.Contains(r.outOn.opsSeen(), "interrupt:"+synth+":"+r.out.id) {
			t.Fatalf("the reply B admitted after the operator spoke was not fenced: %v", r.outOn.opsSeen())
		}
	})
}

// SPEAK TURNED OFF SILENCES THE REPLY ON EVERY ROUTE: before it is admitted
// nothing is synthesized and the words stay text; after, the commit stops
// the voice that is speaking it.
func TestSpeakOffSilencesTheHeardReplyOnEveryRoute(t *testing.T) {
	for _, route := range []struct {
		name, tts string
		out       bool
	}{{"a service", "ElevenLabs", true}, {"another engine", "id.engine.b", true}, {"a voice that cannot speak, whose reply stays text", "id.engine.b", false}} {
		t.Run(route.name+"/before admission", func(t *testing.T) {
			r := newHeardRoute(t, route.tts, route.out)
			r.a.publishVoiceMode(VoiceModeConfig{Listen: listenInteractive, Speak: speakOff, Revision: 2})
			r.hears(answer)
			if got := r.spokeBy(); got != "nobody" {
				t.Fatalf("with speak off the reply was synthesized by %s", got)
			}
			if refs := r.replyRefs(); len(refs) != 1 || !refs[0].TextOnly || refs[0].Route != "plugin" {
				t.Fatalf("with speak off the reply did not stay text: %+v", refs)
			}
		})
		t.Run(route.name+"/after admission", func(t *testing.T) {
			r := newHeardRoute(t, route.tts, route.out)
			on := VoiceModeConfig{Listen: listenInteractive, Speak: speakOn, Revision: 1}
			r.a.publishVoiceMode(on)
			r.hears(answer)
			off := VoiceModeConfig{Listen: listenInteractive, Speak: speakOff, Revision: 2}
			r.a.publishVoiceMode(off)
			r.a.voiceModeCommitted(on, off)
			awaitAppWork(t, r.a)
			if route.tts == "ElevenLabs" {
				if h := r.hushed(); len(h) == 0 || h[0].SynthesisID != "svc-1" {
					t.Fatalf("speak off did not hush the service's reply: %+v", h)
				}
				return
			}
			if !route.out {
				// The reply was text from the start: speak off finds no voice.
				if h := r.hushed(); len(h) != 0 {
					t.Fatalf("speak off hushed a reply no voice was speaking: %+v", h)
				}
				return
			}
			if synth := synthOn(r.outOn); !slices.Contains(r.outOn.opsSeen(), "interrupt:"+synth+":"+r.out.id) {
				t.Fatalf("speak off did not fence the reply B was speaking: %v", r.outOn.opsSeen())
			}
		})
	}
}

// FINISH KEEPS THE FINAL ANSWER WHICHEVER VOICE SPEAKS IT. The input's
// completion drains the heard session once its reply is settled, names who
// is speaking it, and stops nothing: the reply plays out.
func TestFinishKeepsTheFinalAnswerWhicheverVoiceSpeaksIt(t *testing.T) {
	for _, route := range []struct {
		name, tts, says string
		out             bool
	}{
		{"the hearing engine", "", "reply admitted", true},
		{"a service", "ElevenLabs", "cloud voice chosen to speak", true},
		{"another engine", "id.engine.b", "id.engine.b", true},
		{"a voice that cannot speak, whose reply stays text", "id.engine.b", "reply not spoken", false},
	} {
		t.Run(route.name, func(t *testing.T) {
			r := newHeardRoute(t, route.tts, route.out)
			r.hears(answer)
			r.a.voiceInputFinished(r.hear.id)
			select {
			case <-r.hear.drained:
			case <-time.After(5 * time.Second):
				t.Fatal("the input's completion never drained the session")
			}
			closes := r.hearOn.closes()
			if len(closes) != 1 || !strings.HasPrefix(closes[0], "drain: ") || !strings.Contains(closes[0], route.says) {
				t.Fatalf("the drain does not name who speaks the final answer (%q): %v", route.says, closes)
			}
			if h := r.hushed(); len(h) != 0 {
				t.Fatalf("the drain hushed the final answer: %+v", h)
			}
			for _, f := range []*fakeEngineSession{r.hearOn, r.outOn} {
				if f == nil {
					continue
				}
				for _, op := range f.opsSeen() {
					if strings.HasPrefix(op, "interrupt:") {
						t.Fatalf("the drain stopped the final answer: %v", f.opsSeen())
					}
				}
			}
		})
	}
}

// ANOTHER ENGINE'S ADMISSION IS ITS OWN, AND ITS ANSWER IS FINAL. An unknown
// admission there is fenced and leaves the words text — no second voice is
// bought for a reply B may be speaking — and B's definite refusal is taken
// over by the configured voice under the heard session's custody, as the
// hearing engine's own refusal is.
func TestAnotherEnginesAdmissionDecidesTheHeardReply(t *testing.T) {
	t.Run("unknown", func(t *testing.T) {
		r := newHeardRoute(t, "id.engine.b", true)
		r.out.v = &fallbackAckEngine{fakeEngineSession: r.outOn, ackErr: pluginhosttest.ErrAdmissionUnknown}
		r.hears(answer)
		synth := synthOn(r.outOn)
		if got := r.spokeBy(); got != "id.engine.b×1" {
			t.Fatalf("an unknown admission on B bought another voice: %s", got)
		}
		if !slices.Contains(r.outOn.opsSeen(), "interrupt:"+synth+":"+r.out.id) {
			t.Fatalf("the unknown synthesis on B was not fenced: %v", r.outOn.opsSeen())
		}
		if refs := r.replyRefs(); len(refs) != 1 || !refs[0].TextOnly || refs[0].SessionID != r.hear.id {
			t.Fatalf("the unknown reply did not stay text under the session that heard it: %+v", refs)
		}
		if len(r.hearOn.synthed()) != 0 {
			t.Fatal("the hearing engine spoke a reply B may be speaking")
		}
	})
	t.Run("definitely refused", func(t *testing.T) {
		r := newHeardRoute(t, "id.engine.b", true)
		r.mintID = ""
		r.out.v = &fallbackAckEngine{fakeEngineSession: r.outOn, ackErr: pluginhosttest.Refusal("speech.session.synthesize", `{"code":-32000,"message":"busy"}`)}
		r.hears(answer)
		refs := r.replyRefs()
		if len(refs) != 1 || refs[0].Route != "plugin" || !refs[0].TextOnly || refs[0].Fallback || refs[0].SessionID != r.hear.id {
			t.Fatalf("B's refusal did not leave the reply as text under the heard session: %+v", refs)
		}
		if len(r.hearOn.synthed()) != 0 {
			t.Fatal("the hearing engine took over B's refusal")
		}
		if out := outcomeOf(r.hear); !strings.Contains(out, "not spoken") {
			t.Fatalf("the session does not say the reply was not spoken: %q", out)
		}
		r.a.voiceObserved(decodeVoiceFrame(pluginhost.Event{Type: "speech_start", SessionID: r.hear.id}))
		if h := r.hushed(); len(h) != 0 {
			t.Fatalf("the operator speaking hushed a reply no voice was speaking: %+v", h)
		}
	})
}
