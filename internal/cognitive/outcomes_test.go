package cognitive

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
type fakeOutcomes struct {
	self      string
	published int
}

func (f *fakeOutcomes) NextOutcomes(window int) (store.OutcomeBatch, error) {
	return store.OutcomeBatch{From: 0, Through: 9, Outcomes: []store.Outcome{{
		Seq: 9, EntryHash: strings.Repeat("ab", 32), Kind: "intention", ID: "i1", State: "completed", Was: "one", Said: "done",
	}}}, nil
}
func (f *fakeOutcomes) PublishOutcomeCursor(from, through uint64) error { f.published++; return nil }
func (f *fakeOutcomes) OwnFingerprint() (string, error)                 { return f.self, nil }

// .
// .
// .
// .
func TestARecordWithNoFingerprintIsAskedNothing(t *testing.T) {
	model, door, src := &countingLLM{}, &captureDoor{}, &fakeOutcomes{self: ""}
	c := NewConsolidate(nil, model, door, nil, ConsolidateConfig{})
	c.SetOutcomes(src)
	err := c.ObserveOutcomes(context.Background())
	if err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("got %v, want a refusal that names the missing fingerprint", err)
	}
	if model.calls != 0 || len(door.payloads) != 0 || src.published != 0 {
		t.Fatalf("refused, yet the model was called %d times, %d records were minted and the cursor was published %d times",
			model.calls, len(door.payloads), src.published)
	}
}

// .
// .
func TestWithNoDoorTheIntakeAsksNothing(t *testing.T) {
	model, src := &countingLLM{}, &fakeOutcomes{self: strings.Repeat("cd", 32)}
	c := NewConsolidate(nil, model, nil, nil, ConsolidateConfig{})
	c.SetOutcomes(src)
	if err := c.ObserveOutcomes(context.Background()); err == nil {
		t.Fatal("an intake with no ledger door reported a product")
	}
	if model.calls != 0 || src.published != 0 {
		t.Fatalf("with no door the model was called %d times and the cursor published %d times", model.calls, src.published)
	}
}
