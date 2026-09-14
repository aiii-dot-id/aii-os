package pluginhost

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
// .
// .
// .
// .

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
type Snapshot struct {
	SessionID     string `json:"session_id"`
	StateSequence int64  `json:"state_sequence"`
	Lifecycle     string `json:"lifecycle"`
	Input         struct {
		State              string `json:"state"`
		AdmittedEndSample  int64  `json:"admitted_end_sample"`
		ProcessedEndSample int64  `json:"processed_end_sample"`
	} `json:"input"`
	Recognition struct {
		UtteranceOpen       bool `json:"utterance_open"`
		FinalizationPending bool `json:"finalization_pending"`
	} `json:"recognition"`
	Synthesis struct {
		SynthesisID string `json:"synthesis_id"`
		State       string `json:"state"`
	} `json:"synthesis"`
	Playback struct {
		SynthesisID   string `json:"synthesis_id"`
		State         string `json:"state"`
		QueuedSamples int64  `json:"queued_samples"`
	} `json:"playback"`
	// .
	// .
	// .
	// .
	InputCompletion *InputCompletion `json:"input_completion"`
}

// .
// .
// .
type InputCompletion struct {
	StreamID           string `json:"stream_id"`
	EndSample          int64  `json:"end_sample"`
	ProcessedEndSample int64  `json:"processed_end_sample"`
	Sequence           int64  `json:"sequence"`
}

// .
// .
type Event struct {
	Type      string
	SessionID string
	Sequence  int64
	Raw       json.RawMessage
}

// .
// .
// .
// .
func terminal(typ, synthesisID string) bool {
	switch typ {
	case "session_end", "failure":
		return true
	case "cancellation":
		return synthesisID == ""
	}
	return false
}

// .
var telemetryTypes = map[string]bool{"vad_probability": true, "transcript_partial": true}

// .
// .
// .
var ErrSessionDead = errors.New("voicesession: the transport has ended; this session is dead — rebind for a new one")

// .
// .
var ErrStaleReply = errors.New("voicesession: the reply belongs to an earlier session instance and was discarded")

// .
// .
// .
// .
func IsAdmissionUnknown(err error) bool { return errors.Is(err, supervisor.ErrAdmissionUnknown) }

const (
	observerBuffer  = 1024
	telemetryBuffer = 64
)

// .
// .
// .
// .
// .
const SessionInterfaceID = "speech.session"

// .
// .
type VoiceSession struct {
	c   *supervisor.SessionClient
	sup *supervisor.Supervisor

	mu         sync.Mutex
	inst       uint64
	sessionID  string
	openResult json.RawMessage
	engineIn   audio.Format
	engineOut  audio.Format
	admission  string
	finishedIn bool
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	finishCutoff  int64
	finishStream  string
	inputFinished bool
	// .
	// .
	// .
	// .
	streams    map[uint32]string
	reported   map[uint32]int64
	reportMu   sync.Mutex
	inputEnd   int64
	closing    bool
	closed     bool
	closeWhy   string
	failed     bool
	failReason string
	untrusted  bool
	untrustWhy string

	lastSnapshot Snapshot
	lastSeq      int64
	gaps         uint64
	foreign      uint64

	terminalCh chan struct{}
	terminated bool
	faultCh    chan struct{}
	faulted    bool
	endedCh    chan struct{}
	endOnce    sync.Once

	observer   chan Event
	telemetry  chan Event
	telDropped atomic.Uint64

	// .
	// .
	pair       *audioPair
	binding    *audio.Binding
	pump       *audio.Pump
	pumpCancel context.CancelFunc
}

// .
// .
func NewVoiceSession(c *supervisor.SessionClient) *VoiceSession {
	v := &VoiceSession{c: c, finishCutoff: -1, streams: map[uint32]string{}, reported: map[uint32]int64{},
		terminalCh: make(chan struct{}), faultCh: make(chan struct{}), endedCh: make(chan struct{}),
		observer: make(chan Event, observerBuffer), telemetry: make(chan Event, telemetryBuffer)}
	go v.consume()
	return v
}

// .
// .
func (v *VoiceSession) markTerminalLocked() {
	if !v.terminated {
		v.terminated = true
		v.releaseAudioLocked()
		close(v.terminalCh)
	}
}

// .
// .
// .
func (v *VoiceSession) releaseAudioLocked() {
	if v.pumpCancel != nil {
		v.pumpCancel()
		v.pumpCancel = nil
	}
	if v.binding != nil {
		v.binding.Release()
	}
}

