package store

import (
	"strings"
	"testing"
)

// .
// .
// .
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
	put("in1", "p/m", []byte{9, 9, 9})
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
	// .
	put("in2", "p/m2", []byte{1, 1, 1})
	if n, err := s.DropMemoryVectorsOffBasis("p/m2"); err != nil || n != 1 {
		t.Fatalf("drop off basis = %d %v, want 1 (in1 under p/m; in2 was replaced under p/m2)", n, err)
	}
	if n, _ := s.MemoryVectorCount("inbound", "p/m"); n != 0 {
		t.Fatalf("old-basis rows remain: %d", n)
	}
	// .
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
