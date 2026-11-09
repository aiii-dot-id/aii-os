package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// A SHORT SESSION ID NAMES THE ONE SESSION IT BEGINS; recall source=work
// id=ws_4399d2ce was once refused as "not one of your work sessions"
// although it began one. Several are refused naming how many, and
// none is refused saying where the whole ids are.
func TestAShortSessionIDNamesTheOneSessionItBegins(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_s"), "operator")
	var ids []string
	for _, d := range []string{"the first", "the second"} {
		if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": d}); err != nil {
			t.Fatal(err)
		}
		ws, _ := st.ActiveWorkSession()
		ids = append(ids, ws.ID)
		if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "deliver", "result": "served: done"}); err != nil {
			t.Fatal(err)
		}
	}
	short := ids[0][:len("ws_")+8]
	out, err := engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "work", "id": short, "query": ""})
	if err != nil || !strings.Contains(out, ids[0]) {
		t.Fatalf("the short id %s did not read its session: %v\n%s", short, err, out)
	}
	_, err = engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "work", "id": "ws_", "query": ""})
	if err == nil || !strings.Contains(err.Error(), "ws_ begins 2 of your work sessions: use the whole id, as work status lists it") {
		t.Fatalf("an ambiguous prefix was not refused in the words: %v", err)
	}
	_, err = engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "work", "id": "ws_nobody", "query": ""})
	if err == nil || !strings.Contains(err.Error(), "ws_nobody names none of your work sessions: work status lists them with their whole ids, and a short id must begin one of them") {
		t.Fatalf("an unknown id was not refused in the words: %v", err)
	}
}
