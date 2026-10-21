package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func (a *App) voicePlugin() *pluginhost.ActivePlugin {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	for _, p := range a.plugins {
		if v := p.Voice.Load(); v != nil && v.Alive() {
			return p
		}
	}
	return nil
}

func (a *App) voicePlugins() []*pluginhost.ActivePlugin {
	a.pluginMu.RLock()
	defer a.pluginMu.RUnlock()
	var out []*pluginhost.ActivePlugin
	for _, p := range a.plugins {
		if v := p.Voice.Load(); v != nil && v.Alive() {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) VoiceEngine() bool { return a.voicePlugin() != nil }

func (a *App) voiceHandle(id string) *voiceHandle {
	if val, ok := a.voiceSessions.Load(id); ok {
		return val.(*voiceHandle)
	}
	return nil
}

type inflightSynthesis struct {
	id  string
	rev uint64

	trace *voiceTurnTrace
}

func (h *voiceHandle) setInflight(id string, rev uint64, trace *voiceTurnTrace) {
	h.inflight.Store(inflightSynthesis{id: id, rev: rev, trace: trace})
	h.replyAdmitted(id)
}

func (h *voiceHandle) loadInflight() inflightSynthesis {
	v, _ := h.inflight.Load().(inflightSynthesis)
	return v
}

func (h *voiceHandle) detachInflight(id string) bool {
	cur := h.loadInflight()
	if cur.id == "" || cur.id != id {
		return false
	}
	return h.inflight.CompareAndSwap(cur, inflightSynthesis{})
}

type replyOut struct {
	id    string
	heard atomic.Int64
}

const voiceSpeakingBound = 2 * time.Minute

var voiceClockStart = time.Now()

func voiceClock() time.Duration { return time.Since(voiceClockStart) }

func (h *voiceHandle) replyAdmitted(id string) {
	out := &replyOut{id: id}
	out.heard.Store(int64(voiceClock()))
	h.out.Store(out)
}

func (h *voiceHandle) replyHeard(id string) {
	if out := h.out.Load(); out != nil && (id == "" || out.id == id) {
		out.heard.Store(int64(voiceClock()))
	}
}

func (h *voiceHandle) replyOver(id string) {
	if out := h.out.Load(); out != nil && out.id == id {
		h.out.CompareAndSwap(out, nil)
	}
}

func (h *voiceHandle) speaking() bool { return h.speakingAt(voiceClock()) }

func (h *voiceHandle) speakingAt(now time.Duration) bool {
	out := h.out.Load()
	if out == nil {
		return false
	}
	select {
	case <-h.done:
		return false
	default:
	}
	return now-time.Duration(out.heard.Load()) < voiceSpeakingBound
}

type voiceHandle struct {
	id   string
	v    engineSession
	b    *audio.Binding
	done <-chan struct{}

	engine *pluginhost.ActivePlugin

	speaks string

	gen atomic.Uint64

	work sync.WaitGroup

	inputDone    atomic.Bool
	drainOnce    atomic.Bool
	replyOutcome atomic.Value

	applied atomic.Value

	finalsMu sync.Mutex
	finals   map[int64]rememberedFinal

	admit sync.Mutex

	inflight atomic.Value

	out atomic.Pointer[replyOut]

	fencedMu sync.Mutex
	fenced   [8]string
	fencedN  int

	closing atomic.Bool

	closeAsked atomic.Int64

	drained     chan struct{}
	drainedOnce sync.Once

	fallback atomic.Pointer[takenOver]
	hush     func(why string)

	heldMu   sync.Mutex
	held     map[int64]*heldFinal
	withheld map[int64]bool

	heldDraining bool
	heardTail    <-chan struct{}
}

type takenOver struct {
	route string
	id    string
	on    *voiceHandle
}

const replyNotSpokenOutcome = "reply delivered as text: replies are not spoken in this mode"

func (h *voiceHandle) supersede() { h.gen.Add(1) }

type replyVerdict int

const (
	replyAdmitted replyVerdict = iota

	replyRefusedDefinite

	replySuperseded
)

func refusedByEngine(err error) bool {
	return pluginhost.IsSessionRefused(err)
}

func (h *voiceHandle) hushNow(why string) {
	if h.hush != nil {
		h.hush(why)
	}
}

type engineSession interface {
	FinishInputFor(ctx context.Context, sessionID, streamID string, endSample int64) error
	CloseFor(ctx context.Context, sessionID, mode, reason string) error
	InterruptFor(ctx context.Context, sessionID, synthesisID, cause string) error

	SynthesizeForEnqueue(ctx context.Context, sessionID, synthesisID, text string) (func(context.Context) error, error)
	PlaybackReportFor(ctx context.Context, sessionID string, r pluginhost.PlaybackReport) error

	SynthesisFor(stream uint32) string

	MalformedFor(sessionID string) uint64
	Label() string

	InputClosed() <-chan struct{}
	InputCompletionReason() string
}

func (h *voiceHandle) ID() string { return h.id }

func (h *voiceHandle) Finish(ctx context.Context, endSample int64) error {
	ctx, cancel := context.WithTimeout(ctx, pluginhost.SessionControlBound)
	defer cancel()
	return engineUnavailable(h.v.FinishInputFor(ctx, h.id, h.b.InputHandle, endSample))
}

func (h *voiceHandle) Close(ctx context.Context, mode string) error {
	h.admit.Lock()
	h.supersede()
	h.closing.Store(true)
	h.admit.Unlock()
	if mode == "abort" {
		h.hushNow("the session was aborted")
	}
	ctx, cancel := context.WithTimeout(ctx, pluginhost.SessionControlBound)
	defer cancel()
	return engineUnavailable(h.askClose(ctx, mode, "operator"))
}

func (h *voiceHandle) askClose(ctx context.Context, mode, reason string) error {
	h.closeAsked.CompareAndSwap(0, time.Now().UnixNano())
	err := h.v.CloseFor(ctx, h.id, mode, reason)
	if err != nil {
		h.closeAsked.Store(0)
	}
	return err
}

func (h *voiceHandle) Interrupt(ctx context.Context) error {
	h.admit.Lock()
	h.supersede()
	synthID := h.loadInflight().id
	h.admit.Unlock()
	h.hushNow("the operator spoke")
	ctx, cancel := context.WithTimeout(ctx, pluginhost.SessionControlBound)
	defer cancel()
	return engineUnavailable(h.fence(ctx, synthID, "operator"))
}

func engineUnavailable(err error) error {
	if errors.Is(err, pluginhost.ErrNotAdmitted) || errors.Is(err, pluginhost.ErrSessionDead) {
		return fmt.Errorf("%w: %w", dashboard.ErrVoiceEngineUnavailable, err)
	}
	return err
}

func engineOpenFailed(err error) error {
	if marked := engineUnavailable(err); errors.Is(marked, dashboard.ErrVoiceEngineUnavailable) {
		return marked
	}
	return fmt.Errorf("%w: %w", dashboard.ErrVoiceEngineRefused, err)
}
func (h *voiceHandle) Label() string { return h.v.Label() }

func (h *voiceHandle) Malformed() uint64 { return h.v.MalformedFor(h.id) }

func (h *voiceHandle) InputClosed() <-chan struct{}  { return h.v.InputClosed() }
func (h *voiceHandle) InputCompletionReason() string { return h.v.InputCompletionReason() }
func (h *voiceHandle) Done() <-chan struct{}         { return h.done }
func (h *voiceHandle) Released() <-chan struct{}     { return h.b.Released() }

func (h *voiceHandle) PlaybackReport(ctx context.Context, r dashboard.PlaybackReport) error {

	synth := h.v.SynthesisFor(r.Stream)
	ctx, cancel := context.WithTimeout(ctx, pluginhost.SessionControlBound)
	defer cancel()
	if err := h.v.PlaybackReportFor(ctx, h.id, pluginhost.PlaybackReport{Stream: r.Stream, Rendered: r.Rendered, Rate: r.Rate, Channels: r.Channels, Terminal: r.Terminal, Outcome: r.Outcome}); err != nil {
		return err
	}
	if !r.Terminal {
		h.replyHeard(synth)
		return nil
	}
	if synth != "" {
		h.traceOf(synth).end("the page's terminal receipt for its stream came before any frame reached the sink")
		h.detachInflight(synth)

		h.replyOver(synth)
	}
	return nil
}

func (a *App) synthesizeReply(ctx context.Context, sessionID string, gen uint64, reply string) replyVerdict {
	verdict, _ := a.admitReply(ctx, sessionID, gen, reply)
	return verdict
}

func (a *App) admitReply(ctx context.Context, sessionID string, gen uint64, reply string) (replyVerdict, string) {
	if sessionID == "" || reply == "" {
		return replySuperseded, ""
	}
	var h *voiceHandle

	trace := voiceTraceFrom(ctx)
	report := func(why string, superseded bool) {
		trace.end("the reply was refused at admission: " + why)
		a.voiceStaleReplies.Add(1)
		logsink.Info("voice.refusal", "reply for session %s refused at admission: %s", sessionID, why)
		a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: sessionID, Type: "reply_refused", Reason: why, Superseded: superseded})
		if h != nil {
			h.replyOutcome.Store("reply refused: " + why)
		}
	}
	refuse := func(why string) { report(why, false) }

	superseded := func(why string) { report(why, true) }
	if h = a.voiceHandle(sessionID); h == nil {
		refuse("the session ended before its reply")
		return replySuperseded, ""
	}
	textOnly := func() replyVerdict {
		trace.end(replyNotSpokenOutcome)
		h.replyOutcome.Store(replyNotSpokenOutcome)
		if a.voiceReplySink != nil {
			a.voiceReplySink(dashboard.VoiceReplyRef{SessionID: sessionID, Route: "plugin", TextOnly: true}, reply)
		}
		return replyAdmitted
	}

	for {
		h.admit.Lock()
		if h.gen.Load() != gen || h.closing.Load() || ctx.Err() != nil {
			h.admit.Unlock()
			superseded("a barge-in, close, abort or cancellation superseded this reply")
			return replySuperseded, ""
		}
		if _, speak, _ := resolveVoiceMode(a.publishedVoiceMode()); speak == speakOff {
			h.admit.Unlock()
			return textOnly(), ""
		}
		prior := h.loadInflight().id
		if prior == "" {
			break
		}
		h.admit.Unlock()
		fctx, fcancel := context.WithTimeout(ctx, pluginhost.SessionControlBound)
		err := h.fence(fctx, prior, "a newer reply took its place")
		fcancel()
		if err != nil {
			refuse(fmt.Sprintf("the engine did NOT fence preceding synthesis %s (%v); no replacement was sent", prior, err))
			return replySuperseded, ""
		}
		h.detachInflight(prior)
	}
	if h.gen.Load() != gen || h.closing.Load() || ctx.Err() != nil {
		h.admit.Unlock()
		superseded("a barge-in, close, abort or cancellation superseded this reply")
		return replySuperseded, ""
	}

	admittedUnder := a.publishedVoiceMode()
	if _, speak, _ := resolveVoiceMode(admittedUnder); speak == speakOff {
		h.admit.Unlock()
		return textOnly(), ""
	}
	synthID := fmt.Sprintf("%s-syn-%d", sessionID, a.voiceSeq.Add(1))
	ectx, ecancel := context.WithTimeout(ctx, pluginhost.SessionControlBound)
	await, err := h.v.SynthesizeForEnqueue(ectx, h.id, synthID, reply)
	ecancel()
	if err == nil {
		trace.stamp(stageEnqueued)
		h.setInflight(synthID, admittedUnder.Revision, trace)
	}
	h.admit.Unlock()
	if err != nil {
		refuse("not dispatched: " + err.Error())
		return replySuperseded, ""
	}
	actx, acancel := context.WithTimeout(ctx, pluginhost.SessionControlBound)
	err = await(actx)
	acancel()
	if err != nil {
		why := err.Error()
		if pluginhost.IsAdmissionUnknown(err) {

			if ferr := h.fenceBounded(synthID, "admission unknown"); ferr != nil {
				why = fmt.Sprintf("the synthesis admission is unknown and the fence was refused (%v): the engine may speak %s", ferr, synthID)
			} else {
				why = "the synthesis admission is unknown; " + synthID + " was fenced"
			}
		}
		refuse(why)
		if refusedByEngine(err) {
			h.detachInflight(synthID)
			h.replyOver(synthID)
			return replyRefusedDefinite, ""
		}
		return replySuperseded, ""
	}
	trace.stamp(stageAcknowledged)

	if h.gen.Load() != gen || h.closing.Load() || ctx.Err() != nil {
		if ferr := h.fenceBounded(synthID, "superseded at admission"); ferr != nil {
			refuse(fmt.Sprintf("superseded while its admission was pending; the engine did NOT fence synthesis %s (%v) — the barge-in's own stop/cancel and the engine's speech fence are the remaining guards", synthID, ferr))
			return replySuperseded, ""
		}
		superseded("superseded while its admission was pending; its synthesis " + synthID + " was fenced")
		return replySuperseded, ""
	}

	if a.voiceSpeakOffRev.Load() > admittedUnder.Revision {
		if h.detachInflight(synthID) {
			if ferr := h.fenceBounded(synthID, "the mode's speak turned off"); ferr != nil {
				logsink.Warn("voice.refusal", "speak turned off during admission; the engine did NOT fence synthesis %s on %s (%v) — the page's own silence is the remaining guard", synthID, sessionID, ferr)
			}
		}
		return textOnly(), ""
	}
	h.replyOutcome.Store("reply admitted")
	if a.voiceReplySink != nil {
		a.voiceReplySink(dashboard.VoiceReplyRef{SessionID: sessionID, SynthesisID: synthID, Route: "plugin"}, reply)
	}
	return replyAdmitted, synthID
}

