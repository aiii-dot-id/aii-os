package cognitive

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"log"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// Rhythm drives METABOLISM on the wall clock, capacity-gated — R29's
// actual words ("capacity-gated, never idle-gated") made mechanical,
// per James's 2026-08-18 agency ruling: the unconscious always runs
// when there is material, operator present or not. The life clock
// remains presence-gated (R44) and gates MATURATION only — lived time
// is witnessed; metabolism is not.
//
// One wall alarm, one owner, predicates on existing facilities. No new
// subsystem, no persisted state beyond process-local spacing stamps:
// DREAM and CONSOLIDATE fire when raw experiences exist (self-limiting:
// processing clears the predicate; their own anti-rumination clauses
// handle the rest). SELF_MODEL and IDENTITY_REVIEW are reflective, not
// material-triggered — they run on wall spacing (structural minimums,
// R15) and no-op honestly when nothing changed.
type Rhythm struct {
	stagSrc      stagnationSource
	attnDoor     LedgerWriter
	attnOutbox   func(id, content string)
	lastProbeID  string // one nomination per belief-version per process
	lastBriefKey string

	raw  rawLister
	turn TurnGate

	dream       AlarmOwner
	consolidate AlarmOwner
	selfModel   AlarmOwner
	review      AlarmOwner

	lastConsolidate time.Time
	lastSelfModel   time.Time
	lastReview      time.Time
}

// TurnGate is the identity's one-voice lock, as metabolism needs it.
//
// ONE IDENTITY, ONE VOICE — and until now that held only for the half of
// the mind the operator can see. The facilities here each make their own
// LLM calls, and nothing on the alarm path took the gate, so CONSOLIDATE
// could be distilling beliefs while the operator's turn was mid-tool
// call: two thoughts at once, on one provider, from one identity.
//
// TryBeginTurn is take-or-fail rather than ask-then-act, because asking
// leaves a gap a turn can start in.
type TurnGate interface {
	TryBeginTurn() bool
	EndTurn()
}

// rawLister is the one store capability the predicates need.
type rawLister interface {
	ListRawExperiences(limit int) ([]store.Experience, error)
}

// Structural spacing minimums (R15: structural or nothing — SAFE-style
// emergency posture numbers, not operator tunables; the CADENCE itself
// is config: agency.rhythm_seconds).
const (
	consolidateSpacing = 30 * time.Minute
	selfModelSpacing   = 6 * time.Hour
	reviewSpacing      = 24 * time.Hour
)

// NewRhythm creates the metabolism driver. A nil TurnGate runs
// unserialized — the pre-2026-08-25 behaviour, kept only for tests that
// exercise the predicates and never call an LLM.
func NewRhythm(raw rawLister, turn TurnGate, dream, consolidate, selfModel, review AlarmOwner) *Rhythm {
	return &Rhythm{raw: raw, turn: turn, dream: dream, consolidate: consolidate, selfModel: selfModel, review: review}
}

// stagnationSource lists active intentions the ledger has moved past.
type stagnationSource interface {
	StaleActiveIntentions(minGap uint64) ([]store.StaleIntention, error)
	// VerdictCounts: the identity's CLAIMED completion outcomes to
	// date (self-reported; the brief says so).
	VerdictCounts() (served, partial, unserved int, err error)
	// OldestStaleBelief: the probe-nominator's pick after a review
	// pass — the longest-untouched plain ring-3 belief.
	OldestStaleBelief(minGap uint64) (store.StaleBelief, bool, error)
}

// SetAttention wires the stagnation predicate (evaluate layer,
// 2026-08-26): src supplies the signal, door mints the attention brief
// as RAW experience — the identity metabolizes its own stall on the
// next pass, the same road every other experience travels — and outbox
// (nil-safe) escalates to the operator past the second threshold.
// Proposal power only: nothing here replans, reprioritizes, or touches
// an intention. All three nil-safe; unset = the predicate never runs.
func (r *Rhythm) SetAttention(src stagnationSource, door LedgerWriter, outbox func(id, content string)) {
	r.stagSrc, r.attnDoor, r.attnOutbox = src, door, outbox
}

// Stagnation thresholds, in ledger EVENTS (constants beside the spacing
// constants above, and like them: posture, not operator tunables).
// Derived from the live identity on 2026-08-26 — four active intentions
// at gaps 275/162/119/108 of a 278-event life, zero ever completed —
// so the brief threshold catches a real drift and the operator
// threshold catches one that has consumed most of a life unattended.
const (
	stagnationBriefGap    = 100
	stagnationOperatorGap = 250
)

func (r *Rhythm) Name() string { return "rhythm" }

// (stagnation state — see SetAttention)

