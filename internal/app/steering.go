package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
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
const maxPendingSteers = 8

// .
// .
// .
const maxSteerChars = 8_000

var errSteerQueueFull = fmt.Errorf("the identity is holding %d unread messages already; wait for the next tool call", maxPendingSteers)

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
func (a *App) AdmitOperator(text string) (steered bool, err error) {
	return a.admit(roleOperator, text)
}

// .
func (a *App) AdmitParticipant(text string) (steered bool, err error) {
	return a.admit(roleParticipant, text)
}

// .
// .
// .
const admitAttempts = 8

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
func (a *App) admit(role, text string) (steered bool, err error) {
	return a.admitWith(role, text, nil)
}

// .
func (a *App) admitWith(role, text string, voice *voiceBinding) (steered bool, err error) {
	if a.turnGate == nil {
		return false, errors.New("application turn gate is not initialized")
	}
	for i := 0; i < admitAttempts; i++ {
		if a.TryBeginTurn() {
			// .
			// .
			return false, nil
		}
		steered, err = a.steerWith(role, text, voice)
		if err != nil || steered {
			return steered, err
		}
		// .
		// .
	}
	return false, errors.New("the identity's turn state changed faster than this message could be placed; say it again")
}

// .
// .
// .
// .
// .
// .
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

// .
// .
func (a *App) EndTurn() { a.releaseTurn() }

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
func (a *App) Steer(text string) (bool, error) {
	return a.steer(roleOperator, text)
}

// .
// .
// .
// .
// .
type steerEntry struct {
	role    string
	content string
	// .
	// .
	// .
	voice *voiceBinding
}

// .
// .
// .
// .
type voiceBinding struct {
	session string
	gen     uint64
	done    func()
	once    sync.Once
	spoken  bool
	// .
	// .
	// .
	seq  int64
	text string
}

// .
// .
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

// .
func (a *App) steerWith(role, text string, voice *voiceBinding) (bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, errors.New("nothing was said")
	}
	if len(text) > maxSteerChars {
		return false, fmt.Errorf("this is %d characters and the mid-turn channel takes %d; say the short version now and the rest when the turn ends",
			len(text), maxSteerChars)
	}
	if !a.TurnActive() {
		return false, nil
	}
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.turnFacility {
		// .
		// .
		// .
		// .
		// .
		// .
		return false, dashboard.ErrBusyInternal
	}
	if len(a.steers) >= maxPendingSteers {
		return false, errSteerQueueFull
	}
	a.steers = append(a.steers, steerEntry{role: role, content: text, voice: voice})
	log.Printf("steering: operator spoke mid-turn (%d char(s), %d pending)", len(text), len(a.steers))
	return true, nil
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
func (a *App) DrainSteering() []string {
	a.turnMu.Lock()
	said := a.steers
	a.steers = nil
	// .
	// .
	for _, e := range said {
		if e.voice != nil {
			a.turnVoice = append(a.turnVoice, e.voice)
		}
	}
	a.turnMu.Unlock()

	if len(said) == 0 {
		return nil
	}
	if a.engine != nil {
		for i := range said {
			// .
			// .
			// .
			seq, err := a.engine.RecordConversationTurnSeq(said[i].role, said[i].content)
			if err != nil {
				// .
				// .
				log.Printf("steering: operator turn not recorded: %v", err)
				continue
			}
			a.annotateVoiceTurn(seq, said[i].voice)
			// .
			// .
			said[i].content = a.attributeSpoken(seq, said[i].content)
		}
	}
	log.Printf("steering: delivered %d message(s) at a tool boundary", len(said))
	// .
	// .
	if a.dashboard != nil {
		a.dashboard.BroadcastSteering()
	}
	// .
	// .
	out := make([]string, 0, len(said))
	for _, e := range said {
		out = append(out, e.content)
	}
	return out
}

// .
func (a *App) PendingSteers() []string {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	out := make([]string, 0, len(a.steers))
	for _, e := range a.steers {
		out = append(out, e.content)
	}
	return out
}

// .
// .
// .
// .
// .
// .
func (a *App) TurnActive() bool {
	return a.turnGate != nil && len(a.turnGate) == 0
}

// .
// .
// .
// .
// .
// .
func (a *App) CancelTurn() bool {
	a.turnMu.Lock()
	cancel := a.turnCancel
	a.turnMu.Unlock()
	if cancel == nil {
		return false
	}
	log.Printf("steering: operator cancelled the running turn")
	cancel()
	return true
}

// .
// .
// .
// .
func (a *App) beginCancellableTurn(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	a.turnMu.Lock()
	a.turnCancel = cancel
	a.turnMu.Unlock()
	return ctx, func() {
		a.turnMu.Lock()
		a.turnCancel = nil
		a.turnMu.Unlock()
		cancel()
	}
}

// .
// .
// .
// .
// .
// .
// .
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
	// .
	// .
	// .
	// .
	// .
	g.a.turnMu.Lock()
	g.a.turnFacility = false
	g.a.turnMu.Unlock()
	g.a.releaseTurn()
}

// .
// .
func (a *App) holdVoice(b *voiceBinding) {
	if b == nil {
		return
	}
	a.turnMu.Lock()
	a.turnVoice = append(a.turnVoice, b)
	a.turnMu.Unlock()
}

// .
// .
// .
func releaseVoice(entries []steerEntry, why string) {
	for _, e := range entries {
		if e.voice != nil {
			e.voice.release(why)
		}
	}
}

// .
// .
// .
var voiceSynthesize = (*App).synthesizeReply

// .
// .
// .
// .
// .
// .
func (a *App) settleVoice(ctx context.Context, reply string) {
	a.turnMu.Lock()
	held := a.turnVoice
	a.turnVoice = nil
	a.turnMu.Unlock()
	if len(held) == 0 {
		return
	}
	latest := map[string]*voiceBinding{}
	for _, b := range held {
		latest[b.session] = b
	}
	shown := false
	for _, b := range held {
		if latest[b.session] == b && strings.TrimSpace(reply) != "" {
			voiceSynthesize(a, ctx, b.session, b.gen, reply)
			if val, ok := a.voiceSessions.Load(b.session); ok {
				if out, _ := val.(*voiceHandle).replyOutcome.Load().(string); out == "reply admitted" {
					b.spoken = true
					shown = true
				}
			}
		} else if strings.TrimSpace(reply) == "" {
			a.noteReplyOutcome(b.session, "the turn answered with silence")
		}
		b.release("settled")
	}
	if shown {
		// .
		// .
		a.voiceReplyShown.Store(true)
	}
}