func (a *App) speakFallback(ctx context.Context, b *voiceBinding, reply string) bool {
	return a.speakThrough(ctx, b, reply, true, "")
}

func (a *App) speakThrough(ctx context.Context, b *voiceBinding, reply string, refused bool, cannot string) bool {
	h := a.voiceHandle(b.session)
	if h == nil {
		return false
	}
	h.admit.Lock()
	defer h.admit.Unlock()
	if h.gen.Load() != b.gen || h.closing.Load() || ctx.Err() != nil {
		if refused {
			a.noteReplyOutcome(b.session, "reply refused by the engine, then superseded before another voice could take it")
		} else {
			a.noteReplyOutcome(b.session, "reply superseded before the voice chosen to speak it could take it")
		}
		return false
	}

	_, speak, _ := a.voiceMode()

	if speak == speakOff {
		if a.voiceReplySink != nil {
			a.voiceReplySink(dashboard.VoiceReplyRef{SessionID: b.session, Route: "plugin", TextOnly: true}, reply)
		}
		a.noteReplyOutcome(b.session, replyNotSpokenOutcome)
		b.trace.end(replyNotSpokenOutcome)
		return true
	}
	route, id := "browser", ""
	if cannot == "" {
		if id = voiceFallbackMint(a, reply); id != "" {
			route = "cloud"
		}
	}

	if route == "browser" && (refused || cannot != "") {
		why := cannot
		if refused {
			why = "the speech engine refused this reply and no other voice is chosen to speak replies"
		} else {
			a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: b.session, Type: "reply_refused", Reason: cannot})
		}
		logsink.Info("voice.refusal", "reply for session %s is not spoken: %s; it is delivered as text and the browser's own voice does not take it", b.session, why)
		a.noteReplyOutcome(b.session, "reply not spoken: "+why)
		return false
	}
	if !a.takeOver(h, b.gen, takenOver{route: route, id: id}) {
		a.noteReplyOutcome(b.session, "reply superseded while the "+route+" voice took it; nothing of it is spoken")
		return false
	}
	if a.voiceReplySink != nil {
		a.voiceReplySink(dashboard.VoiceReplyRef{SessionID: b.session, SynthesisID: id, Route: route, Fallback: refused}, reply)
	}

	switch {
	case refused:
		a.noteReplyOutcome(b.session, "reply refused by the engine; spoken by the "+route+" voice instead")
	case route == "cloud":
		a.noteReplyOutcome(b.session, "reply spoken by the cloud voice chosen to speak replies")
	default:

		a.noteReplyOutcome(b.session, "the service chosen to speak replies did not take this reply; the browser's own voice speaks it")
	}

	if refused || route == "browser" {
		a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: b.session, Type: "reply_fallback", Reason: route})
	}
	return true
}

