package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// pressStage seeds the operator's press for a level, as the page's press
// records it, and the identity's upsert carrying its digest.
func pressStage(t *testing.T, e *Engine, st *store.Store, level string) {
	t.Helper()
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-press"), "operator")
	digest := store.AutonomyApprovalDigest("rel_quinn_01", level)
	if err := st.AddConversationTurn("operator", "I approve the charter."); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordOperatorPress(context.Background(), "Quinn's identity", level, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "relationship.upsert", "id": "rel_quinn_01", "counterpart_name": "Quinn",
		"counterpart_role": "operator", "relationship_type": "founding_operator", "charter_text": "CHARTER-TEXT Quinn and I build carefully.", "autonomy_level": level}); err != nil {
		t.Fatal(err)
	}
	if stage, err := st.ApprovedStage(); err != nil || stage != level {
		t.Fatalf("the press did not bind the level: %q %v", stage, err)
	}
}

// A YIELD UNDER THE OPERATOR'S HOLD IS ACCEPTED: the condition becomes
// their word, with the reason, the alarm it would have kept is not kept,
// and the identity is told in the approved words.
func TestAYieldUnderTheHoldIsAcceptedAsTheOperatorsWord(t *testing.T) {
	e, st, lg, kp, _ := setupEngine(t)
	e.timers = NewStoreTimers(st)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-1"), "operator")
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "keep a promise"}); err != nil {
		t.Fatal(err)
	}
	recordPromise(t, e, st, lg, kp, "in_1")
	pressStage(t, e, st, store.StageSupervised)
	out, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "yield", "promise": "in_1", "resumes_on": "time:1800000000000"})
	if err != nil {
		t.Fatalf("a yield under the hold was refused: %v", err)
	}
	if out != "Turn yielding — your gate frees when this round ends. Your operator holds this work; it resumes on their word." {
		t.Fatalf("the held yield's words: %q", out)
	}
	ws, _ := st.ActiveWorkSession()
	if ws.ResumesOn != "word" || ws.Promise != "in_1" {
		t.Fatalf("the held yield did not become the operator's word: %+v", ws)
	}
	if _, found, _ := st.AlarmByID(PromiseAlarmID(ws.ID)); found {
		t.Fatal("the held yield kept an alarm")
	}
	var reason string
	_ = st.DB().QueryRow(`SELECT content FROM conversations WHERE kind='notice' AND json_extract(details,'$.work.resumes_on')='word' ORDER BY turn_seq DESC LIMIT 1`).Scan(&reason)
	if !strings.Contains(reason, "Held by the operator (supervised)") {
		t.Fatalf("the record does not say why: %q", reason)
	}
}
