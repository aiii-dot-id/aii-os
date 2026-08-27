package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// steering.go — the operator's reach into a turn already in flight.
//
// A turn owns the identity from acquireTurn to releaseTurn, and nothing
// could reach it. On one connection the dashboard's read loop is blocked
// inside the turn itself, so a second message is not queued — it is not
// even READ. The identity was not slow to answer, it was unreachable,
// while the presence strip still said "present". Thirty tool rounds is a
// long time to be unable to say "stop, that file is already fixed".
//
// This is the pattern the field calls STEERING: the message is not an
// interrupt and does not discard the work. It is delivered at the next
// tool-call boundary, inside the running turn, and the model reads it
// alongside the results it was already waiting for. Cancel is the other
// half, for when information is not enough and the work must stop.
//
// This file is the identity-side half. The dashboard half — a read loop
// that dispatches instead of blocking, and a queue the operator can SEE
// so "queued" is never mistaken for "delivered" — belongs to whoever
// owns internal/dashboard.

// maxPendingSteers bounds what may accumulate between two tool calls.
// Small on purpose: this is speech into a gap of seconds, and the
// conversation loop hands whatever is here to the model inside a prompt
// the Accordion has already budgeted.
const maxPendingSteers = 8

// maxSteerChars bounds one steer. The mid-turn channel is for redirection
// — "stop", "that is already done", "the file you want is elsewhere" —
// not for delivering a document mid-thought.
const maxSteerChars = 8_000

var errSteerQueueFull = fmt.Errorf("the identity is holding %d unread messages already; wait for the next tool call", maxPendingSteers)

// AdmitChat decides one arriving message's fate in ONE step: steered
// into the turn already running, or THE GATE IS TAKEN and the caller now
// owns a new turn (and must release it when the turn ends).
//
// Deciding and claiming have to be the same step. Asking "is a turn
// running?" and then starting one leaves a window in which a second
// message is told "no turn" by a gate the first has not taken yet — so
// words meant to JOIN the work become a second turn queued behind it,
// which is the opposite of what steering exists for. The dashboard read
// loop returns to ReadMessage the instant it launches the turn
// goroutine, so that window is the common case, not a rare one, and two
// tabs on one identity widen it further.
//
// Non-blocking by construction: a receive on the gate with a default
// branch. The caller must not be parked here — it is the loop that reads
// the next thing the operator says.
// AdmitOperator admits words that arrived through an AUTHENTICATED
// OPERATOR CHANNEL — the dashboard today. These may become R52 evidence.
//
// AdmitParticipant admits words from anyone else: a person on a
// messaging channel, a voice in a room. These may not.
//
// TWO METHODS RATHER THAN A ROLE PARAMETER, on purpose. The role is the
// authority claim, so the call site should read as one and there should
// be no string for a caller to get wrong. Nothing outside this file
// chooses a role, and no plugin supplies one.
func (a *App) AdmitOperator(text string) (steered bool, err error) {
	return a.admit(roleOperator, text)
}

// AdmitParticipant admits words from someone who is not the operator.
func (a *App) AdmitParticipant(text string) (steered bool, err error) {
	return a.admit(roleParticipant, text)
}

// admitAttempts bounds the take-or-steer retry below. Two is already
// generous: each pass either claims the gate or queues, and only a turn
// ENDING in the window between them causes another.
const admitAttempts = 8

// admit decides one arriving message's fate in ONE step: steered into
// the turn already running, or THE GATE IS TAKEN and the caller owns a
// new turn (and must release it).
//
// Deciding and claiming have to be the same step. Asking TurnActive and
// then acting on the answer leaves a window in which a second message is
// told "no turn" by a gate the first has not taken yet — so words meant
// to JOIN the work become a second turn queued behind it.
//
// THE LOOP CLOSES THE OTHER HALF OF THAT WINDOW. If the gate is busy we
// try to steer; if the turn ENDED in between, steering reports no turn,
// and returning that would tell the caller it owns a gate it never took
// — whose deferred release would then push a second token into a
// capacity-one channel and block the runtime forever. So we go round
// again: either we get the gate or we queue.
func (a *App) admit(role, text string) (steered bool, err error) {
	if a.turnGate == nil {
		return false, errors.New("application turn gate is not initialized")
	}
	for i := 0; i < admitAttempts; i++ {
		if a.TryBeginTurn() {
			// The gate is ours. From here every other message steers,
			// because TurnActive derives from this same channel.
			return false, nil
		}
		steered, err = a.steer(role, text)
		if err != nil || steered {
			return steered, err
		}
		// The turn ended between the two. Try again rather than claim
		// something we do not hold.
	}
	return false, errors.New("the identity's turn state changed faster than this message could be placed; say it again")
}

// TryBeginTurn takes the turn gate if it is free, WITHOUT BLOCKING, and
// reports whether it got it. The caller releases with releaseTurn.
//
// Asking TurnActive and then acting on the answer is a race — the gap is
// exactly where a second turn slips in. This is the same question and
// the claim together, which is the only form of it that is safe to use.
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

// EndTurn releases a gate taken by TryBeginTurn. Named for callers
// outside this package, who should not reach for releaseTurn.
func (a *App) EndTurn() { a.releaseTurn() }

