package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// WHAT THE LAST MAINTENANCE PASS SAID, AS ATTENTION — and mostly as nothing.
// While all is well no item exists. A pass that failed, or a last snapshot
// two days old, holds the turn's one slot for a DAY and is the morning
// brief's from then on: a pass that fails for weeks must not hold for weeks
// the slot that tensions were given. Fresh snapshots that are kept are
// quiet even when unencrypted. A pass the operator switched off, and a
// SAFE boot, are said by their own surfaces.
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
		var items []AttentionItem
		for _, cost := range []string{attention.CostMedium, attention.CostLow} {
			got, err := New(s).Attention(context.Background(), now, cost)
			if err != nil {
				t.Fatal(err)
			}
			items = append(items, got...)
		}
		var out []AttentionItem
		for _, it := range items {
			if it.Kind == attention.AttentionContinuity {
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
	first := one(t, attention.CostMedium, failedAt(2*time.Hour))
	if !strings.Contains(first.Text, "work action=backup.take") || !strings.Contains(first.Text, "recall source=continuity") {
		t.Errorf("the item does not name the act that answers it: %q", first.Text)
	}
	if strings.Contains(first.Text, "/home/x") || strings.Contains(first.Text, "no space left") {
		t.Errorf("what failed names files and is the operator's; the identity was shown it: %q", first.Text)
	}
	// Failing since three days ago, the last failure an hour ago: the slot
	// was held for its day and is given back.
	one(t, attention.CostLow, failedAt(72*time.Hour), failedAt(48*time.Hour), failedAt(time.Hour))
	// Still inside the first day of a failing run.
	one(t, attention.CostMedium, failedAt(20*time.Hour), failedAt(time.Hour))

	// No pass for two days and a bit: medium; for a week: low.
	if it := one(t, attention.CostMedium, okAt(50*time.Hour)); !strings.Contains(it.Text, "2 days") {
		t.Errorf("a stale snapshot does not say how old: %q", it.Text)
	}
	one(t, attention.CostLow, okAt(7*24*time.Hour))

	// The pass succeeded: no encryption reason is an automatic reminder,
	// in either the turn or the morning brief. Failure and age still matter.
	for _, reason := range []string{
		store.UnencryptedNoKey,
		store.UnencryptedKeyUnusable,
		store.UnencryptedNoEscrow,
		store.UnencryptedReceiptUnread,
		store.UnencryptedReceiptMismatch,
		store.UnencryptedIdentityClosed,
		"unknown_future_reason",
	} {
		t.Run("unencrypted_"+reason, func(t *testing.T) {
			unenc := okAt(time.Hour)
			unenc.Encrypted = false
			unenc.Unencrypted = reason
			if items := continuityOf(t, unenc); len(items) != 0 {
				t.Fatalf("a fresh successful unencrypted snapshot generated attention: %+v", items)
			}
			for _, tc := range []struct {
				age  time.Duration
				cost string
			}{
				{50 * time.Hour, attention.CostMedium},
				{7 * 24 * time.Hour, attention.CostLow},
			} {
				unenc.At = at(tc.age)
				if it := one(t, tc.cost, unenc); !strings.Contains(it.Text, "the daily pass has not run") {
					t.Errorf("staleness was replaced by an encryption reminder: %q", it.Text)
				}
			}
			failed := failedAt(time.Hour)
			if it := one(t, attention.CostMedium, unenc, failed); !strings.Contains(it.Text, "failed and nothing was kept") {
				t.Errorf("a failure after an unencrypted snapshot was not surfaced: %q", it.Text)
			}
		})
	}

	// For its day a continuity item is FIRST among the medium ones — it is
	// what the turn's one slot shows, ahead of an open tension — and after
	// its day the tension has the slot back.
	for _, tc := range []struct {
		failing  []store.ContinuityStatus
		wantSlot string
	}{
		{[]store.ContinuityStatus{failedAt(time.Hour)}, attention.AttentionContinuity},
		{[]store.ContinuityStatus{failedAt(72 * time.Hour), failedAt(time.Hour)}, attention.AttentionContradiction},
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
		medium, err := New(s).Attention(context.Background(), now, attention.CostMedium)
		if err != nil {
			t.Fatal(err)
		}
		if len(medium) == 0 || medium[0].Kind != tc.wantSlot {
			t.Errorf("failing through %d passes: the turn's slot shows %+v, want %s", len(tc.failing), medium, tc.wantSlot)
		}
	}
}