// .
// .
// .
func (v *VoiceSession) markFaultLocked(why string) {
	if !v.untrusted {
		v.untrusted, v.untrustWhy = true, why
	}
	if !v.faulted {
		v.faulted = true
		close(v.faultCh)
	}
}

// .
func (v *VoiceSession) terminal() <-chan struct{} {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.terminalCh
}

// .
// .
// .
// .
func (v *VoiceSession) Untrusted() <-chan struct{} {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.faultCh
}

// .
// .
// .
func (v *VoiceSession) consume() {
	for raw := range v.c.Events() {
		var ev struct {
			Type        string `json:"type"`
			SessionID   string `json:"session_id"`
			SynthesisID string `json:"synthesis_id"`
			Sequence    int64  `json:"sequence"`
		}
		_ = json.Unmarshal(raw, &ev)
		isTerminal := terminal(ev.Type, ev.SynthesisID)
		v.mu.Lock()
		if ev.SessionID != "" && v.sessionID != "" && ev.SessionID != v.sessionID {
			v.foreign++
			v.mu.Unlock()
			continue
		}
		if ev.Sequence != 0 {
			if ev.Sequence <= v.lastSeq {
				v.mu.Unlock()
				continue
			}
			if v.lastSeq != 0 && ev.Sequence != v.lastSeq+1 {
				v.gaps++
			}
			v.lastSeq = ev.Sequence
		}
		if ev.Type == "input_finished" {
			v.applyInputFinishedLocked(raw)
		}
		if ev.Type == "synthesis_start" {
			// .
			// .
			var body struct {
				SynthesisID  string `json:"synthesis_id"`
				OutputStream *int64 `json:"output_stream"`
			}
			if json.Unmarshal(raw, &body) == nil && body.SynthesisID != "" && body.OutputStream != nil && *body.OutputStream >= 0 {
				v.streams[uint32(*body.OutputStream)] = body.SynthesisID
			}
		}
		if isTerminal {
			if ev.Type == "failure" {
				v.failed, v.failReason = true, "engine reported failure"
			} else {
				v.closed, v.closeWhy = true, "engine reported "+ev.Type
			}
			v.markTerminalLocked()
		}
		e := Event{Type: ev.Type, SessionID: ev.SessionID, Sequence: ev.Sequence, Raw: raw}
		if telemetryTypes[ev.Type] {
			v.mu.Unlock()
			select {
			case v.telemetry <- e:
			default:
				v.telDropped.Add(1)
			}
			continue
		}
		select {
		case v.observer <- e:
		default:
			// .
			// .
			v.markFaultLocked("observer overflow: the application did not consume critical events")
		}
		v.mu.Unlock()
	}
	v.transportEnded()
}

// .
// .
// .
func (v *VoiceSession) transportEnded() {
	v.mu.Lock()
	if !v.closed && !v.failed {
		why := "transport ended before the engine reported closure"
		if r := v.c.FaultReason(); r != "" {
			why = r
		}
		v.markFaultLocked(why)
	}
	v.mu.Unlock()
	v.endOnce.Do(func() { close(v.endedCh) })
}

// .
// .
// .
func (v *VoiceSession) Observe() <-chan Event { return v.observer }

// .
// .
// .
func (v *VoiceSession) Telemetry() <-chan Event { return v.telemetry }

// .
func (v *VoiceSession) TelemetryDropped() uint64 { return v.telDropped.Load() }

func (v *VoiceSession) control(ctx context.Context, op string, args map[string]any) (json.RawMessage, error) {
	return v.c.Control(ctx, op, args)
}

// .
func (v *VoiceSession) instance() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.inst
}

// .
func (v *VoiceSession) apply(inst uint64, fn func()) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inst != inst {
		return false
	}
	fn()
	return true
}

// .
// .
// .
// .
// .
// .
// .
func (v *VoiceSession) Open(ctx context.Context, sessionID string, args map[string]any) error {
	return v.open(ctx, sessionID, args, nil)
}

