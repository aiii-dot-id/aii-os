package cognitive

import (
	"context"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// Facility is the shared interface for all five silent facilities.
// Each runs autonomously — the resident never sees counts, backlogs, or chores.
type Facility interface {
	Name() string
	Predicate(ctx context.Context) bool // Should I run? (capacity-gated, never idle-gated R29)
	Execute(ctx context.Context) error  // Run the facility, append results to ledger
}

// evidenceText renders one experience for a facility prompt.
//
// R49: external/plugin output enters the prompt labeled as
// external/untrusted — "for a system whose thesis is 'the prompt IS
// the identity,' unlabeled foreign text is an injection into the
// self." identity/note.go earns the label at write (external is set
// only after the engine confirms the fetch really happened; a
// fabricated citation fails closed), so it is unforgeable evidence,
// not decoration.
//
// The resident's own substrate — self, dream, system — is wrapped in
// nothing. A marker on everything marks nothing.
func evidenceText(e store.Experience) string {
	if e.Provenance != "external" {
		return e.Content
	}
	// The wrapping lives in internal/untrusted — one owner, because this
	// invariant had two implementations and the other one was forgeable.
	return untrusted.Wrap("", e.Content)
}

// AuthoritySource supplies Ring 0, Ring 5, Ring 1, derived Ring 2, and
// the current Ring 3 self-model to every LLM facility.
type AuthoritySource interface {
	AuthorityPreamble() (string, error)
}

// withPreamble prepends the authority rings to a facility prompt
// (nil-safe: no source = the prompt as-is, tests unchanged).
func withPreamble(src AuthoritySource, base string) (string, error) {
	if src == nil {
		return base, nil
	}
	pre, err := src.AuthorityPreamble()
	if err != nil {
		return "", err
	}
	if pre == "" {
		return base, nil
	}
	return pre + "\n\n---\n\n" + base, nil
}