func (a *App) speaksAsService(pointer string) bool {
	p := strings.TrimSpace(pointer)
	return p != "" && !a.engineInstalled(p)
}

func (a *App) speakHeard(ctx context.Context, b *voiceBinding, reply string) replyVerdict {
	heard := a.voiceHandle(b.session)
	if heard == nil {
		return voiceSynthesize(a, ctx, b.session, b.gen, reply)
	}
	choice := strings.TrimSpace(a.configSnapshot().Speech.TTS.Provider)
	if a.speaksAsService(choice) {
		return a.spokenThrough(ctx, b, reply, "")
	}

	if !a.speaksAsService(heard.speaks) {
		choice = heard.speaks
	}
	if choice == "" || heard.engine != nil && heard.engine.ID == choice {
		return voiceSynthesize(a, ctx, b.session, b.gen, reply)
	}
	on := a.speechEngineFor(choice)
	if on == nil {
		return a.spokenThrough(ctx, b, reply, choice+" is chosen to speak replies and is not running here")
	}
	out := a.voiceOutputSession(on)
	if out == nil {
		return a.spokenThrough(ctx, b, reply, choice+" is chosen to speak replies and no page holds its speaker open")
	}
	return a.speakOn(ctx, heard, b, out, reply)
}

func (a *App) spokenThrough(ctx context.Context, b *voiceBinding, reply, cannot string) replyVerdict {
	if a.speakThrough(ctx, b, reply, false, cannot) {
		return replyAdmitted
	}
	return replySuperseded
}

func (a *App) refuseHeard(h *voiceHandle, why string) {
	logsink.Info("voice.refusal", "reply for session %s refused: %s", h.id, why)
	a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: h.id, Type: "reply_refused", Reason: why})
	h.replyOutcome.Store("reply refused: " + why)
}

func (a *App) speakOn(ctx context.Context, heard *voiceHandle, b *voiceBinding, out *voiceHandle, reply string) replyVerdict {
	if heard.gen.Load() != b.gen || heard.closing.Load() || ctx.Err() != nil {
		a.refuseHeard(heard, "a barge-in, close, abort or cancellation superseded this reply")
		return replySuperseded
	}
	name := out.id
	if out.engine != nil {
		name = out.engine.ID
	}
	verdict, synthID := a.admitReply(ctx, out.id, out.gen.Load(), reply)
	if verdict != replyAdmitted || synthID == "" {
		if why, _ := out.replyOutcome.Load().(string); why != "" {
			heard.replyOutcome.Store(why + " (on " + name + ", the engine chosen to speak replies)")
		}
		return verdict
	}
	if !a.takeOver(heard, b.gen, takenOver{route: "engine", id: synthID, on: out}) {
		heard.replyOutcome.Store("reply superseded as " + name + " admitted it; its synthesis " + synthID + " is fenced")
		return replyAdmitted
	}
	heard.replyOutcome.Store("reply admitted on " + name + ", the engine chosen to speak replies")
	return replyAdmitted
}

func (a *App) takeOver(h *voiceHandle, gen uint64, t takenOver) bool {
	if prior := h.fallback.Swap(&t); prior != nil && prior.route != "" {

		a.hushTakenOver(h.id, *prior, "a newer reply took its place")
	}
	if h.gen.Load() != gen || h.closing.Load() {
		a.hushFallback(h, "superseded while another voice took the reply")
		return false
	}
	return true
}

func (a *App) hushFallback(h *voiceHandle, why string) {
	if which := h.fallback.Swap(nil); which != nil && which.route != "" {
		a.hushTakenOver(h.id, *which, why)
	}
}

func (a *App) hushTakenOver(session string, t takenOver, why string) {
	if t.route == "engine" {

		on, id := t.on, t.id
		logsink.Info("voice.decision", "the reply %s speaks for session %s is fenced: %s", on.id, session, why)
		a.engines.start("the fence of the reply "+on.id+" speaks for session "+session, func() {
			select {
			case <-on.done:
				return
			default:
			}
			if err := on.fenceBounded(id, why); err != nil {
				logsink.Warn("voice.refusal", "session %s's reply on %s: the engine did NOT fence synthesis %s (%v)", session, on.id, id, err)
			}
		})
		return
	}
	if t.id != "" {
		a.hushSpoken(t.id)
	}
	logsink.Info("voice.decision", "the %s voice's reply for session %s was hushed: %s", t.route, session, why)
	if a.voiceHushSink != nil {
		a.voiceHushSink(dashboard.VoiceHush{SessionID: session, SynthesisID: t.id, Route: t.route, Reason: why})
	}
}

