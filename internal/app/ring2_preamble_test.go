package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestTheFacilitiesPreamblePrintsRing2ToTheStoresBound(t *testing.T) {
	projection, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projection.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := projection.DB().Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for seq := 1; seq <= 3; seq++ {
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', '2026-10-13T00:00:00Z', 'test.row', 3, '{}', 'h')`, seq)
	}
	note := strings.Repeat("The heron waits at the ford. ", 20)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, first_seq, last_seq) VALUES ('b_two', 'I wait before I act', 2, 0.8, 1, 1)`)
	exec(`INSERT INTO experiences (id, content, provenance, created_seq, created_at) VALUES ('x_long', ?, 'external', 2, '2026-10-13T00:00:00Z')`, note)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('edge_x_long', 'x_long', 'b_two', 'SUPPORTS', 3)`)

	rings := ring.NewManager()
	_ = rings.SealSafePosture("constitution")
	rings.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "boundary"})
	projection.SetRing2EvidenceChars(120)
	authority, err := (ringAuthority{gate: prompt.NewGate(appRingSource{rm: rings}, 32000), st: projection}).AuthorityPreamble()
	if err != nil {
		t.Fatal(err)
	}
	want := "\n  - " + string([]rune(note)[:120]) + "… [120 of 580 characters; whole: recall source=experiences by its words] via SUPPORTS, provenance=external [x_long]"
	if !strings.Contains(authority, want) {
		t.Fatalf("the facilities' preamble does not print the source to the bound the store carries (120); want the line%s\nin\n%s", want, authority)
	}
}
