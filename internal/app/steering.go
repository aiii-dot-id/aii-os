package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/google/uuid"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

const maxPendingSteers = 8

const maxSteerChars = 8_000

var errSteerQueueFull = fmt.Errorf("the identity is holding %d unread messages already; wait for the next tool call", maxPendingSteers)

func (a *App) AdmitOperator(text string) (steered bool, err error) {
	return a.admit(roleOperator, text)
}

func (a *App) admitOperatorChat(ctx context.Context, text string) (bool, error) {
	return a.admitObserved(roleOperator, text, nil, ctx)
}

func (a *App) AdmitParticipant(text string) (steered bool, err error) {
	return a.admit(roleParticipant, text)
}

const admitAttempts = 8

func (a *App) admit(role, text string) (steered bool, err error) {
	return a.admitWith(role, text, nil)
}

func (a *App) admitWith(role, text string, voice *voiceBinding) (steered bool, err error) {
	return a.admitObserved(role, text, voice, nil)
}

func (a *App) admitObserved(role, text string, voice *voiceBinding, recordCtx context.Context) (steered bool, err error) {
	if a.turnGate == nil {
		return false, errors.New("application turn gate is not initialized")
	}
	for i := 0; i < admitAttempts; i++ {
		if a.TryBeginTurn() {

			return false, nil
		}
		steered, err = a.steerObserved(role, text, voice, recordCtx)
		if err != nil || steered {
			return steered, err
		}

	}
	return false, errors.New("the identity's turn state changed faster than this message could be placed; say it again")
}

func (a *App) TryBeginTurn() bool {
	if a.turnGate == nil {
		return false
	}
	select {
	case <-a.turnGate:
		a.holdTurnForeground()
		return true
	default:
		return false
	}
}

func (a *App) EndTurn() { a.releaseTurn() }

func (a *App) Steer(text string) (bool, error) {
	return a.steer(roleOperator, text)
}

type steerEntry struct {
	role      string
	content   string
	recordCtx context.Context

	voice *voiceBinding
}

type voiceBinding struct {
	session string
	gen     uint64
	done    func()
	once    sync.Once
	spoken  bool

	seq  int64
	text string
}

func (b *voiceBinding) release(why string) {
	b.once.Do(func() {
		if b.done != nil {
			b.done()
		}
	})
	_ = why
}

const (
	roleOperator    = "operator"
	roleParticipant = "participant"
)

func (a *App) steer(role, text string) (bool, error) {
	return a.steerWith(role, text, nil)
}

func (a *App) steerWith(role, text string, voice *voiceBinding) (bool, error) {
	return a.steerObserved(role, text, voice, nil)
}

func (a *App) steerObserved(role, text string, voice *voiceBinding, recordCtx context.Context) (bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, errors.New("nothing was said")
	}
	if len(text) > maxSteerChars {
		return false, fmt.Errorf("this is %d characters and the mid-turn channel takes %d; say the short version now and the rest when the turn ends",
			len(text), maxSteerChars)
	}
	if a.steerBeforeQueue != nil {
		a.steerBeforeQueue()
	}
	a.turnMu.Lock()
	defer a.turnMu.Unlock()

	if !a.TurnActive() {
		return false, nil
	}
	if a.turnFacility {

		return false, dashboard.ErrBusyInternal
	}
	if len(a.steers) >= maxPendingSteers {
		return false, errSteerQueueFull
	}
	a.steers = append(a.steers, steerEntry{role: role, content: text, voice: voice, recordCtx: recordCtx})
	logsink.Info("steering.decision", "operator spoke mid-turn (%d char(s), %d pending)", len(text), len(a.steers))
	return true, nil
}

