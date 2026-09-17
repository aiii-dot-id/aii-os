package app

// .
// .
// .
// .
// .

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
func (a *App) voicePlugin() *pluginhost.ActivePlugin {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	for _, p := range a.plugins {
		if p.Voice != nil && p.Voice.Alive() {
			return p
		}
	}
	return nil
}

// .
func (a *App) VoiceEngine() bool { return a.voicePlugin() != nil }

// .
type voiceHandle struct {
	id   string
	v    engineSession
	b    *audio.Binding
	done <-chan struct{}
	// .
	// .
	// .
	// .
	gen atomic.Uint64
	// .
	// .
	// .
	// .
	work sync.WaitGroup
	// .
	// .
	// .
	inputDone    atomic.Bool
	drainOnce    atomic.Bool
	replyOutcome atomic.Value
	// .
	// .
	// .
	// .
	// .
	applied atomic.Value
	// .
	// .
	// .
	finalsMu sync.Mutex
	finals   map[int64]string
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	admit sync.Mutex
	// .
	// .
	// .
	// .
	closing atomic.Bool
	// .
	// .
	// .
	drained     chan struct{}
	drainedOnce sync.Once

	// .
	// .
	// .
	// .
	// .
	fallbackMu sync.Mutex
	fallback   string
	hush       func(why string)

	// .
	// .
	// .
	// .
	heldMu   sync.Mutex
	held     map[int64]*heldFinal
	withheld map[int64]bool
}

// .
// .
const voiceAdmissionBound = 5 * time.Second

// .
// .
func (h *voiceHandle) supersede() { h.gen.Add(1) }

// .
// .
// .
type replyVerdict int

const (
	// .
	replyAdmitted replyVerdict = iota
	// .
	// .
	// .
	replyRefusedDefinite
	// .
	// .
	replySuperseded
)

// .
// .
// .
// .
func refusedByEngine(err error) bool {
	var refused *supervisor.SessionRefusedError
	return errors.As(err, &refused)
}

// .
// .
func (h *voiceHandle) hushNow(why string) {
	if h.hush != nil {
		h.hush(why)
	}
}

// .
// .
// .
// .
// .
// .
type engineSession interface {
	FinishInputFor(ctx context.Context, sessionID, streamID string, endSample int64) error
	CloseFor(ctx context.Context, sessionID, mode, reason string) error
	InterruptFor(ctx context.Context, sessionID, synthesisID, cause string) error
	// .
	// .
	SynthesizeForEnqueue(ctx context.Context, sessionID, synthesisID, text string) (func(context.Context) error, error)
	PlaybackReportFor(ctx context.Context, sessionID string, r pluginhost.PlaybackReport) error
	Label() string
}

func (h *voiceHandle) ID() string { return h.id }

// .
// .
// .
// .
func (h *voiceHandle) Finish(ctx context.Context, endSample int64) error {
	return h.v.FinishInputFor(ctx, h.id, h.b.InputHandle, endSample)
}

// .
// .
// .
// .
func (h *voiceHandle) Close(ctx context.Context, mode string) error {
	h.admit.Lock()
	h.supersede()
	h.closing.Store(true)
	h.admit.Unlock()
	if mode == "abort" {
		h.hushNow("the session was aborted")
	}
	return h.v.CloseFor(ctx, h.id, mode, "operator")
}

// .
// .
// .
// .
// .
// .
// .
// .
func (h *voiceHandle) Interrupt(ctx context.Context) error {
	h.admit.Lock()
	h.supersede()
	h.admit.Unlock()
	h.hushNow("the operator spoke")
	return h.v.InterruptFor(ctx, h.id, "", "operator")
}
func (h *voiceHandle) Label() string             { return h.v.Label() }
func (h *voiceHandle) Done() <-chan struct{}     { return h.done }
func (h *voiceHandle) Released() <-chan struct{} { return h.b.Released() }

