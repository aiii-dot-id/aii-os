package memory

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestARowWithNoTextIsNeverHandedToASourceAndIsNotCountedAsWaiting(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 5, now.Add(-48*time.Hour))
	addExperience(t, s, "e001", "a memory of a harbor", 1, now.Add(-5*time.Hour), false)
	addExperience(t, s, "e002", "", 2, now.Add(-4*time.Hour), false)
	addExperience(t, s, "e003", " \t\r\n ", 3, now.Add(-3*time.Hour), false)
	addExperience(t, s, "e004", "\u00a0\u3000\u2003", 4, now.Add(-2*time.Hour), false)
	addExperience(t, s, "e005", "\u00a0a memory of a harbor\u3000", 5, now.Add(-time.Hour), false)
	// A row of several texts has text when ONE of them has: an intention
	// that says what it is and not yet why.
	if _, err := s.DB().Exec(`INSERT INTO intentions (id, statement, why, state, created_seq, updated_seq) VALUES ('i001', 'a memory of a harbor', ' ', 'active', 5, 5)`); err != nil {
		t.Fatal(err)
	}
	f := New(s)
	src := &asideSource{basis: "org.example.vectors/abababababab/f1", batch: 1, epoch: 1}
	f.SetEmbedder(src)

	rep, err := f.Backfill(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, asked := range src.asked {
		if strings.TrimSpace(asked) == "" {
			t.Fatalf("the source was handed a text with nothing in it: %q of %q", asked, src.asked)
		}
	}
	if rep.Embedded != 3 || len(src.asked) != 3 {
		t.Fatalf("three rows say something: %d embedded in %d calls (%q)", rep.Embedded, len(src.asked), src.asked)
	}
	if rep.SetAside != 0 {
		t.Fatalf("a row with no text is not one the source could not embed: %d set aside", rep.SetAside)
	}
	if remaining(rep) != 0 {
		t.Fatalf("a row with no text waits for nothing: the pass says %d are still waiting", remaining(rep))
	}
	// And the next pass has nothing to do: it asks the source nothing.
	if rep, err = f.Backfill(context.Background(), 10); err != nil || rep.Embedded != 0 || len(src.asked) != 3 {
		t.Fatalf("the next pass found something to hand over: %+v %v (%q)", rep, err, src.asked)
	}

	basis, cov, unavailable, err := f.HeldCoverage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	line := RenderCoverage(basis, cov, unavailable)
	for _, c := range cov {
		if c.Embedded != c.Rows {
			t.Fatalf("%s: %d of %d rows hold a vector after a pass that left nothing waiting: %s", c.Store, c.Embedded, c.Rows, line)
		}
	}
	if !strings.Contains(line, ", complete (") {
		t.Fatalf("the measure read must say the layer is complete: %s", line)
	}
}

// THE QUERY AND THE TEXT MEAN THE SAME WHITE SPACE. The text a source is
// handed is trimmed by strings.TrimSpace; the query that leaves out rows
// with no text trims in SQL. Every character the one removes, the other
// must: a row the query kept and the trim emptied would be handed over as
// the empty string again.
func TestTheQuerysWhiteSpaceIsTheTextsWhiteSpace(t *testing.T) {
	s := newStore(t)
	var all strings.Builder
	n := 0
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.IsSpace(r) {
			all.WriteRune(r)
			n++
		}
	}
	if n < 20 {
		t.Fatalf("only %d white-space characters were found, so this proves little", n)
	}
	var left string
	if err := s.DB().QueryRow("SELECT TRIM(?, "+sqlWhiteSpace+")", all.String()).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != "" {
		t.Fatalf("the query's trim leaves %q of the %d characters TrimSpace removes", left, n)
	}
	// And it removes nothing else: a letter between two of them stays.
	if err := s.DB().QueryRow("SELECT TRIM(?, "+sqlWhiteSpace+")", "\u00a0x\u3000").Scan(&left); err != nil || left != "x" {
		t.Fatalf("the query's trim of a letter between white space gave %q, %v", left, err)
	}
}
