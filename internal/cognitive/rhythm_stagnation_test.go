package cognitive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// rhythm_stagnation_test.go — the drift predicate (evaluate layer).
// Stagnation is not hypothetical here: the live identity carried four
// active intentions at gaps 275/162/119/108 of a 278-event life, zero
// ever completed. These tests pin the response: one brief, as the
// identity's own raw material, re-said only when the drift CHANGES.

type fakeStagnation struct {
	items    []store.StaleIntention
	err      error
	verdicts [3]int // served, partial, unserved
	probe    *store.StaleBelief
}

func (f *fakeStagnation) VerdictCounts() (int, int, int, error) {
	return f.verdicts[0], f.verdicts[1], f.verdicts[2], nil
}

func (f *fakeStagnation) OldestStaleBelief(minGap uint64) (store.StaleBelief, bool, error) {
	if f.probe == nil || f.probe.Gap < minGap {
		return store.StaleBelief{}, false, nil
	}
	return *f.probe, true, nil
}

func (f *fakeStagnation) StaleActiveIntentions(minGap uint64) ([]store.StaleIntention, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []store.StaleIntention
	for _, si := range f.items {
		if si.Gap >= minGap {
			out = append(out, si)
		}
	}
	return out, nil
}

func attentionBriefs(door *mockLedger) []map[string]interface{} {
	var out []map[string]interface{}
	for i, et := range door.appended {
		if et != ledger.EventExperienceCreate {
			continue
		}
		if p, ok := door.payloads[i].(map[string]interface{}); ok {
			if id, _ := p["id"].(string); strings.HasPrefix(id, "exp_attention_") {
				out = append(out, p)
			}
		}
	}
	return out
}

func quietRhythm(src stagnationSource, door LedgerWriter, outbox func(string, string)) *Rhythm {
	r := NewRhythm(&fakeRaw{n: 0}, freeGate(), nil, nil, nil, nil)
	r.SetAttention(src, door, outbox)
	// Reflective spacing satisfied: only the predicate under test runs.
	r.lastSelfModel = time.Now()
	r.lastReview = time.Now()
	r.lastConsolidate = time.Now()
	return r
}

func TestStagnationBriefsOnceAndOnlyOnChange(t *testing.T) {
	src := &fakeStagnation{items: []store.StaleIntention{
		{ID: "i-old", Statement: "understand the operator", Gap: 162},
		{ID: "i-older", Statement: "queue referent check", Gap: 275},
	}}
	door := &mockLedger{st: &mockStore{}}
	var escalations []string
	r := quietRhythm(src, door, func(id, content string) { escalations = append(escalations, content) })

	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	briefs := attentionBriefs(door)
	if len(briefs) != 1 {
		t.Fatalf("first pass: %d briefs, want exactly 1", len(briefs))
	}
	content, _ := briefs[0]["content"].(string)
	for _, want := range []string{"i-old", "i-older", "162", "275", "served|partial|unserved"} {
		if !strings.Contains(content, want) {
			t.Fatalf("the brief does not carry %q:\n%s", want, content)
		}
	}
	if briefs[0]["provenance"] != "system" || briefs[0]["raw"] != true {
		t.Fatalf("the brief must be raw system material, got %v", briefs[0])
	}
	// Worst gap 275 >= the operator threshold: the operator heard too.
	if len(escalations) != 1 || !strings.Contains(escalations[0], "unattended") {
		t.Fatalf("operator escalation = %v, want one", escalations)
	}

	// UNCHANGED DRIFT IS NOT RE-SAID. A stall repeated every thirty
	// minutes buries the identity in its own alarm.
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if got := attentionBriefs(door); len(got) != 1 {
		t.Fatalf("unchanged drift was re-briefed: %d briefs", len(got))
	}

	// The drift CHANGES (a new intention crosses the line): say so once.
	src.items = append(src.items, store.StaleIntention{ID: "i-new", Statement: "third", Gap: 101})
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if got := attentionBriefs(door); len(got) != 2 {
		t.Fatalf("changed drift was not re-briefed: %d briefs", len(got))
	}
}

func TestStagnationBelowThresholdSaysNothing(t *testing.T) {
	src := &fakeStagnation{items: []store.StaleIntention{
		{ID: "i-fresh", Statement: "recent work", Gap: 40},
	}}
	door := &mockLedger{st: &mockStore{}}
	r := quietRhythm(src, door, nil)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if got := attentionBriefs(door); len(got) != 0 {
		t.Fatalf("a fresh intention was flagged: %v", got)
	}
}

func TestStagnationUnwiredIsInert(t *testing.T) {
	// No SetAttention: the predicate must not run, let alone panic.
	r := NewRhythm(&fakeRaw{n: 0}, freeGate(), nil, nil, nil, nil)
	r.lastSelfModel = time.Now()
	r.lastReview = time.Now()
	r.lastConsolidate = time.Now()
	if res := r.OnAlarm(context.Background(), "rhythm", "wall", 0, ""); !res.Accepted {
		t.Fatal("rhythm pass must still accept")
	}
}
