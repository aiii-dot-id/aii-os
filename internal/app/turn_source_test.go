package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A PROMISE MADE IN AN ARRIVAL'S TURN CITES THAT TURN, and the record says
// the turn was an arrival's: the host reads a promise's provenance from the
// turn that made it, never from the identity's word. An alarm's wake is
// marked as the alarm's, with the source of the turn that set it.
func TestAPromiseMadeInAnArrivalsTurnCitesThatTurn(t *testing.T) {
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	pressStage(t, a, store.StageTrusted)
	recordPromise(t, a, "cm_1")
	fixtureModel(t, a, func(n int, w http.ResponseWriter) {
		if n == 1 {
			_, _ = w.Write([]byte(callReply("",
				toolCall("w1", "work", `{"action":"start","description":"answer the contact"}`),
				toolCall("w2", "work", `{"action":"update","promise":"cm_1","resumes_on":"word"}`))))
			return
		}
		_, _ = w.Write([]byte(textReply("noted")))
	})
	ctx := goldenTurnCtx(t)
	if err := a.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.wake(withTurnSource(ctx, turnSourceArrival), string(interaction.Participant), "[messages] a contact wrote"); err != nil {
		a.releaseTurn()
		t.Fatal(err)
	}
	a.releaseTurn()
	ws, err := a.store.ActiveWorkSession()
	if err != nil || ws == nil {
		t.Fatalf("no active session: %v", err)
	}
	if ws.Promise != "cm_1" || ws.PromisedTurn == "" {
		t.Fatalf("the promise does not cite its turn: %+v", ws)
	}
	source, setIn, err := a.store.TurnSourceOf(ws.PromisedTurn)
	if err != nil || source != turnSourceArrival || setIn != "" {
		t.Fatalf("the promise's turn reads %q %q %v, want the arrival's", source, setIn, err)
	}

	// The alarm fires as it does live: the identity's owner queues the
	// wake from the alarm's payload, and the handler runs it.
	a.executor.Stop()
	if r := a.timerOwner.OnAlarm(goldenTurnCtx(t), "d2", "wall", 42, `{"message":"tea","set_in":"operator"}`); !r.Accepted {
		t.Fatalf("the firing was not accepted: %+v", r)
	}
	w, err := a.store.ClaimWork([]string{identity.TimerWakeWorkKind}, time.Now().UnixMilli())
	if err != nil || w == nil {
		t.Fatalf("no queued wake: %+v %v", w, err)
	}
	if err := (&timerWakeHandler{a}).RunWork(goldenTurnCtx(t), w); err != nil {
		t.Fatal(err)
	}
	var turn string
	if err := a.store.DB().QueryRow(`SELECT turn_id FROM conversations WHERE role='system' AND content LIKE '[timer d2 fired %'`).Scan(&turn); err != nil {
		t.Fatal(err)
	}
	source, setIn, err = a.store.TurnSourceOf(turn)
	if err != nil || source != turnSourceTimer || setIn != turnSourceOperator {
		t.Fatalf("the alarm's turn reads %q %q %v, want the alarm's, set in the operator's turn", source, setIn, err)
	}
}
