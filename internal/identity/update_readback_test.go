package identity

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// AN UPDATE READS BACK THE CONDITION IT LEAVES: its reply differs
// whether resumes_on is absent, cleared, word or now, so the log can say
// what the update left. The yield's replies stay as approved.
func TestAnUpdateReadsBackTheConditionItLeaves(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := interaction.WithTurn(context.Background(), "turn-start")
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "keep a promise"}); err != nil {
		t.Fatal(err)
	}
	// ws_child1 is a sub-agent's session, as a spawn makes one, so a wait on
	// its delivery can be kept (store.DeliveryNeverComes).
	if _, err := st.EnqueueWork(&store.WorkItem{ID: "sub_ws_child1", Kind: store.SubagentWorkKind, Payload: "{}", DedupKey: "ws_child1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.StartWorkSession(ctx, "ws_child1", "find the figures"); err != nil {
		t.Fatal(err)
	}
	turn := interaction.WithTurn(context.Background(), "turn-update")
	update := func(args map[string]any) string {
		t.Helper()
		args["action"] = "update"
		out, err := engine.ExecuteAction(turn, "verb", "work", args)
		if err != nil {
			t.Fatalf("update %v: %v", args, err)
		}
		return out
	}
	condition := func() string {
		t.Helper()
		ws, err := st.ActiveWorkSession()
		if err != nil || ws == nil {
			t.Fatalf("no active session: %v", err)
		}
		return ws.ResumesOn
	}
	cases := []struct {
		name  string
		args  map[string]any
		want  string
		leave string
	}{
		{"word", map[string]any{"resumes_on": "word"},
			"Work session updated: this work waits for your operator's next message, as you set.", "word"},
		{"delivery", map[string]any{"resumes_on": "delivery:ws_child1"},
			"Work session updated: this work waits for a delivery from ws_child1, as you set.", "delivery:ws_child1"},
		{"time", map[string]any{"resumes_on": "time:1800000000000"},
			"Work session updated. You resume at 2027-01-15T08:00:00Z (1800000000000).", "time:1800000000000"},
		{"cleared", map[string]any{"resumes_on": ""},
			"Work session updated: nothing holds this work now; it goes on when this turn ends.", ""},
		{"word again", map[string]any{"resumes_on": "word"},
			"Work session updated: this work waits for your operator's next message, as you set.", "word"},
		{"now", map[string]any{"resumes_on": "now"},
			"Work session updated: nothing holds this work now; it goes on when this turn ends.", "now"},
		{"not given", map[string]any{"focus": "the gate"},
			"Work session updated.", "now"},
		{"word with standing", map[string]any{"resumes_on": "word", "standing": "waiting on the gate"},
			"Work session updated, and standing state with it: this work waits for your operator's next message, as you set.", "word"},
		{"time with standing", map[string]any{"resumes_on": "time:1800000000000", "standing": "waiting on the gate"},
			"Work session updated, and standing state with it. You resume at 2027-01-15T08:00:00Z (1800000000000).", "time:1800000000000"},
		{"not given with standing", map[string]any{"state": "the gate failed", "standing": "waiting on the gate"},
			"Work session updated, and standing state with it.", "time:1800000000000"},
	}
	for _, c := range cases {
		out := update(c.args)
		if out != c.want {
			t.Fatalf("%s: the update said %q; the approved words are %q", c.name, out, c.want)
		}
		if left := condition(); left != c.leave {
			t.Fatalf("%s: the update left the condition %q, not %q", c.name, left, c.leave)
		}
	}
}

// A YIELD SAYS WHAT FOLLOWS IT: a yield to now is told
// the update's approved sentence, since the work goes on when the turn
// ends; its other replies stay as approved.
func TestAYieldSaysWhatFollowsIt(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := interaction.WithTurn(context.Background(), "turn-start")
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "keep a promise"}); err != nil {
		t.Fatal(err)
	}
	head := "Turn yielding — your gate frees when this round ends. Your session stays active; "
	for _, c := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"now", map[string]any{"resumes_on": "now"}, head + "nothing holds this work now; it goes on when this turn ends."},
		{"word", map[string]any{"resumes_on": "word"}, head + "your operator's next message wakes you."},
		{"time", map[string]any{"resumes_on": "time:1800000000000"}, head + "you resume at 2027-01-15T08:00:00Z (1800000000000)."},
		{"cleared", map[string]any{"resumes_on": ""}, head + "a delivery or message wakes you."},
		{"not given", map[string]any{}, head + "a delivery or message wakes you."},
	} {
		c.args["action"] = "yield"
		out, err := engine.ExecuteAction(interaction.WithTurn(context.Background(), "turn-"+c.name), "verb", "work", c.args)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if out != c.want {
			t.Fatalf("%s: the yield said %q; the approved words are %q", c.name, out, c.want)
		}
	}
}
