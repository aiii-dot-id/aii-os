package promptsuite

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

type Verdict struct {
	Manifest string `json:"manifest_sha256"`

	Run string `json:"run"`

	Build string `json:"build"`

	Model string `json:"model"`

	Plugins     string `json:"plugins"`
	Task        string `json:"task"`
	TaskVersion int    `json:"task_version"`
	Class       string `json:"class"`
	Trial       int    `json:"trial"`
	Pass        bool   `json:"pass"`

	Checks []CheckResult `json:"checks"`

	Error   string  `json:"error,omitempty"`
	Seconds float64 `json:"seconds"`
	At      string  `json:"at"`
}

func LoadVerdicts(files ...string) ([]Verdict, error) {
	var out []Verdict
	for _, file := range files {
		blob, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(bytes.NewReader(blob))
		sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for n := 1; sc.Scan(); n++ {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			var v Verdict
			if err := canonicaljson.DecodeStrict(line, &v); errors.Is(err, canonicaljson.ErrTrailingContent) {
				return nil, fmt.Errorf("%s:%d: content after the verdict", filepath.Base(file), n)
			} else if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", filepath.Base(file), n, err)
			}
			out = append(out, v)
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(file), err)
		}
	}
	return out, nil
}

type Incomplete struct {
	Run      string
	Missing  []string
	Repeated []string
	Foreign  []string
}

func (e *Incomplete) Error() string {
	var parts []string
	add := func(what string, items []string) {
		if len(items) > 0 {
			sort.Strings(items)
			parts = append(parts, fmt.Sprintf("%d %s: %s", len(items), what, strings.Join(items, "; ")))
		}
	}
	add("trial(s) without a verdict", e.Missing)
	add("trial(s) graded more than once", e.Repeated)
	add("verdict(s) not of this manifest, run, build, model or plugins", e.Foreign)
	return fmt.Sprintf("INCOMPLETE RUN (%s): %s — no aggregate is scored", e.Run, strings.Join(parts, "; "))
}

type Summary struct {
	Manifest string         `json:"manifest_sha256"`
	Revision int            `json:"revision"`
	Run      string         `json:"run"`
	Build    string         `json:"build"`
	Model    string         `json:"model"`
	Plugins  string         `json:"plugins"`
	Classes  []ClassSummary `json:"classes"`
	Tasks    []TaskSummary  `json:"tasks"`
}

type ClassSummary struct {
	ID     string  `json:"id"`
	Trials int     `json:"trials"`
	Passes int     `json:"passes"`
	Rate   float64 `json:"rate"`
}

type TaskSummary struct {
	ID     string `json:"id"`
	Class  string `json:"class"`
	Trials int    `json:"trials"`
	Passes int    `json:"passes"`
}

func Summarize(m *Manifest, verdicts []Verdict, run string) (*Summary, error) {
	inc := &Incomplete{Run: run}
	s := &Summary{Manifest: m.SHA256, Revision: m.Revision, Run: run}
	got := map[string]Verdict{}
	for _, v := range verdicts {
		if v.Run != run || v.Error != "" {
			continue
		}
		key := fmt.Sprintf("%s#%d", v.Task, v.Trial)
		t := m.Task(v.Task)
		switch {
		case v.Manifest != m.SHA256:
			inc.Foreign = append(inc.Foreign, key+" was graded against manifest "+short(v.Manifest))
			continue
		case t == nil || t.Version != v.TaskVersion || t.Class != v.Class || v.Trial < 1 || v.Trial > m.TrialsPerTask:
			inc.Foreign = append(inc.Foreign, key+" is no trial of this manifest")
			continue
		case s.Build == "" && s.Model == "":
			s.Build, s.Model, s.Plugins = v.Build, v.Model, v.Plugins
		case v.Build != s.Build || v.Model != s.Model || v.Plugins != s.Plugins:
			inc.Foreign = append(inc.Foreign, key+" ran another build, model or plugins")
			continue
		}
		if _, dup := got[key]; dup {
			inc.Repeated = append(inc.Repeated, key)
			continue
		}
		got[key] = v
	}
	classes := map[string]*ClassSummary{}
	for _, c := range m.Classes {
		s.Classes = append(s.Classes, ClassSummary{ID: c.ID})
	}
	for i := range s.Classes {
		classes[s.Classes[i].ID] = &s.Classes[i]
	}
	for _, t := range m.Tasks {
		ts := TaskSummary{ID: t.ID, Class: t.Class}
		for n := 1; n <= m.TrialsPerTask; n++ {
			v, ok := got[fmt.Sprintf("%s#%d", t.ID, n)]
			if !ok {
				inc.Missing = append(inc.Missing, fmt.Sprintf("%s#%d", t.ID, n))
				continue
			}
			ts.Trials++
			if v.Pass {
				ts.Passes++
			}
		}
		s.Tasks = append(s.Tasks, ts)
		cs := classes[t.Class]
		cs.Trials += ts.Trials
		cs.Passes += ts.Passes
	}
	if len(inc.Missing)+len(inc.Repeated)+len(inc.Foreign) > 0 {
		return nil, inc
	}
	for i := range s.Classes {
		s.Classes[i].Rate = float64(s.Classes[i].Passes) / float64(s.Classes[i].Trials)
	}
	return s, nil
}

