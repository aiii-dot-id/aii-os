package store

import (
	"path/filepath"
	"testing"
)

func TestEveryReferenceToTheLedgerIsIndexed(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range append(DerivedTables(), EphemeralTables()...) {
		refs, err := s.db.Query(`SELECT "from" FROM pragma_foreign_key_list(?) WHERE "table" = 'ledger'`, table)
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for refs.Next() {
			var col string
			if err := refs.Scan(&col); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, col)
		}
		if err := refs.Err(); err != nil {
			t.Fatal(err)
		}
		refs.Close()
		for _, col := range cols {
			var n int
			if err := s.db.QueryRow(`SELECT count(*) FROM pragma_index_list(?) AS l, pragma_index_info(l.name) AS i
				WHERE l.partial = 0 AND i.seqno = 0 AND i.name = ?`, table, col).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				t.Errorf("%s.%s references ledger(seq) and no index leads with it: replay would scan %s for every ledger row", table, col, table)
			}
		}
	}
}
