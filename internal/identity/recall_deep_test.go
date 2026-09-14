package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .

// .
func TestRecallFindsAMatchBeyondTheFirstPage(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()

	// .
	if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]interface{}{
		"content": "the kingfisher struck the water at dawn",
	}); err != nil {
		t.Fatal(err)
	}
	// .
	for i := 0; i < recallExperiencePage+5; i++ {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]interface{}{
			"content": fmt.Sprintf("routine observation number %d, nothing notable", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]interface{}{"query": "kingfisher"})
	if err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if !strings.Contains(sectionOf(out, "Experiences:"), "kingfisher") {
		t.Errorf("the experiences source did not find a match it holds — the only match is older than its first page:\n%s", out)
	}
	if strings.Contains(out, "No literal substring match") {
		t.Error("recall reported a definitive no-match about content it stores")
	}
}

// .
// .
// .
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

// .
// .
func TestRecallScopesItsNegativeToWhatItSearched(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]interface{}{
			"content": fmt.Sprintf("ordinary note %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall",
		map[string]interface{}{"query": "zzz-nothing-matches-this"})
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

// .
// .
func TestRecallDisclosesAPagedSourceAsIncomplete(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	for i := 0; i < recallExperiencePage+3; i++ {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]interface{}{
			"content": fmt.Sprintf("shared token alpha, item %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]interface{}{"query": "alpha"})
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

// .
// .
// .
// .
func TestACursorWithoutItsSourceIsRefused(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	_, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "anything", "after_seq": float64(12)})
	if err == nil {
		t.Fatal("a cursor with no source was accepted — it means a different position in each source")
	}
	if !strings.Contains(err.Error(), "source") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}

// .
func TestANamedSourceReadsAlone(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "note", map[string]interface{}{
		"content": "heron on the weir",
	}); err != nil {
		t.Fatal(err)
	}
	out, err := engine.ExecuteAction(ctx, "verb", "recall",
		map[string]interface{}{"query": "heron", "source": "experiences"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Experiences:") {
		t.Errorf("the named source did not answer:\n%s", out)
	}
	// .
	// .
	// .
	if strings.Contains(out, "Ledger Events:") {
		t.Errorf("source=experiences also returned ledger events — a named source must read alone:\n%s", out)
	}
}

// .
func TestAnUnknownRecallSourceIsRefused(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "recall",
		map[string]interface{}{"query": "x", "source": "dreams"}); err == nil {
		t.Fatal("an unknown source was accepted and silently read as all sources")
	}
}
