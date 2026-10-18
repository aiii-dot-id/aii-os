package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// recall_bound_test.go — the recall verb is told the loop's bound on a
// tool result. The loop keeps the tail of a result over
// prompt.max_tool_result_chars, and a recall answer leads with its first
// line and its beliefs; so each boot hands the engine the configured
// bound, and a long answer fits under it instead of losing its front.

// seedLongBeliefs writes sixty beliefs that match "lighthouse", each long
// enough that fifty of them make an answer far over the bounds here.
func seedLongBeliefs(t *testing.T, st *store.Store) {
	t.Helper()
	for i := 1; i <= 60; i++ {
		if _, err := st.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq)
			VALUES (?, ?, 3, 0.9, 0, 1, 1)`, fmt.Sprintf("b%02d", i),
			fmt.Sprintf("the lighthouse number %02d stands", i)+strings.Repeat(" weatherproofed", 60)); err != nil {
			t.Fatal(err)
		}
	}
}

func requireRecallWithin(t *testing.T, a *App, bound int) {
	t.Helper()
	out, err := a.engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"query": "lighthouse", "limit": 50})
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(out); n > bound {
		t.Fatalf("the recall answer is %d characters, over the configured bound of %d: the loop would keep its tail and drop its first line", n, bound)
	}
	if !strings.HasPrefix(out, `Recall "lighthouse"`) {
		t.Fatalf("the answer must keep its first line:\n%.300s", out)
	}
}

func TestTheLiveBootsRecallFitsTheConfiguredToolResultBound(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "RecallBound")
	cfg := safebootConfig(t, dir, "RecallBound", keyPath, ledgerPath, dbPath)
	cfg.Prompt.MaxToolResultChars = 12000
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("LIVE boot: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("the fixture booted SAFE (%s)", reason)
	}
	seedLongBeliefs(t, a.store)
	requireRecallWithin(t, a, 12000)
}

// SAFE reads the record it had and writes nothing, so the reinforcement a
// recall makes fails there and is said in a warning; the warning is
// fitted under the bound with the rest.
func TestTheSafeBootsRecallFitsTheConfiguredToolResultBound(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "RecallBoundSafe")
	buildPriorProjection(t, ledgerPath, dbPath)
	st, err := store.NewForTest(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	seedLongBeliefs(t, st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	tamperChain(t, keyPath, ledgerPath)
	cfg := safebootConfig(t, dir, "RecallBoundSafe", keyPath, ledgerPath, dbPath)
	cfg.Prompt.MaxToolResultChars = 12000
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("SAFE boot: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("a tampered chain did not boot SAFE: the probe is not looking at the SAFE boot")
	}
	requireRecallWithin(t, a, 12000)
}
