package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// recordPromise puts an open intention (in_…) or a commitment (cm_…)
// with the given id into the engine's record, so a work call may name it
// as its promise. A commitment needs a counterpart: the operator's
// relationship is minted on the ledger the way verbs_test does it, once.
func recordPromise(t *testing.T, e *Engine, st *store.Store, lg *ledger.Ledger, kp *crypto.KeyPair, id string) {
	t.Helper()
	ctx := interaction.WithTurn(context.Background(), "turn-record-"+id)
	args := map[string]any{"variant": "intention.create", "id": id, "statement": "keep a promise", "why": "a test's reason"}
	if strings.HasPrefix(id, "cm_") {
		if rel, err := st.CurrentOperatorRelationship(); err != nil || rel == nil {
			if err := st.AddConversationTurn("operator", "Yes — rel_test approved."); err != nil {
				t.Fatal(err)
			}
			approval, err := st.GetLatestOperatorTurn()
			if err != nil || approval == nil {
				t.Fatalf("operator approval turn: %+v, %v", approval, err)
			}
			evt, err := lg.Append(ledger.EventRelationshipUpsert, 1, map[string]any{
				"id": "rel_test", "counterpart_name": "Op", "counterpart_role": "operator", "relationship_type": "founding_operator",
				"operator_approval_excerpt": approval.Content, "operator_approval_turn": approval.TurnSeq, "approval_basis": "conversation_turn",
			}, kp)
			if err != nil {
				t.Fatalf("mint relationship: %v", err)
			}
			st.Materialize(evt)
		}
		rel, _ := st.CurrentOperatorRelationship()
		args = map[string]any{"variant": "commitment.promised", "id": id, "counterpart_id": rel.ID, "description": "keep a promise"}
	}
	if _, err := e.ExecuteAction(ctx, "verb", "commit", args); err != nil {
		t.Fatalf("record the promise %s: %v", id, err)
	}
}

// A PROMISE NAMES AN OPEN INTENTION OR COMMITMENT IN THE RECORD; a
// session's alarm id was once accepted, and the floor could never cite it.
// Any other id is refused in words that say what the id is where the record knows, the open ones
// the identity could mean with their words (at most five), or "none
// open"; a known open one and an empty one are accepted, whatever the
// id's form.
func TestAPromiseMustNameAnOpenIntentionOrCommitment(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_op"), "operator")
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "a long task"}); err != nil {
		t.Fatal(err)
	}
	ws, err := st.ActiveWorkSession()
	if err != nil || ws == nil {
		t.Fatalf("no session: %v", err)
	}
	refusal := func(args map[string]any) string {
		t.Helper()
		_, err := engine.ExecuteAction(ctx, "verb", "work", args)
		if err == nil {
			t.Fatalf("%v was accepted as a promise", args["promise"])
		}
		return err.Error()
	}
	// Nothing open: the second sentence says so. The whole text, as the
	// identity reads it (the host, 2026-11-06: no substrings).
	if got, want := refusal(map[string]any{"action": "update", "promise": "item_2527b593"}),
		`rejected_before_effect (promise): promise "item_2527b593" is not an open intention or commitment in your record. You have none open; leave promise empty.`; got != want {
		t.Fatalf("with none open:\n got %s\nwant %s", got, want)
	}
	// The session's own alarm, named by its id: the record knows what it is.
	if got, want := refusal(map[string]any{"action": "update", "promise": PromiseAlarmID(ws.ID)}),
		`promise "`+PromiseAlarmID(ws.ID)+`" is not an open intention or commitment in your record (it is this session's alarm). You have none open; leave promise empty.`; !strings.Contains(got, want) {
		t.Fatalf("the alarm's id:\n got %s\nwant %s", got, want)
	}
	// An open intention, an open commitment with a minted-looking id, and a
	// completed intention: the closed one is refused naming what it is and
	// listing the open ones with their words; the open ones are accepted.
	recordPromise(t, engine, st, lg, kp, "in_known")
	recordPromise(t, engine, st, lg, kp, "cm_known")
	recordPromise(t, engine, st, lg, kp, "in_done")
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "intention.state_change", "id": "in_done", "state": "completed", "outcome": "served: it is done"}); err != nil {
		t.Fatal(err)
	}
	if got, want := refusal(map[string]any{"action": "update", "promise": "in_done"}),
		`promise "in_done" is not an open intention or commitment in your record (it is a completed intention). Open ones you could mean: in_known "keep a promise", cm_known "keep a promise". Or leave promise empty.`; !strings.Contains(got, want) {
		t.Fatalf("a completed intention:\n got %s\nwant %s", got, want)
	}
	// An abandoned intention: the whole text, article and full stop included.
	recordPromise(t, engine, st, lg, kp, "in_dropped")
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "intention.state_change", "id": "in_dropped", "state": "abandoned", "outcome": "unserved: it was dropped"}); err != nil {
		t.Fatal(err)
	}
	if got, want := refusal(map[string]any{"action": "update", "promise": "in_dropped"}),
		`rejected_before_effect (promise): promise "in_dropped" is not an open intention or commitment in your record (it is an abandoned intention). Open ones you could mean: in_known "keep a promise", cm_known "keep a promise". Or leave promise empty.`; got != want {
		t.Fatalf("an abandoned intention:\n got %s\nwant %s", got, want)
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "commitment.state_change", "id": "cm_known", "state": "abandoned"}); err != nil {
		t.Fatal(err)
	}
	if got, want := refusal(map[string]any{"action": "update", "promise": "cm_known"}),
		`promise "cm_known" is not an open intention or commitment in your record (it is an abandoned commitment). Open ones you could mean: in_known "keep a promise". Or leave promise empty.`; !strings.Contains(got, want) {
		t.Fatalf("an abandoned commitment:\n got %s\nwant %s", got, want)
	}
	for _, id := range []string{"in_known", ""} {
		if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "promise": id}); err != nil {
			t.Fatalf("promise %q was refused: %v", id, err)
		}
	}
	// A commit that names no id gets the verb's minted one (item_…); it is
	// an intention all the same, and accepted as a promise.
	out, err := engine.ExecuteAction(ctx, "verb", "commit", map[string]any{"variant": "intention.create", "statement": "an unnamed aim", "why": "a test's reason"})
	if err != nil {
		t.Fatal(err)
	}
	minted := ""
	for _, i := range mustIntentions(t, st) {
		if i.Statement == "an unnamed aim" {
			minted = i.ID
		}
	}
	if !strings.HasPrefix(minted, "item_") {
		t.Fatalf("the unnamed intention's id is %q (commit said: %s)", minted, out)
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "promise": minted}); err != nil {
		t.Fatalf("the minted id %s was refused as a promise: %v", minted, err)
	}
	// Many open: at most five are listed.
	for i := 0; i < 6; i++ {
		recordPromise(t, engine, st, lg, kp, fmt.Sprintf("in_more_%d", i))
	}
	got := refusal(map[string]any{"action": "update", "promise": "in_done"})
	if n := strings.Count(got, `"keep a promise"`) + strings.Count(got, `"an unnamed aim"`); n != 5 {
		t.Fatalf("%d open ones listed, want 5: %s", n, got)
	}
}

func mustIntentions(t *testing.T, st *store.Store) []store.Intention {
	t.Helper()
	out, err := st.ListIntentions()
	if err != nil {
		t.Fatal(err)
	}
	return out
}