func (a *App) DrainSteering() []string {

	listen, _, _ := a.voiceMode()
	meeting := listen == listenMeeting
	a.turnMu.Lock()
	queued := a.steers
	a.steers = nil
	said := queued[:0:0]
	var room []steerEntry
	for _, e := range queued {
		if e.voice != nil && meeting {
			room = append(room, e)
			continue
		}
		said = append(said, e)

		if e.voice != nil {
			a.turnVoice = append(a.turnVoice, e.voice)
		}
	}
	a.turnMu.Unlock()
	for _, e := range room {
		e.voice.release("recorded as the room's: a meeting began while the words waited for the turn")
		a.noteReplyOutcome(e.voice.session, "recorded (meeting), no reply")
		if err := a.recordRoomWords(strings.TrimPrefix(e.content, voiceMarker), e.voice); err != nil {
			logsink.Warn("steering.error", "the room's words were not recorded: %v", err)
		}
	}

	kept := said[:0:0]
	for _, e := range said {
		if !a.arrivalInCurrentFacts(e.recordCtx) && !a.arrivalAlreadyRecorded(e.recordCtx) {
			kept = append(kept, e)
		}
	}
	said = kept

	if len(said) == 0 {
		if len(room) > 0 && a.dashboard != nil {
			a.dashboard.BroadcastSteering()
		}
		return nil
	}
	if a.engine != nil {
		for i := range said {

			a.turnMu.Lock()
			turnID := a.interactionTurn
			a.turnMu.Unlock()
			recordCtx := said[i].recordCtx
			if recordCtx == nil {
				recordCtx = context.Background()
			}
			ref, err := a.recordConversationRef(interaction.WithTurn(recordCtx, turnID), said[i].role, said[i].content)
			seq := ref.Sequence
			if err != nil {

				logsink.Warn("steering.error", "operator turn not recorded: %v", err)
				continue
			}
			a.annotateVoiceTurn(seq, said[i].voice)

			said[i].content = a.attributeSpoken(seq, said[i].content)
		}
	} else {
		for _, e := range said {
			dashboard.ChatNotRecorded(e.recordCtx, "recording owner unavailable")
		}
	}
	logsink.Info("steering.decision", "delivered %d message(s) at a tool boundary", len(said))

	if a.dashboard != nil {
		a.dashboard.BroadcastSteering()
	}

	out := make([]string, 0, len(said))
	for _, e := range said {
		out = append(out, e.content)
	}
	return out
}

func (a *App) PendingSteers() []string {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	out := make([]string, 0, len(a.steers))
	for _, e := range a.steers {
		out = append(out, e.content)
	}
	return out
}

func (a *App) TurnActive() bool {
	return a.turnGate != nil && len(a.turnGate) == 0
}

func (a *App) CancelTurn() bool {
	a.turnMu.Lock()
	cancel := a.turnCancel
	a.turnMu.Unlock()
	if cancel == nil {
		return false
	}
	logsink.Info("steering.decision", "operator cancelled the running turn")
	cancel()
	return true
}

func (a *App) beginCancellableTurn(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	ctx = interaction.WithTurn(ctx, "turn_"+uuid.NewString())
	a.turnMu.Lock()
	a.turnCancel = cancel
	a.interactionTurn = interaction.TurnID(ctx)
	a.turnMu.Unlock()
	return ctx, func() {
		a.turnMu.Lock()
		a.turnCancel = nil
		a.interactionTurn = ""
		a.turnMu.Unlock()
		cancel()
	}
}

type facilityGate struct{ a *App }

func (g facilityGate) TryBeginTurn() bool {
	if !g.a.TryBeginTurn() {
		return false
	}
	g.a.turnMu.Lock()
	g.a.turnFacility = true
	g.a.turnMu.Unlock()
	return true
}

func (g facilityGate) EndTurn() {

	g.a.turnMu.Lock()
	g.a.turnFacility = false
	g.a.turnMu.Unlock()
	g.a.releaseTurn()
}

func (a *App) holdVoice(b *voiceBinding) {
	if b == nil {
		return
	}
	a.turnMu.Lock()
	a.turnVoice = append(a.turnVoice, b)
	a.turnMu.Unlock()
}

func releaseVoice(entries []steerEntry, why string) {
	for _, e := range entries {
		if e.voice != nil {
			e.voice.release(why)
		}
	}
}

var voiceSynthesize = (*App).synthesizeReply

func (a *App) settleVoice(ctx context.Context, reply string) {
	a.turnMu.Lock()
	held := a.turnVoice
	a.turnVoice = nil
	a.turnMu.Unlock()
	if len(held) == 0 {

		if strings.TrimSpace(reply) == "" {
			return
		}
		if b := a.typedReplyVoice(); b != nil {
			held = []*voiceBinding{b}
		} else {
			return
		}
	}
	latest := map[string]*voiceBinding{}
	for _, b := range held {
		latest[b.session] = b
	}
	shown := false
	quietSession := ""
	var refused *voiceBinding
	for _, b := range held {
		if latest[b.session] == b && strings.TrimSpace(reply) != "" {
			switch voiceSynthesize(a, ctx, b.session, b.gen, reply) {
			case replyAdmitted:
				b.spoken = true
				shown = true
			case replyRefusedDefinite:
				refused = b
			default:
				quietSession = b.session
			}
		} else if strings.TrimSpace(reply) == "" {
			a.noteReplyOutcome(b.session, "the turn answered with silence")
		}
		b.release("settled")
	}
	if !shown && refused != nil && a.speakFallback(ctx, refused, reply) {

		shown = true
	}
	if !shown && (refused != nil || quietSession != "") && a.voiceReplySink != nil {
		session := quietSession
		if refused != nil {
			session = refused.session
		}

		a.voiceReplySink(dashboard.VoiceReplyRef{SessionID: session, Route: "plugin", TextOnly: true}, reply)
		shown = true
	}
	if shown {

		a.voiceReplyShown.Store(true)
	}
}