func (h *voiceHandle) fenceBounded(synthID, cause string) error {
	fctx, cancel := context.WithTimeout(context.Background(), pluginhost.SessionControlBound)
	defer cancel()
	return h.fence(fctx, synthID, cause)
}

func (h *voiceHandle) fence(ctx context.Context, synthID, cause string) error {
	if synthID != "" {
		h.fencedMu.Lock()
		h.fenced[h.fencedN%len(h.fenced)] = synthID
		h.fencedN++
		h.fencedMu.Unlock()
	}

	current := h.out.Load()
	err := h.v.InterruptFor(ctx, h.id, synthID, cause)
	if err == nil {

		switch {
		case synthID != "":
			h.replyOver(synthID)
		case current != nil:
			h.replyOver(current.id)
		}

		h.traceOf(synthID).end("its synthesis was fenced before any frame reached the sink: " + cause)
	}
	return err
}

func (h *voiceHandle) fenceEcho(f voiceFrame) bool {
	synth, reason := f.body.SynthesisID, f.body.Reason
	if synth == "" || reason != "stop_playback" && reason != "cancel_synthesis" {
		return false
	}
	h.fencedMu.Lock()
	defer h.fencedMu.Unlock()
	for _, id := range h.fenced {
		if id == synth {
			return true
		}
	}
	return false
}

func (a *App) handleHeard(ctx context.Context, heard heardUtterance) {
	heard.trace.stamp(stageTurnStart)

	if heard.Answer {
		if listen, _, _ := a.voiceMode(); listen == listenMeeting {
			heard.Answer = false
		}
	}
	err := a.observeVoice(ctx, heard)
	switch {
	case err != nil:

		what, outcome := "was not recorded", "no reply: "
		var turn *turnError
		if errors.As(err, &turn) && turn.recorded {
			what = "was recorded; answering it failed"
			if turn.replied {
				what, outcome = "was recorded; the reply was spoken but not recorded", "replied, but the reply was not recorded: "
			}
		}
		logsink.Warn("voice.error", "the engine's transcript %s: %v", what, err)
		a.noteReplyOutcome(heard.SessionID, outcome+err.Error())

		heard.trace.settle("the turn failed: the transcript " + what)
	case strings.TrimSpace(heard.Text) == "":
		a.noteReplyOutcome(heard.SessionID, "empty transcript, no reply")
		heard.trace.end("empty transcript, no reply")
	case !heard.Answer:
		a.noteReplyOutcome(heard.SessionID, "recorded (meeting), no reply")
		heard.trace.end("recorded (meeting), no reply")
	}
}

func (a *App) noteReplyOutcome(sessionID, outcome string) {
	if h := a.voiceHandle(sessionID); h != nil {
		h.replyOutcome.Store(outcome)
	}
}

func (a *App) voiceInputFinished(sessionID string) {
	h := a.voiceHandle(sessionID)
	if h == nil {
		return
	}
	if !h.inputDone.CompareAndSwap(false, true) {
		return
	}

	a.engines.start("the drain after "+sessionID+"'s input", func() {
		h.work.Wait()

		a.voiceDrainAfterInput(a.lifetime(), h)
	})
}

func (a *App) voiceDrainAfterInput(ctx context.Context, h *voiceHandle) {
	defer h.markDrained()
	if h.closing.Load() {
		a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: h.id, Type: "drain_skipped", Reason: "an abort or close superseded the final reply"})
		return
	}
	outcome := "no utterance after Finish, no reply"
	if r, _ := h.replyOutcome.Load().(string); r != "" {
		outcome = r
	}
	if !h.drainOnce.CompareAndSwap(false, true) {
		return
	}
	a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: h.id, Type: "drain_requested", Reason: outcome})
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if cerr := h.askClose(dctx, "drain", outcome); cerr != nil {
		logsink.Warn("voice.error", "drain-close after the input's completion refused on %s: %v", h.id, cerr)
		a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: h.id, Type: "drain_refused", Reason: cerr.Error()})
	}
}

func (h *voiceHandle) markDrained() {
	h.drainedOnce.Do(func() {
		if h.drained != nil {
			close(h.drained)
		}
	})
}

func (a *App) voiceGen(sessionID string) uint64 {
	if h := a.voiceHandle(sessionID); h != nil {
		return h.gen.Load()
	}
	return 0
}

func (a *App) voiceObserved(ev voiceFrame) {
	switch ev.Type {
	case pluginhost.EventSpeechStart, pluginhost.EventInterruptionRequested:
		if h := a.voiceHandle(ev.SessionID); h != nil {
			if ev.Type == pluginhost.EventInterruptionRequested && h.fenceEcho(ev) {
				return
			}
			h.supersede()
			if ev.Type == pluginhost.EventSpeechStart {

				if synthID := h.loadInflight().id; synthID != "" {
					a.runBackground(func() {
						ctx, cancel := context.WithTimeout(a.lifetime(), pluginhost.SessionControlBound)
						defer cancel()
						if err := h.fence(ctx, synthID, "the engine heard the operator speak"); err != nil {
							logsink.Warn("voice.refusal", "speech did not fence synthesis %s on %s: %v", synthID, h.id, err)
						}
					})
				}
			}

			h.hushNow("the engine heard the operator speak")
		}
	case pluginhost.EventInputFinished:
		a.voiceInputFinished(ev.SessionID)
	case pluginhost.EventSynthesisStart, pluginhost.EventSynthesisCancelled:

		if h := a.voiceHandle(ev.SessionID); h != nil {
			t := h.traceOf(ev.body.SynthesisID)
			if ev.Type == pluginhost.EventSynthesisStart {
				t.stamp(stageSynthesisStart)
			} else {
				t.end("the engine cancelled its synthesis before any frame reached the sink")
			}
		}
	}
}

func (a *App) voiceOpenMode(id, asked string) string {
	if listen, _, _ := a.voiceMode(); listen == listenMeeting {
		asked = listenMeeting
	}
	a.voiceModes.Store(id, asked == "conversation")
	return asked
}