// .
// .
// .
// .
func (h *voiceHandle) PlaybackReport(ctx context.Context, r dashboard.PlaybackReport) error {
	return h.v.PlaybackReportFor(ctx, h.id, pluginhost.PlaybackReport{Stream: r.Stream, Rendered: r.Rendered, Rate: r.Rate, Channels: r.Channels, Terminal: r.Terminal, Outcome: r.Outcome})
}

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
// .
// .
func (a *App) synthesizeReply(ctx context.Context, sessionID string, gen uint64, reply string) replyVerdict {
	if sessionID == "" || reply == "" {
		return replySuperseded
	}
	var h *voiceHandle
	refuse := func(why string) {
		a.voiceStaleReplies.Add(1)
		log.Printf("VOICE: reply for session %s refused at admission: %s", sessionID, why)
		a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: sessionID, Type: "reply_refused", Reason: why})
		if h != nil {
			h.replyOutcome.Store("reply refused: " + why)
		}
	}
	val, ok := a.voiceSessions.Load(sessionID)
	if !ok {
		refuse("the session ended before its reply")
		return replySuperseded
	}
	h = val.(*voiceHandle)
	// .
	// .
	// .
	// .
	// .
	// .
	h.admit.Lock()
	if h.gen.Load() != gen || h.closing.Load() || ctx.Err() != nil {
		h.admit.Unlock()
		refuse("a barge-in, close, abort or cancellation superseded this reply")
		return replySuperseded
	}
	synthID := fmt.Sprintf("%s-syn-%d", sessionID, a.voiceSeq.Add(1))
	ectx, ecancel := context.WithTimeout(ctx, voiceAdmissionBound)
	await, err := h.v.SynthesizeForEnqueue(ectx, h.id, synthID, reply)
	ecancel()
	h.admit.Unlock()
	if err != nil {
		refuse("not dispatched: " + err.Error())
		return replySuperseded
	}
	actx, acancel := context.WithTimeout(ctx, voiceAdmissionBound)
	err = await(actx)
	acancel()
	if err != nil {
		why := err.Error()
		if pluginhost.IsAdmissionUnknown(err) {
			// .
			// .
			if ferr := h.fenceBounded(synthID, "admission unknown"); ferr != nil {
				why = fmt.Sprintf("the synthesis admission is unknown and the fence was refused (%v): the engine may speak %s", ferr, synthID)
			} else {
				why = "the synthesis admission is unknown; " + synthID + " was fenced"
			}
		}
		refuse(why)
		if refusedByEngine(err) {
			return replyRefusedDefinite
		}
		return replySuperseded
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if h.gen.Load() != gen || h.closing.Load() || ctx.Err() != nil {
		if ferr := h.fenceBounded(synthID, "superseded at admission"); ferr != nil {
			refuse(fmt.Sprintf("superseded while its admission was pending; the engine did NOT fence synthesis %s (%v) — the barge-in's own stop/cancel and the engine's speech fence are the remaining guards", synthID, ferr))
			return replySuperseded
		}
		refuse("superseded while its admission was pending; its synthesis " + synthID + " was fenced")
		return replySuperseded
	}
	h.replyOutcome.Store("reply admitted")
	if a.voiceReplySink != nil {
		a.voiceReplySink(dashboard.VoiceReplyRef{SessionID: sessionID, SynthesisID: synthID, Route: "plugin"}, reply)
	}
	return replyAdmitted
}

