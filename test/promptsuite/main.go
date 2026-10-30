package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/promptsuite"
)

// Exit statuses.
const (
	exitPass    = 0 // the run is complete (and, for a comparison, passes)
	exitFail    = 1 // a comparison that does not pass, or a run that stopped
	exitRefused = 2 // a refusal: a usage error, an incomplete run, an unfrozen manifest
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: promptsuite smoke|run|pair|noise|score|compare [flags]")
		return exitRefused
	}
	switch args[0] {
	case "run", "pair", "noise", "smoke":
		return runTrials(args[0], args[1:], getenv, stdout, stderr)
	case "score":
		return runScore(args[1:], stdout, stderr)
	case "compare":
		return runCompare(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "promptsuite: unknown verb %q\n", args[0])
	return exitRefused
}

// hostFlags names a build: a binary already built, or a source tree the
// runner builds it from.
type hostFlags struct{ bin, src *string }

func (h hostFlags) take(goTool, out string) (build, error) {
	switch {
	case *h.bin != "" && *h.src != "":
		return build{}, fmt.Errorf("name a binary or a source tree, not both")
	case *h.bin != "":
		return takeHost(*h.bin)
	case *h.src != "":
		return buildHost(goTool, *h.src, out)
	}
	return build{}, fmt.Errorf("no host: name a binary or a source tree")
}

func runTrials(verb string, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifest := fs.String("manifest", "", "the frozen manifest")
	specFile := fs.String("spec", "", "the model spec file (else "+promptsuite.SpecEnv+")")
	out := fs.String("out", "", "the directory to write the run into (created; never overwritten)")
	label := fs.String("label", "run", "run: the run's label in its verdicts")
	only := fs.String("tasks", "", "a comma-separated subset of task ids (a subset is never a complete run)")
	smokeTask := fs.String("task", "", "smoke: the one task to run once")
	pluginDir := fs.String("plugins", "", "the directory, outside the tree, holding the published plugin packages the tasks' roles need")
	trialList := fs.String("trials", "", "a comma-separated subset of trial numbers, to run again the trials a run could not (merge with score)")
	runs := fs.Int("runs", 2, "noise: how many runs of the build")
	keep := fs.Bool("keep", false, "keep each trial's world for a person to read")
	a := hostFlags{fs.String("host", "", "the host binary (pair: build A's)"), fs.String("src", "", "a source tree to build the host from (pair: build A's)")}
	b := hostFlags{fs.String("b-host", "", "pair: build B's binary"), fs.String("b-src", "", "pair: build B's source tree")}
	targets := fs.String("targets", "", "pair: the classes the change targets, comma-separated")
	if err := fs.Parse(args); err != nil {
		return exitRefused
	}
	if *manifest == "" || *out == "" {
		fmt.Fprintln(stderr, verb+": -manifest and -out are required")
		return exitRefused
	}
	m, err := promptsuite.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	spec, err := promptsuite.LoadSpec(*specFile, getenv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	if err := spec.Ready(getenv); err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	if verb == "pair" {
		// Refused before a trial runs: a pair run's purpose is the
		// comparison, and without thresholds there is none to make.
		if ok, open := m.Frozen(); !ok {
			fmt.Fprintln(stderr, (&promptsuite.Unfrozen{Classes: open}).Error())
			return exitRefused
		}
	}
	if verb == "smoke" {
		if *smokeTask == "" || *only != "" || *trialList != "" {
			fmt.Fprintln(stderr, "smoke: name one task with -task, and no -tasks or -trials")
			return exitRefused
		}
		*only = *smokeTask
	}
	tasks, err := pick(m, *only)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	// The plugins are bound before anything is made: a run that needs
	// them and was given none, or given packages that do not verify or do
	// not single out a package per role, is refused with why.
	var plugins pluginSource = noPlugins{}
	var pub *published
	if roles := promptsuite.Roles(tasks, m); len(roles) > 0 {
		if *pluginDir == "" {
			fmt.Fprintf(stderr, "%s: the tasks name the plugin role(s) %s, and a live run %v\n", verb, roleNames(roles), errNoPlugins)
			return exitRefused
		}
		if pub, err = loadPublished(*pluginDir, roles, shippedTrust()); err != nil {
			fmt.Fprintf(stderr, "%s: -plugins %s: %v\n", verb, *pluginDir, err)
			return exitRefused
		}
		plugins = pub
	}
	trials, err := trialNumbers(m, *trialList)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	live, err := install.Root()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	if err := os.Mkdir(*out, 0o700); err != nil {
		fmt.Fprintf(stderr, "%s: the run's directory must be new: %v\n", verb, err)
		return exitRefused
	}
	goTool := getenv("AII_GO")
	if goTool == "" {
		goTool = "go"
	}
	builds := []build{}
	for i, h := range []hostFlags{a, b} {
		if i == 1 && verb != "pair" {
			break
		}
		bin, err := h.take(goTool, filepath.Join(*out, fmt.Sprintf("aii-%c", 'a'+i)))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRefused
		}
		builds = append(builds, bin)
	}
	plat, err := servePlatform()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	defer plat.close()
	r := &runner{m: m, spec: spec, platform: plat, plugins: plugins, work: filepath.Join(*out, "worlds"),
		live: live, keep: *keep, poll: time.Second, settle: 2, log: stderr}
	if err := os.Mkdir(r.work, 0o700); err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	if pub != nil {
		r.pluginsLabel = pub.label()
		if err := writeJSON(filepath.Join(*out, "plugins.json"), pub.records()); err != nil {
			fmt.Fprintln(stderr, err)
			return exitFail
		}
	}
	if verb == "smoke" {
		return smoke(r, builds[0], tasks[0], pub, *out, stdout, stderr)
	}
	verdicts, err := os.OpenFile(filepath.Join(*out, "verdicts.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	defer verdicts.Close()
	fmt.Fprintf(stderr, "promptsuite: manifest %s revision %d, %d task(s) × %d trial(s), model %s\n", m.SHA256, m.Revision, len(tasks), m.TrialsPerTask, spec.Label())

	// THE PLAN. A pair interleaves its builds trial by trial, so the two
	// share whatever the provider's day does to them; noise runs one
	// build as several labelled runs, interleaved the same way.
	type leg struct {
		bin   build
		label string
	}
	var legs []leg
	switch verb {
	case "run":
		legs = []leg{{builds[0], *label}}
	case "pair":
		legs = []leg{{builds[0], "a"}, {builds[1], "b"}}
	case "noise":
		if *runs < 2 {
			fmt.Fprintln(stderr, "noise: -runs must be 2 or more")
			return exitRefused
		}
		for i := 1; i <= *runs; i++ {
			legs = append(legs, leg{builds[0], fmt.Sprintf("r%d", i)})
		}
	}
	enc := json.NewEncoder(verdicts)
	var all []promptsuite.Verdict
	ctx := context.Background()
	for _, t := range tasks {
		for _, n := range trials {
			for _, l := range legs {
				v := r.trial(ctx, l.bin, l.label, t, n)
				all = append(all, v)
				if err := enc.Encode(v); err != nil {
					fmt.Fprintln(stderr, err)
					return exitFail
				}
				if err := verdicts.Sync(); err != nil {
					fmt.Fprintln(stderr, err)
					return exitFail
				}
				word := "fail"
				switch {
				case v.Error != "":
					word = "ERROR " + v.Error
				case v.Pass:
					word = "pass"
				}
				fmt.Fprintf(stderr, "promptsuite: %s %s#%d %s (%.0f s)\n", l.label, t.ID, n, word, v.Seconds)
			}
		}
	}
	if len(tasks) != len(m.Tasks) || len(trials) != m.TrialsPerTask {
		fmt.Fprintf(stderr, "promptsuite: %d of %d tasks and %d of %d trials ran: a subset is no complete run and is not scored here; score it with the run it completes\n", len(tasks), len(m.Tasks), len(trials), m.TrialsPerTask)
		return exitRefused
	}
	var summaries []*promptsuite.Summary
	for _, l := range legs {
		s, err := promptsuite.Summarize(m, all, l.label)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRefused
		}
		summaries = append(summaries, s)
		if err := writeJSON(filepath.Join(*out, "summary-"+l.label+".json"), s); err != nil {
			fmt.Fprintln(stderr, err)
			return exitFail
		}
	}
	switch verb {
	case "pair":
		c, err := promptsuite.Compare(m, summaries[0], summaries[1], splitList(*targets))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRefused
		}
		if err := writeJSON(filepath.Join(*out, "comparison.json"), c); err != nil {
			fmt.Fprintln(stderr, err)
			return exitFail
		}
		printJSON(stdout, c)
		if !c.Pass {
			return exitFail
		}
	case "noise":
		n, err := promptsuite.MeasureNoise(m, summaries)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return exitRefused
		}
		if err := writeJSON(filepath.Join(*out, "noise.json"), n); err != nil {
			fmt.Fprintln(stderr, err)
			return exitFail
		}
		printJSON(stdout, n)
	default:
		printJSON(stdout, summaries[0])
	}
	return exitPass
}

