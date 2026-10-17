package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestAMarkedNotesWordsReachEveryOperatorSurface(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "OperatorReads",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://x", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer stopApp(t, app)
	h := app.buildLiveHandler()

	// One note by the verb, as a caller that still sends the argument does;
	// one by a record shaped as an earlier release wrote it.
	const byVerb = "QUILLFEATHER the harbour ledger was balanced by lamplight"
	const byRecord = "MARROWGATE the orchard key hangs behind the blue door"
	if _, err := app.engine.ExecuteAction(t.Context(), "verb", "note", map[string]any{"content": byVerb, "private": true}); err != nil {
		t.Fatalf("the note verb, sent private = true: %v", err)
	}
	if _, err := app.door.Append(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp_marked_by_record", "content": byRecord, "category": "reflection", "provenance": "self", "private": true,
	}, ""); err != nil {
		t.Fatalf("a record carrying private = true: %v", err)
	}
	notes := map[string]string{"the verb's": byVerb, "the record's": byRecord}

	// THE IDENTITY PAGE lists both, and counts nothing in place of a note.
	st, err := h.GetIdentity()
	if err != nil {
		t.Fatal(err)
	}
	page, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for which, words := range notes {
		if !strings.Contains(string(page), words) {
			t.Errorf("THE IDENTITY PAGE LEAVES OUT %s note: the operator is not shown %q", which, words)
		}
	}
	if regexp.MustCompile(`"private_count":\s*[1-9]`).Match(page) {
		t.Errorf("the identity page counts notes it does not show: %s", regexp.MustCompile(`"private_count":\s*\d+`).Find(page))
	}

	// THE MEMORY PAGE'S SEARCH finds each by a word of its own.
	for which, words := range notes {
		cue := strings.Fields(words)[0]
		out, err := h.Recall(cue)
		if err != nil {
			t.Fatalf("the Memory page's search for %q: %v", cue, err)
		}
		if !strings.Contains(out, words) {
			t.Errorf("THE MEMORY PAGE'S SEARCH PASSES OVER %s note: %q is not in the answer for %q:\n%s", which, words, cue, out)
		}
	}

	for which, words := range notes {
		cue := strings.Fields(words)[0]
		out, err := app.engine.ExecuteAction(t.Context(), "verb", "recall", map[string]any{"query": cue})
		if err != nil {
			t.Fatalf("the identity's recall for %q: %v", cue, err)
		}
		if !strings.Contains(out, words) {
			t.Errorf("THE IDENTITY'S OWN RECALL PASSES OVER %s note for %q: the mark keeps a note out of DREAM and CONSOLIDATE only:\n%s", which, cue, out)
		}
	}

	exps, err := app.store.ListExperiences(50)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range exps {
		for which, words := range notes {
			if e.Content == words {
				ids[which] = e.ID
			}
		}
	}
	if len(ids) != len(notes) {
		t.Fatalf("the store holds %d of the two notes: %v", len(ids), ids)
	}
	mint := func(et ledger.EventType, payload map[string]any) {
		t.Helper()
		if _, err := app.door.Append(et, 3, payload, ""); err != nil {
			t.Fatalf("mint %s: %v", et, err)
		}
	}
	mint(ledger.EventBeliefUpsert, map[string]any{"id": "b_contested", "statement": "The harbour keeps no ledger", "ring": 3, "confidence": 0.5,
		"origin_refs": []map[string]string{{"kind": "experience", "id": ids["the verb's"]}}})
	n := 0
	for _, id := range ids {
		n++
		mint(ledger.EventEdgeCreate, map[string]any{"id": "edge_marked_" + string(rune('0'+n)), "from_id": id, "to_id": "b_contested", "edge_type": "CONTRADICTS"})
	}
	review := app.cognition.reviewFacility.Load()
	if review == nil {
		t.Fatal("the live identity has no review facility")
	}
	if err := review.Execute(context.Background()); err != nil {
		t.Fatalf("the review: %v", err)
	}
	// What the operator's page is given: each finding whole, beside the
	// count that used to stand alone.
	cont, err := h.GetContinuity()
	if err != nil {
		t.Fatal(err)
	}
	if cont.ReviewIssues == 0 || len(cont.ReviewLines) != cont.ReviewIssues {
		t.Fatalf("the page is given %d findings and %d lines: a count without its words", cont.ReviewIssues, len(cont.ReviewLines))
	}
	lines := strings.Join(cont.ReviewLines, "\n")
	for which, words := range notes {
		if !strings.Contains(lines, words[:40]) {
			t.Errorf("THE REVIEW'S LINE WITHHOLDS %s note's words (%q):\n%s", which, words[:40], lines)
		}
	}
	if strings.Contains(lines, "sealed") || strings.Contains(lines, "not shown") {
		t.Errorf("the review tells the operator that something is not shown to them:\n%s", lines)
	}
}
