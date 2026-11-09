package identity

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// seedBeliefAt is seedBelief with the belief's own sequence, as the
// materializer records one belief an event: the listing pages by it.
func seedBeliefAt(t *testing.T, st *store.Store, n int, seq uint64, statement string) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq)
		VALUES (?, ?, 3, 0.9, 0, ?, ?)`, fmt.Sprintf("b%02d", n), statement, seq, seq); err != nil {
		t.Fatal(err)
	}
}

// A LIMIT THE CALL NAMES SIZES THE BELIEF LISTING (`recall source=beliefs
// query="" limit=3` once returned about sixty rows, cut only by the
// answer's room): three beliefs, newest first, the next page named by
// after_seq, and that page the remaining two, with distinct sequences;
// without a limit the listing is whole, as before.
func TestALimitSizesTheBeliefListing(t *testing.T) {
	engine, st, lg, _, _ := setupEngine(t)
	// Five beliefs, each at the sequence of an event of its own (the
	// belief row's first_seq is a ledger sequence).
	var seqs []uint64
	for i, s := range []string{"one", "two", "three", "four", "five"} {
		if _, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]any{"content": "a note before belief " + s}); err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, lg.LastSeq())
		seedBeliefAt(t, st, i+1, lg.LastSeq(), "belief "+s)
	}
	at := func(i int) string { return fmt.Sprintf("b%02d@%d", i+1, seqs[i]) }
	ids := func(out string) []string {
		var got []string
		for _, l := range strings.Split(out, "\n") {
			if m := regexp.MustCompile(`^  \[(b\d\d), seq (\d+),`).FindStringSubmatch(l); m != nil {
				got = append(got, m[1]+"@"+m[2])
			}
		}
		return got
	}
	first := recallOut(t, engine, map[string]any{"source": "beliefs", "query": "", "limit": 3})
	if got, want := strings.Join(ids(first), " "), at(4)+" "+at(3)+" "+at(2); got != want {
		t.Fatalf("limit=3 listed %q, want %q:\n%s", got, want, first)
	}
	m := regexp.MustCompile(`after_seq=(\d+)`).FindStringSubmatch(first)
	if m == nil {
		t.Fatalf("the cut listing does not name its next page:\n%s", first)
	}
	next, _ := strconv.ParseUint(m[1], 10, 64)
	second := recallOut(t, engine, map[string]any{"source": "beliefs", "query": "", "limit": 3, "after_seq": next})
	if got, want := strings.Join(ids(second), " "), at(1)+" "+at(0); got != want {
		t.Fatalf("the next page (after_seq=%d) listed %q, want %q:\n%s", next, got, want, second)
	}
	if got := ids(recallOut(t, engine, map[string]any{"source": "beliefs", "query": ""})); len(got) != 5 {
		t.Fatalf("without a limit the listing is not whole: %v", got)
	}
}
