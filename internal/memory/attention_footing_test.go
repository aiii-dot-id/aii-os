package memory

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/memory/carrd"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
)

// A BELIEF THAT STANDS IS NOT FADING, AND EVIDENCE IS WHAT MAKES IT STAND
// (the operator and the identity, 2026-11-08: the alert asked for evidence that could not
// move its number). A year unrecalled:
//   - b_confirmed rests on three experiences, three origins: it stands, and
//     is not an alert at any age;
//   - b_one rests on one: it does not stand, and is an alert that names
//     what would change it.
//
// Four months unrecalled, each resting on one experience (standard class
// would be about 0.36, under the line):
//   - b_for_value supports an adopted (ring-2) value: Core, about 0.80, not
//     an alert;
//   - b_for_standing supports b_confirmed, a belief that stands: Core, not an
//     alert;
//   - b_for_weak supports b_one, which does not stand: it grants nothing,
//     standard, an alert.
func TestABeliefThatStandsIsNotFadingAndEvidenceIsWhatMakesItStand(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 8, now.Add(-400*24*time.Hour))
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (98, '', ?, 'test.event', 3, '{}', '')`, now.Add(-120*24*time.Hour).UTC().Format(time.RFC3339Nano))
	for i, id := range []string{"e1", "e2", "e3", "e4", "e5", "e6"} {
		addExperience(t, s, id, "an observation "+id, 2+i%3, now.Add(-300*24*time.Hour), false)
	}
	belief := func(id string, ring, evidence, seq int) {
		t.Helper()
		exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES (?, ?, ?, 0.8, ?, ?, ?)`, id, "the statement of "+id, ring, evidence, seq, seq)
	}
	edge := func(id, from, to string) {
		t.Helper()
		exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES (?, ?, ?, 'SUPPORTS', 5)`, id, from, to)
	}
	belief("b_confirmed", 3, 3, 1)
	edge("s1", "e1", "b_confirmed")
	edge("s2", "e2", "b_confirmed")
	edge("s3", "e3", "b_confirmed")
	belief("b_one", 3, 1, 1)
	edge("s4", "e4", "b_one")
	belief("v_value", 2, 0, 1)
	for _, b := range []struct{ id, rests string }{{"b_for_value", "v_value"}, {"b_for_standing", "b_confirmed"}, {"b_for_weak", "b_one"}} {
		belief(b.id, 3, 1, 98)
		edge("s_"+b.id, "e5", b.id)
		edge("r_"+b.id, b.id, b.rests)
	}

	standing, err := s.StandingFor("b_confirmed")
	if err != nil || standing != "confirmed" {
		t.Fatalf("rig: b_confirmed must stand: %q %v", standing, err)
	}
	items, err := New(s).Attention(context.Background(), now, attention.CostLow)
	if err != nil {
		t.Fatal(err)
	}
	alerts := map[string]string{}
	for _, it := range items {
		if it.Kind == attention.AttentionDecayAlert {
			alerts[it.ID] = it.Text
		}
	}
	for _, id := range []string{"b_confirmed", "b_for_value", "b_for_standing"} {
		if text, ok := alerts[id]; ok {
			t.Errorf("%s is called fading: %s", id, text)
		}
	}
	for _, id := range []string{"b_one", "b_for_weak"} {
		if text, ok := alerts[id]; !ok || !strings.Contains(text, "three distinct") {
			t.Errorf("%s is not an alert that names what would change it: %q", id, text)
		}
	}

	// The page and recall read the same footing: the Core belief's
	// strength is Core's.
	footings, err := s.BeliefFootings(context.Background(), []string{"b_for_value", "b_for_weak", "b_confirmed"})
	if err != nil {
		t.Fatal(err)
	}
	if !footings["b_for_value"].Core || footings["b_for_weak"].Core || !footings["b_confirmed"].Stands {
		t.Fatalf("footings: %+v", footings)
	}
	born := now.Add(-120 * 24 * time.Hour)
	if r := BeliefRecall(RecallFacts{Ring: 3, EvidenceCount: 1, Born: born, Footing: footings["b_for_value"]}, now); r.Strength == nil || *r.Strength < 0.75 || r.Fading {
		t.Fatalf("a Core belief four months unrecalled reads %+v; want about 0.80 and not fading", r)
	}

	// No class floor makes the alert inevitable for a belief that stands:
	// at any age, in any class, a standing belief is not fading.
	for _, f := range []RecallFacts{{Ring: 3, EvidenceCount: 0}, {Ring: 3, EvidenceCount: 3}} {
		for _, core := range []bool{false, true} {
			f.Born = now.Add(-100 * 365 * 24 * time.Hour)
			f.Footing.Stands, f.Footing.Core = true, core
			if r := BeliefRecall(f, now); r.Fading {
				t.Fatalf("a belief that stands is fading at a hundred years (%+v): %+v", f, r)
			}
		}
	}
}

// THE DREAM-ONLY CASE AT THE ALERT (the identity, 2026-11-08): a belief whose only
// support beyond one experience is two dreams' notes of that same
// experience has three sources and one origin. It does not stand,
// and a year unrecalled it is still an alert.
func TestABeliefPropUpByDreamsOfOneExperienceIsStillAnAlert(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 8, now.Add(-400*24*time.Hour))
	at := now.Add(-300 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	addExperience(t, s, "e1", "an observation", 2, now.Add(-300*24*time.Hour), false)
	for i, note := range []string{"d1", "d2"} {
		seq := 20 + 2*i
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, 'experience.create', 3, '{}', '')`, seq, at)
		exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES (?, 'e1, as a dream put it', 'observation', 1, 0, 'dream', ?, ?)`, note, seq, at)
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, 'dream.run', 3, ?, '')`, seq+1, at, fmt.Sprintf(`{"inputs":["e1"],"outputs":[%d]}`, seq))
	}
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_dreamt', 'what the dreams say', 3, 0.8, 3, 1, 1)`)
	for _, from := range []string{"e1", "d1", "d2"} {
		exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES (?, ?, 'b_dreamt', 'SUPPORTS', 5)`, "s_"+from, from)
	}
	if got, err := s.StandingFor("b_dreamt"); err != nil || got != "new" {
		t.Fatalf("rig: three sources on one origin must not stand: %q %v", got, err)
	}
	items, err := New(s).Attention(context.Background(), now, attention.CostLow)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Kind == attention.AttentionDecayAlert && it.ID == "b_dreamt" && strings.Contains(it.Text, "three distinct") {
			return
		}
	}
	t.Fatalf("a belief propped up by dreams of one experience is not an alert: %+v", items)
}

// ONE CLASS AND ONE STRENGTH FOR EVERY READER (a review, 2026-11-08, D5; the identity,
// as arbiter: the footing decides Core). Ranked recall and BeliefRecall,
// which the alert, the listing and the trail call, give the same class and
// strength for the same belief: zero and some evidence, cited by a belief
// that stands, by one that does not, by an adopted value, by a retired
// belief, and a ring-2 belief.
func TestRankedRecallAndBeliefRecallGiveOneStrength(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 8, now.Add(-120*24*time.Hour))
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	for i, id := range []string{"e1", "e2", "e3", "e4"} {
		addExperience(t, s, id, "an observation "+id, 2+i%3, now.Add(-100*24*time.Hour), false)
	}
	belief := func(id string, ring, evidence int, archived bool) {
		t.Helper()
		a := 0
		if archived {
			a = 1
		}
		exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq, archived) VALUES (?, ?, ?, 0.8, ?, 1, 1, ?)`, id, "lighthouse "+id, ring, evidence, a)
	}
	edge := func(from, to string) {
		exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES (?, ?, ?, 'SUPPORTS', 5)`, "s_"+from+"_"+to, from, to)
	}
	belief("b_standing", 3, 3, false)
	edge("e1", "b_standing")
	edge("e2", "b_standing")
	edge("e3", "b_standing")
	belief("b_weak", 3, 1, false)
	edge("e4", "b_weak")
	belief("v_value", 2, 0, false)
	belief("b_retired", 3, 0, true)
	cases := map[string]carrd.Class{
		"b_none":         carrd.Operational,
		"b_some":         carrd.Standard,
		"b_for_standing": carrd.Core,
		"b_for_weak":     carrd.Standard,
		"b_for_value":    carrd.Core,
		"b_for_retired":  carrd.Standard,
		"v_value":        carrd.Constitutional,
	}
	belief("b_none", 3, 0, false)
	for id, rests := range map[string]string{"b_some": "", "b_for_standing": "b_standing", "b_for_weak": "b_weak", "b_for_value": "v_value", "b_for_retired": "b_retired"} {
		belief(id, 3, 1, false)
		edge("e4", id)
		if rests != "" {
			edge(id, rests)
		}
	}
	res, err := New(s).Recall(context.Background(), Query{Text: "lighthouse", Stores: []string{"beliefs"}, WordsOnly: true, Limit: MaxLimit, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(cases))
	for id := range cases {
		ids = append(ids, id)
	}
	footings, err := s.BeliefFootings(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, h := range res.Hits {
		want, ok := cases[h.ID]
		if !ok {
			continue
		}
		seen++
		var ring, evidence int
		if err := s.DB().QueryRow(`SELECT ring, evidence_count FROM beliefs WHERE id = ?`, h.ID).Scan(&ring, &evidence); err != nil {
			t.Fatal(err)
		}
		r := BeliefRecall(RecallFacts{Ring: ring, EvidenceCount: evidence, Born: h.Time, Footing: footings[h.ID]}, now)
		if h.Class != want || r.Strength == nil || *r.Strength != h.Strength {
			t.Errorf("%s: ranked recall %s %.4f, BeliefRecall %v; want class %s and one strength", h.ID, h.Class, h.Strength, r.Strength, want)
		}
	}
	if seen != len(cases) {
		t.Fatalf("ranked recall answered %d of the %d beliefs: %+v", seen, len(cases), res.Hits)
	}
}

// A SUSPECT BELIEF IS THE TENSION ITEM'S, NOT THE FADING ALERT'S (a review and
// the identity, 2026-11-08, P2). A year unrecalled, b_contested rests on three
// origins and a current belief contradicts it: the tension is shown, and
// no alert tells it to find evidence that cannot end a contradiction.
// b_short, on one origin with no contradiction, keeps its alert.
func TestASuspectBeliefIsATensionAndNotAFadingAlert(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 8, now.Add(-400*24*time.Hour))
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	for i, id := range []string{"e1", "e2", "e3", "e4"} {
		addExperience(t, s, id, "an observation "+id, 2+i%3, now.Add(-300*24*time.Hour), false)
	}
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_contested', 'the harbour light is green', 3, 0.8, 3, 1, 1)`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_rival', 'the harbour light is red', 3, 0.8, 0, 7, 7)`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_short', 'the ferry is late', 3, 0.8, 1, 1, 1)`)
	for _, from := range []string{"e1", "e2", "e3"} {
		exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES (?, ?, 'b_contested', 'SUPPORTS', 5)`, "s_"+from, from)
	}
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('s_short', 'e4', 'b_short', 'SUPPORTS', 5)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('c', 'b_rival', 'b_contested', 'CONTRADICTS', 6)`)
	f := New(s)
	low, err := f.Attention(context.Background(), now, attention.CostLow)
	if err != nil {
		t.Fatal(err)
	}
	alerted := map[string]bool{}
	lowTension := false
	for _, it := range low {
		if it.Kind == attention.AttentionDecayAlert {
			alerted[it.ID] = true
		}
		lowTension = lowTension || (it.Kind == attention.AttentionContradiction && strings.Contains(it.Text, "the harbour light is green"))
	}
	if alerted["b_contested"] || !alerted["b_short"] {
		t.Fatalf("fading alerts %v: want b_short alone; a suspect belief is the tension item's", alerted)
	}
	// Where its alert would have been said, its contradiction is (the identity,
	// 2026-11-08): the morning brief reads the low-cost items only.
	if !lowTension {
		t.Fatalf("the suspect belief's contradiction is not among the low-cost items, where its alert was: %+v", low)
	}
	medium, err := f.Attention(context.Background(), now, attention.CostMedium)
	if err != nil {
		t.Fatal(err)
	}
	tension := false
	for _, it := range medium {
		tension = tension || (it.Kind == attention.AttentionContradiction && strings.Contains(it.Text, "the harbour light is green"))
	}
	if !tension {
		t.Fatalf("the contradiction is not shown as a tension: %+v", medium)
	}
}
