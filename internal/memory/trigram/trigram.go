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
// .
// .
// .
// .
// .
// .
package trigram

import (
	"sort"
	"strings"
	"unicode"
)

// .
func Trigrams(s string) []string {
	set := trigramSet(s)
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func trigramSet(s string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, word := range words(s) {
		padded := []rune("  " + word + " ")
		for i := 0; i+3 <= len(padded); i++ {
			set[string(padded[i:i+3])] = struct{}{}
		}
	}
	return set
}

// .
func words(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return out
}

// .
// .
func Similarity(a, b string) float64 {
	sa, sb := trigramSet(a), trigramSet(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0
	}
	shared := 0
	for t := range sa {
		if _, ok := sb[t]; ok {
			shared++
		}
	}
	union := len(sa) + len(sb) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

// .
// .
// .
// .
func Windows(word string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range words(word) {
		r := []rune(w)
		for i := 0; i+3 <= len(r); i++ {
			t := string(r[i : i+3])
			if seen[t] {
				continue
			}
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// .
// .
// .
func Coverage(query, text string) float64 {
	q := Windows(query)
	if len(q) == 0 {
		return 0
	}
	have := map[string]bool{}
	for _, w := range Windows(text) {
		have[w] = true
	}
	n := 0
	for _, w := range q {
		if have[w] {
			n++
		}
	}
	return float64(n) / float64(len(q))
}

// .
// .
// .
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// .
// .
// .
// .
func Edits(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
