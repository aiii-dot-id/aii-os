package store

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

// The meaning layer's table (phase 2): vectors are written whole and
// replaced by key, a basis change drops what the old basis made, a
// pruned base row loses its vector, and a malformed row is refused.
func TestMemoryVectorsAreKeyedReplacedAndInvalidatedByBasis(t *testing.T) {
	s := testStore(t)
	if _, err := s.DB().Exec(`INSERT INTO inbound (id, channel, address, body, received_ms) VALUES ('in1', 'sms', 'x', 'the tide tables', 1), ('in2', 'sms', 'x', 'a quiet day', 2)`); err != nil {
		t.Fatal(err)
	}
	put := func(id, basis string, q []byte) {
		t.Helper()
		if err := s.PutMemoryVectors([]MemoryVector{{Store: "inbound", ID: id, Basis: basis, ContentSHA: "sha", Dims: len(q), Scale: 0.01, Q: q}}); err != nil {
			t.Fatal(err)
		}
	}
	put("in1", "p/m", []byte{1, 2, 3})
	put("in2", "p/m", []byte{3, 2, 1})
	put("in1", "p/m", []byte{9, 9, 9}) // replaced, not duplicated
	if n, _ := s.MemoryVectorCount("inbound", "p/m"); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	var q []byte
	if err := s.DB().QueryRow(`SELECT q FROM memory_vectors WHERE store = 'inbound' AND id = 'in1'`).Scan(&q); err != nil || q[0] != 9 {
		t.Fatalf("replacement did not land: %v %v", q, err)
	}
	if err := s.PutMemoryVectors([]MemoryVector{{Store: "inbound", ID: "in3", Basis: "p/m", Dims: 3, Scale: 0.01, Q: []byte{1}}}); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("a vector whose bytes are not its dims must be refused: %v", err)
	}
	// A new basis: the old rows are dropped as invalid.
	put("in2", "p/m2", []byte{1, 1, 1})
	if n, err := s.DropMemoryVectorsOffBasis("p/m2"); err != nil || n != 1 {
		t.Fatalf("drop off basis = %d %v, want 1 (in1 under p/m; in2 was replaced under p/m2)", n, err)
	}
	if n, _ := s.MemoryVectorCount("inbound", "p/m"); n != 0 {
		t.Fatalf("old-basis rows remain: %d", n)
	}
	// A pruned base row loses its vector.
	if _, err := s.DB().Exec(`DELETE FROM inbound WHERE id = 'in2'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneMemoryVectors("inbound", "inbound"); err != nil || n != 1 {
		t.Fatalf("prune = %d %v, want 1", n, err)
	}
	if _, err := s.PruneMemoryVectors("inbound", "inbound; DROP TABLE inbound"); err == nil {
		t.Fatal("a base name that is not an identifier must be refused")
	}
}

// WHAT THE KEPT VECTORS WERE MADE BY IS READ FROM THE VECTORS. Each basis
// is listed once, in order, whatever the number of rows and stores under
// it; an empty table lists none; and after a basis change's drop only the
// basis in use is left. The host reads this to know which source has been
// reading the record, so a basis must not be listed twice or missed.
func TestTheBasesOfTheKeptVectorsAreListedOnceEach(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	if bases, err := s.MemoryVectorBases(ctx); err != nil || len(bases) != 0 {
		t.Fatalf("an empty table: %v %v", bases, err)
	}
	for i, row := range [][2]string{{"inbound", "p/m"}, {"inbound", "p/m"}, {"experiences", "p/m"}, {"experiences", "org.example.vectors/abababababab/f1"}} {
		if err := s.PutMemoryVectors([]MemoryVector{{Store: row[0], ID: "v" + string(rune('0'+i)), Basis: row[1], ContentSHA: "sha", Dims: 2, Scale: 0.01, Q: []byte{1, 2}}}); err != nil {
			t.Fatal(err)
		}
	}
	bases, err := s.MemoryVectorBases(ctx)
	if err != nil || len(bases) != 2 || bases[0] != "org.example.vectors/abababababab/f1" || bases[1] != "p/m" {
		t.Fatalf("two bases over four rows and two stores: %v %v", bases, err)
	}
	if _, err := s.DropMemoryVectorsOffBasis("p/m"); err != nil {
		t.Fatal(err)
	}
	if bases, err := s.MemoryVectorBases(ctx); err != nil || len(bases) != 1 || bases[0] != "p/m" {
		t.Fatalf("after the drop only the basis in use is kept: %v %v", bases, err)
	}
}

func TestTheVectorFunctionsAreRegisteredAndProbed(t *testing.T) {
	s := testStore(t)
	var c float64
	if err := s.DB().QueryRow(`SELECT vec_cosine_q8(X'7F0000', 1.0/127, X'7F0000', 1.0/127)`).Scan(&c); err != nil || c < 0.999 || c > 1 {
		t.Fatalf("cosine of a vector with itself = %v (%v)", c, err)
	}
	if err := s.DB().QueryRow(`SELECT vec_cosine_q8(X'7F0000', 1.0/127, X'007F00', 1.0/127)`).Scan(&c); err != nil || c != 0 {
		t.Fatalf("orthogonal cosine = %v (%v)", c, err)
	}
	var h int
	if err := s.DB().QueryRow(`SELECT vec_hamming_q8(X'7F81', X'7F7F')`).Scan(&h); err != nil || h != 1 {
		t.Fatalf("hamming = %d (%v), want 1", h, err)
	}
	var d float64
	if err := s.DB().QueryRow(`SELECT vec_l2_q8(X'7F00', 1.0/127, X'007F', 1.0/127)`).Scan(&d); err != nil || d < 1.41 || d > 1.42 {
		t.Fatalf("l2 = %v (%v), want √2", d, err)
	}
	if err := s.DB().QueryRow(`SELECT vec_cosine_q8(X'7F', 1.0, X'7F00', 1.0)`).Scan(&c); err == nil {
		t.Fatal("a cosine across dimensions must be an error")
	}
	var null *float64
	if err := s.DB().QueryRow(`SELECT vec_cosine_q8(NULL, 1.0, X'7F', 1.0)`).Scan(&null); err != nil || null != nil {
		t.Fatalf("NULL in must be NULL out: %v %v", null, err)
	}
	if err := probeMemoryFunctions(s.DB()); err != nil {
		t.Fatalf("the probe must cover the vector functions: %v", err)
	}
}

func TestAVectorKeepsHowMuchOfItsTextItWasMadeFrom(t *testing.T) {
	s := testStore(t)
	if _, err := s.DB().Exec(`INSERT INTO inbound (id, channel, address, body, received_ms) VALUES ('in1', 'sms', 'x', 'the tide tables', 1), ('in2', 'sms', 'x', 'a quiet day', 2), ('in3', 'sms', 'x', 'a long letter', 3)`); err != nil {
		t.Fatal(err)
	}
	none, some := 0, 791
	if err := s.PutMemoryVectors([]MemoryVector{
		{Store: "inbound", ID: "in1", Basis: "p/m", ContentSHA: "sha", Dims: 1, Scale: 0.01, Q: []byte{1}, Unread: &none},
		{Store: "inbound", ID: "in2", Basis: "p/m", ContentSHA: "sha", Dims: 1, Scale: 0.01, Q: []byte{1}},
		{Store: "inbound", ID: "in3", Basis: "p/m", ContentSHA: "sha", Dims: 1, Scale: 0.01, Q: []byte{1}, Unread: &some},
	}); err != nil {
		t.Fatal(err)
	}
	said := func(id string) string {
		t.Helper()
		var unread sql.NullInt64
		if err := s.DB().QueryRow(`SELECT `+VectorUnread+` FROM memory_vectors v`+VectorUnreadJoin+` WHERE v.store = 'inbound' AND v.id = ?`, id).Scan(&unread); err != nil {
			t.Fatal(err)
		}
		if !unread.Valid {
			return "untold"
		}
		return strconv.FormatInt(unread.Int64, 10)
	}
	for id, want := range map[string]string{"in1": "0", "in2": "untold", "in3": "791"} {
		if got := said(id); got != want {
			t.Fatalf("%s: %s characters past what was read, want %s", id, got, want)
		}
	}
	under := -1
	if err := s.PutMemoryVectors([]MemoryVector{{Store: "inbound", ID: "in1", Basis: "p/m", ContentSHA: "sha", Dims: 1, Scale: 0.01, Q: []byte{1}, Unread: &under}}); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("fewer than no characters past what was read must be refused: %v", err)
	}

	// THE TABLE IS 0.1.23'S: its columns, with data in them, are the eight
	// it declares and no other.
	cols, err := s.DB().Query(`SELECT name FROM pragma_table_info('memory_vectors') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for cols.Next() {
		var n string
		if err := cols.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	cols.Close()
	if got, want := strings.Join(names, ","), "store,id,basis,content_sha,dims,scale,q,embedded_at"; got != want {
		t.Fatalf("MEMORY_VECTORS IS NOT AS 0.1.23 DECLARES IT, so 0.1.23 refuses this database once a count is held: columns %s, want %s", got, want)
	}
	var tables int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE '%unread%'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("a table of its own for the count is one 0.1.23 refuses: %d %v", tables, err)
	}

	// Made again with nothing said, written over by another program (a
	// vector of its own, another embedded_at), deleted: none says a count.
	if err := s.PutMemoryVectors([]MemoryVector{{Store: "inbound", ID: "in3", Basis: "p/m", ContentSHA: "sha", Dims: 1, Scale: 0.01, Q: []byte{1}}}); err != nil {
		t.Fatal(err)
	}
	if got := said("in3"); got != "untold" {
		t.Fatalf("a vector made again without a count says %s", got)
	}
	if _, err := s.DB().Exec(`INSERT OR REPLACE INTO memory_vectors (store, id, basis, content_sha, dims, scale, q, embedded_at) VALUES ('inbound', 'in1', 'p/m', 'sha', 1, 0.01, x'01', '2026-10-21T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if got := said("in1"); got != "untold" {
		t.Fatalf("A VECTOR ANOTHER PROGRAM WROTE OVER SAYS THE COUNT OF THE ONE BEFORE IT: %s", got)
	}
	if err := s.PutMemoryVectors([]MemoryVector{{Store: "inbound", ID: "in1", Basis: "p/m", ContentSHA: "sha", Dims: 1, Scale: 0.01, Q: []byte{1}, Unread: &some}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMemoryVectors("inbound", []string{"in1"}); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM runtime_meta WHERE key = ?`, VectorUnreadPrefix+"inbound/in1").Scan(&left); err != nil || left != 0 {
		t.Fatalf("a deleted vector left its count behind: %d %v", left, err)
	}
}
