package cognitive

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
type IdentityReviewConfig struct {
	IntervalPulses int64
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
type IdentityReviewFacility struct {
	store  ReviewStore
	config IdentityReviewConfig

	mu   sync.Mutex
	last ReviewSnapshot
}

// .
// .
// .
// .
// .
type ReviewSnapshot struct {
	At          time.Time `json:"at"`
	Clear       bool      `json:"clear"`
	IssueCount  int       `json:"issue_count"`
	Issues      []string  `json:"issues,omitempty"`
	Beliefs     int       `json:"beliefs"`
	Intentions  int       `json:"intentions"`
	Unprocessed int       `json:"unprocessed"`
}

// .
type ReviewStore interface {
	ListBeliefs() ([]store.Belief, error)
	ListIntentions() ([]store.Intention, error)
	UnprocessedExperienceCount() (int, error)
	StandingSource
	TensionsSource
}

// .
func NewIdentityReview(store ReviewStore, cfg IdentityReviewConfig) *IdentityReviewFacility {
	if cfg.IntervalPulses == 0 {
		cfg.IntervalPulses = IdentityReviewCadence
	}
	return &IdentityReviewFacility{
		store:  store,
		config: cfg,
	}
}

// .
func (r *IdentityReviewFacility) Name() string { return "identity_review" }

// .
func (r *IdentityReviewFacility) Predicate(ctx context.Context) bool {
	return true
}

// .
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

	// .
	var issues []string

	// .
	// .
	// .
	// .
	// .
	// .
	standingCounts := make(map[string]int)
	for _, b := range beliefs {
		standing, err := r.store.StandingFor(b.ID)
		if err != nil {
			return fmt.Errorf("identity_review: standing of %s: %w", b.ID, err)
		}
		standingCounts[standing]++
	}
	if len(beliefs) > 0 && len(standingCounts) == 1 {
		if _, allNew := standingCounts["new"]; allNew && len(beliefs) > 5 {
			issues = append(issues, "all beliefs are 'new' — none confirmed or trusted")
		}
	}

	// .
	// .
	// .
	pairs, err := r.store.TensionsView()
	if err != nil {
		return fmt.Errorf("identity_review: tensions view: %w", err)
	}
	if len(pairs) > 0 {
		ids := make([]string, 0, len(pairs)*2)
		for _, p := range pairs {
			ids = append(ids, p.LeftID, p.RightID)
		}
		// .
		// .
		// .
		// .
		// .
		// .
		ends, err := r.store.TensionEnds(ids)
		if err != nil {
			return fmt.Errorf("identity_review: the ends of %d standing contradictions: %w", len(pairs), err)
		}
		for _, pr := range pairs {
			issues = append(issues, fmt.Sprintf("standing contradiction: %s vs %s — consider resolving (edge.archive) or superseding",
				describeTensionEnd(ends[pr.LeftID], pr.LeftID, nil), describeTensionEnd(ends[pr.RightID], pr.RightID, nil)))
		}
	}

	// .
	activeCount := 0
	for _, i := range intentions {
		if i.State == "active" {
			activeCount++
		}
	}
	if activeCount > 10 {
		issues = append(issues, fmt.Sprintf("%d active intentions — may need pruning", activeCount))
	}

	// .
	if unprocessed > 50 {
		issues = append(issues, fmt.Sprintf("%d unprocessed experiences — DREAM/CONSOLIDATE may be stuck", unprocessed))
	}

	if len(issues) > 0 {
		logsink.Info("review.decision", "%d to look at: %s", len(issues), strings.Join(issues, "; "))
	} else {
		// .
		// .
		logsink.Tick("review.pass", "passes, all clear", 0)
	}

	// .
	// .
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

// .
// .
func (r *IdentityReviewFacility) LastReview() ReviewSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// .
func (r *IdentityReviewFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	if err := r.Execute(ctx); err != nil {
		logsink.Warn("review.error", "execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	return AlarmResult{Accepted: true}
}