type Unfrozen struct {
	Classes []string
}

func (e *Unfrozen) Error() string {
	return fmt.Sprintf("the manifest is not frozen: class(es) %s have no threshold — run the noise measure, freeze its numbers in a new revision, and compare against that", strings.Join(e.Classes, ", "))
}

type Comparison struct {
	Manifest string            `json:"manifest_sha256"`
	A        string            `json:"a_build"`
	B        string            `json:"b_build"`
	Model    string            `json:"model"`
	Targets  []string          `json:"targets"`
	Classes  []ClassComparison `json:"classes"`

	Pass bool `json:"pass"`
}

type ClassComparison struct {
	ID     string  `json:"id"`
	A      float64 `json:"a_rate"`
	B      float64 `json:"b_rate"`
	Delta  float64 `json:"delta"`
	Noise  float64 `json:"noise"`
	Result string  `json:"result"`
	Target bool    `json:"target"`
}

const (
	Better  = "better"
	Worse   = "worse"
	InNoise = "noise"
)

func Compare(m *Manifest, a, b *Summary, targets []string) (*Comparison, error) {
	if ok, open := m.Frozen(); !ok {
		return nil, &Unfrozen{Classes: open}
	}
	if a.Manifest != m.SHA256 || b.Manifest != m.SHA256 {
		return nil, fmt.Errorf("a summary is of another manifest")
	}
	if a.Model != b.Model {
		return nil, fmt.Errorf("the runs are on different models (%s, %s): a comparison of builds holds the model still", a.Model, b.Model)
	}
	if a.Plugins != b.Plugins {
		return nil, fmt.Errorf("the runs are on different plugin packages (%q, %q): their descriptions are part of the prompt, and a comparison of builds holds them still", a.Plugins, b.Plugins)
	}
	want := map[string]bool{}
	for _, t := range targets {
		known := false
		for _, c := range m.Classes {
			known = known || c.ID == t
		}
		if !known {
			return nil, fmt.Errorf("target class %q is not in the manifest", t)
		}
		want[t] = true
	}
	out := &Comparison{Manifest: m.SHA256, A: a.Build, B: b.Build, Model: a.Model, Targets: targets, Pass: true}
	for i, c := range m.Classes {
		ra, rb := a.Classes[i].Rate, b.Classes[i].Rate
		cc := ClassComparison{ID: c.ID, A: ra, B: rb, Delta: rb - ra, Noise: c.Threshold.Noise, Target: want[c.ID]}
		switch {
		case cc.Delta > cc.Noise:
			cc.Result = Better
		case cc.Delta < -cc.Noise:
			cc.Result = Worse
		default:
			cc.Result = InNoise
		}
		if cc.Result == Worse || (cc.Target && cc.Result != Better) {
			out.Pass = false
		}
		out.Classes = append(out.Classes, cc)
	}
	return out, nil
}

type Noise struct {
	Manifest string       `json:"manifest_sha256"`
	Build    string       `json:"build"`
	Model    string       `json:"model"`
	Plugins  string       `json:"plugins"`
	Runs     int          `json:"runs"`
	Classes  []ClassNoise `json:"classes"`
}

type ClassNoise struct {
	ID    string    `json:"id"`
	Rates []float64 `json:"rates"`
	Noise float64   `json:"noise"`
}

func MeasureNoise(m *Manifest, runs []*Summary) (*Noise, error) {
	if len(runs) < 2 {
		return nil, fmt.Errorf("noise needs two or more runs of one build, not %d", len(runs))
	}
	n := &Noise{Manifest: m.SHA256, Build: runs[0].Build, Model: runs[0].Model, Plugins: runs[0].Plugins, Runs: len(runs)}
	for _, r := range runs {
		if r.Manifest != m.SHA256 {
			return nil, fmt.Errorf("run %s is of another manifest", r.Run)
		}
		if r.Build != n.Build || r.Model != n.Model || r.Plugins != n.Plugins {
			return nil, fmt.Errorf("run %s is of another build, model or plugins: noise is one build's", r.Run)
		}
	}
	for i, c := range m.Classes {
		cn := ClassNoise{ID: c.ID}
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, r := range runs {
			rate := r.Classes[i].Rate
			cn.Rates = append(cn.Rates, rate)
			lo, hi = math.Min(lo, rate), math.Max(hi, rate)
		}
		cn.Noise = hi - lo
		n.Classes = append(n.Classes, cn)
	}
	return n, nil
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