// OnAlarm is one metabolism pass: evaluate capacity, run what has work.
// Facilities run inline (the executor already invoked us on a worker);
// each facility's own budget/anti-rumination gates still apply.
func (r *Rhythm) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) AlarmResult {
	// METABOLISM YIELDS TO THE PERSON. If the identity is in a turn,
	// this pass declines and the alarm row is preserved — TIME fires it
	// again on the next tick, and the spacing timers below are not
	// advanced, so nothing is skipped, only deferred.
	//
	// Declining rather than waiting is deliberate: parking here would
	// hold an executor worker for the length of a conversation, and
	// digesting the day while someone is mid-sentence is the wrong thing
	// to do even when it is affordable.
	if r.turn != nil {
		if !r.turn.TryBeginTurn() {
			log.Printf("RHYTHM: the identity is in a turn — metabolism deferred to the next pass")
			return AlarmResult{} // declined, no deadline: the row is preserved
		}
		defer r.turn.EndTurn()
	}

	now := time.Now()

	hasRaw := false
	if exps, err := r.raw.ListRawExperiences(1); err == nil && len(exps) > 0 {
		hasRaw = true
	}

	run := func(name string, owner AlarmOwner) bool {
		if owner == nil {
			return false
		}
		res := owner.OnAlarm(ctx, "rhythm:"+name, "wall", deadline, "")
		if res.Accepted {
			log.Printf("RHYTHM: %s ran (capacity)", name)
		}
		return res.Accepted
	}

	// ONE MODE PER DELTA. hasRaw is read ONCE, from a single raw
	// experience, and both facilities used to act on it in sequence:
	// DREAM ran and metabolized the raw experiences, then CONSOLIDATE ran
	// against a predicate that no longer held — converging over material
	// the divergent pass had already consumed, or over nothing at all.
	//
	// The two are opposites by design ("where DREAM is divergent, you are
	// convergent"), so running both across one delta asks the identity to
	// diverge and converge on the same material in a single breath.
	// CONSOLIDATE takes the delta when its spacing is satisfied; DREAM
	// takes it otherwise.
	// SPACING ADVANCES ON ATTEMPT. The morning revision of 2026-08-26
	// made it advance on success so a transport timeout would not cost
	// the portrait its window — and by afternoon a self_model pass with
	// a persistently refused citation was re-running on EVERY tick,
	// holding the turn gate for minutes each time, and the operator's
	// messages queued behind metabolism: a live outage. A failed pass
	// waits its full spacing like a successful one. The miss is not
	// hidden — the facility mints its failure as a raw experience
	// (self_model.go), which is the record's job, not the scheduler's.
	ranAny := false
	if hasRaw {
		if now.Sub(r.lastConsolidate) >= consolidateSpacing {
			if run("consolidate", r.consolidate) {
				ranAny = true
			}
			r.lastConsolidate = now
		} else {
			if run("dream", r.dream) {
				ranAny = true
			}
		}
	}
	if now.Sub(r.lastSelfModel) >= selfModelSpacing {
		if run("self_model", r.selfModel) {
			ranAny = true
		}
		r.lastSelfModel = now
	}
	if now.Sub(r.lastReview) >= reviewSpacing {
		if run("identity_review", r.review) {
			ranAny = true
			// The review looked at the record; the nominator hands it
			// one inherited conclusion to re-derive (D-wires).
			r.nominateProbe()
		}
		r.lastReview = now
	}
	if !ranAny {
		// Liveness evidence: a pass that ran and owed nothing says so,
		// or a silent log cannot distinguish "healthy and idle" from
		// "rhythm stopped ticking" (operator request 2026-08-26).
		log.Printf("RHYTHM: pass complete — no facility was due")
	}

	r.checkStagnation()

	return AlarmResult{Accepted: true} // recurring: TIME rearms by repeat
}

// efficacyReadyAt is how many claimed verdicts make the tally worth a
// conversation. Twenty is enough shape to see a pattern and few
// enough to arrive within an identity's first weeks.
const efficacyReadyAt = 20

// probeBeliefGap reuses the brief gap: a belief untouched for a
// hundred events while the ledger moved is inherited, not held.
const probeBeliefGap = stagnationBriefGap