// .
// .
// .
var voiceFallbackMint = (*App).speakAhead

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
func (a *App) speakFallback(ctx context.Context, b *voiceBinding, reply string) bool {
	val, ok := a.voiceSessions.Load(b.session)
	if !ok {
		return false
	}
	h := val.(*voiceHandle)
	h.admit.Lock()
	defer h.admit.Unlock()
	if h.gen.Load() != b.gen || h.closing.Load() || ctx.Err() != nil {
		a.noteReplyOutcome(b.session, "reply refused by the engine, then superseded before another voice could take it")
		return false
	}
	route, id := "browser", voiceFallbackMint(a, reply)
	if id != "" {
		route = "cloud"
	}
	h.fallbackMu.Lock()
	prior := h.fallback
	h.fallback = route + ":" + id
	h.fallbackMu.Unlock()
	if prior != "" {
		// .
		// .
		a.hushTakenOver(h.id, prior, "a newer reply took its place")
	}
	if a.voiceReplySink != nil {
		a.voiceReplySink(dashboard.VoiceReplyRef{SessionID: b.session, SynthesisID: id, Route: route, Fallback: true}, reply)
	}
	a.noteReplyOutcome(b.session, "reply refused by the engine; spoken by the "+route+" voice instead")
	a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: b.session, Type: "reply_fallback", Reason: route})
	return true
}

// .
// .
// .
// .
func (a *App) hushFallback(h *voiceHandle, why string) {
	h.fallbackMu.Lock()
	which := h.fallback
	h.fallback = ""
	h.fallbackMu.Unlock()
	if which == "" {
		return
	}
	a.hushTakenOver(h.id, which, why)
}

// .
func (a *App) hushTakenOver(session, which, why string) {
	route, id, _ := strings.Cut(which, ":")
	if id != "" {
		a.hushSpoken(id)
	}
	log.Printf("VOICE: the %s voice's reply for session %s was hushed: %s", route, session, why)
	if a.voiceHushSink != nil {
		a.voiceHushSink(dashboard.VoiceHush{SessionID: session, SynthesisID: id, Route: route, Reason: why})
	}
}

// .
// .
// .
func (h *voiceHandle) fenceBounded(synthID, cause string) error {
	fctx, cancel := context.WithTimeout(context.Background(), voiceAdmissionBound)
	defer cancel()
	return h.v.InterruptFor(fctx, h.id, synthID, cause)
}

// .
// .
// .
// .
func (a *App) handleHeard(ctx context.Context, heard heardUtterance) {
	err := a.observeVoice(ctx, heard)
	switch {
	case err != nil:
		log.Printf("VOICE: the engine's transcript was not recorded: %v", err)
		a.noteReplyOutcome(heard.SessionID, "no reply: "+err.Error())
	case strings.TrimSpace(heard.Text) == "":
		a.noteReplyOutcome(heard.SessionID, "empty transcript, no reply")
	case !heard.Answer:
		a.noteReplyOutcome(heard.SessionID, "recorded (meeting), no reply")
	}
}

// .
// .
func (a *App) noteReplyOutcome(sessionID, outcome string) {
	if val, ok := a.voiceSessions.Load(sessionID); ok {
		val.(*voiceHandle).replyOutcome.Store(outcome)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) voiceInputFinished(sessionID string) {
	val, ok := a.voiceSessions.Load(sessionID)
	if !ok {
		return
	}
	h := val.(*voiceHandle)
	if !h.inputDone.CompareAndSwap(false, true) {
		return
	}
	go func() {
		h.work.Wait()
		a.voiceDrainAfterInput(context.Background(), h)
	}()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
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
	if cerr := h.v.CloseFor(dctx, h.id, "drain", outcome); cerr != nil {
		log.Printf("VOICE: drain-close after the input's completion refused on %s: %v", h.id, cerr)
		a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: h.id, Type: "drain_refused", Reason: cerr.Error()})
	}
}

// .
func (h *voiceHandle) markDrained() {
	h.drainedOnce.Do(func() {
		if h.drained != nil {
			close(h.drained)
		}
	})
}

