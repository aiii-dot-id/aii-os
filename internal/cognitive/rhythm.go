package cognitive

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
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
type Rhythm struct {
	decide      func(facility, decision, reason string)
	stagSrc     stagnationSource
	attnDoor    LedgerWriter
	attnOutbox  func(id, content string)
	lastProbeID string

	raw  rawLister
	turn TurnGate

	dream       AlarmOwner
	consolidate AlarmOwner
	selfModel   AlarmOwner
	review      AlarmOwner

	lastConsolidate time.Time

	outcomes          OutcomeIntake
	lastOutcomeIntake time.Time

	conversation ConversationProbe
}

// .
// .
// .
type OutcomeIntake interface {
	OutcomesPending() bool
	ObserveOutcomes(ctx context.Context) error
}

// .
// .
func (r *Rhythm) SetOutcomes(o OutcomeIntake) { r.outcomes = o }

// .
// .
func (r *Rhythm) OutcomesWired() bool { return r.outcomes != nil }

// .
// .
type ConversationProbe interface {
	ConversationPending() bool
}

// .
// .
func (r *Rhythm) SetConversation(p ConversationProbe) { r.conversation = p }

// .
func (r *Rhythm) ConversationWired() bool { return r.conversation != nil }

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
type TurnGate interface {
	TryBeginTurn() bool
	EndTurn()
}

// .
type rawLister interface {
	ListRawExperiences(limit int) ([]store.Experience, error)
}

// .
// .
// .
const (
	consolidateSpacing = 30 * time.Minute
)

// .
// .
const (
	ReflectSelfModelAlarm      = "reflect:self_model"
	ReflectIdentityReviewAlarm = "reflect:identity_review"
)

// .
// .
// .
func NewRhythm(raw rawLister, turn TurnGate, dream, consolidate, selfModel, review AlarmOwner) *Rhythm {
	return &Rhythm{raw: raw, turn: turn, dream: dream, consolidate: consolidate, selfModel: selfModel, review: review}
}

// .
type stagnationSource interface {
	StaleActiveIntentions(minGap uint64) ([]store.StaleIntention, error)
	// .
	// .
	VerdictCounts() (served, partial, unserved int, err error)
	// .
	// .
	OldestStaleBelief(minGap uint64) (store.StaleBelief, bool, error)
	// .
	// .
	EntityExists(id string) (bool, error)
}

// .
// .
// .
// .
// .
// .
// .
func (r *Rhythm) SetAttention(src stagnationSource, door LedgerWriter, outbox func(id, content string)) {
	r.stagSrc, r.attnDoor, r.attnOutbox = src, door, outbox
}

// .
// .
// .
// .
// .
// .
const (
	stagnationBriefGap    = 100
	stagnationOperatorGap = 250
)

func (r *Rhythm) Name() string { return "rhythm" }

// .

// .
// .
// .
func (r *Rhythm) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) AlarmResult {
	if strings.HasPrefix(alarmID, "reflect:") {
		return r.onReflect(ctx, alarmID, deadline)
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
	if r.turn != nil {
		if !r.turn.TryBeginTurn() {
			logsink.Info("rhythm.refusal", "the identity is in a turn — metabolism deferred to the next pass")
			r.decision("metabolism", "defer", "the identity is in a turn")
			return AlarmResult{}
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
			logsink.Info("rhythm.pass", "%s ran (capacity)", name)
			r.decision(name, "run", "capacity")
		} else {
			r.decision(name, "skip", "the facility declined")
		}
		return res.Accepted
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
	ranAny := false
	if r.outcomes != nil && now.Sub(r.lastOutcomeIntake) >= consolidateSpacing && r.outcomes.OutcomesPending() {
		r.lastOutcomeIntake = now
		if err := r.outcomes.ObserveOutcomes(ctx); err != nil {
			logsink.Warn("rhythm.refusal", "%v", err)
			r.decision("outcomes", "skip", err.Error())
		} else {
			logsink.Info("rhythm.pass", "outcome intake ran (capacity)")
			r.decision("outcomes", "run", "an outcome stood unobserved")
			ranAny = true
		}
	} else if hasRaw {
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
	} else if r.conversation != nil && r.conversation.ConversationPending() {
		// .
		// .
		// .
		// .
		if run("dream", r.dream) {
			ranAny = true
		}
	}
	if !ranAny {
		// .
		// .
		// .
		// .
		// .
		// .
		logsink.Tick("rhythm.pass", "passes, none due", 0)
		r.decision("metabolism", "skip", "no facility was due")
	}

	r.checkStagnation()

	return AlarmResult{Accepted: true}
}

// .
// .
// .
// .
// .
// .
// .
// .
func (r *Rhythm) onReflect(ctx context.Context, alarmID string, deadline int64) AlarmResult {
	var owner AlarmOwner
	name := ""
	switch alarmID {
	case ReflectSelfModelAlarm:
		owner, name = r.selfModel, "self_model"
	case ReflectIdentityReviewAlarm:
		owner, name = r.review, "identity_review"
	default:
		logsink.Warn("rhythm.refusal", "unknown reflective alarm %q — ignored", alarmID)
		return AlarmResult{Accepted: true}
	}
	if owner == nil {
		return AlarmResult{Accepted: true}
	}
	if r.turn != nil {
		if !r.turn.TryBeginTurn() {
			next := deadline + 1
			logsink.Info("rhythm.refusal", "%s owed at pulse %d — the identity is in a turn; deferred to the next pulse", name, deadline)
			r.decision(name, "defer", "the identity is in a turn")
			return AlarmResult{Accepted: false, NextDeadline: &next}
		}
		defer r.turn.EndTurn()
	}
	res := owner.OnAlarm(ctx, alarmID, "life", deadline, "")
	logsink.Info("rhythm.pass", "%s ran on lived time (pulse %d, accepted=%v)", name, deadline, res.Accepted)
	r.decision(name, "run", "lived time")
	if alarmID == ReflectIdentityReviewAlarm && res.Accepted {
		// .
		// .
		r.nominateProbe()
	}
	return AlarmResult{Accepted: true}
}

// .
// .
// .
const efficacyReadyAt = 20

// .
// .
const probeBeliefGap = stagnationBriefGap

// .
// .
// .
// .
// .
// .
// .
func (r *Rhythm) nominateProbe() {
	if r.stagSrc == nil || r.attnDoor == nil {
		return
	}
	sb, ok, err := r.stagSrc.OldestStaleBelief(probeBeliefGap)
	if err != nil {
		logsink.Warn("rhythm.error", "probe read failed: %v", err)
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
		logsink.Warn("rhythm.refusal", "probe nomination refused: %v", err)
		return
	}
	r.lastProbeID = id
	logsink.Info("rhythm.decision", "probe nominated — %s (gap %d)", sb.ID, sb.Gap)
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
// .
func (r *Rhythm) checkStagnation() {
	if r.stagSrc == nil || r.attnDoor == nil {
		return
	}
	// .
	// .
	// .
	// .
	// .
	served, partial, unserved, verr := r.stagSrc.VerdictCounts()
	if verr != nil {
		logsink.Warn("rhythm.error", "verdict counts read failed: %v", verr)
	} else if r.attnOutbox != nil && served+partial+unserved >= efficacyReadyAt {
		r.attnOutbox("efficacy_data_ready", fmt.Sprintf(
			"efficacy: %d completion verdicts have accumulated (served %d · partial %d · unserved %d) — enough to read honestly. These are the identity's own CLAIMS, not verified results; the peer record puts self-reported wins at 73.8%% proxy. A pass over which claims held — and which did not — is now worth a conversation.",
			served+partial+unserved, served, partial, unserved))
	}

	stale, err := r.stagSrc.StaleActiveIntentions(stagnationBriefGap)
	if err != nil {
		logsink.Warn("rhythm.error", "stagnation read failed: %v", err)
		return
	}
	if len(stale) == 0 {
		return
	}
	key := ""
	worst := uint64(0)
	lines := make([]string, 0, len(stale))
	for _, si := range stale {
		key += fmt.Sprintf("%s@%d@%d;", si.ID, si.UpdatedSeq, si.Gap/stagnationBriefGap)
		if si.Gap > worst {
			worst = si.Gap
		}
		lines = append(lines, fmt.Sprintf("- %s (untouched for %d events): %s", si.ID, si.Gap, si.Statement))
	}
	id := "exp_attention_" + outputHash(key)
	if exists, err := r.stagSrc.EntityExists(id); err != nil {
		logsink.Warn("rhythm.error", "attention brief lookup failed: %v — nothing minted", err)
		return
	} else if exists {
		return
	}
	content := fmt.Sprintf("attention: %d active intention(s) have drifted — no state change while the ledger moved on. Complete each (outcome: served|partial|unserved), abandon it honestly, or act on it:\n%s",
		len(stale), strings.Join(lines, "\n"))
	if verr == nil {
		content += fmt.Sprintf("\nClaimed outcomes to date (self-reported, unverified): served %d · partial %d · unserved %d.",
			served, partial, unserved)
	}
	if _, err := r.attnDoor.Append(ledger.EventExperienceCreate, 3, map[string]interface{}{
		"id":         id,
		"content":    content,
		"category":   "observation",
		"provenance": "system",
		"raw":        true,
	}, ""); err != nil {
		logsink.Warn("rhythm.refusal", "attention brief refused: %v", err)
		return
	}
	logsink.Info("rhythm.end", "attention brief minted (%d stale intention(s), worst gap %d)", len(stale), worst)
	if r.attnOutbox != nil && worst >= stagnationOperatorGap {
		r.attnOutbox("attention_"+time.Now().UTC().Format("20060102"),
			fmt.Sprintf("[attention] %d intention(s) unattended for %d+ events — the identity has been briefed; a conversation may help.", len(stale), stagnationOperatorGap))
	}
}

// .
// .
// .
func (r *Rhythm) SetDecisionLog(fn func(facility, decision, reason string)) { r.decide = fn }

func (r *Rhythm) decision(facility, decision, reason string) {
	if r.decide != nil {
		r.decide(facility, decision, reason)
	}
}
