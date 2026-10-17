package salience

import (
	"encoding/json"
	"strings"
	"testing"
)

// The salience filter is a pure function: the same features under the
// same policy give the same decision and hash; evidence and impact make
// a belief, their absence a memo, and a value is out of the
// unconscious's reach; the costs count against.
func TestSalienceIsPureAndClassifies(t *testing.T) {
	strong := SalienceFeatures{Impact: 0.9, Novelty: 0.8, Recurrence: 0.7, ProvenanceTrust: 0.6, PersistenceHint: 0.7, EmotionalSalience: 0.5, DownstreamUse: 0.5, CostToStore: 0.2, CostToQuery: 0.1}
	d1 := Salience(strong, DefaultSalience())
	d2 := Salience(strong, DefaultSalience())
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
	if d := Salience(weak, DefaultSalience()); d.Class != SalienceMemo || d.TTLDays != 21 {
		t.Fatalf("weak, costly material is a memo: %+v", d)
	}
	full := SalienceFeatures{Impact: 1, Novelty: 1, Recurrence: 1, TensionDelta: 1, ProvenanceTrust: 1, PersistenceHint: 1, EmotionalSalience: 1, DownstreamUse: 1}
	if d := Salience(full, DefaultSalience()); d.Class != SalienceValue || !strings.Contains(strings.Join(d.Explanations, ";"), "never as a value") {
		t.Fatalf("everything at once scores as a value, which the unconscious never mints: %+v", d)
	}
	// Out-of-range features are clamped, never trusted.
	if d := Salience(SalienceFeatures{Impact: 7}, DefaultSalience()); d.Score > 0.3 {
		t.Fatalf("an impact of 7 is 1: %+v", d)
	}
	// The policy's weights change the answer, and its version rides along.
	strict := DefaultSalience()
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

// Salience's JSON is a contract three ways: SalienceWeights is
// config.json's memory.salience, a decision's features and fields are
// what a memory_decisions row keeps, and the audit hash is computed over
// the JSON of the features and the policy. The literals below were
// written by this code while it was still package memory; a moved
// definition that changed one byte of any of
// them — a tag, a field order, the hash's input — fails here.
func TestSalienceKeepsItsConfiguredAndRecordedBytes(t *testing.T) {
	const weights = `{"impact":0.25,"novelty":0.15,"recurrence":0.2,"tension_delta":0.05,"provenance_trust":0.15,"persistence_hint":0.05,"emotional_salience":0.05,"cost_to_store":-0.05,"cost_to_query":-0.05,"downstream_use":0.1,"belief_at":0.3,"value_at":0.85,"version":"salience-1"}`
	if b, err := json.Marshal(DefaultSalience()); err != nil || string(b) != weights {
		t.Fatalf("salience-1 encodes as\n%s (%v), want\n%s", b, err, weights)
	}
	var configured SalienceWeights
	if err := json.Unmarshal([]byte(weights), &configured); err != nil || configured != DefaultSalience() {
		t.Fatalf("config.json's memory.salience decodes as %+v (%v), want salience-1", configured, err)
	}
	for _, c := range []struct {
		f    SalienceFeatures
		want string
	}{
		{SalienceFeatures{Impact: 0.9, Novelty: 0.8, Recurrence: 0.7, TensionDelta: 0.1, ProvenanceTrust: 0.6, PersistenceHint: 0.7, EmotionalSalience: 0.5, CostToStore: 0.2, CostToQuery: 0.1, DownstreamUse: 0.5},
			`{"class":"belief","score":0.675,"ttl_days":0,"review_after":82,"explanations":["downstream_use 0.50 × 0.10 = +0.050","impact 0.90 × 0.25 = +0.225","novelty 0.80 × 0.15 = +0.120","provenance_trust 0.60 × 0.15 = +0.090","recurrence 0.70 × 0.20 = +0.140"],"features":{"impact":0.9,"novelty":0.8,"recurrence":0.7,"tension_delta":0.1,"provenance_trust":0.6,"persistence_hint":0.7,"emotional_salience":0.5,"cost_to_store":0.2,"cost_to_query":0.1,"downstream_use":0.5},"policy":"salience-1","audit_hash":"e839fca1717f579c"}`},
		{SalienceFeatures{Impact: 0.2, Novelty: 0.3, Recurrence: 0.1, EmotionalSalience: 0.5, CostToStore: 0.9, CostToQuery: 0.8},
			`{"class":"memo","score":0.055,"ttl_days":21,"review_after":0,"explanations":["impact 0.20 × 0.25 = +0.050"],"features":{"impact":0.2,"novelty":0.3,"recurrence":0.1,"tension_delta":0,"provenance_trust":0,"persistence_hint":0,"emotional_salience":0.5,"cost_to_store":0.9,"cost_to_query":0.8,"downstream_use":0},"policy":"salience-1","audit_hash":"cb90b810c1773a55"}`},
	} {
		if b, err := json.Marshal(Salience(c.f, DefaultSalience())); err != nil || string(b) != c.want {
			t.Errorf("decision encodes as\n%s (%v), want\n%s", b, err, c.want)
		}
	}
	// The costs are features too, so their values reach the hash. Words
	// are runs of letters and digits; case does not make a stopword.
	for s, want := range map[string]float64{
		"":                                    1,
		"   ":                                 1,
		"it is what it is":                    1,
		"İt IS":                               1,
		"Harbourmaster charts shoals nightly": 0,
		"The THE tHe harbour":                 0.75,
		"Ünïcödé wörds and the Ålesund":       0.4,
		"it's 42 of 7 — and/or ... the":       0.625,
		"a\xffthe\xfeb":                       2.0 / 3,
		"日本語 the テスト":                         1.0 / 3,
	} {
		if got := CostToQuery(s); got != want {
			t.Errorf("CostToQuery(%q) = %v, want %v", s, got, want)
		}
	}
	if a, b, c := CostToStore("ünï", 400), CostToStore("abc", 0), CostToStore("abcdef", 4); a != 0.0075 || b != 0.0075 || c != 1 {
		t.Errorf("CostToStore counts code points against the bound (0 is 400): %v %v %v", a, b, c)
	}
}