func (v *VoiceSession) open(ctx context.Context, sessionID string, args map[string]any, b *audio.Binding) error {
	select {
	case <-v.endedCh:
		if b != nil {
			b.Release()
		}
		return ErrSessionDead
	default:
	}
	v.mu.Lock()
	if v.sessionID != "" && !v.closed && !v.failed {
		prev := v.sessionID
		v.mu.Unlock()
		if b != nil {
			b.Release()
		}
		return fmt.Errorf("voicesession: session %s is still open — close it before opening %s", prev, sessionID)
	}
	v.releaseAudioLocked()
	v.inst++
	inst := v.inst
	v.binding, v.pump = b, nil
	v.sessionID, v.admission = sessionID, "pending"
	v.lastSeq, v.gaps, v.foreign = 0, 0, 0
	v.finishedIn, v.closing, v.closed, v.closeWhy = false, false, false, ""
	v.finishCutoff, v.finishStream, v.inputFinished, v.inputEnd = -1, "", false, 0
	v.streams, v.reported = map[uint32]string{}, map[uint32]int64{}
	v.failed, v.failReason, v.untrusted, v.untrustWhy = false, "", false, ""
	v.lastSnapshot = Snapshot{}
	v.terminalCh, v.terminated = make(chan struct{}), false
	v.faultCh, v.faulted = make(chan struct{}), false
	v.mu.Unlock()

	if args == nil {
		args = map[string]any{}
	}
	args["session_id"] = sessionID
	result, err := v.control(ctx, "speech.session.open", args)

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inst != inst {
		return fmt.Errorf("voicesession: the open of %s was superseded", sessionID)
	}
	v.openResult = result
	var refused *supervisor.SessionRefusedError
	switch {
	case err == nil:
		v.admission = "admitted"
	case errors.Is(err, supervisor.ErrAdmissionUnknown):
		v.admission = "unknown"
	case errors.As(err, &refused):
		v.admission = "refused"
		v.closed, v.closeWhy = true, "the engine refused the open"
		v.markTerminalLocked()
	default:
		v.admission = "not_admitted"
		v.closed, v.closeWhy = true, "the open never reached the engine"
		v.markTerminalLocked()
	}
	return err
}

// .
// .
// .
// .
var ErrStaleSession = errors.New("voicesession: the session this handle names is not the driver's current session")

// .
// .
// .
// .
func (v *VoiceSession) sessionFor(sessionID string) (uint64, error) {
	inst, _, _, err := v.sessionResources(sessionID)
	return inst, err
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
func (v *VoiceSession) sessionResources(sessionID string) (inst uint64, p *audio.Pump, finished bool, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if sessionID == "" || v.sessionID != sessionID {
		return 0, nil, false, fmt.Errorf("%w: %q, the driver holds %q", ErrStaleSession, sessionID, v.sessionID)
	}
	return v.inst, v.pump, v.finishedIn || v.inputFinished, nil
}

// .
// .
// .
// .
// .
// .
// .
func (v *VoiceSession) applyInputFinishedLocked(raw json.RawMessage) {
	var body struct {
		StreamID  string `json:"stream_id"`
		EndSample *int64 `json:"end_sample"`
	}
	if json.Unmarshal(raw, &body) != nil || body.EndSample == nil {
		v.markFaultLocked("input_finished without an exact cutoff")
		return
	}
	if v.finishCutoff >= 0 && v.finishStream != "" && body.StreamID != v.finishStream {
		v.markFaultLocked(fmt.Sprintf("input_finished names input handle %q; the registered Finish named %q", body.StreamID, v.finishStream))
		return
	}
	if v.inputFinished {
		if *body.EndSample != v.inputEnd {
			v.markFaultLocked(fmt.Sprintf("a second input_finished names cutoff %d; the first named %d", *body.EndSample, v.inputEnd))
		}
		return
	}
	if v.finishCutoff >= 0 && *body.EndSample != v.finishCutoff {
		v.markFaultLocked(fmt.Sprintf("input_finished names cutoff %d; the host registered %d", *body.EndSample, v.finishCutoff))
		return
	}
	v.inputFinished, v.inputEnd = true, *body.EndSample
	if v.finishCutoff < 0 {
		v.finishCutoff = *body.EndSample
	}
}

// .
// .
func (v *VoiceSession) InputFinished() (bool, int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.inputFinished, v.inputEnd
}

// .
// .
// .
func (v *VoiceSession) reconcileFinish(inst uint64, engineEnd int64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := v.Status(ctx)
	if err != nil || v.instance() != inst {
		return false
	}
	if snap.InputCompletion != nil {
		return snap.InputCompletion.EndSample == engineEnd
	}
	return snap.Input.State != "accepting" && snap.Input.AdmittedEndSample == engineEnd
}

// .
// .
func (v *VoiceSession) Synthesize(ctx context.Context, synthesisID, text string) error {
	return v.SynthesizeFor(ctx, v.id(), synthesisID, text)
}

// .
// .
func (v *VoiceSession) SynthesizeFor(ctx context.Context, sessionID, synthesisID, text string) error {
	await, err := v.SynthesizeForEnqueue(ctx, sessionID, synthesisID, text)
	if err != nil {
		return err
	}
	return await(ctx)
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
func (v *VoiceSession) SynthesizeForEnqueue(ctx context.Context, sessionID, synthesisID, text string) (func(context.Context) error, error) {
	if _, err := v.sessionFor(sessionID); err != nil {
		return nil, err
	}
	await, err := v.c.ControlEnqueue(ctx, "speech.session.synthesize", map[string]any{
		"session_id": sessionID, "synthesis_id": synthesisID, "text": text})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		_, err := await(ctx)
		return err
	}, nil
}

// .
// .
// .
// .
func (v *VoiceSession) Interrupt(ctx context.Context, synthesisID, cause string) error {
	return v.InterruptFor(ctx, v.id(), synthesisID, cause)
}

// .
// .
// .
func (v *VoiceSession) InterruptFor(ctx context.Context, sessionID, synthesisID, cause string) error {
	if _, err := v.sessionFor(sessionID); err != nil {
		return err
	}
	var wg sync.WaitGroup
	var stopErr, cancelErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, stopErr = v.control(ctx, "speech.session.stop_playback", map[string]any{
			"session_id": sessionID, "synthesis_id": synthesisID, "reason": cause})
	}()
	go func() {
		defer wg.Done()
		_, cancelErr = v.control(ctx, "speech.session.cancel_synthesis", map[string]any{
			"session_id": sessionID, "synthesis_id": synthesisID, "reason": cause})
	}()
	wg.Wait()
	if stopErr != nil {
		return fmt.Errorf("stop_playback: %w", stopErr)
	}
	if cancelErr != nil {
		return fmt.Errorf("cancel_synthesis: %w", cancelErr)
	}
	return nil
}

