package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// THE IDENTITY SAYS WHAT RESUMES ITS WORK, by update or by yield, and the
// session carries it; the turn that made it is the host's, never an
// argument; "now" is the identity's to write (the default is to
// work).
func TestTheIdentitySaysWhatResumesItsWork(t *testing.T) {
	engine, st, lg, kp, _ := setupEngine(t)
	ctx := interaction.WithTurn(context.Background(), "turn-start")
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "keep a promise"}); err != nil {
		t.Fatal(err)
	}
	recordPromise(t, engine, st, lg, kp, "cm_1")
	promising := interaction.WithTurn(context.Background(), "turn-promising")
	out, err := engine.ExecuteAction(promising, "verb", "work", map[string]any{"action": "update", "promise": "cm_1", "resumes_on": "word", "promised_turn": "turn-forged"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Work session updated") {
		t.Fatalf("an update with only the promise was not an update: %q", out)
	}
	ws, _ := st.ActiveWorkSession()
	if ws == nil || ws.Promise != "cm_1" || ws.ResumesOn != "word" || ws.PromisedTurn != "turn-promising" {
		t.Fatalf("the session does not carry what the identity said: %+v", ws)
	}
	// The vocabulary holds.
	for _, bad := range []string{"later", "delivery:nope", "time:soon"} {
		_, err := engine.ExecuteAction(promising, "verb", "work", map[string]any{"action": "update", "resumes_on": bad})
		if err == nil {
			t.Fatalf("resumes_on %q was accepted", bad)
		}
	}
	ws, _ = st.ActiveWorkSession()
	if ws.ResumesOn != "word" {
		t.Fatalf("a refused condition changed the session: %+v", ws)
	}
	// A yield names the moment and says so.
	yielding := interaction.WithTurn(context.Background(), "turn-yielding")
	out, err = engine.ExecuteAction(yielding, "verb", "work", map[string]any{"action": "yield", "resumes_on": "time:1800000000000"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "you resume at 2027-01-15T08:00:00Z (1800000000000).") {
		t.Fatalf("the yield does not say when it resumes: %q", out)
	}
	ws, _ = st.ActiveWorkSession()
	if ws.ResumesOn != "time:1800000000000" || ws.Promise != "cm_1" {
		t.Fatalf("the yield did not write its condition: %+v", ws)
	}
	// A yield with no condition keeps the words it always had.
	out, err = engine.ExecuteAction(yielding, "verb", "work", map[string]any{"action": "yield"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "a delivery or message wakes you.") {
		t.Fatalf("a plain yield changed its words: %q", out)
	}
}
