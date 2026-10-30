package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/promptsuite"
)

var designClasses = []string{"dyad", "playbook", "claims", "doctrine", "recall", "subagent", "voice", "name", "sycophancy", "safe"}

func TestTheSuiteHoldsThirtyToFortyTasksAcrossTheDesignsClasses(t *testing.T) {
	m := loadSuite(t)
	if n := len(m.Tasks); n < 30 || n > 40 {
		t.Fatalf("the suite holds %d tasks; the design asks for 30 to 40", n)
	}
	per := map[string]int{}
	for _, task := range m.Tasks {
		per[task.Class]++
	}
	for _, c := range designClasses {
		if per[c] == 0 {
			t.Errorf("class %s has no task", c)
		}
	}
	if len(m.Classes) != len(designClasses) {
		t.Errorf("the manifest declares %d classes; the design names %d", len(m.Classes), len(designClasses))
	}
	if m.TrialsPerTask < 2 {
		t.Errorf("a task run once has no trials to vary across; the design asks for several")
	}
}

// suiteOf writes a manifest of the suite's own tasks named by ids, with
// trials trials each and every class it keeps, frozen at noise when noise
// is not negative. It is how a test runs the runner's verbs end to end on
// a few of the real tasks.
func suiteOf(t *testing.T, dir string, noise float64, trials int, ids ...string) string {
	t.Helper()
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keep := map[string]bool{}
	var tasks []any
	for _, task := range m["tasks"].([]any) {
		tm := task.(map[string]any)
		for _, id := range ids {
			if tm["id"] == id {
				tasks = append(tasks, tm)
				keep[tm["class"].(string)] = true
			}
		}
	}
	var classes []any
	for _, c := range m["classes"].([]any) {
		cm := c.(map[string]any)
		if keep[cm["id"].(string)] {
			if noise >= 0 {
				cm["threshold"] = map[string]any{"noise": noise, "runs": 3}
			}
			classes = append(classes, cm)
		}
	}
	m["tasks"], m["classes"], m["trials_per_task"], m["trial_timeout_seconds"] = tasks, classes, trials, proofTrialSeconds
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(file, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := promptsuite.LoadManifest(file); err != nil {
		t.Fatal(err)
	}
	return file
}

// specOf is a model spec as the JSON a run is given it in. A value is
// never spliced into JSON text: a Windows path's backslashes are escapes
// there.
func specOf(t *testing.T, s promptsuite.ModelSpec) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// goodScript is the task's proof transcript done well.
func goodScript(t *testing.T, id string) script {
	t.Helper()
	p, ok := loadProofs(t)[id]
	if !ok {
		t.Fatalf("no proof for %s", id)
	}
	return p.Good
}

func TestARunHandsTheHostItsKeyAndWritesTheKeyNowhere(t *testing.T) {
	const secret = "sk-promptsuite-sentinel-7f3a9c1e5b2d"
	const keyEnv = "PROMPTSUITE_TEST_MODEL_KEY"
	dir := t.TempDir()
	bin := testHost(t, dir)
	manifest := suiteOf(t, dir, -1, 1, "claims-count-needs-a-read")
	fake := newKeyedFakeModel(t, goodScript(t, "claims-count-needs-a-read"), secret)
	t.Setenv(keyEnv, secret)
	spec := specOf(t, promptsuite.ModelSpec{Kind: promptsuite.KindOpenAICompatible, Model: fake.model, URL: fake.url(), KeyEnv: keyEnv})
	env := map[string]string{promptsuite.SpecEnv: spec, keyEnv: secret}
	out := filepath.Join(dir, "run")
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "-manifest", manifest, "-host", bin.path, "-out", out}, func(k string) string { return env[k] }, &stdout, &stderr)
	if code != exitPass {
		t.Fatalf("the run did not complete (exit %d): %s", code, stderr.String())
	}
	if un := fake.unserved(); len(un) > 0 {
		t.Fatalf("the model did not serve the task: %v", un)
	}
	vs, err := promptsuite.LoadVerdicts(filepath.Join(out, "verdicts.jsonl"))
	if err != nil || len(vs) != 1 || !vs[0].Pass || vs[0].Model == "" {
		t.Fatalf("one passing verdict naming its model: %+v %v", vs, err)
	}
	if _, err := os.Stat(filepath.Join(out, "summary-run.json")); err != nil {
		t.Fatalf("a complete run writes its summary: %v", err)
	}
	for what, text := range map[string]string{"stdout": stdout.String(), "stderr": stderr.String()} {
		if strings.Contains(text, secret) {
			t.Fatalf("the run printed the key on %s", what)
		}
	}
	err = filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == filepath.Join(out, "aii-a") {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("%s holds the key", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	worlds, err := os.ReadDir(filepath.Join(out, "worlds"))
	if err != nil || len(worlds) != 0 {
		t.Fatalf("a trial's world is removed when it ends: %v %v", worlds, err)
	}
}

func TestTheRunnerRefusesToCompareAgainstAnUnfrozenManifest(t *testing.T) {
	dir := t.TempDir()
	manifest := suiteOf(t, dir, -1, 1, "voice-no-steps-narration")
	spec := `{"kind":"openai-compatible","model":"m","url":"http://127.0.0.1:1/v1"}`
	getenv := func(k string) string {
		if k == promptsuite.SpecEnv {
			return spec
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	out := filepath.Join(dir, "pair")
	if code := run([]string{"pair", "-manifest", manifest, "-host", "/nonexistent", "-b-host", "/nonexistent", "-out", out}, getenv, &stdout, &stderr); code != exitRefused || !strings.Contains(stderr.String(), "not frozen") {
		t.Fatalf("a pair run on an unfrozen manifest must be refused before a trial (exit %d): %s", code, stderr.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused pair run makes nothing: %v", err)
	}
	stderr.Reset()
	if code := run([]string{"compare", "-manifest", manifest, filepath.Join(dir, "none.jsonl")}, getenv, &stdout, &stderr); code != exitRefused || !strings.Contains(stderr.String(), "not frozen") {
		t.Fatalf("a comparison of verdicts on an unfrozen manifest must be refused (exit %d): %s", code, stderr.String())
	}
}

// THE PAIRED AND NOISE MODES RUN END TO END. A pair runs the same task on
// two builds, trial by trial, and compares them class by class against
// the frozen thresholds; a noise run runs one build several times and
// measures what a new revision freezes. Both on the scripted model.
func TestThePairedAndNoiseModesRunTheirBuildsAndReport(t *testing.T) {
	dir := t.TempDir()
	bin := testHost(t, dir)
	const id = "voice-no-steps-narration"
	frozen := suiteOf(t, dir, 0.2, 1, id)
	fake := newFakeModel(t, goodScript(t, id))
	spec := specOf(t, promptsuite.ModelSpec{Kind: promptsuite.KindOpenAICompatible, Model: fake.model, URL: fake.url()})
	getenv := func(k string) string {
		if k == promptsuite.SpecEnv {
			return spec
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	pairOut := filepath.Join(dir, "pair")
	if code := run([]string{"pair", "-manifest", frozen, "-host", bin.path, "-b-host", bin.path, "-out", pairOut, "-targets", ""}, getenv, &stdout, &stderr); code != exitPass {
		t.Fatalf("a pair of one build with itself passes a comparison with no target (exit %d): %s", code, stderr.String())
	}
	var c promptsuite.Comparison
	raw, err := os.ReadFile(filepath.Join(pairOut, "comparison.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &c); err != nil || len(c.Classes) != 1 || c.Classes[0].Result != promptsuite.InNoise {
		t.Fatalf("the comparison names each class's result: %+v %v", c, err)
	}
	vs, err := promptsuite.LoadVerdicts(filepath.Join(pairOut, "verdicts.jsonl"))
	if err != nil || len(vs) != 2 || vs[0].Run != "a" || vs[1].Run != "b" {
		t.Fatalf("a pair interleaves its builds trial by trial: %+v %v", vs, err)
	}

	stderr.Reset()
	noiseOut := filepath.Join(dir, "noise")
	if code := run([]string{"noise", "-manifest", frozen, "-host", bin.path, "-runs", "2", "-out", noiseOut}, getenv, &stdout, &stderr); code != exitPass {
		t.Fatalf("a noise run completes (exit %d): %s", code, stderr.String())
	}
	var n promptsuite.Noise
	raw, err = os.ReadFile(filepath.Join(noiseOut, "noise.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &n); err != nil || n.Runs != 2 || len(n.Classes) != 1 || len(n.Classes[0].Rates) != 2 {
		t.Fatalf("the noise names each class's rates across the runs: %+v %v", n, err)
	}
}

func TestATrialsWorldIsNeverInsideTheAccountsIdentities(t *testing.T) {
	live := t.TempDir()
	base := filepath.Join(live, "work")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := newHome(base, build{path: "/nonexistent"}, live, nil); err == nil || !strings.Contains(err.Error(), "inside the account's identities") {
		t.Fatalf("a world under the account's ~/.aii must be refused before anything is made: %v", err)
	}
	if left, _ := os.ReadDir(base); len(left) != 0 {
		t.Fatalf("a refused world leaves nothing behind: %v", left)
	}
}

func TestATrialSignsInOnlyToTheDashboardThatProvesItIsTheSlots(t *testing.T) {
	stranger := dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	if _, err := stranger.Start(""); err != nil { // in the clear, as a trial's host is
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stranger.Shutdown(context.Background()) })
	slot := t.TempDir()
	h := &home{root: t.TempDir(), slot: slot}
	const token = "5f0c2a9e7d4b1c8a3e6f0d2b9c7a5e1f4d8b2c6a0e3f7d1b5c9a2e6f0d4b8c3a"
	writeSlotDashboard(t, h, portOf(t, stranger.Origin()), token)
	alive := &process{done: make(chan struct{})} // the slot's host, not yet ended

	var other *install.NotServingError
	if _, _, err := h.dashboard(); !errors.As(err, &other) || !other.Other {
		t.Fatalf("a port held by a dashboard without the slot's proof is not the slot's: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if d, err := (&runner{}).attach(ctx, h, alive); err == nil {
		d.close()
		t.Fatal("the runner signed in to a dashboard that is not the slot's host")
	}

	own := dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	own.SetAccessToken(true, token)
	key, err := install.WriteServingKey(slot)
	if err != nil {
		t.Fatal(err)
	}
	own.SetServingKey(key)
	if _, err := own.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = own.Shutdown(context.Background()) })
	writeSlotDashboard(t, h, portOf(t, own.Origin()), token)
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, err := (&runner{}).attach(ctx, h, alive)
	if err != nil {
		t.Fatalf("the slot's own dashboard is attached: %v", err)
	}
	d.close()
}

func writeSlotDashboard(t *testing.T, h *home, port int, token string) {
	t.Helper()
	cfg, err := json.Marshal(map[string]any{"dashboard": map[string]any{"port": port, "require_token": true, "access_token": token}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(h.configPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeWhole(h.configPath(), cfg); err != nil {
		t.Fatal(err)
	}
}

func portOf(t *testing.T, origin string) int {
	t.Helper()
	u, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// A TRIAL THAT COULD NOT RUN IS NO VERDICT, AND IS RUN AGAIN. A model that
// gives the birth no words leaves the trial unborn: its line says why and
// grades nothing, so the run is not scored. Each trial run again alone
// (-tasks, -trials) is not scored on its own either; the files together
// are the run, and score as one.
func TestATrialThatCouldNotRunIsRunAgainAndScoredWithItsRun(t *testing.T) {
	dir := t.TempDir()
	bin := testHost(t, dir)
	const id = "voice-no-steps-narration"
	manifest := suiteOf(t, dir, -1, 2, id)
	mute := goodScript(t, id)
	mute.Greeting = "" // the founding reply is empty: nothing is born of it
	silent := newFakeModel(t, mute)
	spec := specOf(t, promptsuite.ModelSpec{Kind: promptsuite.KindOpenAICompatible, Model: silent.model, URL: silent.url()})
	getenv := func(k string) string {
		if k == promptsuite.SpecEnv {
			return spec
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	first := filepath.Join(dir, "first")
	if code := run([]string{"run", "-manifest", manifest, "-host", bin.path, "-out", first, "-tasks", id, "-trials", "1"}, getenv, &stdout, &stderr); code != exitRefused {
		t.Fatalf("a run of named trials is not scored on its own (exit %d): %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "is not scored here") || !strings.Contains(stderr.String(), "ERROR") {
		t.Fatalf("the unborn trial is said, and the subset is not scored: %s", stderr.String())
	}
	if code := run([]string{"run", "-manifest", manifest, "-host", bin.path, "-out", first}, getenv, &stdout, &stderr); code != exitRefused {
		t.Fatalf("a run's directory is never written into twice (exit %d)", code)
	}
	fixed := newFakeModel(t, goodScript(t, id))
	spec = specOf(t, promptsuite.ModelSpec{Kind: promptsuite.KindOpenAICompatible, Model: fixed.model, URL: fixed.url()})
	var files []string
	for _, n := range []string{"1", "2"} {
		out := filepath.Join(dir, "again-"+n)
		if code := run([]string{"run", "-manifest", manifest, "-host", bin.path, "-out", out, "-trials", n}, getenv, &stdout, &stderr); code != exitRefused {
			t.Fatalf("trial %s alone is not scored on its own (exit %d)", n, code)
		}
		files = append(files, filepath.Join(out, "verdicts.jsonl"))
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(append([]string{"score", "-manifest", manifest, "-run", "run", filepath.Join(first, "verdicts.jsonl")}, files[:1]...), getenv, &stdout, &stderr); code != exitRefused || !strings.Contains(stderr.String(), "INCOMPLETE RUN") {
		t.Fatalf("without its second trial the run is incomplete (exit %d): %s", code, stderr.String())
	}
	stdout.Reset()
	if code := run(append([]string{"score", "-manifest", manifest, "-run", "run", filepath.Join(first, "verdicts.jsonl")}, files...), getenv, &stdout, &stderr); code != exitPass {
		t.Fatalf("the run's files score as one complete run (exit %d): %s", code, stderr.String())
	}
	var s promptsuite.Summary
	if err := json.Unmarshal(stdout.Bytes(), &s); err != nil || s.Classes[0].Trials != 2 || s.Classes[0].Passes != 2 {
		t.Fatalf("the summary counts the trials that ran: %+v %v", s, err)
	}
}