// .
// .
// .
func (a *App) voiceGen(sessionID string) uint64 {
	if val, ok := a.voiceSessions.Load(sessionID); ok {
		return val.(*voiceHandle).gen.Load()
	}
	return 0
}

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
func (a *App) voiceObserved(ev pluginhost.Event) {
	switch ev.Type {
	case "speech_start", "interruption_requested":
		if val, ok := a.voiceSessions.Load(ev.SessionID); ok {
			h := val.(*voiceHandle)
			h.supersede()
			// .
			// .
			// .
			h.hushNow("the engine heard the operator speak")
		}
	case "input_finished":
		a.voiceInputFinished(ev.SessionID)
	}
}

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
func (a *App) OpenVoiceSession(ctx context.Context, inputID, outputID, mode string) (dashboard.VoiceSession, error) {
	ap := a.voicePlugin()
	if ap == nil {
		return nil, errors.New("no speech engine is active")
	}
	id := fmt.Sprintf("vs-%d", a.voiceSeq.Add(1))
	b, err := a.AudioPlane().Bind(id, inputID, outputID, ap.Contained)
	if err != nil {
		return nil, err
	}
	a.voiceModes.Store(id, mode == "conversation")
	a.ensureVoiceObserver(ap)
	if err := ap.Voice.OpenWithAudio(ctx, id, b, map[string]any{"mode": mode}); err != nil {
		// .
		// .
		a.voicePending.Delete(id)
		a.voiceModes.Delete(id)
		return nil, err
	}
	h := &voiceHandle{id: id, v: ap.Voice, b: b, done: ap.Voice.Done(), drained: make(chan struct{})}
	h.hush = func(why string) { a.hushFallback(h, why) }
	// .
	// .
	a.voiceSessions.Store(id, h)
	a.adoptPendingSettings(h)
	go func() {
		<-h.done
		a.voiceSessionEnded(h)
	}()
	return h, nil
}

// .
// .
// .
func (a *App) voiceSessionEnded(h *voiceHandle) {
	h.supersede()
	h.closing.Store(true)
	a.withholdHeld(h, "the session ended before the speaker was decided")
	h.markDrained()
	a.voiceSessions.Delete(h.id)
	a.voiceModes.Delete(h.id)
	a.voicePending.Delete(h.id)
	a.forgetPendingObservations(h.id)
}

// .
// .
// .
// .
// .
// .
// .
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
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			h.admit.Lock()
			h.supersede()
			h.closing.Store(true)
			h.admit.Unlock()
			if err := h.v.CloseFor(ctx, h.id, "abort", "SAFE: "+reason); err != nil {
				log.Printf("VOICE: SAFE could not abort session %s (%s → %s): %v", h.id, h.b.InputID, h.b.OutputID, err)
				return
			}
			log.Printf("VOICE: SAFE aborted session %s (%s → %s): no audio leaves the host while SAFE holds (%s)", h.id, h.b.InputID, h.b.OutputID, reason)
		}()
		return true
	})
}

// .
// .
// .
func (a *App) ensureVoiceObserver(ap *pluginhost.ActivePlugin) {
	a.voiceObsMu.Lock()
	defer a.voiceObsMu.Unlock()
	if a.voiceObs == nil {
		a.voiceObs = map[*pluginhost.VoiceSession]bool{}
	}
	if a.voiceObs[ap.Voice] {
		return
	}
	a.voiceObs[ap.Voice] = true
	go a.observeEngine(ap, ap.Voice)
}

func (a *App) observeEngine(ap *pluginhost.ActivePlugin, v *pluginhost.VoiceSession) {
	// .
	// .
	// .
	// .
	// .
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
	// .
	// .
	// .
	// .
	// .
	// .
	telemetryDone := make(chan struct{})
	go func() {
		defer close(telemetryDone)
		for ev := range v.Telemetry() {
			_, safe := a.SafeMode()
			if ve, ok := voiceEventFor(ev, safe); ok {
				enqueue(ve)
			}
		}
	}()
	for ev := range v.Observe() {
		a.voiceEngineEvent(ap, ev, enqueue)
	}
	<-telemetryDone
	// .
	// .
	// .
	// .
	// .
	close(fan)
	<-fanDone
	term := dashboard.VoiceEvent{Type: "closed"}
	if v.Faulted() {
		term.Type, term.Reason = "failed", v.FaultReason()
	}
	a.fanVoiceEvent(term)
	a.voiceObsMu.Lock()
	delete(a.voiceObs, v)
	a.voiceObsMu.Unlock()
}

