package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// A REPLAY NAMES THE RECORD IT REFUSES, IN A TYPE. The boot routes on the
// kind of failure, never on its words: an event type this binary does not
// know, and any other event the rules refuse, each carry where the event
// stands in the record. The words are the ones the untyped errors had.
func TestAReplayRefusalCarriesTheRecordItRefused(t *testing.T) {
	st, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	birth := &ledger.Event{Seq: 1, Type: ledger.EventType("a.kind.of.the.future"), Ring: 3, Payload: []byte(`{}`)}
	err = st.ReplayAll(func(yield func(*ledger.Event) error) error { return yield(birth) })
	var refused *ReplayRefusedError
	var unknown *UnknownEventTypeError
	if !errors.As(err, &refused) || !errors.As(err, &unknown) {
		t.Fatalf("an unknown event type in a replay is not typed: %T %v", err, err)
	}
	if refused.Seq != 1 || refused.Type != birth.Type || unknown.Type != birth.Type {
		t.Fatalf("the refusal does not carry the record it refused: %+v %+v", refused, unknown)
	}
	if got := err.Error(); !strings.Contains(got, "materialize failed at seq 1: ") || !strings.Contains(got, "unknown event type: a.kind.of.the.future") {
		t.Fatalf("the words changed: %q", got)
	}
}
