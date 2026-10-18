package identity

import (
	"context"
	"strings"
	"testing"
)

func TestRecallHeardIsExplicitAndReadOnly(t *testing.T) {
	e, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := e.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "heard", "query": ""}); err == nil {
		t.Fatal("unwired history silently accepted")
	}
	calls := 0
	e.heardHistory = func(query string, after uint64, limit int) (string, error) {
		calls++
		if query != "track-a" || after != 7 || limit != 3 {
			t.Fatalf("arguments lost: %s %d %d", query, after, limit)
		}
		return "untrusted context", nil
	}
	out, err := e.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "heard", "query": "track-a", "after_seq": 7, "limit": 3})
	if err != nil || out != "untrusted context" || calls != 1 {
		t.Fatalf("explicit history: %s %v", out, err)
	}
	_, _ = e.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": "track-a"})
	if calls != 1 {
		t.Fatal("ordinary recall silently read withheld speech")
	}
	for _, decay := range []string{"none", ""} {
		out, err = e.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "heard", "query": "track-a", "after_seq": 7, "limit": 3, "exact": false, "decay": decay})
		if err != nil || out != "untrusted context" {
			t.Fatalf("neutral policy refused: %s %v", decay, err)
		}
	}
	for key, value := range map[string]any{"exact": true, "decay": "carrd"} {
		_, err = e.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "heard", "query": "track-a", key: value})
		if err == nil || !strings.Contains(err.Error(), "omit") {
			t.Fatalf("unsupported semantics silently accepted: %v", err)
		}
	}
	if _, err = e.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "heard", "decay": "actr"}); err == nil {
		t.Fatal("ACT-R ranking silently accepted")
	}
}

// A heard read that names no limit leaves the page to the host, which reads
// its operator's speech.heard_history.page_rows (VO-1); a limit named is
// passed as named.
func TestRecallHeardLeavesTheDefaultPageToTheHost(t *testing.T) {
	e, _, _, _, _ := setupEngine(t)
	got := -1
	e.heardHistory = func(_ string, _ uint64, limit int) (string, error) { got = limit; return "ok", nil }
	if _, err := e.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": "heard", "query": ""}); err != nil || got != 0 {
		t.Fatalf("no limit named reached the host as %d (%v); want 0, the host's page_rows", got, err)
	}
	if _, err := e.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": "heard", "query": "", "limit": 7}); err != nil || got != 7 {
		t.Fatalf("a limit of 7 reached the host as %d (%v)", got, err)
	}
}