// .
// .
// .
// .
// .
// .
func (a *App) voiceEngineEvent(ap *pluginhost.ActivePlugin, ev pluginhost.Event, enqueue func(dashboard.VoiceEvent)) {
	reason, safe := a.SafeMode()
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	pol := a.speakerPolicyNow()
	ve, shown := voiceEventFor(ev, safe)
	if shown && pol.restricted() && ev.Type == "transcript_partial" {
		a.speakerWithheldPartials.Add(1)
		shown = false
	}
	// .
	// .
	// .
	skipNote := false
	if ev.Type == "speaker_observation" && !safe {
		skipNote = a.observationForHeld(ev)
	}
	if shown && !(pol.restricted() && ev.Type == "transcript_final") {
		enqueue(ve)
	}
	// .
	// .
	a.voiceObserved(ev)
	if ev.Type == "session_ready" {
		a.noteAppliedSettings(ev)
	}
	if ev.Type == "speaker_observation" && !skipNote {
		a.noteSpeakerObservation(ev, safe)
	}
	if ev.Type != "transcript_final" {
		return
	}
	if safe {
		a.voiceSafeDropped.Add(1)
		log.Printf("VOICE: SAFE holds (%s): the engine's transcript is heard by no record (%d bytes)", reason, len(ev.Raw))
		// .
		// .
		a.noteReplyOutcome(ev.SessionID, "withheld under SAFE, no reply")
		return
	}
	var body struct {
		Text    string `json:"text"`
		Speaker string `json:"speaker"`
	}
	_ = json.Unmarshal(ev.Raw, &body)
	if !pol.restricted() {
		a.rememberFinal(ev.SessionID, ev.Sequence, body.Text)
	}
	answer := false
	if m, ok := a.voiceModes.Load(ev.SessionID); ok {
		answer, _ = m.(bool)
	}
	source := "speech engine"
	if ap != nil {
		source += " " + ap.ID
	}
	// .
	// .
	// .
	// .
	heard := heardUtterance{Source: source, Text: body.Text, Speaker: body.Speaker, Answer: answer, SessionID: ev.SessionID, Sequence: ev.Sequence, Gen: a.voiceGen(ev.SessionID), Operator: true}
	if pol.restricted() {
		// .
		// .
		// .
		// .
		// .
		if val, ok := a.voiceSessions.Load(ev.SessionID); ok {
			h := val.(*voiceHandle)
			if !h.inputDone.Load() {
				a.holdFinal(h, &heldFinal{ve: ve, shown: shown, heard: heard, enqueue: enqueue})
				return
			}
		}
		a.speakerWithheldFinals.Add(1)
		a.fanVoiceEvent(dashboard.VoiceEvent{SessionID: ev.SessionID, Sequence: ev.Sequence, Type: "transcript_withheld",
			Reason: "no session to hold the words for a decision", Revision: pol.revision})
		return
	}
	// .
	// .
	// .
	// .
	if val, ok := a.voiceSessions.Load(ev.SessionID); ok {
		h := val.(*voiceHandle)
		if h.inputDone.Load() {
			log.Printf("VOICE: %s sent a final transcript AFTER input_finished on %s — the engine's contract says none follows", source, ev.SessionID)
		} else {
			h.work.Add(1)
			go func() {
				defer h.work.Done()
				a.handleHeard(context.Background(), heard)
			}()
			return
		}
	}
	go a.handleHeard(context.Background(), heard)
}

// .
// .
const maxRememberedFinals = 64

