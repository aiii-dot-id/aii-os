package cognitive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .

type fakeStagnation struct {
	items    []store.StaleIntention
	err      error
	verdicts [3]int
	probe    *store.StaleBelief
	door     *mockLedger
}

// .
// .
func (f *fakeStagnation) EntityExists(id string) (bool, error) {
	if f.door == nil {
		return false, nil
	}
	for _, b := range attentionBriefs(f.door) {
		if b["id"] == id {
			return true, nil
		}
	}
	return false, nil
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
	// .
	r.lastConsolidate = time.Now()
	return r
}

func TestStagnationBriefsOnceAndOnlyOnChange(t *testing.T) {
	src := &fakeStagnation{items: []store.StaleIntention{
		{ID: "i-old", Statement: "understand the operator", Gap: 162},
		{ID: "i-older", Statement: "queue referent check", Gap: 275},
	}}
	door := &mockLedger{st: &mockStore{}}
	src.door = door
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
	// .
	if len(escalations) != 1 || !strings.Contains(escalations[0], "unattended") {
		t.Fatalf("operator escalation = %v, want one", escalations)
	}

	// .
	// .
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if got := attentionBriefs(door); len(got) != 1 {
		t.Fatalf("unchanged drift was re-briefed: %d briefs", len(got))
	}

	// .
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
	// .
	r := NewRhythm(&fakeRaw{n: 0}, freeGate(), nil, nil, nil, nil)
	r.lastConsolidate = time.Now()
	if res := r.OnAlarm(context.Background(), "rhythm", "wall", 0, ""); !res.Accepted {
		t.Fatal("rhythm pass must still accept")
	}
}

// .
// .
// .
// .
// .
// .
func TestStagnationBriefSurvivesRestart(t *testing.T) {
	src := &fakeStagnation{items: []store.StaleIntention{
		{ID: "i-old", Statement: "understand the operator", Gap: 136},
	}}
	door := &mockLedger{st: &mockStore{}}
	src.door = door
	r1 := quietRhythm(src, door, nil)
	r1.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if got := attentionBriefs(door); len(got) != 1 {
		t.Fatalf("first process: %d briefs, want 1", len(got))
	}

	// .
	// .
	src.items[0].Gap = 137
	r2 := quietRhythm(src, door, nil)
	r2.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if got := attentionBriefs(door); len(got) != 1 {
		t.Fatalf("the restarted process re-briefed an unchanged stall: %d briefs", len(got))
	}
}

// .
// .
func TestStagnationRedriftAfterATouchIsBriefedAgain(t *testing.T) {
	src := &fakeStagnation{items: []store.StaleIntention{
		{ID: "i-old", Statement: "understand the operator", Gap: 120, UpdatedSeq: 40},
	}}
	door := &mockLedger{st: &mockStore{}}
	src.door = door
	r := quietRhythm(src, door, nil)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if got := attentionBriefs(door); len(got) != 1 {
		t.Fatalf("unchanged drift: %d briefs, want 1", len(got))
	}
	// .
	src.items[0].UpdatedSeq = 300
	src.items[0].Gap = 105
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if got := attentionBriefs(door); len(got) != 2 {
		t.Fatalf("a re-drift after a touch was not briefed: %d briefs", len(got))
	}
}
