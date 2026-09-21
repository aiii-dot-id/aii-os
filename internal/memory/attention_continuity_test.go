package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestContinuityIsAttentionOnlyWhenItIsNotWell(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	continuityOf := func(t *testing.T, statuses ...store.ContinuityStatus) []AttentionItem {
		t.Helper()
		s := newStore(t)
		for _, st := range statuses {
			if err := s.SetContinuityStatus(st); err != nil {
				t.Fatal(err)
			}
		}
		items, err := New(s).Attention(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		var out []AttentionItem
		for _, it := range items {
			if it.Kind == AttentionContinuity {
				out = append(out, it)
			}
		}
		return out
	}
	ok := store.ContinuityStatus{Outcome: store.ContinuityOK, Snapshot: "ledger-x", Record: 9, Encrypted: true}
	okAt := func(d time.Duration) store.ContinuityStatus { st := ok; st.At = at(d); return st }
	failedAt := func(d time.Duration) store.ContinuityStatus {
		return store.ContinuityStatus{Outcome: store.ContinuityFailed, At: at(d), Detail: "copy: /home/x/data/backups: no space left on device"}
	}

	for name, statuses := range map[string][]store.ContinuityStatus{
		"no pass has run":           nil,
		"a pass that published":     {okAt(3 * time.Hour)},
		"a day and a half old":      {okAt(36 * time.Hour)},
		"switched off":              {{Outcome: store.ContinuityDisabled, At: at(90 * 24 * time.Hour)}},
		"SAFE":                      {{Outcome: store.ContinuitySafe, At: at(5 * 24 * time.Hour)}},
		"failed, then published":    {failedAt(30 * time.Hour), okAt(time.Hour)},
		"unencrypted with no cause": {{Outcome: store.ContinuityOK, At: at(time.Hour)}},
	} {
		if items := continuityOf(t, statuses...); len(items) != 0 {
			t.Errorf("%s: WELL IS NOTHING, and this said %q", name, items[0].Text)
		}
	}

	one := func(t *testing.T, wantCost string, statuses ...store.ContinuityStatus) AttentionItem {
		t.Helper()
		items := continuityOf(t, statuses...)
		if len(items) != 1 || items[0].Cost != wantCost {
			t.Fatalf("want one %s item, got %+v", wantCost, items)
		}
		return items[0]
	}
	first := one(t, CostMedium, failedAt(2*time.Hour))
	if !strings.Contains(first.Text, "work action=backup.take") || !strings.Contains(first.Text, "recall source=continuity") {
		t.Errorf("the item does not name the act that answers it: %q", first.Text)
	}
	if strings.Contains(first.Text, "/home/x") || strings.Contains(first.Text, "no space left") {
		t.Errorf("what failed names files and is the operator's; the identity was shown it: %q", first.Text)
	}
	// .
	// .
	one(t, CostLow, failedAt(72*time.Hour), failedAt(48*time.Hour), failedAt(time.Hour))
	// .
	one(t, CostMedium, failedAt(20*time.Hour), failedAt(time.Hour))

	// .
	if it := one(t, CostMedium, okAt(50*time.Hour)); !strings.Contains(it.Text, "2 days") {
		t.Errorf("a stale snapshot does not say how old: %q", it.Text)
	}
	one(t, CostLow, okAt(7*24*time.Hour))

	// .
	unenc := store.ContinuityStatus{Outcome: store.ContinuityOK, At: at(time.Hour), Snapshot: "ledger-x", Unencrypted: store.UnencryptedNoEscrow}
	if it := one(t, CostLow, unenc); !strings.Contains(it.Text, "your operator's act") || strings.Contains(it.Text, "aii escrow") {
		t.Errorf("the unencrypted item is not worded for the identity: %q", it.Text)
	}

	// .
	// .
	// .
	for _, tc := range []struct {
		failing  []store.ContinuityStatus
		wantSlot string
	}{
		{[]store.ContinuityStatus{failedAt(time.Hour)}, AttentionContinuity},
		{[]store.ContinuityStatus{failedAt(72 * time.Hour), failedAt(time.Hour)}, AttentionContradiction},
	} {
		s := newStore(t)
		seedLedger(t, s, 8, now.Add(-10*24*time.Hour))
		addBelief(t, s, "b_a", "the lighthouse is red", 3, 1)
		addBelief(t, s, "b_b", "the lighthouse is white", 3, 2)
		if _, err := s.DB().Exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_t', 'b_a', 'b_b', 'CONTRADICTS', 3)`); err != nil {
			t.Fatal(err)
		}
		for _, st := range tc.failing {
			if err := s.SetContinuityStatus(st); err != nil {
				t.Fatal(err)
			}
		}
		items, err := New(s).Attention(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		medium := OfCost(items, CostMedium)
		if len(medium) == 0 || medium[0].Kind != tc.wantSlot {
			t.Errorf("failing through %d passes: the turn's slot shows %+v, want %s", len(tc.failing), medium, tc.wantSlot)
		}
	}
}
