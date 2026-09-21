package ledger

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// .
// .
func TestTheCitationGrammarHoldsEveryVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/cites_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MaxCitations int `json:"max_citations"`
		Vectors      []struct {
			Name    string          `json:"name"`
			Payload json.RawMessage `json:"payload"`
			Valid   bool            `json:"valid"`
			Count   int             `json:"count"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.MaxCitations != MaxCitations {
		t.Fatalf("the vectors say at most %d citations, the code %d", doc.MaxCitations, MaxCitations)
	}
	if len(doc.Vectors) < 20 {
		t.Fatalf("only %d vectors were read", len(doc.Vectors))
	}
	for _, v := range doc.Vectors {
		cites, err := ParseCitations(v.Payload)
		switch {
		case v.Valid && err != nil:
			t.Errorf("%s: refused: %v", v.Name, err)
		case v.Valid && len(cites) != v.Count:
			t.Errorf("%s: %d citations, want %d", v.Name, len(cites), v.Count)
		case !v.Valid && err == nil:
			t.Errorf("%s: ADMITTED: %+v", v.Name, cites)
		case !v.Valid && !errors.Is(err, ErrCitation):
			t.Errorf("%s: refused, but not as a citation error: %v", v.Name, err)
		}
	}
}

func TestOnlyThreeTypesCarryCitations(t *testing.T) {
	for _, et := range AllEventTypes() {
		want := et == EventExperienceCreate || et == EventBeliefUpsert || et == EventEdgeCreate
		if CitesAllowed(et) != want {
			t.Errorf("%s: CitesAllowed=%v", et, !want)
		}
	}
}
