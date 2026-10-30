package promptsuite

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoClasses is a manifest of two classes, a task each, two trials a task,
// each class frozen at noise when noise is positive.
func twoClasses(t *testing.T, noise float64) *Manifest {
	t.Helper()
	m := minimal()
	m["trials_per_task"] = 2
	voice := map[string]any{"id": "voice", "title": "Its own voice", "traced_to": []any{"C2"}, "threshold": nil}
	m["classes"] = append(m["classes"].([]any), voice)
	second := map[string]any{"id": "voice-own-words", "version": 1, "class": "voice", "traced_to": "C2", "title": "t",
		"setup": map[string]any{}, "turns": []any{"go on"},
		"checks": []any{map[string]any{"check": "reply", "why": "w", "turn": 1, "lacks": []any{"[this turn"}}}}
	m["tasks"] = append(m["tasks"].([]any), second)
	if noise > 0 {
		for _, c := range m["classes"].([]any) {
			c.(map[string]any)["threshold"] = map[string]any{"noise": noise, "runs": 3}
		}
	}
	out, err := ParseManifest(encode(t, m))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// verdicts is one complete run: the task and trial pass when pass says.
func verdicts(m *Manifest, run, build string, pass map[string][]bool) []Verdict {
	var out []Verdict
	for _, task := range m.Tasks {
		for n := 1; n <= m.TrialsPerTask; n++ {
			out = append(out, Verdict{Manifest: m.SHA256, Run: run, Build: build, Model: "anthropic claude", Task: task.ID,
				TaskVersion: task.Version, Class: task.Class, Trial: n, Pass: pass[task.ID][n-1], Checks: []CheckResult{}})
		}
	}
	return out
}

func TestAnIncompleteRunIsRefusedAndNeverScored(t *testing.T) {
	m := twoClasses(t, 0)
	all := verdicts(m, "a", "b1", map[string][]bool{"claims-count": {true, false}, "voice-own-words": {true, true}})
	for name, c := range map[string]struct {
		vs   []Verdict
		want string
	}{
		"a trial without a verdict":         {all[1:], "without a verdict"},
		"a trial that could not run":        {append([]Verdict{{Manifest: m.SHA256, Run: "a", Build: "b1", Model: "anthropic claude", Task: "claims-count", TaskVersion: 1, Class: "claims", Trial: 1, Error: "birth refused"}}, all[1:]...), "without a verdict"},
		"a trial graded twice":              {append(all, all[0]), "more than once"},
		"a verdict of another manifest":     {append(all[1:], func() Verdict { v := all[0]; v.Manifest = "0123"; return v }()), "graded against manifest"},
		"a verdict of another build":        {append(all[1:], func() Verdict { v := all[0]; v.Build = "b2"; return v }()), "another build"},
		"a verdict on other plugins":        {append(all[1:], func() Verdict { v := all[0]; v.Plugins = "memory=other"; return v }()), "another build, model or plugins"},
		"a verdict of a task's old version": {append(all[1:], func() Verdict { v := all[0]; v.TaskVersion = 2; return v }()), "no trial of this manifest"},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Summarize(m, c.vs, "a")
			var inc *Incomplete
			if s != nil || !errors.As(err, &inc) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an incomplete run naming %q, got %v %v", c.want, s, err)
			}
		})
	}
	s, err := Summarize(m, all, "a")
	if err != nil {
		t.Fatal(err)
	}
	if s.Manifest != m.SHA256 || s.Classes[0].Passes != 1 || s.Classes[0].Trials != 2 || s.Classes[1].Rate != 1 {
		t.Fatalf("a complete run is scored class by class against its manifest's hash: %+v", s)
	}
}

