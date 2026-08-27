package store

import (
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// ReplayAll rebuilds all projections from the ledger.
// This is the f(ledger) = projections function.
//
// Runs at every startup (fresh or healed projection from the chain).
// Delete order is CHILDREN FIRST, ledger mirror LAST: projections
// FK-reference ledger(seq), and foreign keys are now genuinely enforced
// per connection (finding 7, 2026-08-17 review) — the old clear-ledger-
// first order only worked because the FK pragma never reached most
// pooled connections.
// ONE transaction end to end — canon PROJECTION.md, Publication and
// recovery requirements: "Readers observe either the complete prior
// projection or the complete verified replacement, never a mixture."
// Commit publishes only a fully rebuilt candidate; ANY failure rolls
// back and the prior admitted projection stands untouched. The pre-fix
// shape autocommitted the destructive clears and each event separately,
// so a rebuild that failed mid-way had already destroyed the prior
// projection and published a partial one (external claim H2, confirmed).
func (s *Store) ReplayAll(events []ledger.Event) (retErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin replay transaction: %w", err)
	}
	s.txh = tx
	committed := false
	defer func() {
		s.txh = nil
		if !committed {
			if err := tx.Rollback(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("rollback projection rebuild: %w", err))
			} else if retErr != nil {
				retErr = fmt.Errorf("%w (rebuild rolled back — the prior projection stands)", retErr)
			}
		}
	}()

	// Clear ONLY f(ledger) projection tables. Store-only tables —
	// conversations, work_sessions, outbox, alarms — are NOT projections:
	// they have no producing ledger events, so clearing them destroys
	// operational state nothing can rebuild. (Sandbox-test finding: every
	// restart erased the transcript because replay cleared conversations
	// and the ledger could not restore it.)
	//
	// witness_receipts and commitments ARE projections (f(ledger): every
	// row comes from a materialized event) and MUST be cleared — replay
	// runs at every startup, and materializeSystemWitnessed plain-INSERTs,
	// so leaving old rows duplicated every receipt on each boot
	// (2026-08-17 review). witness_identity is NOT f(ledger) (the stable
	// synthesized envelope) — never clear it.
	childrenFirst := []string{
		"witness_receipts", "trust_epochs", "commitments", "edges", "intentions",
		"experiences", "self_model_synthesis", "beliefs", "relationships",
	}
	for _, table := range childrenFirst {
		if _, err := tx.Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	// The mirror goes last — the projections referencing it are gone.
	if _, err := tx.Exec("DELETE FROM ledger"); err != nil {
		return fmt.Errorf("clear ledger mirror: %w", err)
	}

	// Re-materialize all events INSIDE the same transaction (pure
	// f(ledger): replay mode skips live cross-checks by design).
	for i := range events {
		if err := s.materializeLocked(&events[i], true); err != nil {
			return fmt.Errorf("materialize failed at seq %d: %w", events[i].Seq, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("publish rebuilt projection: %w", err)
	}
	committed = true
	return nil
}

// ReplayFromFile reads the ledger JSONL and rebuilds all projections.
func (s *Store) ReplayFromFile(ledgerPath string) error {
	events, err := ledger.ReadAll(ledgerPath)
	if err != nil {
		return fmt.Errorf("read ledger: %w", err)
	}
	return s.ReplayAll(events)
}
