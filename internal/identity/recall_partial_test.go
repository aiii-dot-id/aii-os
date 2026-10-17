package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/vec"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestAMeaningReadStoppedByItsBoundSaysSoInTheAnswer(t *testing.T) {
	e, st, _, _ := newCurrencyFixture(t)
	_, vs, err := currencyEmbedder{}.Embed(context.Background(), memory.KindMemory, []string{"boat"})
	if err != nil {
		t.Fatal(err)
	}
	q, scale := vec.Quantize(vs[0])
	tx, err := st.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	at := time.Now().UTC().Format(time.RFC3339Nano)
	var vectors []store.MemoryVector
	for i := range memory.MeaningReadBound {
		id := fmt.Sprintf("stale-%d", i)
		if _, err := tx.Exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES (?, ?, 'observation', 1, 0, 'self', 1, ?)`, id, fmt.Sprintf("an entry rewritten since, the %dth", i), at); err != nil {
			t.Fatal(err)
		}
		vectors = append(vectors, store.MemoryVector{Store: "experiences", ID: id, Basis: "currency/v1", ContentSHA: strings.Repeat("0", 64), Dims: len(q), Scale: scale, Q: q})
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := st.PutMemoryVectors(vectors); err != nil {
		t.Fatal(err)
	}
	out := mustVerb(t, e, "recall", map[string]any{"query": "boat"})
	if !strings.Contains(out, "Meaning layer: consulted in part (basis currency/v1, floor 0.600)") || !strings.Contains(out, "read no further") {
		t.Fatalf("a meaning read stopped by its bound must say so:\n%s", out)
	}
	if strings.Contains(out, "no memory scored at or above the floor") {
		t.Fatalf("a meaning read stopped by its bound claimed nothing scored:\n%s", out)
	}
}
