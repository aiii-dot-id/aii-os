package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"sync"
	"uuid"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

const maxPendingSteers = 8

const maxSteerChars = 8_000

var errSteerQueueFull = fmt.Errorf("the identity is holding %d unread messages already; wait for the next tool call", maxPendingSteers)

func (a *App) admitOperatorChat(ctx context.Context, text string) (bool, error) {
	return a.admit(roleOperator, text, nil, ctx)
}

func (a *App) admitConstrainedChat(ctx context.Context, text string) error {
	if _, present := conversation.RequestOptionsFromContext(ctx); !present {
		return errors.New("request options are missing")
	}
	if a.turnGate == nil || a.toolReg == nil {
		return errors.New("application request admission is not initialized")
	}
	if _, err := conversation.FilterRequestTools(ctx, a.buildToolDefinitions()); err != nil {
		return err
	}
	if a.lifetime().Err() != nil {
		return errWordsStopping
	}
	if !a.TryBeginTurn() {
		return errors.New("another turn is active; constrained request was not admitted")
	}
	return nil
}

func (a *App) AdmitParticipant(text string) (steered bool, err error) {
	return a.admit(roleParticipant, text, nil, nil)
}

var errWordsStopping = fmt.Errorf("%w; the words were not delivered", errStopping)

const admitAttempts = 8

func (a *App) admit(role, text string, voice *voiceBinding, recordCtx context.Context) (steered bool, err error) {
	if a.turnGate == nil {
		return false, errors.New("application turn gate is not initialized")
	}

	if a.lifetime().Err() != nil {
		return false, errWordsStopping
	}
	for i := 0; i < admitAttempts; i++ {
		if a.TryBeginTurn() {

			return false, nil
		}
		steered, err = a.steer(role, text, voice, recordCtx)
		if err != nil || steered {
			return steered, err
		}

	}
	return false, errors.New("the identity's turn state changed faster than this message could be placed; say it again")
}

func (a *App) TryBeginTurn() bool {
	if a.turnGate == nil || a.lifetime().Err() != nil {
		return false
	}
	select {
	case <-a.turnGate:
		a.turnTaken()
		return true
	default:
		return false
	}
}

func (a *App) EndTurn() { a.releaseTurn() }

func (a *App) Steer(text string) (bool, error) {
	return a.steer(roleOperator, text, nil, nil)
}

func (a *App) arrivalCtx(e steerEntry) context.Context {
	if e.recordCtx != nil {
		return e.recordCtx
	}
	return a.lifetime()
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

	recognized  string
	corrections int64

	words *spokenWords

	trace *voiceTurnTrace
}

