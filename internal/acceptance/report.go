package acceptance

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const UsageTimeColumn = "ts_ms"

var notSummed = map[string]bool{UsageTimeColumn: true, "id": true}

type Report struct {
	Set            string `json:"set"`
	ManifestSHA256 string `json:"manifest_sha256"`

	Pass          bool         `json:"pass"`
	Jobs          []JobReport  `json:"jobs"`
	NegativeCases []CaseReport `json:"negative_cases"`

	Usage Usage `json:"usage"`
}

type JobReport struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Threshold int    `json:"threshold"`
	Attempts  int    `json:"attempts"`
	Successes int    `json:"successes"`

	Assisted      int            `json:"assisted"`
	Failures      int            `json:"failures"`
	Unsupported   int            `json:"unsupported"`
	Interventions map[string]int `json:"interventions"`

	Violations []string `json:"violations"`
	Usage      Usage    `json:"usage"`
	Pass       bool     `json:"pass"`
}

type CaseReport struct {
	ID            string         `json:"id"`
	Job           string         `json:"job,omitempty"`
	AttemptID     string         `json:"attempt_id"`
	Outcome       string         `json:"outcome"`
	Violations    []string       `json:"violations"`
	Interventions map[string]int `json:"interventions"`
}

type Usage struct {
	Rows   map[string]int                `json:"rows"`
	Totals map[string]map[string]float64 `json:"totals"`

	LowerBound bool     `json:"lower_bound"`
	Because    []string `json:"because,omitempty"`
}

type Incomplete struct {
	Set string

	Missing []string

	Repeated []string

	Unpaired []string

	Unknown []string
}

func (e *Incomplete) Error() string {
	var parts []string
	add := func(what string, items []string) {
		if len(items) > 0 {
			parts = append(parts, fmt.Sprintf("%d %s: %s", len(items), what, strings.Join(items, "; ")))
		}
	}
	add("case(s) never attempted", e.Missing)
	add("repeated", e.Repeated)
	add("unpaired", e.Unpaired)
	add("not in this set", e.Unknown)
	return fmt.Sprintf("INCOMPLETE RUN (%s): %s — no aggregate is scored", e.Set, strings.Join(parts, "; "))
}

func (e *Incomplete) empty() bool {
	return len(e.Missing)+len(e.Repeated)+len(e.Unpaired)+len(e.Unknown) == 0
}

type attempt struct {
	verdict     Verdict
	observation Observation
}