// .
// .
// .
// .
// .
// .
// .
type PlaybackReport struct {
	Stream   uint32
	Rendered int64
	Rate     int
	Channels int
	Terminal bool
	Outcome  string
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
func (v *VoiceSession) PlaybackReportFor(ctx context.Context, sessionID string, r PlaybackReport) error {
	inst, p, _, err := v.sessionResources(sessionID)
	if err != nil {
		return err
	}
	if p == nil {
		return errors.New("voicesession: no audio pump: a playback report needs a session opened with audio")
	}
	v.reportMu.Lock()
	defer v.reportMu.Unlock()
	v.mu.Lock()
	b, synthID, last, cur := v.binding, v.streams[r.Stream], v.reported[r.Stream], v.inst
	v.mu.Unlock()
	if b == nil || cur != inst {
		return ErrStaleSession
	}
	if r.Rate != b.OutFormat.Rate || r.Channels != b.OutFormat.Channels {
		return fmt.Errorf("voicesession: the report renders %d Hz/%d ch; the session's speaker is %s", r.Rate, r.Channels, b.OutFormat)
	}
	st, ok := p.OutputStream(r.Stream)
	if !ok {
		return fmt.Errorf("voicesession: output stream %d was never delivered on session %s", r.Stream, sessionID)
	}
	if synthID == "" {
		return fmt.Errorf("voicesession: no synthesis owns output stream %d on session %s", r.Stream, sessionID)
	}
	if r.Rendered < last {
		return fmt.Errorf("voicesession: rendered %d on stream %d moves backward from %d", r.Rendered, r.Stream, last)
	}
	if r.Rendered > st.Written {
		return fmt.Errorf("voicesession: rendered %d on stream %d exceeds the %d written to the speaker", r.Rendered, r.Stream, st.Written)
	}
	switch r.Outcome {
	case "drained":
		if !r.Terminal || !st.Ended || r.Rendered != st.Written {
			return fmt.Errorf("voicesession: a drained report needs the stream's END written and every sample rendered (stream %d: %d of %d rendered, ended=%v)", r.Stream, r.Rendered, st.Written, st.Ended)
		}
		// .
		// .
		// .
		// .
		// .
		if st.Refused > 0 {
			return fmt.Errorf("voicesession: the speaker refused %d frame(s) of stream %d: the reply was never delivered whole, so no report can drain it", st.Refused, r.Stream)
		}
	case "stopped":
		if !r.Terminal {
			return errors.New("voicesession: a stopped report is terminal")
		}
	case "progress", "":
		if r.Terminal {
			return errors.New("voicesession: a terminal report names its outcome, drained or stopped")
		}
	default:
		return fmt.Errorf("voicesession: playback outcome %q is not drained, stopped or progress", r.Outcome)
	}
	engineRendered, err := p.EngineRendered(r.Stream, r.Rendered)
	if err != nil {
		return err
	}
	if _, err := v.control(ctx, "speech.session.playback_report", map[string]any{
		"session_id": sessionID, "synthesis_id": synthID, "output_stream": r.Stream,
		"rendered_samples": engineRendered, "terminal": r.Terminal}); err != nil {
		return err
	}
	v.apply(inst, func() { v.reported[r.Stream] = r.Rendered })
	return nil
}

// .
// .
// .
func (v *VoiceSession) FinishInput(ctx context.Context, streamID string, endSample int64) error {
	return v.FinishInputFor(ctx, v.id(), streamID, endSample)
}

// .
// .
// .
func (v *VoiceSession) FinishInputFor(ctx context.Context, sessionID, streamID string, endSample int64) error {
	// .
	inst, p, _, err := v.sessionResources(sessionID)
	if err != nil {
		return err
	}
	engineEnd := endSample
	if p != nil {
		// .
		// .
		// .
		// .
		endSample = p.Cutoff(endSample)
		engineEnd = p.EngineCutoff(endSample)
	}
	// .
	// .
	// .
	// .
	v.mu.Lock()
	if v.inst != inst {
		v.mu.Unlock()
		return ErrStaleSession
	}
	if v.inputFinished && v.inputEnd == engineEnd {
		v.mu.Unlock()
		return nil
	}
	if v.finishCutoff < 0 {
		v.finishCutoff, v.finishStream = engineEnd, streamID
	}
	registered := v.finishCutoff
	v.mu.Unlock()
	_, err = v.control(ctx, "speech.session.finish_input", map[string]any{
		"session_id": sessionID, "stream_id": streamID, "end_sample": engineEnd})
	switch {
	case err == nil:
	case IsAdmissionUnknown(err):
		// .
		// .
		if !v.reconcileFinish(inst, engineEnd) {
			return err
		}
	default:
		// .
		// .
		// .
		v.apply(inst, func() {
			if v.finishCutoff == registered && registered == engineEnd && !v.finishedIn && !v.inputFinished {
				v.finishCutoff, v.finishStream = -1, ""
			}
		})
		return err
	}
	if !v.apply(inst, func() { v.finishedIn = true }) {
		return ErrStaleReply
	}
	return nil
}

// .
// .
// .
// .
// .
func (v *VoiceSession) Close(ctx context.Context, mode, reason string) error {
	return v.CloseFor(ctx, v.id(), mode, reason)
}

// .
// .
func (v *VoiceSession) CloseFor(ctx context.Context, sessionID, mode, reason string) error {
	// .
	// .
	inst, _, ok, err := v.sessionResources(sessionID)
	if err != nil {
		return err
	}
	if mode == "drain" && !ok {
		return fmt.Errorf("voicesession: a drain close needs a prior finish_input cutoff — refusing to invent one")
	}
	if _, err := v.control(ctx, "speech.session.close", map[string]any{
		"session_id": sessionID, "mode": mode, "reason": reason}); err != nil {
		return err
	}
	if !v.apply(inst, func() { v.closing = true }) {
		return ErrStaleReply
	}
	return nil
}

// .
// .
// .
// .
// .
// .
func (v *VoiceSession) Status(ctx context.Context) (Snapshot, error) {
	inst := v.instance()
	raw, err := v.control(ctx, "speech.session.status", map[string]any{"session_id": v.id()})
	if err != nil {
		var refused *supervisor.SessionRefusedError
		if errors.As(err, &refused) {
			// .
			// .
			v.apply(inst, func() {
				if v.admission == "unknown" && !v.closed && !v.failed {
					v.closed, v.closeWhy = true, "the engine does not know the session (status refused)"
					v.markTerminalLocked()
				}
			})
		}
		return Snapshot{}, err
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("voicesession: status snapshot malformed: %w", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.inst != inst {
		return Snapshot{}, ErrStaleReply
	}
	if snap.StateSequence >= v.lastSnapshot.StateSequence {
		v.lastSnapshot = snap
		switch snap.Lifecycle {
		case "closed":
			v.closed, v.closeWhy = true, "engine snapshot reports closed"
			v.markTerminalLocked()
		case "failed":
			v.failed, v.failReason = true, "engine snapshot reports failed"
			v.markTerminalLocked()
		case "opening", "open", "draining":
			if v.admission == "unknown" {
				v.admission = "admitted"
			}
		}
	} else {
		snap = v.lastSnapshot
	}
	return snap, nil
}

// .
// .
// .
// .
// .
func (v *VoiceSession) Label() string {
	v.mu.Lock()
	s := v.lastSnapshot
	failed, closed, closing := v.failed || v.untrusted, v.closed, v.closing
	opening := v.admission == "pending" || v.admission == "unknown"
	v.mu.Unlock()
	unresolved := s.Recognition.FinalizationPending || s.Synthesis.State == "generating" || s.Playback.State == "playing" || s.Playback.QueuedSamples > 0
	switch {
	case failed || s.Lifecycle == "failed":
		return "Failed"
	case closed || s.Lifecycle == "closed":
		return "Closed"
	case closing || s.Lifecycle == "draining":
		if unresolved {
			return "Draining"
		}
		return "Drained"
	case s.Playback.State == "playing":
		return "Speaking"
	case s.Input.State == "accepting" && s.Recognition.UtteranceOpen:
		return "Listening"
	case opening && s.Lifecycle == "":
		return "Opening"
	case unresolved:
		return "Draining"
	default:
		return "Idle"
	}
}

// .
// .
// .
// .
// .
func (v *VoiceSession) IsOpen() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sessionID != "" && !v.closed && !v.failed
}

// .
// .
func (v *VoiceSession) Alive() bool {
	select {
	case <-v.c.Done():
		return false
	default:
	}
	return !v.c.Faulted()
}

// .
// .
// .
func (v *VoiceSession) Faulted() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.failed || v.untrusted || v.c.Faulted()
}
func (v *VoiceSession) FaultReason() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	switch {
	case v.failReason != "":
		return v.failReason
	case v.untrustWhy != "":
		return v.untrustWhy
	}
	return v.c.FaultReason()
}

