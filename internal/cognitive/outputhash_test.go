package cognitive

import "testing"

// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
const (
	collidedA = "glbvs"
	collidedB = "yacxa"
)

func TestContentThatCollidedUnderTheOldHashNoLongerDoes(t *testing.T) {
	if outputHash(collidedA) == outputHash(collidedB) {
		t.Fatalf("%q and %q still share an id suffix — one durable row overwrites the other",
			collidedA, collidedB)
	}
}

// .
// .
func TestTheIdSuffixIsWideEnoughToBeUncollidable(t *testing.T) {
	h := outputHash("a dream about the ledger")
	if len(h) != 32 {
		t.Fatalf("id suffix is %d hex chars (%d bits) — 32-bit suffixes are brute-forceable by anyone who can get text in front of the identity", len(h), len(h)*4)
	}
	for _, c := range h {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("id suffix is not lowercase hex: %q", h)
		}
	}
}

// .
// .
func TestTheSameContentAlwaysNamesTheSameRow(t *testing.T) {
	const content = "the outbox and the ledger disagree"
	// .

	//lint:ignore SA4000 determinism check, not a mistaken comparison
	if outputHash(content) != outputHash(content) {
		t.Fatal("outputHash is not deterministic")
	}
	if outputHash(content) == outputHash(content+" ") {
		t.Fatal("a trailing space produced the same id — the hash is not reading all of its input")
	}
}
