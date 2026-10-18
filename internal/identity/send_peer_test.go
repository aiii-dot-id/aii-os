package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The identity learns only whether a name can be reached — never how.
// book is the operator's address book as the identity sees it.
func book(t *testing.T, e *Engine, names ...string) {
	t.Helper()
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	e.reachable = func(name string) bool { return known[name] }
}

func queued(t *testing.T, e *Engine) []store.OutboxMessage {
	t.Helper()
	msgs, err := engineStore(e).UndeliveredMessages()
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

// Knowing someone and knowing how to reach them are ONE fact now: there
// is no correspondent to be known independently of a way to contact them,
// so there is one refusal rather than two, and it says what to do.
func TestSendRefusesANameItCannotReach(t *testing.T) {
	e, _ := timerEngine(t)

	out, err := e.verbSend(context.Background(), map[string]any{
		"to": "someone-who-does-not-exist", "message": "hello?",
	})
	if err == nil {
		t.Fatalf("send to an unreachable name reported success: %q", out)
	}
	if !strings.Contains(err.Error(), "no way to reach") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
	if !strings.Contains(err.Error(), "ask your operator") {
		t.Fatalf("the refusal does not say what to do about it: %v", err)
	}
	if n := len(queued(t, e)); n != 0 {
		t.Fatalf("a refused send left %d message(s) queued for nobody", n)
	}
}

func TestSendQueuesForSomeoneReachable(t *testing.T) {
	e, _ := timerEngine(t)
	book(t, e, "quinn")

	out, err := e.verbSend(context.Background(), map[string]any{"to": "quinn", "message": "the build is green"})
	if err != nil {
		t.Fatalf("send to a reachable person was refused: %v", err)
	}
	if !strings.Contains(out, "quinn") {
		t.Fatalf("the answer does not say who it went to: %q", out)
	}

	msgs := queued(t, e)
	if len(msgs) != 1 {
		t.Fatalf("got %d queued, want 1", len(msgs))
	}
	// The row records WHO, not where: the address resolves at delivery.
	if msgs[0].ToRole != "peer" || msgs[0].ToIdentity != "quinn" {
		t.Fatalf("the queued row does not name the person: %+v", msgs[0])
	}
}

// The identity must not be handed a channel vocabulary nothing can act
// on. "quinn on signal" is a name the book has never heard of, and it is
// refused as one — not silently accepted as if the channel were chosen.
func TestNamingAChannelIsNotASecretFeature(t *testing.T) {
	e, _ := timerEngine(t)
	book(t, e, "quinn")

	if _, err := e.verbSend(context.Background(), map[string]any{
		"to": "quinn on signal", "message": "call me",
	}); err == nil {
		t.Fatal("\"quinn on signal\" was accepted — the surface advertises a choice it cannot make")
	}
	if n := len(queued(t, e)); n != 0 {
		t.Fatalf("a refused send left %d message(s) queued", n)
	}
	// And it must not be ADVERTISED either: a description promising a
	// feature the code does not have is the same defect one layer up.
	for _, v := range Verbs() {
		if v.Name != "send" {
			continue
		}
		if strings.Contains(v.Description, "on <channel>") || strings.Contains(v.Description, "on signal") {
			t.Fatalf("send still advertises channel-naming: %q", v.Description)
		}
	}
}

// The path that always worked must keep working.
func TestSendToOperatorStillWorks(t *testing.T) {
	e, _ := timerEngine(t)

	out, err := e.verbSend(context.Background(), map[string]any{"message": "the build is green"})
	if err != nil {
		t.Fatalf("send to the operator was refused: %v", err)
	}
	if !strings.Contains(out, "operator") {
		t.Fatalf("a real send did not report itself: %q", out)
	}
	msgs := queued(t, e)
	if len(msgs) != 1 || msgs[0].ToRole != "operator" {
		t.Fatalf("the operator's message did not reach the delivery queue: %+v", msgs)
	}
}

// Peer-addressed rows must never be shown to the operator as their own
// mail — the defect that made "Sent to peer." worse than a lie.
func TestOperatorDeliveryNeverCarriesSomeoneElsesMail(t *testing.T) {
	e, _ := timerEngine(t)
	st := engineStore(e)

	if err := st.AddOutboxMessage("msg_for_operator", "operator", "", "yours", nil); err != nil {
		t.Fatal(err)
	}
	book(t, e, "quinn")
	if err := st.AddOutboxMessage("msg_for_peer", "peer", "quinn", "not yours", nil); err != nil {
		t.Fatal(err)
	}

	all, err := st.UndeliveredMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("the unfiltered query lost a row: %d", len(all))
	}
	mine, err := st.UndeliveredFor("operator")
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].ID != "msg_for_operator" {
		t.Fatalf("operator delivery carried mail that was not theirs: %+v", mine)
	}
}
