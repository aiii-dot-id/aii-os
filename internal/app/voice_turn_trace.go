package app

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type voiceStage int

const (
	stageFinal voiceStage = iota

	stageReleased

	stageTurnStart

	stageReply

	stageEnqueued

	stageAcknowledged

	stageSynthesisStart

	stageFirstFrame
	voiceStages
)

var voiceStageNames = [voiceStages]string{"final", "released", "turn_start", "reply", "enqueued", "acknowledged", "synthesis_start", "first_frame"}

type voiceTurnTrace struct {
	session string
	seq     int64
	at      [voiceStages]atomic.Pointer[time.Time]
	written atomic.Bool
}

func newVoiceTurnTrace(session string, seq int64, first voiceStage) *voiceTurnTrace {
	t := &voiceTurnTrace{session: session, seq: seq}
	t.stamp(first)
	return t
}

func (t *voiceTurnTrace) stamp(s voiceStage) {
	if t == nil || t.at[s].Load() != nil {
		return
	}
	t.stampAt(s, voiceNow())
}

func (t *voiceTurnTrace) stampAt(s voiceStage, at time.Time) {
	if t == nil {
		return
	}
	t.at[s].CompareAndSwap(nil, &at)
}

func (t *voiceTurnTrace) end(why string) {
	if t == nil || !t.written.CompareAndSwap(false, true) {
		return
	}
	logsink.Info("voice.end", "%s", t.line(why))
}

func (t *voiceTurnTrace) settle(why string) {
	if t != nil && t.at[stageEnqueued].Load() == nil {
		t.end(why)
	}
}

func (t *voiceTurnTrace) line(why string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VOICE_TURN session %s ", t.session)
	if t.at[stageFinal].Load() != nil {
		fmt.Fprintf(&b, "final %d:", t.seq)
	} else {
		b.WriteString("reply to typed words:")
	}
	var first *time.Time
	for s := voiceStage(0); s < voiceStages; s++ {
		if s > 0 {
			b.WriteString(",")
		}
		at := t.at[s].Load()
		if at == nil {
			fmt.Fprintf(&b, " %s not reached", voiceStageNames[s])
			continue
		}
		if first == nil {
			first = at
		}
		fmt.Fprintf(&b, " %s %d ms", voiceStageNames[s], at.Sub(*first).Milliseconds())
	}
	if why != "" {
		b.WriteString("; not reached: " + why)
	}
	return b.String()
}

type voiceTraceKey struct{}

func withVoiceTrace(ctx context.Context, t *voiceTurnTrace) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, voiceTraceKey{}, t)
}

func voiceTraceFrom(ctx context.Context) *voiceTurnTrace {
	t, _ := ctx.Value(voiceTraceKey{}).(*voiceTurnTrace)
	return t
}

func (h *voiceHandle) traceOf(synthID string) *voiceTurnTrace {
	if in := h.loadInflight(); synthID != "" && in.id == synthID {
		return in.trace
	}
	return nil
}

type firstFrameSink struct {
	audio.Sink
	wrote func(fr audio.Frame, at time.Time)

	noted atomic.Uint64
}

func (s *firstFrameSink) Write(ctx context.Context, fr audio.Frame) error {
	err := s.Sink.Write(ctx, fr)

	if err == nil && (fr.Kind == audio.KindEnd && s.noted.Load() != uint64(fr.Stream)+1 || fr.Kind == audio.KindPCM && s.noted.Swap(uint64(fr.Stream)+1) != uint64(fr.Stream)+1) {
		s.wrote(audio.Frame{Kind: fr.Kind, Stream: fr.Stream}, voiceNow())
	}
	return err
}

func (a *App) voiceFrameWritten(session string, fr audio.Frame, at time.Time) {
	h := a.voiceHandle(session)
	if h == nil {
		return
	}
	t := h.traceOf(h.v.SynthesisFor(fr.Stream))
	if fr.Kind == audio.KindPCM {
		t.stampAt(stageFirstFrame, at)
		t.end("")
		return
	}
	t.end("its output stream ended at the sink with no audio frame")
}