func Score(m *Manifest, set string, verdicts []Verdict, observations []Observation) (*Report, error) {
	cases, err := m.Cases(set)
	if err != nil {
		return nil, err
	}
	variantOf := map[string]Variant{}
	jobOf := map[string]int{}
	for i, j := range m.Jobs {
		n := 0
		for _, v := range j.Variants {
			if v.Set == set {
				variantOf[v.ID] = v
				jobOf[v.ID] = i
				n++
			}
		}
		if n < j.ThresholdSuccesses {
			return nil, fmt.Errorf("job %q has %d %s variant(s), fewer than its threshold %d: it cannot be scored in this set", j.ID, n, set, j.ThresholdSuccesses)
		}
	}
	negativeOf := map[string]NegativeCase{}
	for _, c := range m.NegativeCases {
		negativeOf[c.ID] = c
	}

	inc := &Incomplete{Set: set}
	byVerdict := map[string][]Verdict{}
	for _, v := range verdicts {
		byVerdict[v.AttemptID] = append(byVerdict[v.AttemptID], v)
	}
	byObservation := map[string][]Observation{}
	for _, o := range observations {
		byObservation[o.AttemptID] = append(byObservation[o.AttemptID], o)
	}
	attemptsOf := map[string][]attempt{}
	for id, vs := range byVerdict {
		obs := byObservation[id]
		switch {
		case len(vs) > 1:
			inc.Repeated = append(inc.Repeated, fmt.Sprintf("attempt %s has %d verdicts", id, len(vs)))
			continue
		case len(obs) > 1:
			inc.Repeated = append(inc.Repeated, fmt.Sprintf("attempt %s has %d observations", id, len(obs)))
			continue
		case len(obs) == 0:
			inc.Unpaired = append(inc.Unpaired, fmt.Sprintf("attempt %s has a verdict and no observation", id))
			continue
		}
		v, o := vs[0], obs[0]
		if v.VariantID != o.VariantID || v.NegativeCaseID != o.NegativeCaseID {
			inc.Unpaired = append(inc.Unpaired, fmt.Sprintf("attempt %s: verdict and observation name different cases", id))
			continue
		}
		_, isVariant := variantOf[v.VariantID]
		_, isNegative := negativeOf[v.NegativeCaseID]
		if !isVariant && !isNegative {
			inc.Unknown = append(inc.Unknown, fmt.Sprintf("attempt %s names %s", id, v.VariantID+v.NegativeCaseID))
			continue
		}
		key := v.VariantID + v.NegativeCaseID
		attemptsOf[key] = append(attemptsOf[key], attempt{v, o})
	}
	for id, obs := range byObservation {
		if len(byVerdict[id]) == 0 {
			inc.Unpaired = append(inc.Unpaired, fmt.Sprintf("attempt %s has an observation and no verdict", id))
			if len(obs) > 1 {
				inc.Repeated = append(inc.Repeated, fmt.Sprintf("attempt %s has %d observations", id, len(obs)))
			}
		}
	}
	for _, c := range cases {
		switch n := len(attemptsOf[c.ID()]); {
		case n == 0:
			inc.Missing = append(inc.Missing, c.ID())
		case n > 1:
			inc.Repeated = append(inc.Repeated, fmt.Sprintf("case %s attempted %d times", c.ID(), n))
		}
	}
	if !inc.empty() {
		for _, s := range [][]string{inc.Missing, inc.Repeated, inc.Unpaired, inc.Unknown} {
			sort.Strings(s)
		}
		return nil, inc
	}

	r := &Report{Set: set, ManifestSHA256: m.SHA256, Usage: newUsage()}
	for _, j := range m.Jobs {
		r.Jobs = append(r.Jobs, JobReport{ID: j.ID, Title: j.Title, Threshold: j.ThresholdSuccesses,
			Interventions: map[string]int{}, Violations: []string{}, Usage: newUsage()})
	}
	jobIndex := map[string]int{}
	for i, j := range m.Jobs {
		jobIndex[j.ID] = i
	}

	for _, c := range cases {
		for _, a := range attemptsOf[c.ID()] {
			v, o := a.verdict, a.observation
			r.Usage.add(o)
			if c.VariantID != "" {
				jr := &r.Jobs[jobOf[c.VariantID]]
				jr.Attempts++
				switch v.Outcome {
				case Success:
					if allowed(v.Interventions, variantOf[c.VariantID].AllowedInterventions) {
						jr.Successes++
					} else {
						jr.Assisted++
					}
				case Failure:
					jr.Failures++
				case Unsupported:
					jr.Unsupported++
				}
				for _, i := range v.Interventions {
					jr.Interventions[i.Class] += i.Count
				}
				for _, x := range v.Violations {
					jr.Violations = append(jr.Violations, fmt.Sprintf("attempt %s (variant %s): %s", o.AttemptID, c.VariantID, x))
				}
				jr.Usage.add(o)
				continue
			}
			nc := negativeOf[c.NegativeCaseID]
			cr := CaseReport{ID: nc.ID, Job: nc.Job, AttemptID: o.AttemptID, Outcome: v.Outcome,
				Violations: append([]string{}, v.Violations...), Interventions: map[string]int{}}
			for _, i := range v.Interventions {
				cr.Interventions[i.Class] += i.Count
			}
			r.NegativeCases = append(r.NegativeCases, cr)
			if nc.Job != "" {
				jr := &r.Jobs[jobIndex[nc.Job]]
				for _, x := range v.Violations {
					jr.Violations = append(jr.Violations, fmt.Sprintf("attempt %s (negative case %s): %s", o.AttemptID, nc.ID, x))
				}
				jr.Usage.add(o)
			}
		}
	}

	r.Pass = true
	for i := range r.Jobs {
		jr := &r.Jobs[i]
		jr.Pass = jr.Successes >= jr.Threshold && len(jr.Violations) == 0
		r.Pass = r.Pass && jr.Pass
	}
	for _, cr := range r.NegativeCases {
		if len(cr.Violations) > 0 {
			r.Pass = false
		}
	}
	return r, nil
}

func allowed(done []Intervention, permitted []string) bool {
	for _, i := range done {
		if !slices.Contains(permitted, i.Class) {
			return false
		}
	}
	return true
}

func newUsage() Usage {
	return Usage{Rows: map[string]int{}, Totals: map[string]map[string]float64{}}
}

func (u *Usage) add(o Observation) {
	for table, rows := range o.Usage {
		u.Rows[table] += len(rows)
		for _, row := range rows {
			for col, val := range row {
				f, ok := number(val)
				if !ok || notSummed[col] {
					continue
				}
				if u.Totals[table] == nil {
					u.Totals[table] = map[string]float64{}
				}
				u.Totals[table][col] += f
			}
		}
	}
	if !o.UsageComplete {
		u.LowerBound = true
		why := "usage incomplete"
		if len(o.UsageGaps) > 0 {
			why += " (" + strings.Join(o.UsageGaps, "; ") + ")"
		}
		u.Because = append(u.Because, o.AttemptID+": "+why)
	}
	if o.Unattributed {
		u.LowerBound = true
		u.Because = append(u.Because, o.AttemptID+": unattributed — the identity was not at rest when it began")
	}
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}
