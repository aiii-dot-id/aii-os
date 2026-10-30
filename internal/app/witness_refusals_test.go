package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// witness_refusals_test.go — the witness's refusals asking again cannot
// answer, as the running identity meets them: context_required, and
// key_already_enrolled where nothing is adopted (the witness design,
// revision 8; witness.Anchorer.StandingRefusal). And the minter's last door
// against a receipt in a later version's form.

// A WITNESS REFUSAL THAT STANDS TELLS THE OPERATOR AND ASKS NO MORE. The
// witness answers with context_required, or with key_already_enrolled and
// no registered envelope to adopt on: the identity is not SAFE, the
// operator is told once in the words for that refusal, and no later pass of
// the run sends the witness a bookmark.
func TestAWitnessRefusalThatStandsTellsTheOperatorAndAsksNoMore(t *testing.T) {
	for _, c := range []struct {
		code, message, prefix, said string
	}{
		{"context_required", "witness context is required for first enrolment", "witness_context_required_", witnessContextRequiredSaid},
		{"key_already_enrolled", "identity signing key is already enrolled", "witness_key_enrolled_", witnessKeyEnrolledSaid},
	} {
		t.Run(c.code, func(t *testing.T) {
			f := newSealedFixture(t)
			a := New(f.cfg)
			if err := startLiveForTest(a); err != nil {
				t.Fatal(err)
			}
			defer stopApp(t, a)
			if err := a.anchorer.CheckAndAnchor(); err != nil {
				t.Fatalf("the first anchor: %v", err)
			}
			f.witness.RefusesWithCode(409, c.code, c.message)
			appendFixtureEvent(t, a, f.keyPath, "exp_before_the_refusal")
			err := a.anchorer.CheckAndAnchor()
			var ce *witness.ConflictError
			if !errors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("want the witness's %s, got: %v", c.code, err)
			}
			if reason, safe := a.SafeMode(); safe {
				t.Fatalf("A WITNESS'S %s PUT THE IDENTITY IN SAFE: %s", c.code, reason)
			}
			ids, err := a.store.OutboxIDsUnder(c.prefix)
			if err != nil || len(ids) != 1 {
				t.Fatalf("THE OPERATOR WAS NOT TOLD OF THE WITNESS'S %s: %q %v", c.code, ids, err)
			}
			if said, _ := a.store.OutboxContent(ids[0]); !strings.HasPrefix(said, fmt.Sprintf(c.said, f.cfg.Witness.URL)) {
				t.Fatalf("the notice is not the one for %s: %q", c.code, said)
			}
			asked := f.witness.BookmarksSeen()
			appendFixtureEvent(t, a, f.keyPath, "exp_after_the_refusal")
			if err := a.anchorer.CheckAndAnchor(); err == nil || f.witness.BookmarksSeen() != asked {
				t.Fatalf("a later pass of the run asked the witness again (%d → %d): %v", asked, f.witness.BookmarksSeen(), err)
			}
		})
	}
}

// AN ADOPTED ENROLMENT IS TOLD, once on a day, in the words for it.
func TestAnAdoptedEnrolmentIsToldToTheOperator(t *testing.T) {
	f := newSealedFixture(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	now := time.Now()
	a.tellWitnessEnrolmentAdopted(f.cfg.Witness.URL, "did:aiii:identity:sha256:registered", now)
	a.tellWitnessEnrolmentAdopted(f.cfg.Witness.URL, "did:aiii:identity:sha256:registered", now)
	ids, err := a.store.OutboxIDsUnder("witness_enrolment_adopted_")
	if err != nil || len(ids) != 1 {
		t.Fatalf("the adoption is not told once: %q %v", ids, err)
	}
	if said, _ := a.store.OutboxContent(ids[0]); said != fmt.Sprintf(witnessEnrolmentAdoptedSaid, f.cfg.Witness.URL, "did:aiii:identity:sha256:registered") {
		t.Fatalf("the notice is not the one for an adopted enrolment: %q", said)
	}
}
