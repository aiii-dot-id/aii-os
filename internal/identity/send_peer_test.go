package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// send addresses a PERSON (operator ruling 2026-08-24). What it must
// never do is report a delivery nothing can make — the property this
// file has guarded since "Sent to peer." was a lie about a message with
// no recipient, no carrier and no destination.
//
// "peer" is no longer a magic value; it is simply a name the address book
// has never heard of, and it is refused the same way any other unknown
// name is.

// The identity learns only whether a name can be reached — never how.
// book is the operator's address book as the identity sees it.
func book(t *testing.T, e *Engine, names ...string) {
	t.Helper()
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	e.SetReachable(func(name string) bool { return known[name] })
}

func queued(t *testing.T, e *Engine) []store.OutboxMessage {
	t.Helper()
	msgs, err := e.store.UndeliveredMessages()
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

	out, err := e.verbSend(context.Background(), map[string]interface{}{
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
	book(t, e, "james")

	out, err := e.verbSend(context.Background(), map[string]interface{}{"to": "james", "message": "the build is green"})
	if err != nil {
		t.Fatalf("send to a reachable person was refused: %v", err)
	}
	if !strings.Contains(out, "james") {
		t.Fatalf("the answer does not say who it went to: %q", out)
	}

	msgs := queued(t, e)
	if len(msgs) != 1 {
		t.Fatalf("got %d queued, want 1", len(msgs))
	}
	// The row records WHO, not where: the address resolves at delivery.
	if msgs[0].ToRole != "peer" || msgs[0].ToIdentity != "james" {
		t.Fatalf("the queued row does not name the person: %+v", msgs[0])
	}
}

// The identity must not be handed a channel vocabulary nothing can act
// on. "james on signal" is a name the book has never heard of, and it is
// refused as one — not silently accepted as if the channel were chosen.
func TestNamingAChannelIsNotASecretFeature(t *testing.T) {
	e, _ := timerEngine(t)
	book(t, e, "james")

	if _, err := e.verbSend(context.Background(), map[string]interface{}{
		"to": "james on signal", "message": "call me",
	}); err == nil {
		t.Fatal("\"james on signal\" was accepted — the surface advertises a choice it cannot make")
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

	out, err := e.verbSend(context.Background(), map[string]interface{}{"message": "the build is green"})
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
	st := e.store

	if err := st.AddOutboxMessage("msg_for_operator", "operator", "", "yours", nil); err != nil {
		t.Fatal(err)
	}
	book(t, e, "james")
	if err := st.AddOutboxMessage("msg_for_peer", "peer", "james", "not yours", nil); err != nil {
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
