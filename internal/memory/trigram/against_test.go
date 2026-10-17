package trigram

import (
	"math/rand"
	"strings"
	"testing"
)

func jaccard(a, b string) float64 {
	sa, sb := trigramSet(a), trigramSet(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0
	}
	shared := 0
	for t := range sa {
		if _, ok := sb[t]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(sa)+len(sb)-shared)
}

func statements(n int) []string {
	r := rand.New(rand.NewSource(44))
	words := strings.Fields("the operator prefers concise answers evidence cited inline memory recall ledger belief tension a an of in on for with é ü 東京 x yy zzz 42")
	out := make([]string, n)
	for i := range out {
		var b strings.Builder
		for w := r.Intn(14); w >= 0; w-- {
			b.WriteString(words[r.Intn(len(words))])
			b.WriteByte(" ,.-"[r.Intn(4)])
		}
		out[i] = b.String()
	}
	return append(out, "", "   ", "a", "!!")
}

// A HELD SIDE ANSWERS AS BOTH SIDES DID. One statement held against many,
// the same held function called again and again, the empty and the
// punctuation-only statements: every score equals the reference, both ways
// round.
func TestAgainstAgreesWithTheJaccardOfBothSets(t *testing.T) {
	all := statements(150)
	for _, a := range all {
		like := Against(a)
		for _, b := range all {
			if got, want := like(b), jaccard(a, b); got != want {
				t.Fatalf("Against(%q)(%q) = %v, want %v", a, b, got, want)
			}
			if got, want := Similarity(b, a), jaccard(a, b); got != want {
				t.Fatalf("Similarity(%q, %q) = %v, want %v", b, a, got, want)
			}
		}
	}
}

// BenchmarkOneAgainstMany is admission's shape: one statement compared
// with 200 active beliefs.
func BenchmarkOneAgainstMany(b *testing.B) {
	beliefs := statements(200)
	stmt := "the operator prefers concise answers with evidence cited inline"
	b.Run("both-sets-each-time", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, other := range beliefs {
				_ = jaccard(other, stmt)
			}
		}
	})
	b.Run("held", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			like := Against(stmt)
			for _, other := range beliefs {
				_ = like(other)
			}
		}
	})
}