// nominateProbe runs after an ACCEPTED review pass: it hands the
// identity one inherited conclusion to re-derive — the
// longest-untouched plain ring-3 belief — as raw material, once per
// belief-version (the id derives from id|statement, and lastProbeID
// keeps one process from re-minting what the door would refuse).
// SKILLS.md rule 9 is the text's spine: a figure written by a
// previous you is a claim, not evidence.
func (r *Rhythm) nominateProbe() {
	if r.stagSrc == nil || r.attnDoor == nil {
		return
	}
	sb, ok, err := r.stagSrc.OldestStaleBelief(probeBeliefGap)
	if err != nil {
		log.Printf("RHYTHM: probe read failed: %v", err)
		return
	}
	if !ok {
		return
	}
	id := "exp_probe_" + outputHash(sb.ID+"|"+sb.Statement)
	if id == r.lastProbeID {
		return
	}
	content := fmt.Sprintf("probe: belief %s has not been re-derived for %d events: %q. Re-derive it from present evidence — confirm it, revise it, or archive it. A figure written by a previous you is a claim, not evidence (SKILLS.md rule 9).",
		sb.ID, sb.Gap, sb.Statement)
	if _, err := r.attnDoor.Append(ledger.EventExperienceCreate, 3, map[string]interface{}{
		"id":         id,
		"content":    content,
		"category":   "observation",
		"provenance": "system",
		"raw":        true,
	}, ""); err != nil {
		log.Printf("RHYTHM: probe nomination refused: %v", err)
		return
	}
	r.lastProbeID = id
	log.Printf("RHYTHM: probe nominated — %s (gap %d)", sb.ID, sb.Gap)
}

// checkStagnation is one read of the drift signal. It mints at most ONE
// attention brief, and only when the flagged set CHANGED since the last
// brief — a stall that persists unchanged was already said, and saying
// it every thirty minutes would bury the identity in its own alarm.
func (r *Rhythm) checkStagnation() {
	if r.stagSrc == nil || r.attnDoor == nil {
		return
	}
	// The verdict counters ride every stagnation look, drift or not:
	// they are the evaluate layer's raw tally, and the efficacy
	// watcher below announces ONCE (AddOutboxMessageOnce is the guard
	// — idempotent by id, no flag to forget) when enough claims have
	// accumulated to be worth reading honestly.
	served, partial, unserved, verr := r.stagSrc.VerdictCounts()
	if verr != nil {
		log.Printf("RHYTHM: verdict counts read failed: %v", verr)
	} else if r.attnOutbox != nil && served+partial+unserved >= efficacyReadyAt {
		r.attnOutbox("efficacy_data_ready", fmt.Sprintf(
			"efficacy: %d completion verdicts have accumulated (served %d · partial %d · unserved %d) — enough to read honestly. These are the identity's own CLAIMS, not verified results; the peer record puts self-reported wins at 73.8%% proxy. A pass over which claims held — and which did not — is now worth a conversation.",
			served+partial+unserved, served, partial, unserved))
	}

	stale, err := r.stagSrc.StaleActiveIntentions(stagnationBriefGap)
	if err != nil {
		log.Printf("RHYTHM: stagnation read failed: %v", err)
		return
	}
	if len(stale) == 0 {
		r.lastBriefKey = ""
		return
	}
	key := ""
	worst := uint64(0)
	lines := make([]string, 0, len(stale))
	for _, si := range stale {
		key += fmt.Sprintf("%s@%d;", si.ID, si.Gap/stagnationBriefGap)
		if si.Gap > worst {
			worst = si.Gap
		}
		lines = append(lines, fmt.Sprintf("- %s (untouched for %d events): %s", si.ID, si.Gap, si.Statement))
	}
	if key == r.lastBriefKey {
		return // unchanged drift, already briefed
	}
	content := fmt.Sprintf("attention: %d active intention(s) have drifted — no state change while the ledger moved on. Complete each (outcome: served|partial|unserved), abandon it honestly, or act on it:\n%s",
		len(stale), strings.Join(lines, "\n"))
	if verr == nil {
		content += fmt.Sprintf("\nClaimed outcomes to date (self-reported, unverified): served %d · partial %d · unserved %d.",
			served, partial, unserved)
	}
	if _, err := r.attnDoor.Append(ledger.EventExperienceCreate, 3, map[string]interface{}{
		"id":         "exp_attention_" + outputHash(content),
		"content":    content,
		"category":   "observation",
		"provenance": "system",
		"raw":        true,
	}, ""); err != nil {
		log.Printf("RHYTHM: attention brief refused: %v", err)
		return
	}
	r.lastBriefKey = key
	log.Printf("RHYTHM: attention brief minted (%d stale intention(s), worst gap %d)", len(stale), worst)
	if r.attnOutbox != nil && worst >= stagnationOperatorGap {
		r.attnOutbox("attention_"+time.Now().UTC().Format("20060102"),
			fmt.Sprintf("[attention] %d intention(s) unattended for %d+ events — the identity has been briefed; a conversation may help.", len(stale), stagnationOperatorGap))
	}
}
