package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

const SessionControlBound = 5 * time.Second

var checkSpeaker = audio.Format{Rate: 48000, Channels: 1}

type SessionUnendedError struct {
	PluginID string
	Session  string
	Cause    error
}

func (e *SessionUnendedError) Error() string {
	return fmt.Sprintf("plugin %s: the engine did not end the check's session %s after the check's abort; the candidate is stopped, which ends it", e.PluginID, e.Session)
}

func (e *SessionUnendedError) Unwrap() error { return e.Cause }

func (ap *ActivePlugin) sessionLane() func(context.Context, string, packagefmt.Check) (SessionAnswer, error) {
	if ap.Voice.Load() == nil {
		return nil
	}
	return ap.sessionCheck
}

func (ap *ActivePlugin) sessionCheck(ctx context.Context, id string, c packagefmt.Check) (SessionAnswer, error) {
	ans := SessionAnswer{Level: math.Inf(-1)}
	v := ap.Voice.Load()
	if v == nil {
		return ans, fmt.Errorf("plugin %s: no session lane is bound", ap.ID)
	}
	plane := audio.NewPlane()
	speaker := newCheckSpeaker(checkSpeaker)
	in, out := id+"-in", id+"-out"
	if err := plane.Register(&audio.Endpoint{ID: out, Label: "check", Sink: speaker}); err != nil {
		return ans, err
	}
	var src *audio.PacedSource
	var frames int64
	var b *audio.Binding
	var err error
	if c.Audio != "" {
		w, werr := audio.ReadWAV(c.WAV)
		if werr != nil {
			return ans, werr
		}
		file, ferr := audio.NewFileSource(bytes.NewReader(c.WAV), audio.Format{}, w.Format.Rate/50)
		if ferr != nil {
			return ans, ferr
		}
		frames = int64(len(w.PCM) / w.Format.BytesPerSample())
		src = audio.NewPacedSource(file)
		if err := plane.Register(&audio.Endpoint{ID: in, Label: "check", Source: src}); err != nil {
			return ans, err
		}
		b, err = plane.Bind(id, in, out, ap.Contained)
	} else {
		ans.SynthesisID = id + "-s"
		b, err = plane.BindOutput(id, out, ap.Contained)
	}
	if err != nil {
		return ans, err
	}
	if b.HasInput() {
		b.InputHandle = in
	}
	b.OutputHandle = out

	ap.binding.BeginOperation(broker.OperationScope{Operation: SessionInterfaceID, Validation: true})
	defer ap.binding.EndOperation()

	sent := time.Now()
	octx, cancel := context.WithTimeout(ctx, SessionControlBound)
	events, err := v.OpenCheck(octx, id, b)
	cancel()
	if events == nil {
		return ans, err
	}
	term := v.terminal()
	stream, fail := ap.listen(ctx, v, id, c, src, frames, b, speaker, events, sent, &ans, err)
	ans.Took = time.Since(sent)
	if ans.SynthesisID != "" && stream >= 0 {
		if _, p := v.Audio(); p != nil {
			if st, ok := p.OutputStream(uint32(stream)); ok {
				ans.Samples = st.EngineReceived
			}
		}
		_, engineOut := v.EngineFormats()
		ans.Rate, ans.Level = engineOut.Rate, speaker.level(uint32(stream))
	}
	if uerr := ap.endCheckSession(ctx, v, id, term, events); uerr != nil {
		uerr.Cause = fail
		return ans, uerr
	}
	return ans, fail
}

