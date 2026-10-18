package memory

import (
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/memory/vec"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestOperatorActEligibilityHoldsBeforeEveryRecallLimit(t *testing.T) {
	s := newStore(t)
	packed, scale := vec.Quantize([]float32{1, 0})
	conversations, _ := Lookup("conversations")
	var vectors []store.MemoryVector
	var want string
	for i := 0; i < 10; i++ {
		actor := "main"
		if i == 9 {
			actor = "operator"
		}
		turn := fmt.Sprintf("act-%d", i)
		if err := s.RecordToolStart(turn, 1, actor, "", "", "probe", "{}"); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolDoneMeasured(turn, 1, "probe", "{}", "lighthouse harbor", interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.DB().QueryRow(`SELECT id FROM conversations WHERE kind='tool_result' AND turn_id=?`, turn).Scan(&id); err != nil {
			t.Fatal(err)
		}
		vectors = append(vectors, store.MemoryVector{Store: "conversations", ID: id, Basis: "test", ContentSHA: shaOfRow(t, s, conversations, id), Q: packed, Scale: scale, Dims: 2})
		if actor == "operator" {
			want = id
		}
	}
	if err := s.PutMemoryVectors(vectors); err != nil {
		t.Fatal(err)
	}
	d, _ := Lookup("conversations")
	q := Query{}
	exact, _, err := layerExact(t.Context(), s.DB(), d, q, `"lighthouse"`, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	fuzzy, err := layerFuzzy(t.Context(), s.DB(), d, q, "lighhouse", []string{"lighhouse"}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	meaning, _, err := layerMeaning(t.Context(), s.DB(), d, q, &queryVector{basis: "test", q: packed, scale: scale}, 1)
	if err != nil {
		t.Fatal(err)
	}
	listed, total, err := New(s).Enumerate(t.Context(), "conversations", "", false, 0, 1)
	if err != nil || total != 1 {
		t.Fatalf("enumeration widened: %d %v", total, err)
	}
	for name, hits := range map[string][]Hit{"exact": exact, "fuzzy": fuzzy, "meaning": meaning, "enumerated": listed} {
		if len(hits) != 1 || hits[0].ID != want || hits[0].Attribution != "tool" {
			t.Fatalf("%s lost scope/provenance before limit: %+v", name, hits)
		}
	}
}
