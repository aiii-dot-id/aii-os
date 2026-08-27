package cognitive

import "testing"

// outputHash names DURABLE ROWS: "exp_dream_"+hash for experiences,
// "belief_"+hash for beliefs. Experiences are written INSERT OR REPLACE
// and beliefs ON CONFLICT DO UPDATE SET statement — so two contents
// sharing a suffix do not collide harmlessly, they OVERWRITE. One dream
// note replaces another; one belief's statement becomes a different
// belief's.

// A real FNV-1a 32-bit collision: both of these hashed to 0xa1bc9a4f
// under the function this replaced. Found by brute force in ~11M
// candidates, which is the point — 32 bits is not a search, it is an
// afternoon.
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

// 128 bits, so the suffix is wide enough that neither a birthday
// collision over an identity's lifetime nor a crafted one is reachable.
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

// Stability is the whole reason it is a hash and not a counter: the same
// content must name the same row across runs and across replay.
func TestTheSameContentAlwaysNamesTheSameRow(t *testing.T) {
	const content = "the outbox and the ledger disagree"
	if outputHash(content) != outputHash(content) {
		t.Fatal("outputHash is not deterministic")
	}
	if outputHash(content) == outputHash(content+" ") {
		t.Fatal("a trailing space produced the same id — the hash is not reading all of its input")
	}
}