// Steer delivers the operator's words into a turn already in flight.
//
// It REFUSES rather than drops. A silent drop would leave the operator
// believing they had spoken to an identity that never heard them, which
// is the exact failure this file exists to end — a queue that swallows is
// worse than no queue, because it also lies.
// It reports DELIVERY structurally rather than in prose: (false, nil)
// means there was no turn to steer, so the caller should open one. A
// caller deciding that by matching error text would be deciding control
// flow on a sentence (AGENTS.md 5.2), and this sentence would be read by
// the one component that must never guess.
func (a *App) Steer(text string) (bool, error) {
	return a.steer(roleOperator, text)
}

// steerEntry is one queued arrival AND WHO SAID IT. The queue used to
// hold bare strings and DrainSteering stamped every one of them
// "operator" — so the same sentence was participant when the identity
// was idle and operator when it was busy, and R52 cites operator turns.
// A microphone in a room was one active turn away from Ring 1 consent.
type steerEntry struct {
	role    string
	content string
}

const (
	roleOperator    = "operator"
	roleParticipant = "participant"
)

func (a *App) steer(role, text string) (bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, errors.New("nothing was said")
	}
	if len(text) > maxSteerChars {
		return false, fmt.Errorf("this is %d characters and the mid-turn channel takes %d; say the short version now and the rest when the turn ends",
			len(text), maxSteerChars)
	}
	if !a.TurnActive() {
		return false, nil // nothing to steer — this is an ordinary message
	}
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	if a.turnFacility {
		// The holder is a facility pass: it will never read this queue
		// (only the conversation loop drains, at tool boundaries).
		// Refuse — structurally, so the dashboard parks the message and
		// opens its own turn the moment the pass ends. Queueing here
		// would be the lie this file exists to end: accepted, never
		// heard.
		return false, dashboard.ErrBusyInternal
	}
	if len(a.steers) >= maxPendingSteers {
		return false, errSteerQueueFull
	}
	a.steers = append(a.steers, steerEntry{role: role, content: text})
	log.Printf("steering: operator spoke mid-turn (%d char(s), %d pending)", len(text), len(a.steers))
	return true, nil
}

// DrainSteering implements conversation.Steering. The loop calls it at
// every tool-call boundary; it empties as it reads, because a steer
// delivered twice would be the operator saying it twice.
//
// The record is not incidental. A steer is a person speaking to the
// identity, so it belongs in the conversation the same way the message
// that opened the turn does — otherwise a real exchange sits outside the
// history, and the identity's account of its own turn would be missing
// the part that changed its mind. It is recorded here, at delivery, and
// not at deposit: the ledger write then happens on the turn's own
// goroutine, in the order the identity actually heard things.
func (a *App) DrainSteering() []string {
	a.turnMu.Lock()
	said := a.steers
	a.steers = nil
	a.turnMu.Unlock()

	if len(said) == 0 {
		return nil
	}
	if a.engine != nil {
		for _, s := range said {
			// THE ROLE THE ENTRY ARRIVED WITH. Stamping "operator" here
			// made every queued arrival operator evidence regardless of
			// who said it.
			if err := a.engine.RecordConversationTurn(s.role, s.content); err != nil {
				// The words still reach the model — losing the record is
				// bad, losing the turn is worse.
				log.Printf("steering: operator turn not recorded: %v", err)
			}
		}
	}
	log.Printf("steering: delivered %d message(s) at a tool boundary", len(said))
	// The queue just emptied, and that is the moment worth showing: until
	// now the operator could see words accepted but never see them heard.
	if a.dashboard != nil {
		a.dashboard.BroadcastSteering()
	}
	// The loop wants the WORDS; the roles were already applied above,
	// where they decide what each arrival becomes.
	out := make([]string, 0, len(said))
	for _, e := range said {
		out = append(out, e.content)
	}
	return out
}

// PendingSteers is what has been accepted and not yet delivered.
func (a *App) PendingSteers() []string {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	out := make([]string, 0, len(a.steers))
	for _, e := range a.steers {
		out = append(out, e.content)
	}
	return out
}

// TurnActive reports whether a turn holds the identity right now.
//
// DERIVED from the gate that does the holding, never stored beside it.
// A second field saying the same thing is a fact in two places with
// nothing forcing agreement, which is how "present" came to be displayed
// over an identity that could not hear.
func (a *App) TurnActive() bool {
	return a.turnGate != nil && len(a.turnGate) == 0
}

// CancelTurn stops the running turn. It reports whether there was one to
// stop, so the caller can tell the operator the truth either way.
//
// Steering adds information; this ends the work. Both are needed: the
// expensive failure is not a stale identity, it is a turn that spends
// thirty rounds going somewhere the operator can already see is wrong.
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

// beginCancellableTurn makes the running turn stoppable and returns the
// cleanup to defer. Withdrawing the cancel is the load-bearing half: a
// cancel that outlived its turn would stop the NEXT one, and an operator
// pressing stop on finished work must stop nothing at all.
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

// facilityGate is the turn gate AS HANDED TO FACILITIES (rhythm, the
// attention brief). It takes and releases the same one token everyone
// else uses — one voice stays one voice — but it marks the hold,
// because a facility pass never reaches a tool boundary and so never
// drains the steer queue. steer() refuses a marked hold with
// dashboard.ErrBusyInternal and the message waits for its own turn
// instead of rotting in a queue nobody reads.
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
	// The mark clears BEFORE the token returns: a message arriving
	// between the two sees an unmarked hold, queues as an ordinary
	// steer, and releaseTurn's leftover flush delivers it. The reverse
	// order would leave a marked FREE gate bouncing messages while
	// nobody holds the turn at all.
	g.a.turnMu.Lock()
	g.a.turnFacility = false
	g.a.turnMu.Unlock()
	g.a.releaseTurn()
}