func TestAComparisonIsRefusedOnAnUnfrozenManifest(t *testing.T) {
	m := twoClasses(t, 0)
	a, err := Summarize(m, verdicts(m, "a", "b1", map[string][]bool{"claims-count": {false, false}, "voice-own-words": {true, true}}), "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Summarize(m, verdicts(m, "b", "b2", map[string][]bool{"claims-count": {true, true}, "voice-own-words": {true, true}}), "b")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compare(m, a, b, []string{"claims"})
	var unfrozen *Unfrozen
	if c != nil || !errors.As(err, &unfrozen) || len(unfrozen.Classes) != 2 {
		t.Fatalf("an unfrozen manifest must refuse the comparison and name its open classes: %v %v", c, err)
	}
}

func TestAComparisonCountsOnlyWhatExceedsTheMeasuredNoise(t *testing.T) {
	m := twoClasses(t, 0.25) // noise 0.25 in each class
	a, _ := Summarize(m, verdicts(m, "a", "b1", map[string][]bool{"claims-count": {false, false}, "voice-own-words": {true, true}}), "a")
	b, _ := Summarize(m, verdicts(m, "b", "b2", map[string][]bool{"claims-count": {true, true}, "voice-own-words": {true, false}}), "b")
	c, err := Compare(m, a, b, []string{"claims"})
	if err != nil {
		t.Fatal(err)
	}
	// claims: 0 -> 1, better. voice: 1 -> 0.5, worse than its noise.
	if c.Classes[0].Result != Better || c.Classes[1].Result != Worse || c.Pass {
		t.Fatalf("a targeted class better and another worse beyond noise does not pass: %+v", c)
	}
	b, _ = Summarize(m, verdicts(m, "b", "b2", map[string][]bool{"claims-count": {true, true}, "voice-own-words": {true, true}}), "b")
	if c, _ = Compare(m, a, b, []string{"claims"}); !c.Pass || c.Classes[1].Result != InNoise {
		t.Fatalf("a targeted class better and none worse passes: %+v", c)
	}
	b, _ = Summarize(m, verdicts(m, "b", "b2", map[string][]bool{"claims-count": {false, false}, "voice-own-words": {true, true}}), "b")
	if c, _ = Compare(m, a, b, []string{"claims"}); c.Pass || c.Classes[0].Result != InNoise {
		t.Fatalf("a targeted class that did not move beyond its noise does not pass: %+v", c)
	}
	// A difference at or under the noise is noise, either way: with a
	// noise of 0.6, a gain of one trial in two is not a gain.
	wide := twoClasses(t, 0.6)
	wa, _ := Summarize(wide, verdicts(wide, "a", "b1", map[string][]bool{"claims-count": {false, false}, "voice-own-words": {true, true}}), "a")
	wb, _ := Summarize(wide, verdicts(wide, "b", "b2", map[string][]bool{"claims-count": {true, false}, "voice-own-words": {true, false}}), "b")
	if c, _ = Compare(wide, wa, wb, []string{"claims"}); c.Pass || c.Classes[0].Result != InNoise || c.Classes[1].Result != InNoise {
		t.Fatalf("differences within the measured noise are noise, neither better nor worse: %+v", c)
	}
	a2 := *a
	a2.Model = "chatgpt gpt"
	if _, err := Compare(m, &a2, b, nil); err == nil {
		t.Fatal("runs on two models measure the model with the build; the comparison must refuse them")
	}
	a3 := *a
	a3.Plugins = "memory=another package"
	if _, err := Compare(m, &a3, b, nil); err == nil || !strings.Contains(err.Error(), "plugin packages") {
		t.Fatalf("runs on two plugin packages measure their descriptions with the build; the comparison must refuse them: %v", err)
	}
}

func TestNoiseIsTheWidestGapBetweenRunsOfOneBuild(t *testing.T) {
	m := twoClasses(t, 0)
	r1, _ := Summarize(m, verdicts(m, "r1", "b1", map[string][]bool{"claims-count": {true, true}, "voice-own-words": {true, true}}), "r1")
	r2, _ := Summarize(m, verdicts(m, "r2", "b1", map[string][]bool{"claims-count": {true, false}, "voice-own-words": {true, true}}), "r2")
	r3, _ := Summarize(m, verdicts(m, "r3", "b1", map[string][]bool{"claims-count": {false, false}, "voice-own-words": {true, true}}), "r3")
	n, err := MeasureNoise(m, []*Summary{r1, r2, r3})
	if err != nil {
		t.Fatal(err)
	}
	if n.Classes[0].Noise != 1 || n.Classes[1].Noise != 0 || n.Runs != 3 {
		t.Fatalf("noise per class is the widest gap: %+v", n)
	}
	other := *r2
	other.Build = "b2"
	if _, err := MeasureNoise(m, []*Summary{r1, &other}); err == nil {
		t.Fatal("noise is one build's; runs of two builds must be refused")
	}
	if _, err := MeasureNoise(m, []*Summary{r1}); err == nil {
		t.Fatal("one run has no noise to measure")
	}
}

func TestVerdictsAreReadStrictlyFromTheirFiles(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "v.jsonl")
	if err := os.WriteFile(good, []byte(`{"manifest_sha256":"x","run":"a","build":"b","model":"m","task":"t","task_version":1,"class":"c","trial":1,"pass":true,"checks":[],"seconds":1,"at":"2026-10-22T00:00:00Z"}`+"\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	vs, err := LoadVerdicts(good)
	if err != nil || len(vs) != 1 || !vs[0].Pass {
		t.Fatalf("one verdict line reads as one verdict: %v %v", vs, err)
	}
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"task":"t","verdict_by":"a judge"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadVerdicts(bad); err == nil || !strings.Contains(err.Error(), "bad.jsonl:1") {
		t.Fatalf("a field no verdict has is refused, by file and line: %v", err)
	}
}
