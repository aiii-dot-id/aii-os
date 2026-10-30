package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/promptsuite"
)

// proof is one task's two transcripts.
type proof struct {
	Task    string  `json:"task"`
	Good    script  `json:"good"`
	Failure failure `json:"failure"`
	// Absent, for a task whose answer must be recalled: text that must
	// not be in the first request the task's turn sends, so the task
	// does hold what it claims to (the fact is outside the window).
	Absent *absence `json:"absent,omitempty"`
	Noted  []int    `json:"noted,omitempty"`
	// Plugins binds each plugin role the task names to the fixture that
	// plays it in the proof (testdata/fixtures.json), by key.
	Plugins map[string]string `json:"plugins,omitempty"`
}

type failure struct {
	DrawnFrom string `json:"drawn_from"`
	// Fails are the checks (from 1, in the task's order) the failure
	// must fail.
	Fails []int `json:"fails"`
	script
}

type absence struct {
	On    string   `json:"on"`
	Texts []string `json:"texts"`
}

// proofTrialSeconds bounds an offline trial's rests. The scripted model
// answers at once, so a trial that does not rest within it is a runner or
// script fault; the manifest's own bound is a live model's.
const proofTrialSeconds = 120

// proofParallel is how many offline trials run at once: each is a host
// process, its plugin workers and a scripted model, and more of them
// side by side are slower, not more proof.
const proofParallel = 4

var (
	hostOnce  sync.Once
	hostDir   string
	hostBuilt build
	hostErr   error
)

// TestMain gives the test binary a directory for the one host it builds,
// shared by its tests and removed when they end.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "promptsuite-host-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "the test binary's host directory:", err)
		os.Exit(1)
	}
	hostDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// testHost builds this tree's cmd/aii once for the test binary.
func testHost(t *testing.T, _ string) build {
	t.Helper()
	dir := hostDir
	hostOnce.Do(func() {
		goTool := os.Getenv("AII_GO")
		if goTool == "" {
			goTool = "go"
		}
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			hostErr = err
			return
		}
		hostBuilt, hostErr = buildHost(goTool, root, filepath.Join(dir, "aii"+exeSuffix()))
	})
	if hostErr != nil {
		t.Fatal(hostErr)
	}
	return hostBuilt
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func loadProofs(t *testing.T) map[string]proof {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "proofs", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]proof{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var p proof
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if p.Task+".json" != filepath.Base(f) {
			t.Fatalf("%s holds the proof of %q", f, p.Task)
		}
		out[p.Task] = p
	}
	return out
}

func loadSuite(t *testing.T) *promptsuite.Manifest {
	t.Helper()
	m, err := promptsuite.LoadManifest("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEveryTaskHasOneProofAndEveryProofATask(t *testing.T) {
	m, proofs := loadSuite(t), loadProofs(t)
	var missing, stray []string
	for _, task := range m.Tasks {
		if _, ok := proofs[task.ID]; !ok {
			missing = append(missing, task.ID)
		}
	}
	for id := range proofs {
		if m.Task(id) == nil {
			stray = append(stray, id)
		}
	}
	sort.Strings(stray)
	if len(missing)+len(stray) > 0 {
		t.Fatalf("tasks without a proof: %v; proofs without a task: %v", missing, stray)
	}
	fixtures := loadFixtures(t)
	for _, task := range m.Tasks {
		p := proofs[task.ID]
		for _, role := range task.Setup.Plugins {
			if f := fixtures[p.Plugins[role]]; f == nil || f.Role != role {
				t.Errorf("%s: the proof binds no fixture of the %s role", task.ID, role)
			}
		}
		if len(p.Plugins) != len(task.Setup.Plugins) {
			t.Errorf("%s: the proof binds %d role(s) and the task names %d", task.ID, len(p.Plugins), len(task.Setup.Plugins))
		}
		if strings.TrimSpace(p.Failure.DrawnFrom) == "" || len(p.Failure.Fails) == 0 {
			t.Errorf("%s: the failure transcript must say what it is drawn from and which checks it fails", task.ID)
		}
		for _, n := range p.Failure.Fails {
			if n < 1 || n > len(task.Checks) {
				t.Errorf("%s: the failure names check %d of %d", task.ID, n, len(task.Checks))
			}
		}
	}
}

// TestEveryGraderPassesItsTaskDoneWellAndFailsTheFailureItWasDrawnFrom is
// the offline proof of every grader in the manifest.
func TestEveryGraderPassesItsTaskDoneWellAndFailsTheFailureItWasDrawnFrom(t *testing.T) {
	m, proofs := loadSuite(t), loadProofs(t)
	// The offline bound replaces the live one; nothing else of the
	// manifest changes.
	offline := *m
	offline.TrialTimeoutSeconds = proofTrialSeconds
	dir := t.TempDir()
	bin := testHost(t, dir)
	plat, err := servePlatform()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plat.close() })
	signer := newFixtureSigner(dir)
	fixtures := loadFixtures(t)
	work := filepath.Join(dir, "worlds")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, proofParallel)

	for i := range m.Tasks {
		task := &m.Tasks[i]
		p, ok := proofs[task.ID]
		if !ok {
			t.Fatalf("%s has no proof", task.ID)
		}
		t.Run(task.ID, func(t *testing.T) {
			t.Parallel()
			slots <- struct{}{}
			defer func() { <-slots }()
			var state *promptsuite.State
			trial := func(side string, sc script) (promptsuite.Verdict, *fakeModel) {
				fake := newFakeModel(t, sc)
				spec := &promptsuite.ModelSpec{Kind: promptsuite.KindOpenAICompatible, Model: fake.model, URL: fake.url()}
				r := &runner{m: &offline, spec: spec, platform: plat, work: work,
					plugins: fixturePlugins{signer: signer, fixtures: fixtures, bind: p.Plugins},
					live:    liveRoot(t), poll: 50 * time.Millisecond, settle: 3, log: testLog{t},
					observe: func(s *promptsuite.State) { state = s }}
				v := r.trial(context.Background(), bin, side, task, 1)
				if v.Error != "" {
					t.Fatalf("%s: the trial could not run: %s", side, v.Error)
				}
				if un := fake.unserved(); len(un) > 0 {
					t.Fatalf("%s: the host did not follow the script, so it proves nothing: %s", side, strings.Join(un, "; "))
				}
				return v, fake
			}

			good, fake := trial("good", p.Good)
			if !good.Pass {
				t.Errorf("the task done well must pass:\n%s", describeChecks(good.Checks))
			}
			for _, n := range p.Noted {
				if n < 1 || n > len(state.Turns) || len(state.Turns[n-1].Notes) == 0 {
					t.Errorf("turn %d of the task done well carries no host note, so its grader faced none", n)
				}
			}
			if a := p.Absent; a != nil {
				first := fake.firstRequest(a.On)
				if first == "" {
					t.Errorf("no request opened %q", a.On)
				}
				for _, text := range a.Texts {
					if strings.Contains(first, text) {
						t.Errorf("the turn's first request already holds %q: the task does not need what it says it needs", text)
					}
				}
			}

			bad, _ := trial("failure", p.Failure.script)
			if bad.Pass {
				t.Fatalf("the failure the task was drawn from (%s) must fail:\n%s", p.Failure.DrawnFrom, describeChecks(bad.Checks))
			}
			for _, n := range p.Failure.Fails {
				if bad.Checks[n-1].Pass {
					t.Errorf("the failure (%s) must fail check %d (%s), which held: %s", p.Failure.DrawnFrom, n, bad.Checks[n-1].Kind, bad.Checks[n-1].Detail)
				}
			}
			for i, c := range bad.Checks {
				if !c.Pass && !slices.Contains(p.Failure.Fails, i+1) {
					t.Errorf("the failure fails check %d (%s), which its proof does not name: %s", i+1, c.Kind, c.Detail)
				}
			}
		})
	}
}

func describeChecks(cs []promptsuite.CheckResult) string {
	var b strings.Builder
	for i, c := range cs {
		fmt.Fprintf(&b, "  %d %s pass=%v: %s\n", i+1, c.Kind, c.Pass, c.Detail)
	}
	return b.String()
}

// liveRoot is the account's own ~/.aii, which no trial may be under.
func liveRoot(t *testing.T) string {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".aii")
}

// testLog routes the runner's notes to the test's log.
type testLog struct{ t *testing.T }

func (l testLog) Write(b []byte) (int, error) {
	l.t.Log(strings.TrimRight(string(b), "\n"))
	return len(b), nil
}