func (a *App) OpenVoiceSession(ctx context.Context, inputID, outputID, mode string, processing *dashboard.CaptureProcessing) (dashboard.VoiceSession, error) {

	chosen := a.configSnapshot().Speech
	half, pointer := "hearing", chosen.STT.Provider
	if inputID == "" {
		half, pointer = "speaking", chosen.TTS.Provider
	}

	gaveUp := func(err error) error {
		return fmt.Errorf("the voice session was not opened: this open was given up while a session in its way was ending (%w)", err)
	}
	if err := a.awaitVoiceClosing(ctx, a.speechEngineFor(pointer)); err != nil {
		return nil, gaveUp(err)
	}
	release, err := a.voiceOpens.take(ctx)
	if err != nil {
		return nil, fmt.Errorf("the voice session was not opened: this open was given up while another was still being answered (%w)", err)
	}
	defer release()
	ap := a.speechEngineFor(pointer)
	if ap == nil {
		if a.voicePlugin() == nil {
			return nil, errors.New("no speech engine is active")
		}
		return nil, fmt.Errorf("%q is chosen for %s and is not a speech engine running here; no session opens on another engine in its place", strings.TrimSpace(pointer), half)
	}
	v := ap.Voice.Load()

	nonce, err := speakID()
	if err != nil {
		return nil, fmt.Errorf("voice session identity: %w", err)
	}
	id := "vs-" + nonce

	a.noteVoiceOpened(id, v)
	var b *audio.Binding

	if inputID != "" || dashboard.OperatorGesture(ctx) {
		a.yieldVoiceOutput(ctx, ap)
	}

	if err := a.awaitVoiceClosing(ctx, ap); err != nil {
		return nil, gaveUp(err)
	}
	if inputID == "" {

		b, err = a.AudioPlane().BindOutput(id, outputID, ap.Contained)
		mode = voiceOutputMode
	} else {
		b, err = a.AudioPlane().Bind(id, inputID, outputID, ap.Contained)
	}
	if err != nil {
		return nil, err
	}

	b.Sink = &firstFrameSink{Sink: b.Sink, wrote: func(fr audio.Frame, at time.Time) {

		a.runBackground(func() { a.voiceFrameWritten(id, fr, at) })
	}}
	if b.HasInput() {
		mode = a.voiceOpenMode(id, mode)
	} else {
		a.voiceModes.Store(id, false)
	}
	a.ensureVoiceObserver(ap, v)
	args := map[string]any{"mode": mode}
	if b.HasInput() && processing != nil {
		if len(processing.ChannelRoles) != 0 {
			args["capture_channel_roles"] = append([]string(nil), processing.ChannelRoles...)
		}
		reported := map[string]any{
			"echo_cancellation": processing.EchoCancellation,
			"noise_suppression": processing.NoiseSuppression,
			"auto_gain_control": processing.AutoGainControl,
			"tested":            false,
		}
		if processing.SampleRate > 0 {
			reported["sample_rate"] = processing.SampleRate
		}
		args["capture_processing"] = reported
	}
	if err := voiceEngineOpen(v, ctx, id, b, args); err != nil {

		a.voicePending.Delete(id)
		a.voiceModes.Delete(id)
		return nil, engineOpenFailed(err)
	}
	h := &voiceHandle{id: id, v: v, b: b, done: v.Done(), engine: ap, drained: make(chan struct{})}
	if b.HasInput() {
		h.speaks = strings.TrimSpace(chosen.TTS.Provider)
	}
	h.hush = func(why string) { a.hushFallback(h, why) }

	a.voiceSessions.Store(id, h)
	a.adoptPendingSettings(h)
	a.watchSessionEnd(h)
	return h, nil
}

func (a *App) watchSessionEnd(h *voiceHandle) {
	a.engines.start("the end of voice session "+h.id, func() {
		<-h.done
		a.voiceSessionEnded(h)
	})
}

type openTurn struct {
	once sync.Once
	held chan struct{}
}