// .
// .
func (a *App) rememberFinal(session string, seq int64, text string) {
	if seq == 0 || strings.TrimSpace(text) == "" {
		return
	}
	val, ok := a.voiceSessions.Load(session)
	if !ok {
		return
	}
	h := val.(*voiceHandle)
	h.finalsMu.Lock()
	defer h.finalsMu.Unlock()
	if h.finals == nil {
		h.finals = map[int64]string{}
	}
	h.finals[seq] = text
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

// .
func (a *App) finalText(session string, seq int64) (string, bool) {
	val, ok := a.voiceSessions.Load(session)
	if !ok {
		return "", false
	}
	h := val.(*voiceHandle)
	h.finalsMu.Lock()
	defer h.finalsMu.Unlock()
	text, ok := h.finals[seq]
	return text, ok
}

// .
// .
// .
// .
// .
func (a *App) noteAppliedSettings(ev pluginhost.Event) {
	var body struct {
		SessionID string `json:"session_id"`
		Models    struct {
			OperatorSettings map[string]interface{} `json:"operator_settings"`
		} `json:"models"`
	}
	if json.Unmarshal(ev.Raw, &body) != nil || body.Models.OperatorSettings == nil {
		return
	}
	id := ev.SessionID
	if id == "" {
		id = body.SessionID
	}
	if val, ok := a.voiceSessions.Load(id); ok {
		val.(*voiceHandle).applied.Store(body.Models.OperatorSettings)
		return
	}
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
	if _, minted := a.voiceModes.Load(id); !minted {
		return
	}
	a.voicePending.Store(id, body.Models.OperatorSettings)
	// .
	// .
	// .
	if val, ok := a.voiceSessions.Load(id); ok {
		a.adoptPendingSettings(val.(*voiceHandle))
	}
}

// .
// .
// .
// .
func (a *App) adoptPendingSettings(h *voiceHandle) {
	if val, ok := a.voicePending.LoadAndDelete(h.id); ok {
		if settings, ok := val.(map[string]interface{}); ok && settings != nil {
			h.applied.Store(settings)
		}
	}
}

// .
// .
// .
func (a *App) appliedVoiceSettings() map[string]map[string]interface{} {
	var out map[string]map[string]interface{}
	a.voiceSessions.Range(func(_, val any) bool {
		h := val.(*voiceHandle)
		select {
		case <-h.done:
			return true
		default:
		}
		values, _ := h.applied.Load().(map[string]interface{})
		if values == nil {
			return true
		}
		if out == nil {
			out = map[string]map[string]interface{}{}
		}
		copied := make(map[string]interface{}, len(values))
		for k, v := range values {
			copied[k] = v
		}
		out[h.id] = copied
		return true
	})
	return out
}

// .
// .
// .
// .
// .
// .
func voiceEventFor(ev pluginhost.Event, safe bool) (dashboard.VoiceEvent, bool) {
	isFinal := ev.Type == "transcript_final"
	// .
	// .
	if safe && (isFinal || ev.Type == "transcript_partial" || ev.Type == "speaker_observation") {
		return dashboard.VoiceEvent{}, false
	}
	var body struct {
		speakerObservation
		Text string `json:"text"`
	}
	if json.Unmarshal(ev.Raw, &body) != nil {
		// .
		body.speakerObservation = speakerObservation{}
	}
	ve := dashboard.VoiceEvent{SessionID: ev.SessionID, Sequence: ev.Sequence, Type: ev.Type, Text: body.Text, Speaker: body.Speaker, Final: isFinal,
		Operator: isFinal || ev.Type == "transcript_partial"}
	if ev.Type == "speaker_observation" {
		ve.SpeakerID = body.knownID()
		ve.RefersTo, ve.Decision, ve.Score, ve.Late = body.RefersTo, body.Decision, body.Score, body.Late
		// .
		// .
		ve.Reason = body.Reason
		ve.Attribution = body.attribution()
	}
	if ev.Type == "failure" {
		// .
		// .
		// .
		// .
		ve.Reason = body.Reason
		if strings.TrimSpace(ve.Reason) == "" {
			ve.Reason = "the engine reported a failure"
		}
	}
	return ve, true
}

// .
// .
func (a *App) fanVoiceEvent(ev dashboard.VoiceEvent) {
	if a.voiceEventSink != nil {
		a.voiceEventSink(ev)
	}
}
