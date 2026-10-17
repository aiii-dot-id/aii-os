package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// subAgentSession makes ws a sub-agent's session, as a spawn does, and
// delivers result on it unless result is empty (the child still runs).
func subAgentSession(t *testing.T, st *store.Store, ws, goal, result string) {
	t.Helper()
	if _, enq, err := st.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{Kind: SubagentWorkKind, Payload: "{}", DedupKey: ws, Source: "identity", LeaseMs: 1000},
		8, ws, store.SubagentDescription(goal)); err != nil || !enq {
		t.Fatalf("enqueue %s: enq=%v err=%v", ws, enq, err)
	}
	if result == "" {
		return
	}
	if err := st.DeliverWorkSession(context.Background(), ws, result, "", ""); err != nil {
		t.Fatal(err)
	}
}

// readDelivery follows `work action=status id=…` from its first part to
// its last, as the identity would: each part's closing label names the
// next. It returns the result's text as the parts carried it, and how
// many parts there were.
func readDelivery(t *testing.T, e *Engine, id string) (string, int) {
	t.Helper()
	var whole strings.Builder
	arg := id
	for parts := 1; parts <= 20; parts++ {
		out, err := e.ExecuteAction(context.Background(), "verb", "work", map[string]any{"action": "status", "id": arg})
		if err != nil {
			t.Fatalf("work status id=%s: %v", arg, err)
		}
		_, body, framed := strings.Cut(out, "\n\n")
		end := strings.LastIndex(body, "\n\n[end of ")
		if !framed || end < 0 {
			t.Fatalf("part %d is not framed by a heading and a closing label: %.300q", parts, out)
		}
		whole.WriteString(body[:end])
		_, next, more := strings.Cut(body[end:], `continues: work action=status id="`)
		if !more {
			return whole.String(), parts
		}
		arg, _, _ = strings.Cut(next, `"`)
	}
	t.Fatal("the parts never ended")
	return "", 0
}

func TestWorkStatusReadsADeliveredResultWholeInParts(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	long := "served: WHOLE_HEAD the long one\n" + strings.Repeat("ünïcode line ✓ with ordinary words — ", 900) + "WHOLE_TAIL"
	subAgentSession(t, st, "ws_long", "survey the long thing", long)
	subAgentSession(t, st, "ws_short", "survey the short thing", "partial: SHORT_WHOLE two of three")

	list, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]any{"action": "status"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "delivered ws_long") || !strings.Contains(list, "work action=status id=") {
		t.Errorf("the rendezvous lists deliveries without naming the read of their results: %q", list)
	}
	props, _ := lookupVerb("work").Params["properties"].(map[string]any)
	id, _ := props["id"].(map[string]any)
	if desc, _ := id["description"].(string); !strings.Contains(desc, "(status)") {
		t.Errorf("id does not name status as a mode that reads it: %q", desc)
	}

	got, parts := readDelivery(t, engine, "ws_long")
	if got != long {
		t.Fatalf("the parts do not make the whole result: %d runes read of %d (head %v, tail %v)",
			len([]rune(got)), len([]rune(long)), strings.HasPrefix(got, "served: WHOLE_HEAD"), strings.HasSuffix(got, "WHOLE_TAIL"))
	}
	if parts != 3 {
		t.Fatalf("a result of %d runes read in %d part(s), want 3", len([]rune(long)), parts)
	}
	first, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]any{"action": "status", "id": "ws_long"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "survey the long thing") || !strings.Contains(first, "part 1 of 3") {
		t.Fatalf("a part does not say whose result it is and where it stands: %.200q", first)
	}

	short, parts := readDelivery(t, engine, "ws_short")
	if short != "partial: SHORT_WHOLE two of three" || parts != 1 {
		t.Fatalf("a short result reads whole in one part: %d part(s), %q", parts, short)
	}

	if subs, err := st.UnharvestedDeliveries(8); err != nil || len(subs) != 2 {
		t.Fatalf("reading a result changed what is unharvested: %d (%v)", len(subs), err)
	}
	if ws, err := st.WorkSessionByID("ws_long"); err != nil || ws.Result != long || ws.Status != "delivered" {
		t.Fatalf("reading a result changed its session: %+v (%v)", ws, err)
	}
}

// The read refuses, typed and with no effect, an id that is not a
// delivered sub-agent's: one never recorded, a session of the identity's
// own seat, a child still running, and a part the result does not have.
func TestWorkStatusRefusesWhatIsNotADeliveredSubAgent(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	subAgentSession(t, st, "ws_done", "the delivered one", "served: done")
	subAgentSession(t, st, "ws_running", "the running one", "")
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]any{"action": "start", "description": "my own"}); err != nil {
		t.Fatal(err)
	}
	mine, err := st.ActiveWorkSession()
	if err != nil || mine == nil {
		t.Fatalf("the seat's session: %v", err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]any{"action": "deliver", "result": "served: MINE"}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, id, code string }{
		{"never recorded", "ws_nobody", "rejected_before_effect (status.id)"},
		{"the seat's own session", mine.ID, "rejected_before_effect (status.id)"},
		{"still running", "ws_running", "rejected_before_effect (status.delivered)"},
		{"a part it does not have", "ws_done part:2", "rejected_before_effect (status.part)"},
		{"part zero", "ws_done part:0", "rejected_before_effect (status.part)"},
	} {
		out, err := engine.ExecuteAction(context.Background(), "verb", "work", map[string]any{"action": "status", "id": tc.id})
		if err == nil || !strings.HasPrefix(err.Error(), tc.code) {
			t.Errorf("%s (%s): want a refusal %q, got %v %.120q", tc.name, tc.id, tc.code, err, out)
		}
	}

	if subs, err := st.UnharvestedDeliveries(8); err != nil || len(subs) != 1 || subs[0].ID != "ws_done" {
		t.Fatalf("a refused read changed what is unharvested: %+v (%v)", subs, err)
	}
	if ws, err := st.WorkSessionByID("ws_running"); err != nil || ws.Status != "active" {
		t.Fatalf("a refused read changed a running child: %+v (%v)", ws, err)
	}
}