func (ap *ActivePlugin) listen(ctx context.Context, v *VoiceSession, id string, c packagefmt.Check, src *audio.PacedSource, frames int64,
	b *audio.Binding, speaker *checkSpeakerSink, events <-chan Event, sent time.Time, ans *SessionAnswer, opened error) (stream int64, err error) {
	stream = -1
	if refused := refusal("speech.session.open", opened); refused != nil {
		ans.Refused = refused
		return stream, nil
	}
	if opened != nil {
		return stream, opened
	}
	deadline := time.NewTimer(time.Until(sent.Add(c.Within)))
	defer deadline.Stop()
	var ended <-chan struct{}
	started, synthEnded := false, false
	done := func() bool {
		if src != nil {
			return false
		}
		return synthEnded && stream >= 0 && speaker.ended(uint32(stream))
	}
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return stream, errors.New("the session's lane ended before its answer was complete")
			}
			ev, frame := checkEvent(e)
			ans.Events = append(ans.Events, ev)
			switch {
			case ev.Type == EventSessionReady && !started:
				started = true
				if src != nil {
					src.Start()
					ended = src.Ended()
					break
				}
				sctx, cancel := context.WithTimeout(ctx, SessionControlBound)
				err = v.SynthesizeFor(sctx, id, ans.SynthesisID, c.Text)
				cancel()
				if refused := refusal("speech.session.synthesize", err); refused != nil {
					ans.Refused = refused
					return stream, nil
				}
				if err != nil {
					return stream, err
				}
			case ev.Type == EventInputFinished && src != nil:
				ans.Complete = true
				return stream, nil
			case (ev.Type == EventSynthesisStart || ev.Type == EventSynthesisEnd) && ev.SynthesisID == ans.SynthesisID && ans.SynthesisID != "":
				if frame.OutputStream != nil && *frame.OutputStream >= 0 && *frame.OutputStream <= math.MaxUint32 && stream < 0 {
					stream = *frame.OutputStream
				}
				synthEnded = synthEnded || ev.Type == EventSynthesisEnd
			case ev.Type == EventFailure || terminal(ev.Type, ev.SynthesisID) ||
				((ev.Type == EventSynthesisCancelled || ev.Type == EventCancellation) && ev.SynthesisID != "" && ev.SynthesisID == ans.SynthesisID):
				return stream, nil
			}
			if done() {
				ans.Complete = true
				return stream, nil
			}
		case <-ended:
			ended = nil
			fctx, cancel := context.WithTimeout(ctx, SessionControlBound)
			err = v.FinishInputFor(fctx, id, b.InputHandle, frames)
			cancel()
			if refused := refusal("speech.session.finish_input", err); refused != nil {
				ans.Refused = refused
				return stream, nil
			}
			if err != nil {
				return stream, err
			}
		case <-speaker.changed:
			if done() {
				ans.Complete = true
				return stream, nil
			}
		case <-deadline.C:
			return stream, nil
		case <-ctx.Done():
			return stream, ctx.Err()
		}
	}
}

func refusal(control string, err error) *SessionRefusal {
	var refused *supervisor.SessionRefusedError
	if !errors.As(err, &refused) {
		return nil
	}
	return &SessionRefusal{Control: control, Words: string(refused.Err)}
}

func checkEvent(e Event) (SessionEvent, VoiceFrame) {
	if e.Raw == nil {
		return SessionEvent{Type: e.Type, Withheld: true}, VoiceFrame{}
	}
	f, err := DecodeVoiceFrame(e.Raw)
	if err != nil {
		return SessionEvent{Type: e.Type, Withheld: true}, VoiceFrame{}
	}
	return SessionEvent{Type: f.Type, Text: f.Text, SynthesisID: f.SynthesisID, Reason: f.Reason, ReasonCode: f.ReasonCode, RetryMayPass: f.RetryMayPass}, f
}

func (ap *ActivePlugin) endCheckSession(ctx context.Context, v *VoiceSession, id string, term <-chan struct{}, events <-chan Event) *SessionUnendedError {
	select {
	case <-term:
		return nil
	default:
	}
	lasting := context.WithoutCancel(ctx)
	actx, cancel := context.WithTimeout(lasting, SessionControlBound)
	_ = v.CloseFor(actx, id, "abort", "the publisher's check is over")
	cancel()
	wait := time.NewTimer(StopBudget)
	defer wait.Stop()
	for {
		select {
		case <-term:
			return nil
		case <-v.endedCh:
			return nil
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case <-wait.C:
			sctx, cancel := context.WithTimeout(lasting, statusReconcileBound)
			_, _ = v.Status(sctx)
			cancel()
			select {
			case <-term:
				return nil
			default:
			}
			return &SessionUnendedError{PluginID: ap.ID, Session: id}
		}
	}
}

type checkSpeakerSink struct {
	f       audio.Format
	mu      sync.Mutex
	levels  map[uint32]*audio.Level
	ends    map[uint32]bool
	changed chan struct{}
}

func newCheckSpeaker(f audio.Format) *checkSpeakerSink {
	return &checkSpeakerSink{f: f, levels: map[uint32]*audio.Level{}, ends: map[uint32]bool{}, changed: make(chan struct{}, 1)}
}

func (s *checkSpeakerSink) Format() audio.Format { return s.f }
func (s *checkSpeakerSink) Close() error         { return nil }

func (s *checkSpeakerSink) Write(_ context.Context, fr audio.Frame) error {
	s.mu.Lock()
	switch fr.Kind {
	case audio.KindPCM:
		l := s.levels[fr.Stream]
		if l == nil {
			l = &audio.Level{}
			s.levels[fr.Stream] = l
		}
		l.Add(fr.PCM)
	case audio.KindEnd:
		s.ends[fr.Stream] = true
	}
	s.mu.Unlock()
	if fr.Kind == audio.KindEnd {
		select {
		case s.changed <- struct{}{}:
		default:
		}
	}
	return nil
}

func (s *checkSpeakerSink) ended(stream uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ends[stream]
}

func (s *checkSpeakerSink) level(stream uint32) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l := s.levels[stream]; l != nil {
		return l.DBFS()
	}
	return math.Inf(-1)
}