// .
// .
func (v *VoiceSession) Dropped() uint64 { return v.c.Dropped() }
func (v *VoiceSession) Gaps() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.gaps
}

// .
// .
func (v *VoiceSession) Done() <-chan struct{} {
	out := make(chan struct{})
	term, fault := v.terminal(), v.Untrusted()
	go func() {
		select {
		case <-term:
		case <-fault:
		case <-v.endedCh:
		}
		close(out)
	}()
	return out
}

// .
// .
// .
// .
func (v *VoiceSession) pinReleased() <-chan struct{} {
	out := make(chan struct{})
	var exited <-chan struct{}
	if v.sup != nil {
		exited = v.sup.Exited()
	} else {
		exited = v.endedCh
	}
	term := v.terminal()
	go func() {
		select {
		case <-term:
		case <-exited:
			// .
			// .
			v.mu.Lock()
			v.releaseAudioLocked()
			v.mu.Unlock()
		}
		close(out)
	}()
	return out
}

func (v *VoiceSession) id() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sessionID
}

// .
// .
// .
func (ap *ActivePlugin) Pinned() bool {
	return ap.Voice != nil && ap.Voice.IsOpen()
}

// .
// .
// .
func (ap *ActivePlugin) PinReleased() <-chan struct{} {
	if ap.Voice == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return ap.Voice.pinReleased()
}

