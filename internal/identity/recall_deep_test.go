package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The only matching experience is older than the whole first page.
func TestRecallFindsAMatchBeyondTheFirstPage(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()

	// The needle FIRST, so it is the oldest by created_seq.
	if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{
		"content": "the kingfisher struck the water at dawn",
	}); err != nil {
		t.Fatal(err)
	}
	// Then more than a full page of newer, non-matching experiences.
	for i := 0; i < recallExperiencePage+5; i++ {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{
			"content": fmt.Sprintf("routine observation number %d, nothing notable", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "kingfisher"})
	if err != nil {
		t.Fatal(err)
	}
	// ASSERT ON THE EXPERIENCES GROUP, not on the whole answer. recall
	// also searches the LEDGER MIRROR by payload substring, and a note's
	// payload carries its content — so "the needle appears somewhere in
	// the output" is true even when the experiences path never found it.
	// My first version of this test asserted exactly that and passed
	// against the page-then-filter mutation, which is how I learned the
	// difference between an answer containing the word and the SOURCE
	// having been searched.
	if !strings.Contains(sectionOf(out, "Experiences:"), "kingfisher") {
		t.Errorf("the experiences source did not find a match it holds — the only match is older than its first page:\n%s", out)
	}
	if strings.Contains(out, "No literal substring match") {
		t.Error("recall reported a definitive no-match about content it stores")
	}
}

// sectionOf returns the lines under a named group heading, up to the
// next heading. Group headings are the only unindented lines that end
// in a colon; items are indented two spaces.
func sectionOf(out, heading string) string {
	lines := strings.Split(out, "\n")
	var body []string
	in := false
	for _, ln := range lines {
		if ln == heading {
			in = true
			continue
		}
		if in {
			if !strings.HasPrefix(ln, "  ") {
				break
			}
			body = append(body, ln)
		}
	}
	return strings.Join(body, "\n")
}

// A query that genuinely matches nothing must say what it searched, not
// claim a flat negative over ground it never covered.
func TestRecallScopesItsNegativeToWhatItSearched(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{
			"content": fmt.Sprintf("ordinary note %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall",
		map[string]any{"query": "zzz-nothing-matches-this"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Coverage") {
		t.Errorf("a negative answer does not say what it covered:\n%s", out)
	}
	if !strings.Contains(out, "searched to completion") {
		t.Errorf("a small store was searched to the end and the answer does not say so:\n%s", out)
	}
}

// A source that filled its page is disclosed as possibly incomplete —
// the caller must be able to tell a complete search from a first page.
func TestRecallDisclosesAPagedSourceAsIncomplete(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	for i := 0; i < recallExperiencePage+3; i++ {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{
			"content": fmt.Sprintf("shared token alpha, item %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PAGED and possibly incomplete") {
		t.Errorf("a full page was presented without saying more may exist:\n%s", out)
	}
	if !strings.Contains(out, "after_seq") {
		t.Errorf("the incomplete source names no way to continue:\n%s", out)
	}
}

// A sequence number belongs to ONE source. after_seq used to be applied
// to experiences, syntheses and the ledger mirror at once — three
// independent sequence spaces — so one number advanced all three and
// could skip unseen content in the two it did not come from.
func TestACursorWithoutItsSourceIsRefused(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	_, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]any{"query": "anything", "after_seq": float64(12)})
	if err == nil {
		t.Fatal("a cursor with no source was accepted — it means a different position in each source")
	}
	if !strings.Contains(err.Error(), "source") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}

// A named source reads ALONE, so the seq it reports back is unambiguous.
func TestANamedSourceReadsAlone(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{
		"content": "heron on the weir",
	}); err != nil {
		t.Fatal(err)
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall",
		map[string]any{"query": "heron", "source": "experiences"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Experiences:") {
		t.Errorf("the named source did not answer:\n%s", out)
	}
	// The ledger mirror also matches "heron" (a note's payload carries
	// its content), so this is the assertion that proves the selector
	// actually scoped the read rather than merely being accepted.
	if strings.Contains(out, "Ledger Events:") {
		t.Errorf("source=experiences also returned ledger events — a named source must read alone:\n%s", out)
	}
}

// An unknown source is refused rather than silently read as "all".
func TestAnUnknownRecallSourceIsRefused(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]any{"query": "x", "source": "dreams"}); err == nil {
		t.Fatal("an unknown source was accepted and silently read as all sources")
	}
}
