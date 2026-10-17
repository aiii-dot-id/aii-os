package store

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestLedgerInspectionReferencesKeepCanonicalHeadersAndPaging(t *testing.T) {
	r := newOutcomeRig(t)
	want := map[uint64]ledger.Citation{}
	for i := 0; i < 7; i++ {
		evt := r.write(ledger.EventExperienceCreate, map[string]any{"id": fmt.Sprintf("inspection_%d", i), "content": "inspection-marker " + strings.Repeat("界Ω", 200)})
		want[evt.Seq] = ledger.Citation{Identity: r.kp.Fingerprint(), Seq: evt.Seq, EntryHash: evt.EntryHash()}
	}
	before := uint64(math.MaxInt64)
	seen := map[uint64]bool{}
	for {
		rows, total, err := r.st.SearchLedgerMirror("inspection-marker", before, 3)
		if err != nil {
			t.Fatal(err)
		}
		if total != int(r.lg.LastSeq()) || len(rows) > 3 {
			t.Fatalf("paging/count changed: total=%d rows=%d", total, len(rows))
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if row.Seq >= before || seen[row.Seq] {
				t.Fatal("cursor repeated or reordered a record")
			}
			if row.CitationError != nil || row.Citation != want[row.Seq] {
				t.Fatalf("wrong header reference at %d: %+v %v", row.Seq, row.Citation, row.CitationError)
			}
			if utf8.RuneCountInString(row.Payload) > 200 {
				t.Fatal("reference widened the payload excerpt")
			}
			seen[row.Seq] = true
			before = row.Seq
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("inspection lost rows: %d want %d", len(seen), len(want))
	}
}