func (t *openTurn) take(ctx context.Context) (func(), error) {
	t.once.Do(func() { t.held = make(chan struct{}, 1) })
	select {
	case t.held <- struct{}{}:
		return func() { <-t.held }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (a *App) handOverSessions(prev *pluginhost.ActivePlugin, bound time.Duration) (release func(), err error) {
	if prev == nil || prev.Voice.Load() == nil {
		return func() {}, nil
	}
	if bound <= 0 {
		bound = pluginhost.HandOverBudget
	}

	ctx, cancel := context.WithTimeout(a.lifetime(), bound)
	defer cancel()
	release, err = a.voiceOpens.take(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: an open on it was still being answered", pluginhost.ErrHandOverBusy)
	}
	open := a.sessionsOn(prev)
	for _, h := range open {
		select {
		case <-h.done:
		case <-ctx.Done():
		}
	}
	for _, h := range a.sessionsOn(prev) {
		logsink.Warn("voice.session", "session %s on %s %s did not end within %s of its successor being ready — ended as the operator's stop ends one, so that one release writes the plugin's storage at a time", h.id, prev.ID, prev.Version, bound)
		sctx, scancel := context.WithTimeout(a.lifetime(), pluginhost.StopBudget)
		if cerr := h.Close(sctx, "abort"); cerr != nil {
			logsink.Warn("voice.error", "session %s could not be asked to end: %v", h.id, cerr)
		}
		select {
		case <-h.done:
		case <-sctx.Done():
		}
		scancel()
	}
	if left := a.sessionsOn(prev); len(left) > 0 {
		release()
		return nil, fmt.Errorf("%w: session %s on it did not end when asked", pluginhost.ErrHandOverBusy, left[0].id)
	}
	return release, nil
}

func (a *App) sessionsOn(ap *pluginhost.ActivePlugin) []*voiceHandle {
	var out []*voiceHandle
	a.voiceSessions.Range(func(_, val any) bool {
		h := val.(*voiceHandle)
		if h.engine != ap {
			return true
		}
		select {
		case <-h.done:
		default:
			out = append(out, h)
		}
		return true
	})
	return out
}

const voiceOutputMode = "output"

func (a *App) voiceOutputSession(on *pluginhost.ActivePlugin) *voiceHandle {
	var found *voiceHandle
	a.voiceSessions.Range(func(_, val any) bool {
		h := val.(*voiceHandle)
		if on != nil && h.engine != on || h.b == nil || h.b.HasInput() || h.closing.Load() {
			return true
		}
		select {
		case <-h.done:
			return true
		default:
		}
		found = h
		return false
	})
	return found
}

func (a *App) awaitVoiceClosing(ctx context.Context, on *pluginhost.ActivePlugin) error {
	if on == nil {
		return nil
	}
	bound := a.configSnapshot().Speech.openWait()
	var gaveUp error
	a.voiceSessions.Range(func(_, val any) bool {
		h := val.(*voiceHandle)
		if h.engine != on {
			return true
		}
		gaveUp = awaitVoiceEnd(ctx, h, bound)
		return gaveUp == nil
	})
	return gaveUp
}

func awaitVoiceEnd(ctx context.Context, h *voiceHandle, bound time.Duration) error {
	asked := h.closeAsked.Load()
	if asked == 0 {
		return nil
	}
	left := time.Until(time.Unix(0, asked).Add(bound))
	if left <= 0 {
		return nil
	}
	t := time.NewTimer(left)
	defer t.Stop()
	select {
	case <-h.done:
	case <-t.C:
		logsink.Warn("voice.session", "session %s was asked to end and had not within %s; the open that waited for it goes to the engine", h.id, bound)
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	return nil
}

func (a *App) yieldVoiceOutput(ctx context.Context, on *pluginhost.ActivePlugin) {
	h := a.voiceOutputSession(on)
	if h == nil {
		return
	}

	bound := a.configSnapshot().Speech.openWait()
	cctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	if err := h.Close(cctx, "abort"); err != nil {
		logsink.Warn("voice.error", "output-only session %s could not be asked to yield the engine: %v", h.id, err)
		return
	}
	logsink.Info("voice.session", "output-only session %s was asked to yield the engine to the page the operator is using", h.id)

	_ = awaitVoiceEnd(ctx, h, bound)
}

func (a *App) typedReplyVoice() *voiceBinding {
	choice := a.configSnapshot().Speech.TTS.Provider
	if a.speaksAsService(choice) {
		return nil
	}
	h := a.speakingSession(choice)
	if h == nil {
		return nil
	}
	return &voiceBinding{session: h.id, gen: h.gen.Load()}
}

func (a *App) typedReplyInConversation() *voiceBinding {
	if _, speak, _ := a.voiceMode(); speak != speakOn {
		return nil
	}
	if a.speaksAsService(a.configSnapshot().Speech.TTS.Provider) {
		return nil
	}
	var found *voiceHandle
	a.voiceSessions.Range(func(_, val any) bool {
		h := val.(*voiceHandle)
		if h.b == nil || !h.b.HasInput() || h.closing.Load() {
			return true
		}
		select {
		case <-h.done:
			return true
		default:
		}
		found = h
		return false
	})
	if found == nil {
		return nil
	}
	return &voiceBinding{session: found.id, gen: found.gen.Load()}
}

func (a *App) speakingSession(choice string) *voiceHandle {
	if on := a.speechEngineFor(choice); on != nil {
		if h := a.voiceOutputSession(on); h != nil {
			return h
		}
	}
	return a.voiceOutputSession(nil)
}

func (a *App) voiceSpeakerCarried() string {
	choice := a.configSnapshot().Speech.TTS.Provider
	if a.speaksAsService(choice) {
		return ""
	}
	h := a.speakingSession(choice)
	if h == nil || h.engine == nil {
		return ""
	}
	if on := a.speechEngineFor(choice); on != nil && on.ID == h.engine.ID {
		return ""
	}
	return h.engine.ID
}

func (a *App) voiceSessionEnded(h *voiceHandle) {
	h.supersede()
	h.closing.Store(true)
	h.loadInflight().trace.end("the session ended before any frame of its synthesis reached the sink")
	a.withholdHeld(h, "the session ended before the speaker was decided")
	h.markDrained()
	a.voiceSessions.Delete(h.id)
	a.voiceModes.Delete(h.id)
	a.voicePending.Delete(h.id)
	a.forgetPendingObservations(h.id)
}

func (a *App) abortVoiceSessionsUnderSafe(reason string) {
	a.voiceSessions.Range(func(_, val any) bool {
		h := val.(*voiceHandle)
		if h.b.Contained && !h.b.Remote {
			return true
		}
		select {
		case <-h.done:
			return true
		default:
		}

		a.engines.start("the SAFE abort of session "+h.id, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			h.admit.Lock()
			h.supersede()
			h.closing.Store(true)
			h.admit.Unlock()
			if err := h.askClose(ctx, "abort", "SAFE: "+reason); err != nil {
				logsink.Error("voice.error", "SAFE could not abort session %s (%s → %s): %v", h.id, h.b.InputID, h.b.OutputID, err)
				return
			}
			logsink.Warn("voice.session", "SAFE aborted session %s (%s → %s): no audio leaves the host while SAFE holds (%s)", h.id, h.b.InputID, h.b.OutputID, reason)
		})
		return true
	})
}

func (a *App) ensureVoiceObserver(ap *pluginhost.ActivePlugin, v *pluginhost.VoiceSession) {
	a.voiceObsMu.Lock()
	defer a.voiceObsMu.Unlock()
	if a.voiceObs == nil {
		a.voiceObs = map[*pluginhost.VoiceSession]bool{}
	}
	if a.voiceObs[v] {
		return
	}
	a.voiceObs[v] = true

	a.engines.start("the observer of "+engineName(ap), func() { a.observeEngine(ap, v) })
}

func engineName(ap *pluginhost.ActivePlugin) string {
	if ap == nil {
		return "a speech engine"
	}
	return "speech engine " + ap.ID
}

func (a *App) observeEngine(ap *pluginhost.ActivePlugin, v *pluginhost.VoiceSession) {

	fan := make(chan dashboard.VoiceEvent, 64)
	fanDone := make(chan struct{})
	go func() {
		defer close(fanDone)
		for ev := range fan {
			a.fanVoiceEvent(ev)
		}
	}()
	enqueue := func(ev dashboard.VoiceEvent) {
		select {
		case fan <- ev:
		default:
			a.voiceFanDropped.Add(1)
		}
	}

	telemetryDone := make(chan struct{})
	go func() {
		defer close(telemetryDone)
		for ev := range v.Telemetry() {
			if ev.Type == pluginhost.EventTranscriptPartial && !a.openedHere(ev.SessionID) {
				continue
			}
			_, safe := a.SafeMode()
			if ve, ok := a.voicePageEvent(decodeVoiceFrame(ev), safe); ok {
				enqueue(ve)
			}
		}
	}()
	for ev := range v.Observe() {
		a.voiceEngineEvent(ap, ev, enqueue)
	}
	<-telemetryDone

	close(fan)
	<-fanDone
	term := dashboard.VoiceEvent{Type: "closed"}
	if v.Faulted() {
		term.Type, term.Reason = "failed", v.FaultReason()
	}
	a.fanVoiceEvent(term)
	a.voiceObsMu.Lock()
	delete(a.voiceObs, v)

	for id, on := range a.voiceOpened {
		if on == v {
			delete(a.voiceOpened, id)
		}
	}
	a.voiceObsMu.Unlock()
}

type voiceStep struct {
	ap      *pluginhost.ActivePlugin
	ev      voiceFrame
	enqueue func(dashboard.VoiceEvent)

	safe   bool
	reason string

	pol  speakerPolicy
	hold bool

	ve    dashboard.VoiceEvent
	shown bool
}

func (a *App) voiceEngineEvent(ap *pluginhost.ActivePlugin, raw pluginhost.Event, enqueue func(dashboard.VoiceEvent)) {

	s := &voiceStep{ap: ap, ev: decodeVoiceFrame(raw), enqueue: enqueue}
	if a.withheldAtTheDoor(s) || a.heardOnASessionNotOpenedHere(s) || !a.ownedObservation(s) || a.heardWithNoInput(s) {
		return
	}
	s.reason, s.safe = a.SafeMode()
	s.pol = a.speakerPolicyNow()
	s.hold = a.holdsForSpeaker(s)
	if a.toThePage(s) {
		return
	}
	a.applyCustody(s)
	if s.ev.Type == pluginhost.EventTranscriptFinal {
		a.takeFinal(s)
	}
}

func (a *App) withheldAtTheDoor(s *voiceStep) bool {
	ev := s.ev

	if ev.err != nil {
		if ev.Type == pluginhost.EventTranscriptFinal {
			a.speakerWithheldFinals.Add(1)
			s.enqueue(dashboard.VoiceEvent{Type: "transcript_withheld", SessionID: ev.SessionID, Sequence: ev.Sequence,
				Reason: "the engine's transcript did not decode; no words admitted"})
		}
		return true
	}
	if ev.Type == pluginhost.EventTranscriptFinal {
		if segment := ev.segment(); ev.body.TrackDeclared && !segment.valid() {
			a.speakerWithheldFinals.Add(1)
			logsink.Warn("voice.refusal", "session %s: final %d declares a speaker track that is not valid and is withheld (track_id %q, start_sample %s, end_sample %s): no words and no speaker reach the page, a record or a turn",
				ev.SessionID, ev.Sequence, segment.TrackID, sampleText(segment.StartSample), sampleText(segment.EndSample))
			s.enqueue(dashboard.VoiceEvent{Type: "transcript_withheld", SessionID: ev.SessionID, Sequence: ev.Sequence,
				Reason: "invalid transcript segment; no words admitted"})
			return true
		}
	}
	return false
}

func (a *App) ownedObservation(s *voiceStep) bool {
	if s.ev.Type != pluginhost.EventSpeakerObservation {
		return true
	}
	if !a.acceptSpeakerObservation(s.ev) {
		return false
	}
	a.amendHeard(s.ev)
	return true
}

func (a *App) noteVoiceOpened(id string, v *pluginhost.VoiceSession) {
	a.voiceObsMu.Lock()
	defer a.voiceObsMu.Unlock()
	if a.voiceOpened == nil {
		a.voiceOpened = map[string]*pluginhost.VoiceSession{}
	}
	a.voiceOpened[id] = v
}

func (a *App) openedHere(id string) bool {
	if id == "" {
		return false
	}
	a.voiceObsMu.Lock()
	_, opened := a.voiceOpened[id]
	a.voiceObsMu.Unlock()
	return opened || a.voiceHandle(id) != nil
}

func (a *App) heardOnASessionNotOpenedHere(s *voiceStep) bool {
	ev := s.ev
	if ev.Type != pluginhost.EventTranscriptFinal && ev.Type != pluginhost.EventTranscriptPartial {
		return false
	}
	if a.openedHere(ev.SessionID) {
		return false
	}
	engine := "the speech engine"
	if s.ap != nil {
		engine += " " + s.ap.ID
	}
	logsink.Warn("voice.refusal", "%s sent a %s on session %q, which this application did not open — the words reach no page, record or turn", engine, ev.Type, ev.SessionID)
	return true
}

func (a *App) heardWithNoInput(s *voiceStep) bool {
	ev := s.ev
	if ev.Type != pluginhost.EventTranscriptFinal && ev.Type != pluginhost.EventTranscriptPartial {
		return false
	}
	h := a.voiceHandle(ev.SessionID)
	if h == nil || h.b == nil || h.b.Source != nil || h.b.InputHandle != "" {
		return false
	}

	engine := "the speech engine"
	if s.ap != nil {
		engine += " " + s.ap.ID
	}
	logsink.Warn("voice.refusal", "%s sent a %s on %s, a session opened with no input — the contract says none comes; the words reach no page, record or turn", engine, ev.Type, ev.SessionID)
	return true
}

func (a *App) holdsForSpeaker(s *voiceStep) bool {
	ev := s.ev
	if ev.Type != pluginhost.EventTranscriptFinal {
		return s.pol.restricted()
	}
	if s.pol.restricted() || ev.segment().valid() {
		return true
	}
	h := a.voiceHandle(ev.SessionID)
	if h == nil {
		return false
	}
	h.heldMu.Lock()
	defer h.heldMu.Unlock()
	return len(h.held) != 0 || h.heldDraining
}

func (a *App) toThePage(s *voiceStep) bool {
	ev := s.ev
	s.ve, s.shown = a.voicePageEvent(ev, s.safe)

	if ev.Type == pluginhost.EventInterruptionRequested {
		if h := a.voiceHandle(ev.SessionID); h != nil && h.fenceEcho(ev) {
			s.ve.Stream = ev.outputStream()
		}
	}

	if ev.Type == pluginhost.EventSpeakerObservation && !s.safe && a.observationForHeld(ev) {
		return true
	}
	if s.shown && !(s.hold && ev.Type == pluginhost.EventTranscriptFinal) {
		if a.voiceControlSink != nil && playbackControl(ev.Type) {
			a.deliverVoiceControl(s.ve)
		} else {
			s.enqueue(s.ve)
		}
	}
	return false
}

func playbackControl(typ string) bool {
	return typ == pluginhost.EventSpeechStart || typ == pluginhost.EventInterruptionRequested || typ == pluginhost.EventSynthesisStart
}

func (a *App) deliverVoiceControl(ev dashboard.VoiceEvent) {

	_ = a.voiceControlSink(ev)
}

func (a *App) applyCustody(s *voiceStep) {
	a.voiceObserved(s.ev)
	if s.ev.Type == pluginhost.EventSessionReady {
		a.noteAppliedSettings(s.ev)
	}
	if s.ev.Type == pluginhost.EventSpeakerObservation {
		a.noteSpeakerObservation(s.ev, s.safe)
	}
}

func (a *App) takeFinal(s *voiceStep) {
	ev := s.ev
	if s.safe {
		a.voiceSafeDropped.Add(1)
		logsink.Warn("voice.refusal", "SAFE holds (%s): the engine's transcript is heard by no record (%d bytes)", s.reason, len(ev.Raw))

		a.noteReplyOutcome(ev.SessionID, "withheld under SAFE, no reply")
		return
	}
	body := ev.body
	if !s.hold {
		a.rememberFinal(ev.SessionID, ev.Sequence, body.Text)
		a.retainHeard(ev.SessionID, ev.Sequence, body.Text, s.ve, "delivered", "", nil)
	}
	answer := false
	if m, ok := a.voiceModes.Load(ev.SessionID); ok {
		answer, _ = m.(bool)
	}

	heard := heardUtterance{Source: s.source(), Text: body.Text, Speaker: body.Speaker, Answer: answer, SessionID: ev.SessionID, Sequence: ev.Sequence, Gen: a.voiceGen(ev.SessionID), Operator: true}
	heard.Recognized, heard.Corrections = ev.corrected()

	heard.trace = newVoiceTurnTrace(ev.SessionID, ev.Sequence, stageFinal)
	if s.hold {
		a.holdForSpeaker(s, heard)
		return
	}
	a.answerFinal(s, heard)
}

func (s *voiceStep) source() string {
	if s.ap != nil {
		return "speech engine " + s.ap.ID
	}
	return "speech engine"
}

func (a *App) holdForSpeaker(s *voiceStep, heard heardUtterance) {
	ev := s.ev
	if h := a.voiceHandle(ev.SessionID); h != nil && !h.inputDone.Load() {
		a.holdFinal(h, &heldFinal{ve: s.ve, shown: s.shown, heard: heard, enqueue: s.enqueue})
		if !s.pol.restricted() && !ev.segment().valid() {

			a.decideHeld(h, ev.Sequence, "", nil, false, nil)
		}
		return
	}
	a.speakerWithheldFinals.Add(1)
	a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: ev.SessionID, Sequence: ev.Sequence, Type: "transcript_withheld",
		Reason: "no session to hold the words for a decision", Revision: s.pol.revision})
}

