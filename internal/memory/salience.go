package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

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
const (
	SalienceMemo   = "memo"
	SalienceBelief = "belief"
	SalienceValue  = "value"
)

// .
type SalienceFeatures struct {
	Impact            float64 `json:"impact"`
	Novelty           float64 `json:"novelty"`
	Recurrence        float64 `json:"recurrence"`
	TensionDelta      float64 `json:"tension_delta"`
	ProvenanceTrust   float64 `json:"provenance_trust"`
	PersistenceHint   float64 `json:"persistence_hint"`
	EmotionalSalience float64 `json:"emotional_salience"`
	CostToStore       float64 `json:"cost_to_store"`
	CostToQuery       float64 `json:"cost_to_query"`
	DownstreamUse     float64 `json:"downstream_use"`
}

// .
// .
type SalienceWeights struct {
	Impact            float64 `json:"impact"`
	Novelty           float64 `json:"novelty"`
	Recurrence        float64 `json:"recurrence"`
	TensionDelta      float64 `json:"tension_delta"`
	ProvenanceTrust   float64 `json:"provenance_trust"`
	PersistenceHint   float64 `json:"persistence_hint"`
	EmotionalSalience float64 `json:"emotional_salience"`
	CostToStore       float64 `json:"cost_to_store"`
	CostToQuery       float64 `json:"cost_to_query"`
	DownstreamUse     float64 `json:"downstream_use"`
	// .
	// .
	// .
	BeliefAt float64 `json:"belief_at"`
	ValueAt  float64 `json:"value_at"`
	// .
	Version string `json:"version"`
}

// .
// .
// .
// .
// .
var DefaultSalience = SalienceWeights{
	Impact: 0.25, Novelty: 0.15, Recurrence: 0.20, TensionDelta: 0.05, ProvenanceTrust: 0.15,
	PersistenceHint: 0.05, EmotionalSalience: 0.05, CostToStore: -0.05, CostToQuery: -0.05, DownstreamUse: 0.10,
	BeliefAt: 0.30, ValueAt: 0.85, Version: "salience-1",
}

// .
type SalienceDecision struct {
	Class        string           `json:"class"`
	Score        float64          `json:"score"`
	TTLDays      int              `json:"ttl_days"`
	ReviewAfter  int              `json:"review_after"`
	Explanations []string         `json:"explanations"`
	Features     SalienceFeatures `json:"features"`
	Policy       string           `json:"policy"`
	AuditHash    string           `json:"audit_hash"`
}

// .
// .
func Salience(f SalienceFeatures, w SalienceWeights) SalienceDecision {
	clamp := func(v float64) float64 { return math.Max(0, math.Min(1, v)) }
	terms := []struct {
		name   string
		value  float64
		weight float64
	}{
		{"impact", clamp(f.Impact), w.Impact},
		{"novelty", clamp(f.Novelty), w.Novelty},
		{"recurrence", clamp(f.Recurrence), w.Recurrence},
		{"tension_delta", clamp(f.TensionDelta), w.TensionDelta},
		{"provenance_trust", clamp(f.ProvenanceTrust), w.ProvenanceTrust},
		{"persistence_hint", clamp(f.PersistenceHint), w.PersistenceHint},
		{"emotional_salience", clamp(f.EmotionalSalience), w.EmotionalSalience},
		{"cost_to_store", clamp(f.CostToStore), w.CostToStore},
		{"cost_to_query", clamp(f.CostToQuery), w.CostToQuery},
		{"downstream_use", clamp(f.DownstreamUse), w.DownstreamUse},
	}
	var score, positive float64
	var explanations []string
	for _, t := range terms {
		c := t.value * t.weight
		score += c
		if t.weight > 0 {
			positive += t.weight
		}
		if math.Abs(c) >= 0.05 {
			explanations = append(explanations, fmt.Sprintf("%s %.2f × %.2f = %+.3f", t.name, t.value, t.weight, c))
		}
	}
	if positive > 0 {
		score /= positive
	}
	score = math.Round(score*1000) / 1000
	sort.Strings(explanations)
	d := SalienceDecision{Score: score, Explanations: explanations, Features: f, Policy: w.Version}
	switch {
	case score >= w.ValueAt:
		d.Class = SalienceValue
		d.ReviewAfter = 365
		d.Explanations = append(d.Explanations, "scores as a value: needs provenance, recurrence or governance before it is one — minted as a belief by the unconscious, never as a value")
	case score >= w.BeliefAt:
		d.Class = SalienceBelief
		d.ReviewAfter = 82
	default:
		d.Class = SalienceMemo
		d.TTLDays = 21
	}
	d.AuditHash = salienceHash(f, w, d)
	return d
}

func salienceHash(f SalienceFeatures, w SalienceWeights, d SalienceDecision) string {
	b, _ := json.Marshal(struct {
		F SalienceFeatures `json:"f"`
		W SalienceWeights  `json:"w"`
		C string           `json:"class"`
		S float64          `json:"score"`
	}{f, w, d.Class, d.Score})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// .
// .
var salienceStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "to": true, "and": true, "or": true, "is": true, "in": true, "it": true, "its": true,
	"that": true, "this": true, "these": true, "those": true, "for": true, "on": true, "at": true, "by": true, "from": true, "with": true,
	"as": true, "be": true, "am": true, "are": true, "was": true, "were": true, "been": true, "being": true, "i": true, "my": true, "me": true,
	"we": true, "our": true, "you": true, "your": true, "he": true, "she": true, "his": true, "her": true, "they": true, "them": true, "their": true,
	"what": true, "which": true, "who": true, "whom": true, "how": true, "when": true, "where": true, "why": true, "not": true, "no": true,
	"but": true, "so": true, "if": true, "then": true, "than": true, "there": true, "here": true, "do": true, "does": true, "did": true,
	"have": true, "has": true, "had": true, "will": true, "would": true, "can": true, "could": true, "should": true, "may": true, "might": true,
}

// .
func CostToQuery(statement string) float64 {
	words := wordsOf(statement)
	if len(words) == 0 {
		return 1
	}
	stop := 0
	for _, w := range words {
		if salienceStopwords[strings.ToLower(w)] {
			stop++
		}
	}
	return float64(stop) / float64(len(words))
}

// .
// .
func CostToStore(statement string, bound int) float64 {
	if bound <= 0 {
		bound = 400
	}
	return math.Min(1, float64(len([]rune(statement)))/float64(bound))
}