// .
// .
// .
// .
// .
func (ap *ActivePlugin) bindVoiceSession(sup *supervisor.Supervisor) error {
	frames, stdin, ok := sup.SessionChannels()
	if !ok {
		return fmt.Errorf("pluginhost: %s: no running child to bind a session to", ap.ID)
	}
	client := supervisor.NewSessionClientFrames(frames, stdin, sup.Dispatcher(), 256)
	sctx, cancel := context.WithCancel(context.Background())
	ap.sessionCancel = cancel
	v := NewVoiceSession(client)
	v.sup = sup
	if in, out, ok := sup.AudioPair(); ok {
		v.pair = newAudioPair(in, out)
	}
	ap.Voice = v
	go func() {
		if err := client.Run(sctx); err != nil {
			v.mu.Lock()
			if v.untrustWhy == "" {
				v.untrustWhy = "transport: " + err.Error()
			}
			v.mu.Unlock()
		}
	}()
	return nil
}

// .
// .
func (ap *ActivePlugin) RebindVoice() error {
	if ap.sessionCancel != nil {
		ap.sessionCancel()
	}
	if ap.sup == nil {
		return fmt.Errorf("pluginhost: %s: not a supervised plugin", ap.ID)
	}
	return ap.bindVoiceSession(ap.sup)
}