func runScore(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifest := fs.String("manifest", "", "the frozen manifest")
	label := fs.String("run", "run", "the run's label")
	if err := fs.Parse(args); err != nil {
		return exitRefused
	}
	m, err := promptsuite.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	vs, err := promptsuite.LoadVerdicts(fs.Args()...)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	s, err := promptsuite.Summarize(m, vs, *label)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	printJSON(stdout, s)
	return exitPass
}

func runCompare(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifest := fs.String("manifest", "", "the frozen manifest")
	a := fs.String("a", "a", "run A's label")
	b := fs.String("b", "b", "run B's label")
	targets := fs.String("targets", "", "the classes the change targets, comma-separated")
	if err := fs.Parse(args); err != nil {
		return exitRefused
	}
	m, err := promptsuite.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	if ok, open := m.Frozen(); !ok {
		fmt.Fprintln(stderr, (&promptsuite.Unfrozen{Classes: open}).Error())
		return exitRefused
	}
	vs, err := promptsuite.LoadVerdicts(fs.Args()...)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	sa, err := promptsuite.Summarize(m, vs, *a)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	sb, err := promptsuite.Summarize(m, vs, *b)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	c, err := promptsuite.Compare(m, sa, sb, splitList(*targets))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	printJSON(stdout, c)
	if !c.Pass {
		return exitFail
	}
	return exitPass
}

// smokeReport is what a smoke run says: how far one trial got. It grades
// nothing: whether a birth, a turn and the read of the final state work
// on a spec is a question about the runner and the model's route, asked
// before any trial is scored.
type smokeReport struct {
	Task    string         `json:"task"`
	Build   string         `json:"build"`
	Model   string         `json:"model"`
	Plugins []pluginRecord `json:"plugins"`
	progress
	// Seen counts what the read of the final state found.
	Seen  map[string]int `json:"seen,omitempty"`
	Error string         `json:"error,omitempty"`
	OK    bool           `json:"ok"`
}

// smoke runs one trial of t and reports how far it got.
func smoke(r *runner, bin build, t *promptsuite.Task, pub *published, out string, stdout, stderr io.Writer) int {
	rep := smokeReport{Task: t.ID, Build: bin.label(), Model: r.spec.Label(), Plugins: []pluginRecord{}}
	if pub != nil {
		rep.Plugins = pub.records()
	}
	state, rested, err := r.play(context.Background(), bin, t, &rep.progress)
	if err != nil {
		rep.Error = err.Error()
	}
	if state != nil {
		rep.Seen = map[string]int{"calls": len(state.Calls), "deliveries": len(state.Deliveries), "ledger_records": len(state.Ledger), "files": len(state.Files)}
		replies := 0
		for _, turn := range state.Turns {
			if turn.Reply != "" {
				replies++
			}
		}
		rep.Seen["replies"] = replies
	}
	rep.OK = err == nil && rested == "" && rep.Born && rep.Collected && len(rep.Turns) == len(t.Turns)
	if err := writeJSON(filepath.Join(out, "smoke.json"), rep); err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	printJSON(stdout, rep)
	if !rep.OK {
		return exitFail
	}
	return exitPass
}

func roleNames(roles []promptsuite.Role) string {
	var names []string
	for _, r := range roles {
		names = append(names, r.Role)
	}
	return strings.Join(names, ", ")
}

// pick is the manifest's tasks, or the named subset of them.
func pick(m *promptsuite.Manifest, only string) ([]*promptsuite.Task, error) {
	var out []*promptsuite.Task
	if only == "" {
		for i := range m.Tasks {
			out = append(out, &m.Tasks[i])
		}
		return out, nil
	}
	for _, id := range splitList(only) {
		t := m.Task(id)
		if t == nil {
			return nil, fmt.Errorf("task %q is not in the manifest", id)
		}
		out = append(out, t)
	}
	return out, nil
}

// trialNumbers is every trial number of the manifest, or the named ones.
func trialNumbers(m *promptsuite.Manifest, list string) ([]int, error) {
	if list == "" {
		out := make([]int, m.TrialsPerTask)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}
	var out []int
	for _, p := range splitList(list) {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > m.TrialsPerTask {
			return nil, fmt.Errorf("trial %q is not one of 1 to %d", p, m.TrialsPerTask)
		}
		out = append(out, n)
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func writeJSON(file string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