func (b *voiceBinding) ref() string {
	if b == nil || b.seq == 0 {
		return ""
	}
	return voiceRefKey(b.session, b.seq)
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

func operatorSpoke(entries []steerEntry) bool {
	for _, e := range entries {
		if e.role == roleOperator {
			return true
		}
	}
	return false
}

func (a *App) steer(role, text string, voice *voiceBinding, recordCtx context.Context) (bool, error) {
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

func splitRoom(queued []steerEntry, meeting bool) (said, room []steerEntry) {
	said = queued[:0:0]
	for _, e := range queued {
		if e.voice != nil && meeting {
			room = append(room, e)
			continue
		}
		said = append(said, e)
	}
	return said, room
}

func (a *App) recordRoom(room []steerEntry) {
	for _, e := range room {

		a.noteReplyOutcome(e.voice.session, "recorded (meeting), no reply")
		e.voice.trace.end("recorded (meeting), no reply: a meeting began while the words waited for the turn")
		e.voice.release("recorded as the room's: a meeting began while the words waited for the turn")
		if err := a.recordRoomWords(a.arrivalCtx(e), strings.TrimPrefix(e.content, voiceMarker), e.voice); err != nil {
			logsink.Warn("steering.error", "the room's words were not recorded: %v", err)
		}
	}
}

func (a *App) DrainSteering() []string {

	listen, _, _ := a.voiceMode()
	a.turnMu.Lock()
	queued := a.steers
	a.steers = nil
	said, room := splitRoom(queued, listen == listenMeeting)

	for _, e := range said {
		if e.voice != nil {
			a.turnVoice = append(a.turnVoice, e.voice)
		}
	}
	a.turnMu.Unlock()
	a.recordRoom(room)

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
	if operatorSpoke(said) {

		a.resetAsk()
	}
	if a.engine != nil {
		for i := range said {

			a.turnMu.Lock()
			turnID := a.interactionTurn
			a.turnMu.Unlock()

			recordCtx := context.WithoutCancel(a.arrivalCtx(said[i]))
			ref, err := a.recordConversationRef(interaction.WithTurn(recordCtx, turnID), said[i].role, said[i].content)
			seq := ref.Sequence
			if err != nil {

				logsink.Warn("steering.error", "operator turn not recorded: %v", err)
				continue
			}
			a.annotateVoiceTurn(seq, said[i].voice)

			said[i].content = a.attributeSpoken(seq, said[i].content)

			noteCtx := interaction.WithTurn(recordCtx, turnID)
			if err := a.recordCameWith(noteCtx, ref.ID); err != nil {

				logsink.Warn("steering.error", "what came with a delivered message is told on this turn and is not in the record: %v", err)
				if a.dashboard != nil {
					a.dashboard.BroadcastResponse("system", "Your message reached the running turn with what came with it, but "+err.Error()+": the identity is told of it on this turn, and the record does not hold it.")
				}
			}
			said[i].content = withAttached(said[i].content, dashboard.Attachments(noteCtx)) +
				a.steeredPictures(noteCtx, ref.ID, dashboard.Attachments(noteCtx))
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
	cancel(conversation.StoppedByOperator)
	return true
}

func (a *App) beginCancellableTurn(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	ctx = interaction.WithTurn(ctx, "turn_"+uuid.New().String())
	a.turnMu.Lock()
	a.turnCancel = cancel
	a.interactionTurn = interaction.TurnID(ctx)
	a.turnMu.Unlock()
	if lifetime := a.lifetime(); lifetime.Err() != nil {
		cancel(context.Cause(lifetime))
	}
	return ctx, func() {
		a.turnMu.Lock()
		a.turnCancel = nil
		a.interactionTurn = ""
		a.turnMu.Unlock()
		cancel(nil)
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
			e.voice.trace.end("no turn took the words: " + why)
			e.voice.release(why)
		}
	}
}

func (a *App) settleVoice(ctx context.Context, reply string) {
	a.turnMu.Lock()
	held := a.turnVoice
	a.turnVoice = nil
	a.turnMu.Unlock()

	speak := a.speakHeard
	if len(held) == 0 {

		if strings.TrimSpace(reply) == "" {
			return
		}
		if b := a.typedReplyVoice(); b != nil {
			held = []*voiceBinding{b}
			speak = func(ctx context.Context, b *voiceBinding, reply string) replyVerdict {
				return voiceSynthesize(a, ctx, b.session, b.gen, reply)
			}
		} else if b := a.typedReplyInConversation(); b != nil {

			held = []*voiceBinding{b}
		} else {
			return
		}

		held[0].trace = newVoiceTurnTrace(held[0].session, 0, stageReply)
	}
	latest := map[string]*voiceBinding{}
	for _, b := range held {
		latest[b.session] = b
	}
	shown := false
	quietSession := ""
	var refused *voiceBinding
	for _, b := range held {

		b.trace.stamp(stageReply)
		unspoken := "a later utterance of the same turn carries the reply"
		if latest[b.session] == b && strings.TrimSpace(reply) != "" {
			switch speak(withVoiceTrace(ctx, b.trace), b, reply) {
			case replyAdmitted:
				b.spoken = true
				shown = true
			case replyRefusedDefinite:
				refused = b
			default:
				quietSession = b.session
			}
			unspoken = "no synthesis was enqueued on an engine for this reply"
		} else if strings.TrimSpace(reply) == "" {
			a.noteReplyOutcome(b.session, "the turn answered with silence")
			unspoken = "the turn ended with no reply to speak"
		}

		b.trace.settle(unspoken)
		b.release("settled")

		a.retirePendingObservation(b)
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
