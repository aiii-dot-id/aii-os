// Package speecheval measures a transcription endpoint against a frozen
// corpus.
//
// IT EXISTS SO THE ACCEPTANCE BAR IS A THING THAT RUNS. A contract
// written in prose is agreed to and then remembered differently by
// everyone; a contract that computes a number and exits nonzero is not.
// The voice arc that preceded this shipped green twice on tests that
// proved components worked, so the rule here is that every threshold in
// docs/VOICE_EVAL_CONTRACT.md has a function under it and a known-answer
// test under that.
//
// WHAT IS HERE AND WHAT IS NOT. This package SCORES results it is
// given: it reads a frozen manifest, aligns transcripts, and decides
// whether a set of results clears the contract. It does not send audio
// anywhere, and it does not choose or tune a model.
//
// The runner that reads clips off disk and sends them lands with the
// endpoint in step 4, and must send them through the SAME
// internal/speech client the identity uses — so that a multipart
// dialect the client gets wrong fails the evaluation too, rather than
// passing an evaluation that speaks a dialect of its own.
package speecheval

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// contractions is expanded on BOTH sides before scoring.
//
// A speaker says "don't" and a model may write "do not". Counting that
// as one substitution plus one insertion measures the tokenizer, not the
// hearing.
//
// ONLY APOSTROPHE FORMS ARE LISTED, AND THAT IS A CORRECTNESS RULE
// RATHER THAN AN OMISSION. The apostrophe-stripped spellings collide
// with ordinary English: "were", "well", "its", "ill", "lets", "cant"
// and "wont" are all real words, so a map that also expanded those
// would rewrite "we were there" into "we we are there" and quietly
// corrupt every reference containing them. A model that drops
// apostrophes entirely pays a small tokenizer penalty here; that is the
// cheaper error, and it is visible in the report rather than silent in
// the normalizer.
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

// digitWords spells numerals out, because that is how they are SPOKEN.
// This domain is full of ports and addresses — "8180" is said "eight one
// eight zero" and a reference that writes it as digits would score every
// correct hearing as four errors.
var digitWords = map[rune]string{
	'0': "zero", '1': "one", '2': "two", '3': "three", '4': "four",
	'5': "five", '6': "six", '7': "seven", '8': "eight", '9': "nine",
}

// Normalize reduces a transcript to the comparable form defined in
// docs/VOICE_EVAL_CONTRACT.md. The steps are ordered and each one is
// listed there, so a score can always be traced back to a rule.
func Normalize(s string) []string {
	s = strings.ToLower(s)

	// Punctuation to space, EXCEPT the apostrophe, which is still
	// carrying contraction meaning at this point.
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r):
			// Nonspacing marks stay ATTACHED to the letter they modify,
			// so a decomposed "e"+accent stays one word instead of
			// becoming a bare letter and a word break.
			//
			// THIS DOES NOT MAKE COMPOSED AND DECOMPOSED EQUAL, and an
			// earlier comment here claimed it did. Precomposed "é" and
			// "e"+U+0301 remain different token sequences and will
			// score as an error against each other. Real Unicode
			// folding means NFC/NFKC, which means golang.org/x/text —
			// a new module dependency in an identity runtime that
			// currently has none, taken on for an evaluation harness.
			//
			// So the pilot corpus is ASCII, that limit is written down
			// in docs/VOICE_EVAL_CONTRACT.md rather than assumed, and
			// this stays a known bound rather than a silent one. If the
			// corpus ever needs accented ground truth, take the
			// dependency then — deliberately.
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
		// A run of digits becomes one word per digit.
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

// isAllDigits is ASCII-only, and that is load-bearing rather than lazy.
//
// unicode.IsDigit admits Devanagari, Arabic-Indic and a dozen other
// digit families. digitWords is keyed by ASCII runes, so a Bengali
// numeral satisfied the old test, missed the map, and appended the
// ZERO VALUE — an empty string, entering the token stream as a word
// that is not there and shifting every alignment after it. A scorer
// that invents tokens is worse than one that refuses characters.
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

// Edits is one alignment's error breakdown.
type Edits struct {
	Sub, Del, Ins, RefWords int
}

// Rate is (S+D+I)/N. A reference with no words has no rate — that is
// the hallucination measurement's job, not this one, and returning 0
// here would let a non-speech clip flatter the corpus average.
func (e Edits) Rate() (float64, bool) {
	if e.RefWords == 0 {
		return 0, false
	}
	return float64(e.Sub+e.Del+e.Ins) / float64(e.RefWords), true
}

// Align computes the standard Levenshtein alignment over words.
func Align(ref, hyp []string) Edits {
	n, m := len(ref), len(hyp)
	// d[i][j] = cost, plus the counts that produced it.
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
			s := prev[j-1] // substitute
			s.cost++
			s.sub++
			d := prev[j] // delete (ref word missing from hyp)
			d.cost++
			d.del++
			in := cur[j-1] // insert (hyp word not in ref)
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

// TermCount counts occurrences of a (possibly multi-word) term in an
// already-normalized token stream.
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

// TermScore is one domain term's showing across the corpus.
type TermScore struct {
	Term   string
	InRef  int
	InHyp  int
	Recall float64
}

// ScoreTerms reports, per term, how many times it was SAID and how many
// of those the endpoint produced.
//
// POSITION-FREE, AND THAT IS THE POINT. A term recovered a word late is
// recovered; this metric answers "does this endpoint know the word at
// all", which is the question that decides whether an endpoint is usable
// here. Overall WER cannot answer it: an endpoint can score well while
// missing every proper noun in the system, and those are exactly the
// words an operator says when something is wrong.
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
				h = r // credit is capped by what was actually said
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

// Percentile returns the p-th percentile (0..1) by nearest-rank. Sorting
// happens here rather than at the caller so a caller cannot forget.
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

// FormatPct renders a rate for the report.
func FormatPct(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }
