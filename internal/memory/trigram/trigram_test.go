package trigram

import (
	"math"
	"reflect"
	"testing"
)

// .
// .
// .
// .
func TestSimilarityMatchesThePgTrgmParityCorpus(t *testing.T) {
	corpus := []struct {
		a, b string
		want float64
	}{
		{"cat", "cat", 1.0},
		{"", "", 0},
		{"", "hello", 0},
		{"foo", "bar", 0},
		{"pizza", "xyzabc", 0},
		{"cat", "cats", 0.5},
		{"apple", "apples", 0.625},
		{"WORD", "word", 1.0},
		{"hello", "hello world", 0.5},
		{"CAT FISH", "cat fish", 1.0},
		{"foo-bar", "foo bar", 1.0},
		{"The quick brown fox", "the quick brown fox", 1.0},
	}
	for _, row := range corpus {
		got := Similarity(row.a, row.b)
		if math.Abs(got-row.want) > 0.001 {
			t.Errorf("Similarity(%q, %q) = %v, want %v", row.a, row.b, got, row.want)
		}
		if back := Similarity(row.b, row.a); back != got {
			t.Errorf("Similarity is not symmetric for %q/%q: %v vs %v", row.a, row.b, got, back)
		}
	}
}

func TestTrigramsFollowPgTrgmPadding(t *testing.T) {
	got := Trigrams("cat")
	want := []string{"  c", " ca", "at ", "cat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Trigrams(\"cat\") = %q, want %q", got, want)
	}
	// .
	// .
	if got := Trigrams("foo-bar"); !reflect.DeepEqual(got, Trigrams("foo bar")) {
		t.Errorf("punctuation must split words exactly as space does: %q", got)
	}
	// .
	if got := Trigrams("r2"); !reflect.DeepEqual(got, []string{"  r", " r2", "r2 "}) {
		t.Errorf("Trigrams(\"r2\") = %q", got)
	}
	// .
	if got := Trigrams("café"); !reflect.DeepEqual(got, []string{"  c", " ca", "afé", "caf", "fé "}) {
		t.Errorf("Trigrams(\"café\") = %q", got)
	}
}

// .
// .
// .
func TestWindowsAndCoverageServeTheTrigramSidecar(t *testing.T) {
	if got := Windows("Lighthouse"); !reflect.DeepEqual(got, []string{"lig", "igh", "ght", "hth", "tho", "hou", "ous", "use"}) {
		t.Errorf("Windows(Lighthouse) = %q", got)
	}
	if got := Windows("of"); len(got) != 0 {
		t.Errorf("a two-letter word has no windows: %q", got)
	}
	if got := Windows("aaaa"); !reflect.DeepEqual(got, []string{"aaa"}) {
		t.Errorf("repeated windows collapse: %q", got)
	}
	if got := Windows("foo bar"); !reflect.DeepEqual(got, []string{"foo", "bar"}) {
		t.Errorf("windows never span a word boundary: %q", got)
	}
	cases := []struct {
		query, text string
		want        float64
	}{
		{"lighthouse", "the lighthouse keeper", 1},
		{"keeper", "a lightweight housekeeper", 1},
		{"lighthouse", "the lihgthouse keeper", 0.5},
		{"lighthouse", "the lightmouse keeper", 5.0 / 8.0},
		{"lighthouse", "nothing alike", 0},
		{"of", "of course", 0},
		{"Kingfisher struck", "the kingfisher struck at dawn", 1},
	}
	for _, c := range cases {
		if got := Coverage(c.query, c.text); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Coverage(%q, %q) = %v, want %v", c.query, c.text, got, c.want)
		}
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"flaw", "lawn", 2},
		{"café", "cafe", 1},
		{"same", "same", 0},
	}
	for _, c := range cases {
		if got := Levenshtein(c.a, c.b); got != c.want {
			t.Errorf("Levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestEditsCountsATranspositionOnce(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"shoals", "sohals", 1},
		{"pilot", "pliot", 1},
		{"kitten", "sitting", 3},
		{"", "ab", 2},
		{"abcd", "badc", 2},
		{"café", "cafe", 1},
		{"same", "same", 0},
	}
	for _, c := range cases {
		got := Edits(c.a, c.b)
		if got != c.want {
			t.Errorf("Edits(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if Levenshtein(c.a, c.b) < got {
			t.Errorf("Edits(%q, %q) must never exceed Levenshtein", c.a, c.b)
		}
	}
}
