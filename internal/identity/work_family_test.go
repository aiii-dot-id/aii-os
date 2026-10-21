package identity

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// THE KIND OF WORK AT THE VERB: start
// names the session it started, and its family when given; a family that
// is not a slug is refused before anything is started; update names or
// corrects it; deliver names the session and the calls the record counted.
func TestWorkStartNamesTheSessionAndItsFamilyAndDeliverItsCalls(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	work := func(args map[string]any) (string, error) {
		return engine.ExecuteAction(ctx, "verb", "work", args)
	}
	if _, err := work(map[string]any{"action": "start", "description": "deploy", "family": "Deploy Preview"}); err == nil || !strings.Contains(err.Error(), "rejected_before_effect (start.family)") {
		t.Fatalf("a family outside the grammar must be refused before effect: %v", err)
	}
	if active, _ := st.ActiveWorkSession(); active != nil {
		t.Fatalf("a refused start left a session: %+v", active)
	}
	out, err := work(map[string]any{"action": "start", "description": "deploy the preview", "family": "deploy-preview"})
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`^Started work session (ws_[0-9a-f-]+) \(family deploy-preview\): deploy the preview$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("start must name the session and its family: %q", out)
	}
	if _, err := work(map[string]any{"action": "update", "family": "preview-deploy"}); err != nil {
		t.Fatal(err)
	}
	if ws, _ := st.WorkSessionByID(m[1]); ws == nil || ws.Family != "preview-deploy" {
		t.Fatalf("update must correct the family: %+v", ws)
	}
	delivered, err := work(map[string]any{"action": "deliver", "result": "served: preview up"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(delivered, "Delivered "+m[1]+" (Ring 4) after ") || !strings.Contains(delivered, " tool calls.") {
		t.Fatalf("deliver must name the session and its counted calls: %q", delivered)
	}
}

// THE SHORTER WAY IS IN THE RECORD. Two served attempts of one kind, the second shorter:
// `work measure family=` names both with their calls and the shorter one's
// calls in order, which is what the playbook is written from.
func TestMeasureFamilyShowsTheShortestServedAttemptAndItsCalls(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	work := func(args map[string]any) string {
		t.Helper()
		out, err := engine.ExecuteAction(ctx, "verb", "work", args)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	attempt := func(turn string, tools ...string) {
		work(map[string]any{"action": "start", "description": "deploy the preview", "family": "deploy-preview"})
		for i, tool := range tools {
			if err := st.RecordToolStart(turn, i+1, "main", "m", "", tool, "{}"); err != nil {
				t.Fatal(err)
			}
		}
		work(map[string]any{"action": "deliver", "result": "served: preview up"})
	}
	attempt("turn_long", "read", "shell", "shell", "shell", "web_fetch", "shell")
	attempt("turn_short", "read", "shell")
	out := work(map[string]any{"action": "measure", "family": "deploy-preview"})
	for _, want := range []string{
		"Kind deploy-preview: 2 delivered attempt(s), newest first: served 2, partial 0, unserved 0; graded 0 (served 0).",
		"served, ungraded, 2 calls", "served, ungraded, 6 calls",
		"Its calls: 1 read, 2 shell.",
		"Your playbook for this kind: playbooks/deploy-preview/SKILL.md in your home.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("measure family lacks %q:\n%s", want, out)
		}
	}
	if !regexp.MustCompile(`Shortest served attempt: ws_[0-9a-f-]+ in 2 calls\.`).MatchString(out) {
		t.Fatalf("measure family must name the shortest served attempt:\n%s", out)
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "measure", "family": "Not A Slug"}); err == nil {
		t.Fatal("a family outside the grammar must be refused")
	}
}
