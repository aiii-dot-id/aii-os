package speecheval

import (
	"fmt"
	"sort"
	"strings"
)

func (r Report) Format(c Contract) string {
	var b strings.Builder
	line := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	line("%s", r.Provenance())
	if r.Target != "" {
		line("target %s", r.Target)
	}
	line("clips %d, reference words %d, failed requests %d (%s)", r.Clips, r.RefWords, len(r.Failures), FormatPct(r.FailureRate))
	line("hallucination on non-speech: %s (%d of %d clips produced words)", FormatPct(r.HallucinationRate), len(r.Hallucinated), r.NonSpeechClips)
	line("WER overall %s", FormatPct(r.WER))
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
		if ok {
			line("  %-10s %s (ceiling %s)", cond, FormatPct(r.WERByCondition[cond]), FormatPct(limit))
		} else {
			line("  %-10s %s (no ceiling)", cond, FormatPct(r.WERByCondition[cond]))
		}
	}
	line("domain terms (floor %s each, said at least %d times):", FormatPct(c.MinTermRecall), c.MinTermOccurrences)
	for _, t := range r.Terms {
		line("  %-24s %s (%d of %d)", t.Term, FormatPct(t.Recall), t.InHyp, t.InRef)
	}
	line("real-time factor p50 %.3f, p95 %.3f (ceiling %.2f)", r.RTFp50, r.RTFp95, c.MaxRTFP95)
	if v := r.Violations(c); len(v) > 0 {
		line("")
		for _, s := range v {
			line("%s", s)
		}
		line("")
		line("VERDICT: FAIL (%d finding(s) above)", countFindings(v))
	} else {
		line("")
		line("VERDICT: PASS")
	}
	return b.String()
}

func countFindings(v []string) int {
	n := 0
	for _, s := range v {
		if !strings.HasPrefix(s, " ") {
			n++
		}
	}
	return n
}
