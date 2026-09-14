package fuse

import (
	"math"
	"reflect"
	"testing"
)

func key(store, id string) Key { return Key{Store: store, ID: id} }

// .
// .
func TestAgreementOutranksASingleFirstPlace(t *testing.T) {
	exact := Pool{Layer: "exact_words", Keys: []Key{key("experiences", "a"), key("experiences", "b")}}
	fuzzy := Pool{Layer: "fuzzy", Keys: []Key{key("experiences", "c"), key("experiences", "b")}}
	got := RRF(K, exact, fuzzy)
	if len(got) != 3 {
		t.Fatalf("fused %d keys, want 3: %+v", len(got), got)
	}
	if got[0].Key != key("experiences", "b") {
		t.Fatalf("the key both layers ranked must lead: %+v", got)
	}
	want := 1/(K+2) + 1/(K+2)
	if math.Abs(got[0].Score-want) > 1e-12 {
		t.Errorf("score of b = %v, want %v", got[0].Score, want)
	}
	if !reflect.DeepEqual(got[0].Layers, []string{"exact_words", "fuzzy"}) {
		t.Errorf("layers of b = %v", got[0].Layers)
	}
	if got[0].BestRank != 2 {
		t.Errorf("best rank of b = %d, want 2", got[0].BestRank)
	}
	// .
	if got[1].Key != key("experiences", "a") || got[2].Key != key("experiences", "c") {
		t.Errorf("ties must break on store then id: %+v", got[1:])
	}
	if got[1].Score != got[2].Score {
		t.Errorf("a and c must tie: %v vs %v", got[1].Score, got[2].Score)
	}
}

func TestTheOrderIsTheSameTwice(t *testing.T) {
	pools := []Pool{
		{Layer: "exact_words", Keys: []Key{key("beliefs", "x"), key("experiences", "x"), key("beliefs", "y")}},
		{Layer: "fuzzy", Keys: []Key{key("experiences", "x"), key("beliefs", "x")}},
	}
	first := RRF(K, pools...)
	for i := 0; i < 20; i++ {
		if again := RRF(K, pools...); !reflect.DeepEqual(again, first) {
			t.Fatalf("run %d differed:\n%+v\n%+v", i, again, first)
		}
	}
}

func TestARepeatWithinOnePoolCountsOnce(t *testing.T) {
	p := Pool{Layer: "exact_words", Keys: []Key{key("s", "a"), key("s", "a"), key("s", "a")}}
	got := RRF(K, p)
	if len(got) != 1 || math.Abs(got[0].Score-1/(K+1)) > 1e-12 {
		t.Fatalf("a repeated key must count once at its first rank: %+v", got)
	}
}

func TestEmptyAndDefaultK(t *testing.T) {
	if got := RRF(K); len(got) != 0 {
		t.Fatalf("no pools must fuse to nothing: %+v", got)
	}
	if got := RRF(0, Pool{Layer: "l", Keys: []Key{key("s", "a")}}); math.Abs(got[0].Score-1/(K+1)) > 1e-12 {
		t.Fatalf("a non-positive k must fall back to the default: %+v", got)
	}
}
