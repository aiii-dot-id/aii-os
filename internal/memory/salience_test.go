package memory

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestSalienceIsPureAndClassifies(t *testing.T) {
	strong := SalienceFeatures{Impact: 0.9, Novelty: 0.8, Recurrence: 0.7, ProvenanceTrust: 0.6, PersistenceHint: 0.7, EmotionalSalience: 0.5, DownstreamUse: 0.5, CostToStore: 0.2, CostToQuery: 0.1}
	d1 := Salience(strong, DefaultSalience)
	d2 := Salience(strong, DefaultSalience)
	if d1.AuditHash != d2.AuditHash || d1.Score != d2.Score || d1.Class != d2.Class {
		t.Fatalf("the filter must be deterministic: %+v vs %+v", d1, d2)
	}
	if d1.Class != SalienceBelief || d1.ReviewAfter != 82 || d1.Policy != "salience-1" {
		t.Fatalf("strong evidence with impact is a belief: %+v", d1)
	}
	if len(d1.Explanations) == 0 || !strings.Contains(strings.Join(d1.Explanations, ";"), "impact") {
		t.Fatalf("a decision explains itself: %+v", d1.Explanations)
	}
	weak := SalienceFeatures{Impact: 0.2, Novelty: 0.3, Recurrence: 0.1, EmotionalSalience: 0.5, CostToStore: 0.9, CostToQuery: 0.8}
	if d := Salience(weak, DefaultSalience); d.Class != SalienceMemo || d.TTLDays != 21 {
		t.Fatalf("weak, costly material is a memo: %+v", d)
	}
	full := SalienceFeatures{Impact: 1, Novelty: 1, Recurrence: 1, TensionDelta: 1, ProvenanceTrust: 1, PersistenceHint: 1, EmotionalSalience: 1, DownstreamUse: 1}
	if d := Salience(full, DefaultSalience); d.Class != SalienceValue || !strings.Contains(strings.Join(d.Explanations, ";"), "never as a value") {
		t.Fatalf("everything at once scores as a value, which the unconscious never mints: %+v", d)
	}
	// .
	if d := Salience(SalienceFeatures{Impact: 7}, DefaultSalience); d.Score > 0.3 {
		t.Fatalf("an impact of 7 is 1: %+v", d)
	}
	// .
	strict := DefaultSalience
	strict.BeliefAt, strict.Version = 0.9, "strict-test"
	if d := Salience(strong, strict); d.Class != SalienceMemo || d.Policy != "strict-test" || d.AuditHash == d1.AuditHash {
		t.Fatalf("a stricter policy makes a memo of the same material: %+v", d)
	}
}

func TestSalienceCosts(t *testing.T) {
	if c := CostToQuery("it is what it is"); c < 0.99 {
		t.Fatalf("stopwords alone cost %v", c)
	}
	if c := CostToQuery("Harbourmaster charts shoals nightly"); c != 0 {
		t.Fatalf("distinct words cost %v", c)
	}
	if c := CostToQuery(""); c != 1 {
		t.Fatalf("nothing to find costs %v", c)
	}
	if c := CostToStore(strings.Repeat("x", 200), 400); c != 0.5 {
		t.Fatalf("half the bound costs %v", c)
	}
	if c := CostToStore(strings.Repeat("x", 900), 400); c != 1 {
		t.Fatalf("past the bound costs %v", c)
	}
}
