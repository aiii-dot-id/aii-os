package speecheval

import (
	"fmt"
	"sort"
	"time"
)

// .
// .

// .
// .
// .
// .
// .
// .
type ClipResult struct {
	File     string
	Hyp      string
	Duration time.Duration
	Elapsed  time.Duration
	// .
	// .
	Err string
}

// .
type Report struct {
	Corpus         string
	Frozen         string
	ManifestSHA256 string

	// .
	// .
	// .
	Missing    []string
	Duplicated []string
	Unknown    []string

	Clips             int
	Failures          []string
	FailureRate       float64
	WER               float64
	WERByCondition    map[string]float64
	RefWords          int
	Terms             []TermScore
	Hallucinated      []string
	NonSpeechClips    int
	HallucinationRate float64
	RTFp50, RTFp95    float64
	SpeakerClips      map[string]int
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
// .
// .
// .
// .
// .
// .
// .
// .
// .
func Aggregate(m *Manifest, results []ClipResult) Report {
	r := Report{
		Corpus:         m.Corpus,
		Frozen:         m.Frozen,
		ManifestSHA256: m.SourceSHA256,
		Clips:          len(results),
		WERByCondition: map[string]float64{},
		SpeakerClips:   map[string]int{},
	}

	byFile := make(map[string]Clip, len(m.Clips))
	for _, c := range m.Clips {
		byFile[c.File] = c
	}
	count := map[string]int{}
	for _, res := range results {
		count[res.File]++
	}
	for file, n := range count {
		if _, known := byFile[file]; !known {
			r.Unknown = append(r.Unknown, file)
		} else if n > 1 {
			r.Duplicated = append(r.Duplicated, fmt.Sprintf("%s (%d results)", file, n))
		}
	}
	for _, c := range m.Clips {
		if count[c.File] == 0 {
			r.Missing = append(r.Missing, c.File)
		}
	}
	sort.Strings(r.Unknown)
	sort.Strings(r.Duplicated)
	sort.Strings(r.Missing)

	var totalEdits int
	editsBy := map[string]int{}
	wordsBy := map[string]int{}
	var refs, hyps [][]string
	var rtfs []float64

	for _, res := range results {
		clip, known := byFile[res.File]
		if !known {
			continue
		}
		if clip.Speaker != "" {
			r.SpeakerClips[clip.Speaker]++
		}
		if res.Err != "" {
			r.Failures = append(r.Failures, res.File+": "+res.Err)
			continue
		}
		if res.Duration > 0 {
			rtfs = append(rtfs, res.Elapsed.Seconds()/res.Duration.Seconds())
		}
		hyp := Normalize(res.Hyp)

		if !clip.Speech {
			r.NonSpeechClips++
			if len(hyp) > 0 {
				r.Hallucinated = append(r.Hallucinated,
					fmt.Sprintf("%s (%s): %q", clip.File, clip.Condition, res.Hyp))
			}
			continue
		}

		ref := Normalize(clip.Transcript)
		e := Align(ref, hyp)
		totalEdits += e.Sub + e.Del + e.Ins
		r.RefWords += e.RefWords
		editsBy[clip.Condition] += e.Sub + e.Del + e.Ins
		wordsBy[clip.Condition] += e.RefWords
		refs = append(refs, ref)
		hyps = append(hyps, hyp)
	}

	if len(results) > 0 {
		r.FailureRate = float64(len(r.Failures)) / float64(len(results))
	}
	if r.RefWords > 0 {
		r.WER = float64(totalEdits) / float64(r.RefWords)
	}
	for cond, words := range wordsBy {
		if words > 0 {
			r.WERByCondition[cond] = float64(editsBy[cond]) / float64(words)
		}
	}
	if r.NonSpeechClips > 0 {
		r.HallucinationRate = float64(len(r.Hallucinated)) / float64(r.NonSpeechClips)
	}
	r.Terms = ScoreTerms(m.Vocabulary, refs, hyps)
	r.RTFp50 = Percentile(rtfs, 0.5)
	r.RTFp95 = Percentile(rtfs, 0.95)
	return r
}

// .
// .
// .
// .
// .
// .
// .
func (r Report) Violations(c Contract) []string {
	var v []string

	// .
	// .
	if len(r.Missing) > 0 || len(r.Duplicated) > 0 || len(r.Unknown) > 0 {
		v = append(v, fmt.Sprintf(
			"INCOMPLETE RUN: %d clip(s) missing, %d repeated, %d not in the corpus — every score below describes a different corpus from the one that was frozen",
			len(r.Missing), len(r.Duplicated), len(r.Unknown)))
		for _, f := range r.Missing {
			v = append(v, "    never submitted: "+f)
		}
		for _, f := range r.Duplicated {
			v = append(v, "    submitted twice: "+f)
		}
		for _, f := range r.Unknown {
			v = append(v, "    not in the corpus: "+f)
		}
	}

	if r.HallucinationRate > c.MaxHallucination {
		v = append(v, fmt.Sprintf(
			"HALLUCINATION: %s of non-speech clips produced words (%d of %d), ceiling %s — these do not lower a score, they enter the record as things someone said",
			FormatPct(r.HallucinationRate), len(r.Hallucinated), r.NonSpeechClips, FormatPct(c.MaxHallucination)))
		for _, h := range r.Hallucinated {
			v = append(v, "    invented: "+h)
		}
	}

	// .
	// .
	// .
	var thin []TermScore
	for _, t := range r.Terms {
		if t.InRef < c.MinTermOccurrences {
			thin = append(thin, t)
		}
	}
	if len(thin) > 0 {
		v = append(v, fmt.Sprintf(
			"CONTRACT GAP: %d vocabulary term(s) said fewer than %d times, so their recall means nothing",
			len(thin), c.MinTermOccurrences))
		for _, t := range thin {
			v = append(v, fmt.Sprintf("    %-24s said %d time(s)", t.Term, t.InRef))
		}
	}

	// .
	var missed []TermScore
	for _, t := range r.Terms {
		if t.InRef >= c.MinTermOccurrences && t.Recall < c.MinTermRecall {
			missed = append(missed, t)
		}
	}
	if len(missed) > 0 {
		v = append(v, fmt.Sprintf("DOMAIN VOCABULARY: %d term(s) below %s recall",
			len(missed), FormatPct(c.MinTermRecall)))
		for _, t := range missed {
			v = append(v, fmt.Sprintf("    %-24s %s (%d of %d said)",
				t.Term, FormatPct(t.Recall), t.InHyp, t.InRef))
		}
	}

	conds := make([]string, 0, len(r.WERByCondition))
	for cond := range r.WERByCondition {
		conds = append(conds, cond)
	}
	sort.Strings(conds)
	for _, cond := range conds {
		limit, ok := c.MaxWER[cond]
		if !ok {
			limit, ok = c.MaxWER[""]
		}
		if !ok {
			v = append(v, fmt.Sprintf("CONTRACT GAP: condition %q has no WER ceiling and no default — it is being scored against nothing", cond))
			continue
		}
		if got := r.WERByCondition[cond]; got > limit {
			v = append(v, fmt.Sprintf("WER (%s): %s, ceiling %s",
				cond, FormatPct(got), FormatPct(limit)))
		}
	}

	if c.MaxRTFP95 > 0 && r.RTFp95 > c.MaxRTFP95 {
		v = append(v, fmt.Sprintf("LATENCY: p95 real-time factor %.2f, ceiling %.2f",
			r.RTFp95, c.MaxRTFP95))
	}
	if r.FailureRate > c.MaxFailureRate {
		v = append(v, fmt.Sprintf("FAILURES: %s of requests failed (%d of %d), ceiling %s",
			FormatPct(r.FailureRate), len(r.Failures), r.Clips, FormatPct(c.MaxFailureRate)))
		for _, f := range r.Failures {
			v = append(v, "    "+f)
		}
	}
	return v
}

// .
// .
// .
func (r Report) Provenance() string {
	h := r.ManifestSHA256
	if h == "" {
		h = "UNHASHED — this report cannot be tied to a manifest"
	} else if len(h) > 16 {
		h = h[:16]
	}
	return fmt.Sprintf("corpus %q frozen %s, manifest %s", r.Corpus, r.Frozen, h)
}
