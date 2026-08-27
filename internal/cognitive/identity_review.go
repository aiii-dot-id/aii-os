package cognitive

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// IdentityReviewConfig holds IDENTITY_REVIEW parameters.
type IdentityReviewConfig struct {
	IntervalPulses int64 // Life-clock ticks between runs
}

// IdentityReviewFacility implements IDENTITY_REVIEW — structural conscience.
// Checks for orphans, staleness, drift. The Method folds in here.
//
// Fires on a life alarm (every N lived pulses).
//
// IDENTITY_REVIEW has no resident-visible output path. It logs structural
// observations for the system operator but does not write notes, create
// edges, or nudge the resident. Per R30: attention composes at read time
// from durable source records — IDENTITY_REVIEW is a maintenance check,
// not a communication channel. The LAST RESULT is held for the operator
// dashboard's continuity strip (an operator surface, not a resident one).
type IdentityReviewFacility struct {
	store  ReviewStore
	config IdentityReviewConfig

	mu   sync.Mutex
	last ReviewSnapshot
}

// ReviewSnapshot is the last completed review, held in memory for the
// operator dashboard. Volatile by design: it dies with the process, and
// a restart shows "not yet run" rather than a stale verdict. No store
// writes — the schema directive forbids new tables, and R30 forbids a
// resident-visible channel; this is operator telemetry, like the log.
type ReviewSnapshot struct {
	At          time.Time `json:"at"`
	Clear       bool      `json:"clear"`
	IssueCount  int       `json:"issue_count"`
	Issues      []string  `json:"issues,omitempty"`
	Beliefs     int       `json:"beliefs"`
	Intentions  int       `json:"intentions"`
	Unprocessed int       `json:"unprocessed"`
}

// ReviewStore is the store interface IDENTITY_REVIEW needs.
type ReviewStore interface {
	ListBeliefs() ([]store.Belief, error)
	ListIntentions() ([]store.Intention, error)
	UnprocessedExperienceCount() (int, error)
	StandingSource
	TensionsSource // the review sees standing contradictions too
}

// NewIdentityReview creates an IDENTITY_REVIEW facility.
func NewIdentityReview(store ReviewStore, cfg IdentityReviewConfig) *IdentityReviewFacility {
	if cfg.IntervalPulses == 0 {
		cfg.IntervalPulses = IdentityReviewCadence
	}
	return &IdentityReviewFacility{
		store:  store,
		config: cfg,
	}
}

// Name returns the facility name.
func (r *IdentityReviewFacility) Name() string { return "identity_review" }

// Predicate — time-based, always true when alarm fires.
func (r *IdentityReviewFacility) Predicate(ctx context.Context) bool {
	return true
}

// Execute runs IDENTITY_REVIEW: check for structural issues.
func (r *IdentityReviewFacility) Execute(ctx context.Context) error {
	beliefs, err := r.store.ListBeliefs()
	if err != nil {
		return fmt.Errorf("identity_review: list beliefs: %w", err)
	}

	intentions, err := r.store.ListIntentions()
	if err != nil {
		return fmt.Errorf("identity_review: list intentions: %w", err)
	}

	unprocessed, err := r.store.UnprocessedExperienceCount()
	if err != nil {
		return fmt.Errorf("identity_review: unprocessed count: %w", err)
	}

	// Structural checks
	var issues []string

	// Check for stale beliefs (all same standing — may indicate no growth)
	standingCounts := make(map[string]int)
	for _, b := range beliefs {
		standingCounts[r.store.StandingFor(b.ID)]++
	}
	if len(beliefs) > 0 && len(standingCounts) == 1 {
		if _, allNew := standingCounts["new"]; allNew && len(beliefs) > 5 {
			issues = append(issues, "all beliefs are 'new' — none confirmed or trusted")
		}
	}

	// The TENSIONS VIEW (derived, UNCONSCIOUS_V2 §2.2): standing
	// contradictions are review observations — the review NOTICES; the
	// resident decides (the C coherence gate refused; we observe).
	if pairs, err := r.store.TensionsView(); err == nil && len(pairs) > 0 {
		ids := make([]string, 0, len(pairs)*2)
		for _, p := range pairs {
			ids = append(ids, p.LeftID, p.RightID)
		}
		stmts, _ := r.store.StatementsFor(ids)
		for _, pr := range pairs {
			l, lok := stmts[pr.LeftID]
			rr, rok := stmts[pr.RightID]
			if lok && rok {
				issues = append(issues, fmt.Sprintf("standing contradiction: %q vs %q — consider resolving (edge.archive) or superseding", l, rr))
			} else {
				issues = append(issues, fmt.Sprintf("standing contradiction: %s vs %s", pr.LeftID, pr.RightID))
			}
		}
	}

	// Check for abandoned intentions
	activeCount := 0
	for _, i := range intentions {
		if i.State == "active" {
			activeCount++
		}
	}
	if activeCount > 10 {
		issues = append(issues, fmt.Sprintf("%d active intentions — may need pruning", activeCount))
	}

	// Check for unprocessed backlog
	if unprocessed > 50 {
		issues = append(issues, fmt.Sprintf("%d unprocessed experiences — DREAM/CONSOLIDATE may be stuck", unprocessed))
	}

	if len(issues) > 0 {
		for _, issue := range issues {
			log.Printf("IDENTITY_REVIEW: %s", issue)
		}
	} else {
		log.Printf("IDENTITY_REVIEW: all clear (%d beliefs, %d intentions, %d unprocessed)",
			len(beliefs), activeCount, unprocessed)
	}

	// Hold the last result for the operator's continuity strip —
	// volatile, never a store write.
	r.mu.Lock()
	r.last = ReviewSnapshot{
		At:          time.Now().UTC(),
		Clear:       len(issues) == 0,
		IssueCount:  len(issues),
		Issues:      issues,
		Beliefs:     len(beliefs),
		Intentions:  activeCount,
		Unprocessed: unprocessed,
	}
	r.mu.Unlock()

	return nil
}

// LastReview returns the last completed review snapshot (operator
// telemetry; zero-value At means "not yet run this process").
func (r *IdentityReviewFacility) LastReview() ReviewSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// OnAlarm handles TIME alarm dispatch.
func (r *IdentityReviewFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	if err := r.Execute(ctx); err != nil {
		log.Printf("IDENTITY_REVIEW: execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	return AlarmResult{Accepted: true}
}