func (a *App) answerFinal(s *voiceStep, heard heardUtterance) {
	heard.trace.stamp(stageReleased)
	h := a.voiceHandle(s.ev.SessionID)
	if h == nil {
		a.startHeard(heard)
		return
	}
	if h.inputDone.Load() {
		logsink.Warn("voice.refusal", "%s sent a final transcript AFTER input_finished on %s — the engine's contract says none follows", s.source(), s.ev.SessionID)
		a.startHeard(heard)
		return
	}

	h.work.Add(1)
	heard.words = a.deliverSpoken(heard, h.work.Done)
	h.heldMu.Lock()
	ordered := h.heardTail != nil
	h.heldMu.Unlock()
	if ordered {

		a.admitHeldInOrder(h, &heldFinal{heard: heard})
		return
	}
	a.startHeard(heard)
}

func (a *App) startHeard(heard heardUtterance) {
	a.engines.start("the heard words of session "+heard.SessionID, func() {
		defer a.settleSpoken(heard.words)
		a.handleHeard(a.lifetime(), heard)
	})
}

const maxRememberedFinals = 64

type rememberedFinal struct {
	text                string
	segment             speakerSegment
	observationRevision uint64
	registryRevision    string
}

func (a *App) rememberFinal(session string, seq int64, text string, metadata ...dashboard.VoiceEvent) {
	if seq == 0 || strings.TrimSpace(text) == "" {
		return
	}
	h := a.voiceHandle(session)
	if h == nil {
		return
	}
	h.finalsMu.Lock()
	defer h.finalsMu.Unlock()
	if h.finals == nil {
		h.finals = map[int64]rememberedFinal{}
	}
	row := rememberedFinal{text: text}
	if len(metadata) == 1 {
		v := metadata[0]
		row.segment = speakerSegment{TrackID: v.TrackID, StartSample: v.StartSample, EndSample: v.EndSample}
	}
	h.finals[seq] = row
	for len(h.finals) > maxRememberedFinals {
		oldest := int64(-1)
		for s := range h.finals {
			if oldest < 0 || s < oldest {
				oldest = s
			}
		}
		delete(h.finals, oldest)
	}
}

