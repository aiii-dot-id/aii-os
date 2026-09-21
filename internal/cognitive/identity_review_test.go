package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .

// .
// .
// .
// .
func TestAReviewThatCouldNotReadDoesNotPublishClear(t *testing.T) {
	for _, tc := range []struct {
		what   string
		breaks func(*mockStore)
	}{
		{"the standing of a belief", func(m *mockStore) { m.standingErr = map[string]error{"b1": errors.New("standing: disk")} }},
		{"the tensions view", func(m *mockStore) { m.tensionsErr = errors.New("tensions: disk") }},
		{"the ends of a contradiction", func(m *mockStore) {
			m.tensions = []store.TensionPair{{LeftID: "b1", RightID: "b2"}}
			m.tensionEndsErr = errors.New("ends: disk")
		}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			m := &mockStore{beliefs: []store.Belief{{ID: "b1", Statement: "one"}, {ID: "b2", Statement: "two"}}}
			r := NewIdentityReview(m, IdentityReviewConfig{IntervalPulses: 100})

			// .
			if err := r.Execute(context.Background()); err != nil {
				t.Fatalf("the pass that could read: %v", err)
			}
			before := r.LastReview()
			if !before.Clear || before.At.IsZero() {
				t.Fatalf("rig: the first pass did not publish a clear result: %+v", before)
			}

			tc.breaks(m)
			err := r.Execute(context.Background())
			if err == nil {
				t.Fatalf("A REVIEW THAT COULD NOT READ %s PUBLISHED ITS VERDICT ANYWAY", tc.what)
			}
			if !strings.Contains(err.Error(), "identity_review") {
				t.Errorf("the refusal does not name what failed: %v", err)
			}
			if got := r.LastReview(); got.At != before.At || got.Clear != before.Clear || got.IssueCount != before.IssueCount {
				t.Errorf("the failed pass moved the last completed result: was %+v, now %+v", before, got)
			}
		})
	}
}

// .
func TestAReviewThatReadPublishesEitherWay(t *testing.T) {
	m := &mockStore{beliefs: []store.Belief{{ID: "b1", Statement: "one"}}}
	r := NewIdentityReview(m, IdentityReviewConfig{IntervalPulses: 100})
	if err := r.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.LastReview(); !got.Clear || got.IssueCount != 0 || got.At.IsZero() {
		t.Fatalf("a clean review: %+v", got)
	}
	m.tensions = []store.TensionPair{{LeftID: "b1", RightID: "b2"}}
	if err := r.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.LastReview(); got.Clear || got.IssueCount != 1 || len(got.Issues) != 1 {
		t.Fatalf("a review with something to say: %+v", got)
	}
}
