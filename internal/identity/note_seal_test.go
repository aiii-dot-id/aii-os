package identity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestAnUnmetabolizedNoteIsShownAndRecalled(t *testing.T) {
	engine, st, lg, kp, dir := setupEngine(t)
	ctx := context.Background()

	const byName, byOldName, byOldRecord = "a very hard day, by the argument's name", "a very hard day, by the argument's earlier name", "a very hard day, in a record an earlier program wrote"
	out, err := engine.ExecuteAction(ctx, "verb", "note", map[string]any{"content": byName, "unmetabolized": true})
	if err != nil || out != "Noted." {
		t.Fatalf("note unmetabolized=true: %q %v", out, err)
	}
	// The earlier name is given what it asked for, and the resident is
	// told what it is and is not: "Noted." alone would leave them
	// believing the note was hidden from someone.
	out, err = engine.ExecuteAction(ctx, "verb", "note", map[string]any{"content": byOldName, "private": true})
	if err != nil || out != notedUnderTheOldName {
		t.Fatalf("note private=true must be kept out of DREAM and CONSOLIDATE and said: %q %v", out, err)
	}
	// Byte for byte what the earlier verb appended.
	evt, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp_old_record", "content": byOldRecord, "category": "reflection", "private": true, "provenance": "self",
	}, kp)
	if err != nil {
		t.Fatalf("THE RECORD NO LONGER ADMITS AN EVENT AN EARLIER PROGRAM WROTE: %v", err)
	}
	if err := st.Materialize(evt); err != nil {
		t.Fatalf("the materializer refused an event an earlier program wrote: %v", err)
	}

	// This program writes the mark under its true name only.
	record, err := os.ReadFile(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(record)), "\n") {
		if strings.Contains(line, byName) || strings.Contains(line, byOldName) {
			if !strings.Contains(line, "unmetabolized") || strings.Contains(line, "private") {
				t.Fatalf("the verb's record must carry unmetabolized and never private: %s", line)
			}
		}
	}

	fresh, err := store.NewForTest(filepath.Join(dir, "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fresh.Close() })
	if err := fresh.ReplayFromFile(filepath.Join(dir, "ledger.jsonl")); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*store.Store{"live": st, "after replay": fresh} {
		raw, err := s.ListRawExperiences(10)
		if err != nil {
			t.Fatal(err)
		}
		forConsolidate, err := s.ListExperiencesForConsolidate(10)
		if err != nil {
			t.Fatal(err)
		}
		// Every other reader's lists hold it: the brief's and the portrait's.
		day, err := s.ListExperiencesSince(time.Time{}, 20)
		if err != nil {
			t.Fatal(err)
		}
		byWhose, err := s.ListExperiencesByProvenance([]string{"self"}, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, words := range []string{byName, byOldName, byOldRecord} {
			// LISTED, WITH ITS WORDS AND ITS MARK: what the operator's page reads.
			got := experienceByContent(t, s, words)
			if got.Raw != 0 || got.Unmetabolized != 1 {
				t.Fatalf("%s: %q is raw=%d unmetabolized=%d, want 0 and 1", name, words, got.Raw, got.Unmetabolized)
			}
			// In neither of the two reads DREAM and CONSOLIDATE make.
			for _, r := range append(raw, forConsolidate...) {
				if r.Content == words {
					t.Fatalf("%s: %q IS IN DREAM'S OR CONSOLIDATE'S READING", name, words)
				}
			}
			// And in every other: the mark reaches those two and nothing else.
			for reader, list := range map[string][]store.Experience{"the morning brief's day": day, "the portrait's rows by whose words": byWhose} {
				found := false
				for _, r := range list {
					found = found || r.Content == words
				}
				if !found {
					t.Fatalf("%s: THE MARK KEEPS %q OUT OF %s, which is not DREAM or CONSOLIDATE", name, words, reader)
				}
			}
		}
	}

	// RECALLED: the resident's conscious read, and the verb the operator's
	// own Memory search runs.
	out, err = engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "experiences"})
	if err != nil {
		t.Fatal(err)
	}
	for _, words := range []string{byName, byOldName, byOldRecord} {
		if !strings.Contains(out, words) {
			t.Fatalf("AN UNMETABOLIZED NOTE IS KEPT FROM RECALL (%q):\n%s", words, out)
		}
		// With its mark, where the line says whose words it is.
		if !strings.Contains(out, ", self, kept out of DREAM and CONSOLIDATE] "+words) {
			t.Fatalf("recall lists %q without the mark its row carries:\n%s", words, out)
		}
	}
	// On those and on the rig's seeded origin, which an earlier record
	// marked, and on no other line.
	if n := strings.Count(out, "kept out of DREAM and CONSOLIDATE]"); n != 4 {
		t.Fatalf("the mark is on %d lines, want 4:\n%s", n, out)
	}

	// The tool offers the argument under its true name and no other.
	for _, v := range verbRegistry {
		if v.Name != "note" {
			continue
		}
		props, _ := v.Params["properties"].(map[string]any)
		if props["unmetabolized"] == nil || props["private"] != nil {
			t.Fatalf("the note tool must offer unmetabolized and not private: %v %v", props["unmetabolized"], props["private"])
		}
	}
}

func experienceByContent(t *testing.T, st *store.Store, content string) store.Experience {
	t.Helper()
	exps, err := st.ListExperiences(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range exps {
		if e.Content == content {
			return e
		}
	}
	t.Fatalf("no experience with content %q", content)
	return store.Experience{}
}