func (a *App) finalText(session string, seq int64) (string, bool) {
	h := a.voiceHandle(session)
	if h == nil {
		return "", false
	}
	h.finalsMu.Lock()
	defer h.finalsMu.Unlock()
	row, ok := h.finals[seq]
	return row.text, ok
}

func (a *App) noteAppliedSettings(ev voiceFrame) {
	body := ev.body
	if ev.err != nil || body.Models.OperatorSettings == nil {
		return
	}
	id := ev.SessionID
	if id == "" {
		id = body.SessionID
	}
	if h := a.voiceHandle(id); h != nil {
		h.applied.Store(body.Models.OperatorSettings)
		return
	}

	if _, minted := a.voiceModes.Load(id); !minted {
		return
	}
	a.voicePending.Store(id, body.Models.OperatorSettings)

	if h := a.voiceHandle(id); h != nil {
		a.adoptPendingSettings(h)
	}
}

func (a *App) adoptPendingSettings(h *voiceHandle) {
	if val, ok := a.voicePending.LoadAndDelete(h.id); ok {
		if settings, ok := val.(map[string]any); ok && settings != nil {
			h.applied.Store(settings)
		}
	}
}

func (a *App) appliedVoiceSettings() map[string]map[string]any {
	var out map[string]map[string]any
	a.voiceSessions.Range(func(_, val any) bool {
		h := val.(*voiceHandle)
		select {
		case <-h.done:
			return true
		default:
		}
		values, _ := h.applied.Load().(map[string]any)
		if values == nil {
			return true
		}
		if out == nil {
			out = map[string]map[string]any{}
		}
		copied := make(map[string]any, len(values))
		for k, v := range values {
			copied[k] = v
		}
		out[h.id] = copied
		return true
	})
	return out
}

func voiceEventFor(ev voiceFrame, safe bool) (dashboard.VoiceEvent, bool) {
	isFinal := ev.Type == pluginhost.EventTranscriptFinal

	if ev.err != nil || safe && (isFinal || ev.Type == pluginhost.EventTranscriptPartial || ev.Type == pluginhost.EventSpeakerObservation) {
		return dashboard.VoiceEvent{}, false
	}
	body := struct {
		speakerObservation
		Text string
	}{ev.observation(), ev.body.Text}
	ve := dashboard.VoiceEvent{SessionID: ev.SessionID, Sequence: ev.Sequence, Type: ev.Type, Final: isFinal,
		Operator: isFinal || ev.Type == pluginhost.EventTranscriptPartial}

	if isFinal || ev.Type == pluginhost.EventTranscriptPartial {
		ve.Text, ve.Speaker = body.Text, body.Speaker

		ve.RecognizedText, ve.Corrections = ev.corrected()
		if body.speakerSegment.valid() {
			ve.TrackID, ve.StartSample, ve.EndSample = body.TrackID, body.StartSample, body.EndSample
		}
	}
	if ev.Type == pluginhost.EventSpeakerObservation {
		ve.Speaker = body.Speaker
		ve.SpeakerID = body.knownID()
		ve.RefersTo, ve.Decision, ve.Score, ve.Late = body.RefersTo, body.Decision, body.Score, body.Late

		ve.Reason = body.Reason
		ve.Attribution = body.attribution()
		if body.speakerSegment.valid() {
			ve.TrackID, ve.StartSample, ve.EndSample = body.TrackID, body.StartSample, body.EndSample
		}
		if body.validUUID() {
			ve.SpeakerUUID, ve.RegistryRevision, ve.Continuity, ve.DisplayLabel = body.SpeakerUUID, body.RegistryRevision, body.Continuity, body.DisplayLabel
		}
	}

	if ev.Type == pluginhost.EventSynthesisStart || ev.Type == pluginhost.EventSynthesisEnd || ev.Type == pluginhost.EventSynthesisCancelled {
		ve.Stream = ev.outputStream()
	}
	if ev.Type == pluginhost.EventFailure {

		ve.Reason = body.Reason
		if strings.TrimSpace(ve.Reason) == "" {
			ve.Reason = "the engine reported a failure"
		}
	}
	return ve, true
}

func (a *App) voicePageEvent(ev voiceFrame, safe bool) (dashboard.VoiceEvent, bool) {
	ve, shown := voiceEventFor(ev, safe)
	if shown && ev.Type == pluginhost.EventTranscriptPartial && a.speakerPolicyNow().restricted() {
		a.speakerWithheldPartials.Add(1)
		return dashboard.VoiceEvent{}, false
	}
	return ve, shown
}

func (a *App) fanVoiceEvent(ev dashboard.VoiceEvent) {
	if a.voiceEventSink != nil {
		a.voiceEventSink(ev)
	}
}
