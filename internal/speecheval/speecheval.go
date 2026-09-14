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
// .
// .
// .
// .
// .
package speecheval

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

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
var contractions = map[string]string{
	"don't": "do not", "won't": "will not", "can't": "cannot",
	"isn't": "is not", "aren't": "are not", "wasn't": "was not",
	"weren't": "were not", "doesn't": "does not", "didn't": "did not",
	"couldn't": "could not", "shouldn't": "should not",
	"wouldn't": "would not", "haven't": "have not", "hasn't": "has not",
	"it's": "it is", "that's": "that is", "there's": "there is",
	"what's": "what is", "here's": "here is", "he's": "he is",
	"she's": "she is", "who's": "who is",
	"i'm": "i am", "i've": "i have", "i'll": "i will", "i'd": "i would",
	"we're": "we are", "we've": "we have", "we'll": "we will",
	"they're": "they are", "they've": "they have", "they'll": "they will",
	"you're": "you are", "you've": "you have", "you'll": "you will",
	"let's": "let us",
}

// .
// .
// .
// .
var digitWords = map[rune]string{
	'0': "zero", '1': "one", '2': "two", '3': "three", '4': "four",
	'5': "five", '6': "six", '7': "seven", '8': "eight", '9': "nine",
}

// .
// .
// .
func Normalize(s string) []string {
	s = strings.ToLower(s)

	// .
	// .
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r):
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
			// .
			b.WriteRune(r)
		case r == '\'' || r == '’':
			b.WriteRune('\'')
		default:
			b.WriteRune(' ')
		}
	}

	out := make([]string, 0, 16)
	for _, w := range strings.Fields(b.String()) {
		if exp, ok := contractions[w]; ok {
			out = append(out, strings.Fields(exp)...)
			continue
		}
		w = strings.ReplaceAll(w, "'", "")
		// .
		if isAllDigits(w) {
			for _, r := range w {
				if word := digitWords[r]; word != "" {
					out = append(out, word)
				}
			}
			continue
		}
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// .
// .
// .
// .
// .
// .
// .
// .
func isAllDigits(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// .
type Edits struct {
	Sub, Del, Ins, RefWords int
}

// .
// .
// .
func (e Edits) Rate() (float64, bool) {
	if e.RefWords == 0 {
		return 0, false
	}
	return float64(e.Sub+e.Del+e.Ins) / float64(e.RefWords), true
}

// .
func Align(ref, hyp []string) Edits {
	n, m := len(ref), len(hyp)
	// .
	type cell struct{ cost, sub, del, ins int }
	prev := make([]cell, m+1)
	cur := make([]cell, m+1)
	for j := 1; j <= m; j++ {
		prev[j] = cell{j, 0, 0, j}
	}
	for i := 1; i <= n; i++ {
		cur[0] = cell{i, 0, i, 0}
		for j := 1; j <= m; j++ {
			if ref[i-1] == hyp[j-1] {
				cur[j] = prev[j-1]
				cur[j].cost = prev[j-1].cost
				continue
			}
			s := prev[j-1]
			s.cost++
			s.sub++
			d := prev[j]
			d.cost++
			d.del++
			in := cur[j-1]
			in.cost++
			in.ins++
			best := s
			if d.cost < best.cost {
				best = d
			}
			if in.cost < best.cost {
				best = in
			}
			cur[j] = best
		}
		prev, cur = cur, prev
	}
	e := prev[m]
	return Edits{Sub: e.sub, Del: e.del, Ins: e.ins, RefWords: n}
}

// .
// .
func TermCount(tokens []string, term []string) int {
	if len(term) == 0 || len(tokens) < len(term) {
		return 0
	}
	n := 0
	for i := 0; i+len(term) <= len(tokens); i++ {
		match := true
		for j := range term {
			if tokens[i+j] != term[j] {
				match = false
				break
			}
		}
		if match {
			n++
			i += len(term) - 1
		}
	}
	return n
}

// .
type TermScore struct {
	Term   string
	InRef  int
	InHyp  int
	Recall float64
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func ScoreTerms(vocab []string, refs, hyps [][]string) []TermScore {
	out := make([]TermScore, 0, len(vocab))
	for _, raw := range vocab {
		term := Normalize(raw)
		if len(term) == 0 {
			continue
		}
		ts := TermScore{Term: raw}
		for i := range refs {
			r := TermCount(refs[i], term)
			if r == 0 {
				continue
			}
			ts.InRef += r
			h := TermCount(hyps[i], term)
			if h > r {
				h = r
			}
			ts.InHyp += h
		}
		if ts.InRef > 0 {
			ts.Recall = float64(ts.InHyp) / float64(ts.InRef)
		}
		out = append(out, ts)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Recall != out[j].Recall {
			return out[i].Recall < out[j].Recall
		}
		return out[i].Term < out[j].Term
	})
	return out
}

// .
// .
func Percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	if p <= 0 {
		return s[0]
	}
	if p >= 1 {
		return s[len(s)-1]
	}
	rank := int(float64(len(s))*p+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}

// .
func FormatPct(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }
