package identity

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// WORK STATUS LISTS RECENT SESSIONS, NEWEST FIRST, FULL IDS, THE CURRENT
// ONE NOT REPEATED:
// five at most, each with its state, promise, last activity and a cut
// description, with the route to read one's calls in the heading.
func TestWorkStatusListsRecentSessions(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_s"), "operator")
	var ids []string
	for _, d := range []string{"the first task", "the second task, whose description is longer than sixty characters and is cut"} {
		if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": d}); err != nil {
			t.Fatal(err)
		}
		ws, _ := st.ActiveWorkSession()
		ids = append(ids, ws.ID)
		if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "deliver", "result": "served: done"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "the current task"}); err != nil {
		t.Fatal(err)
	}
	current, _ := st.ActiveWorkSession()
	out, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "status"})
	if err != nil {
		t.Fatal(err)
	}
	head := "Recent sessions, newest first (read one's calls with recall source=work id=<its id>):"
	i := strings.Index(out, head)
	if i < 0 {
		t.Fatalf("status lists no sessions:\n%s", out)
	}
	lines := strings.Split(strings.TrimSpace(out[i+len(head):]), "\n")
	if len(lines) != 2 {
		t.Fatalf("listed %d sessions, want 2 (the current one not repeated):\n%s", len(lines), out[i:])
	}
	if strings.Contains(out[i:], current.ID) {
		t.Fatalf("the current session is repeated:\n%s", out[i:])
	}
	row := regexp.MustCompile(`^(ws_\S+) · (\S+) · (.+?) · last (\d\d:\d\dZ|seq \d+) · (.+)$`)
	for n, want := range []struct{ id, desc string }{{ids[1], "the second task, whose description is longer than sixty cha…"}, {ids[0], "the first task"}} {
		m := row.FindStringSubmatch(lines[n])
		if m == nil || m[1] != want.id || m[2] != "delivered" || m[3] != "no promise" || !strings.HasPrefix(m[5], want.desc[:14]) {
			t.Fatalf("row %d: %q, want %s · delivered · no promise · last … · %s", n, lines[n], want.id, want.desc)
		}
		if len([]rune(m[5])) > 61 {
			t.Fatalf("row %d: the description is not cut at 60: %q", n, m[5])
		}
	}
	// A past session's calls are read by its full id, the existing route.
	rec, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "work", "id": ids[0], "query": ""})
	if err != nil || !strings.Contains(rec, ids[0]) {
		t.Fatalf("recall by id: %v\n%s", err, rec)
	}
}
