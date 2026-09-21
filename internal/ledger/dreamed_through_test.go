package ledger

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// .
// .
func TestTheDreamedThroughGrammarHoldsEveryVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/dreamed_through_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Vectors []struct {
			Name    string          `json:"name"`
			Payload json.RawMessage `json:"payload"`
			Valid   bool            `json:"valid"`
			Present bool            `json:"present"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) < 12 {
		t.Fatalf("only %d vectors read — the file did not parse as the grammar's", len(file.Vectors))
	}
	for _, v := range file.Vectors {
		got, err := ParseDreamedThrough(v.Payload)
		switch {
		case v.Valid && err != nil:
			t.Errorf("%s: refused: %v", v.Name, err)
		case !v.Valid && err == nil:
			t.Errorf("%s: admitted as %+v", v.Name, got)
		case !v.Valid && !errors.Is(err, ErrDreamedThrough):
			t.Errorf("%s: refused with %v, want ErrDreamedThrough", v.Name, err)
		case v.Valid && (got != nil) != v.Present:
			t.Errorf("%s: present=%v, want %v", v.Name, got != nil, v.Present)
		}
	}
	if !DreamedThroughAllowed(EventExperienceCreate) || DreamedThroughAllowed(EventBeliefUpsert) {
		t.Error("dreamed_through is carried by experience.create and by nothing else")
	}
}
