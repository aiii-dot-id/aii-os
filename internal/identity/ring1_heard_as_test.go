package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestRing1ShowsWhatTheRecognizerHeardBesideACorrectedWord(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	say := func(text string) uint64 {
		t.Helper()
		ref, err := engine.RecordConversationRef(ctx, "operator", text, interaction.Details{})
		if err != nil {
			t.Fatal(err)
		}
		return ref.Sequence
	}
	notice := func(id string) string {
		t.Helper()
		notices, err := st.PageNotices()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range notices {
			if strings.HasPrefix(n.ID, "ring1_") && strings.Contains(n.Content, id) {
				return n.Content
			}
		}
		t.Fatalf("no Ring 1 notice names %s", id)
		return ""
	}

	st.SetOperatorVoices([]string{"6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"})
	known := func(turn uint64, key string) {
		t.Helper()
		if err := st.AnnotateTurn(turn, store.AnnotationSpeaker, key, `{"decision":"known","speaker":"Quinn","speaker_id":"quinn-desk","speaker_uuid":"6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f","continuity":"matched"}`); err != nil {
			t.Fatal(err)
		}
	}

	// Spoken, and corrected by one rule before it was recorded.
	spoken := say("[voice] Yes Quinn, record it as our charter.")
	known(spoken, "vs-1/7")
	if err := st.AnnotateTurn(spoken, store.AnnotationVoice, "vs-1/7",
		`{"session":"vs-1","sequence":7,"recognized_text":"Yes Queen, record it as our charter.","corrections":1}`); err != nil {
		t.Fatal(err)
	}
	out, err := engine.ExecuteAction(ctx, "verb", "commit", ring1Args("rel_heard_as_1"))
	if err != nil {
		t.Fatalf("the mint citing the corrected spoken word: %v", err)
	}
	for _, want := range []string{`"[voice] Yes Quinn, record it as our charter."`, "1 correction rule(s) changed them", `the recognizer heard: "Yes Queen, record it as our charter."`} {
		if !strings.Contains(out, want) {
			t.Fatalf("the result does not say %q: %s", want, out)
		}
		if said := notice("rel_heard_as_1"); !strings.Contains(said, want) {
			t.Fatalf("the operator's notice does not say %q: %s", want, said)
		}
	}

	// Spoken, and no rule changed it: the reference alone.
	plain := say("[voice] Yes, and add the second clause.")
	known(plain, "vs-1/8")
	if err := st.AnnotateTurn(plain, store.AnnotationVoice, "vs-1/8", `{"session":"vs-1","sequence":8}`); err != nil {
		t.Fatal(err)
	}
	second := ring1Args("rel_heard_as_2")
	second["supersedes"] = "rel_heard_as_1"
	out, err = engine.ExecuteAction(ctx, "verb", "commit", second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "recognizer heard") || strings.Contains(notice("rel_heard_as_2"), "recognizer heard") {
		t.Fatalf("a spoken word no rule changed is said to have been corrected: %s", out)
	}

	// Typed: no annotation at all.
	say("Yes, record the third.")
	third := ring1Args("rel_heard_as_3")
	third["supersedes"] = "rel_heard_as_2"
	out, err = engine.ExecuteAction(ctx, "verb", "commit", third)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "recognizer heard") {
		t.Fatalf("a typed word is said to have been corrected: %s", out)
	}
}
