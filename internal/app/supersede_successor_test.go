package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestASupersedeNamingNoBeliefIsRefusedByName(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	st := newAdmissionStore(t, dir)
	signed := 0
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st,
		appendPrepared: func(et ledger.EventType, r int, p ledger.PreparedPayload, k *crypto.KeyPair) (*ledger.Event, error) {
			signed++
			return lg.AppendPrepared(et, r, p, k)
		}}
	engine := identity.NewEngine(st, door, ring.NewManager(), nil, identity.Ports{})
	commit := func(args map[string]any) error {
		t.Helper()
		_, err := engine.ExecuteAction(context.Background(), "verb", "commit", args)
		return err
	}
	if _, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]any{"id": "exp_origin", "content": "the ferry left at dawn", "provenance": "self"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := commit(map[string]any{
		"variant": "belief.upsert", "id": "b_dawn", "statement": "The ferry leaves at dawn", "evidence_refs": "exp_origin",
	}); err != nil {
		t.Fatal(err)
	}
	record := func() string {
		t.Helper()
		raw, err := os.ReadFile(lg.Path())
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("seq %d\n%s\n%s", lg.LastSeq(), raw, projectionRows(t, st.DB(), "ledger", "beliefs", "edges"))
	}
	before, signedBefore := record(), signed
	if !strings.Contains(before, "beliefs [b_dawn") || !strings.Contains(before, "edges [") {
		t.Fatalf("inconclusive: the projection holds no belief and edge to compare:\n%s", before)
	}

	err := commit(map[string]any{
		"variant": "belief.supersede", "old_id": "b_dawn", "new_id": "b_ghost", "reason": "the timetable changed",
	})
	var missing *store.MissingEventTargetError
	if !errors.Is(err, store.ErrRefused) || !errors.As(err, &missing) || missing.Kind != "belief" || missing.ID != "b_ghost" {
		t.Fatalf("a supersede naming a successor the record does not hold was not refused by name: %v", err)
	}
	if signed != signedBefore {
		t.Fatalf("the door signed and appended a supersede it should have refused")
	}
	if after := record(); after != before {
		t.Fatalf("a refused supersede changed the record or its projection:\nbefore %s\nafter  %s", before, after)
	}

	err = commit(map[string]any{
		"variant": "belief.supersede", "old_id": "b_dawn", "new_id": "b_dawn", "reason": "restated",
	})
	if !errors.Is(err, store.ErrRefused) || !strings.Contains(err.Error(), "its own successor") {
		t.Fatalf("a belief superseding itself was not refused at the door: %v", err)
	}
	if signed != signedBefore {
		t.Fatalf("the door signed a self-supersession it should have refused")
	}
	if after := record(); after != before {
		t.Fatalf("a refused self-supersession changed the record or its projection:\nbefore %s\nafter  %s", before, after)
	}
}

// projectionRows is every row of the named tables, in key order.
func projectionRows(t *testing.T, db *sql.DB, tables ...string) string {
	t.Helper()
	var b strings.Builder
	for _, table := range tables {
		rows, err := db.Query("SELECT * FROM " + table + " ORDER BY 1")
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %v\n", table, vals)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}
