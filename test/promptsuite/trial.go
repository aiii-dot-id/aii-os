package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/promptsuite"
)

// runner runs trials: one disposable identity per trial, born, set up,
// spoken to, read and torn down.
type runner struct {
	m        *promptsuite.Manifest
	spec     *promptsuite.ModelSpec
	platform *platform
	// plugins plays the plugin roles a task names: the published packages
	// of a live run, or a proof's fixtures.
	plugins pluginSource
	// pluginsLabel names the packages in every verdict of the run.
	pluginsLabel string
	// work is where trials make their worlds; live is the account's own
	// ~/.aii, which no trial may be under.
	work string
	live string
	// keep leaves a trial's world in place after it ends, for a person
	// to read; a run keeps none by default.
	keep bool
	// poll and settle are how rest is judged: settle quiet looks a poll
	// apart.
	poll   time.Duration
	settle int
	log    io.Writer
	// observe, when set, is handed each graded trial's final state: the
	// offline proofs read from it what a task's grader must have faced.
	observe func(*promptsuite.State)
}

// notRecord is the line a SAFE task appends to its identity's ledger, as
// the SAFE drill does (packaging/drills/safe.sh): a complete line that is
// not a record, which the next start's verification refuses.
const notRecord = `{"promptsuite":"this line is not a ledger record"}` + "\n"

const startWait = 2 * time.Minute

// stopGrace is how long a stopping host has before it is killed.
const stopGrace = 20 * time.Second

// trial runs one trial of task t on build bin and grades it. A trial
// that could not be run or read returns a verdict with Error set and no
// grade: it is no verdict, and the run that holds it is incomplete.
func (r *runner) trial(ctx context.Context, bin build, run string, t *promptsuite.Task, n int) promptsuite.Verdict {
	began := time.Now()
	v := promptsuite.Verdict{Manifest: r.m.SHA256, Run: run, Build: bin.label(), Model: r.spec.Label(), Plugins: r.pluginsLabel,
		Task: t.ID, TaskVersion: t.Version, Class: t.Class, Trial: n, Checks: []promptsuite.CheckResult{}}
	state, rested, err := r.play(ctx, bin, t, &progress{})
	v.Seconds = time.Since(began).Seconds()
	v.At = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	v.Pass, v.Checks = promptsuite.Grade(t, state)
	if r.observe != nil {
		r.observe(state)
	}
	if rested != "" {
		// THE BOUND IS PART OF THE BAR. An identity that does not come to
		// rest within the manifest's bound fails the trial, whatever the
		// state shows at the bound: the work was not finished in the time
		// every build is given.
		v.Pass = false
		v.Checks = append(v.Checks, promptsuite.CheckResult{Kind: "rest", Why: "the identity comes to rest within the manifest's bound after each turn", Detail: rested})
	}
	return v
}

// progress is how far a trial got: what a smoke run reports, and nothing
// a verdict reads.
type progress struct {
	Born      bool     `json:"born"`
	Turns     []string `json:"turns"` // per task turn: "at rest", or why not
	Collected bool     `json:"collected"`
}

// play is the trial itself. rested is empty, or says which turn did not
// come to rest within the bound. prog records how far it got.
func (r *runner) play(ctx context.Context, bin build, t *promptsuite.Task, prog *progress) (state *promptsuite.State, rested string, err error) {
	h, err := newHome(r.work, bin, r.live, passEnv(r.spec))
	if err != nil {
		return nil, "", err
	}
	var p *process
	var d *dash
	defer func() {
		if d != nil {
			d.close()
		}
		if serr := p.stop(stopGrace); serr != nil && err == nil {
			err = serr
		}
		if r.keep {
			fmt.Fprintf(r.log, "promptsuite: trial %s kept at %s\n", t.ID, h.root)
		} else if rerr := h.remove(); rerr != nil && err == nil {
			err = rerr
		}
	}()

	roles, err := r.prepare(h, t)
	if err != nil {
		return nil, "", err
	}
	if p, d, err = r.startAttached(ctx, h); err != nil {
		return nil, "", err
	}
	if err := r.born(ctx, h, d); err != nil {
		return nil, "", err
	}
	prog.Born = true
	bound := time.Duration(r.m.TrialTimeoutSeconds) * time.Second
	if err := r.restWithin(ctx, d, bound); err != nil {
		return nil, "", fmt.Errorf("after the birth: %w", err)
	}
	if err := r.pluginsUp(ctx, d, roles); err != nil {
		return nil, "", err
	}
	for i, words := range t.Setup.Turns {
		if why, err := r.turn(ctx, d, words, bound); err != nil {
			return nil, "", fmt.Errorf("setup turn %d: %w", i+1, err)
		} else if why != "" {
			return nil, "", fmt.Errorf("setup turn %d did not come to rest: %s", i+1, why)
		}
	}
	if t.Setup.Safe {
		d.close()
		d = nil
		if err := p.stop(stopGrace); err != nil {
			return nil, "", err
		}
		if err := appendNotRecord(h); err != nil {
			return nil, "", err
		}
		if p, d, err = r.startAttached(ctx, h); err != nil {
			return nil, "", err
		}
		if err := r.restWithin(ctx, d, bound); err != nil {
			return nil, "", fmt.Errorf("after the SAFE start: %w", err)
		}
	}

	tr := &transcript{}
	mark := d.mark()
	for i, words := range t.Turns {
		ref, why, err := r.taskTurn(ctx, d, words, bound)
		if err != nil {
			return nil, "", fmt.Errorf("turn %d: %w", i+1, err)
		}
		tr.turns, tr.said = append(tr.turns, ref), append(tr.said, words)
		if why != "" {
			prog.Turns = append(prog.Turns, why)
			rested = fmt.Sprintf("turn %d: %s", i+1, why)
			break
		}
		prog.Turns = append(prog.Turns, "at rest")
	}
	tr.events = d.toolEvents(mark)
	read, cancel := context.WithTimeout(ctx, startWait)
	defer cancel()
	sources := []string{"recorded"}
	if t.Setup.Safe {
		// A SAFE identity writes nothing: what they did is held in the
		// process, and the transcript serves it as the transient source.
		sources = append(sources, "transient")
	}
	for _, source := range sources {
		rows, err := d.interactions(read, source)
		if err != nil {
			return nil, "", err
		}
		tr.rows = append(tr.rows, rows...)
	}
	ledgerJSON, err := h.ledger()
	if err != nil && !t.Setup.Safe {
		// A SAFE identity's ledger ends in the line that put them there, and
		// the reader says so and stops: what it printed before is the
		// record, and is read.
		return nil, "", err
	}
	if state, err = tr.state(ledgerJSON, h.slot, t.Setup.Files); err != nil {
		return nil, "", err
	}
	state.PluginTools = pluginTools(r.m, roles)
	prog.Collected = true
	return state, rested, nil
}

// prepare makes the trial's slot what the task's setup says, before the
// host first starts: the port, the quiet configuration and the task's
// own keys, the plugins its roles name, and the files. It returns the
// plugin each role is.
func (r *runner) prepare(h *home, t *promptsuite.Task) ([]bound, error) {
	if err := r.movePort(h); err != nil {
		return nil, err
	}
	keys := quiet(r.platform.url)
	plugins, roles, err := r.plugins.install(t, h.slot)
	if err != nil {
		return nil, err
	}
	merge(keys, plugins)
	merge(keys, t.Setup.Config)
	if err := h.editConfig(keys); err != nil {
		return nil, err
	}
	for rel, body := range t.Setup.Files {
		file := filepath.Join(h.slot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			return nil, err
		}
	}
	return roles, nil
}

const portTries = 3

// startAttached starts the trial's host and signs in to its dashboard. A
// host that ended because its port was taken in the meantime is started
// again on another port, at most portTries times in all.
func (r *runner) startAttached(ctx context.Context, h *home) (*process, *dash, error) {
	for try := 1; ; try++ {
		p, err := h.start()
		if err != nil {
			return nil, nil, err
		}
		d, err := r.attach(ctx, h, p)
		if err == nil {
			return p, d, nil
		}
		var ended *hostEndedError
		if try >= portTries || !errors.As(err, &ended) || !strings.Contains(ended.tail, "address already in use") {
			return p, nil, err
		}
		if serr := p.stop(stopGrace); serr != nil {
			return nil, nil, serr
		}
		if err := r.movePort(h); err != nil {
			return nil, nil, err
		}
	}
}

// hostEndedError is a host that ended before its dashboard answered, with
// the end of its output: the trial's directory is a test's temporary one
// and is gone by the time anyone reads the failure.
type hostEndedError struct {
	err  error
	out  string
	tail string
}

func (e *hostEndedError) Error() string {
	return fmt.Sprintf("the host ended before its dashboard answered (%v); its output is %s, ending:\n%s", e.err, e.out, e.tail)
}

// attach waits for a started host's dashboard and signs in to it.
func (r *runner) attach(ctx context.Context, h *home, p *process) (*dash, error) {
	ctx, cancel := context.WithTimeout(ctx, startWait)
	defer cancel()
	var last error
	for {
		if !p.alive() {
			out := filepath.Join(h.root, "host.out")
			tail := tailOf(out, hostOutTail)
			return nil, &hostEndedError{err: p.err, out: out, tail: tail}
		}
		origin, token, err := h.dashboard()
		if err == nil {
			d, cerr := connect(ctx, origin, token)
			if cerr == nil {
				return d, nil
			}
			err = cerr
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("the host's dashboard did not answer within %s: %v", startWait, last)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (r *runner) born(ctx context.Context, h *home, d *dash) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.m.TrialTimeoutSeconds)*time.Second)
	defer cancel()
	entry := promptsuite.Entry[r.spec.Kind]
	if err := d.discover(ctx, entry, r.spec.URL); err != nil {
		return fmt.Errorf("have the provider registry written: %w", err)
	}
	endpoint, err := provide(filepath.Dir(h.configPath()), r.spec)
	if err != nil {
		return err
	}
	if _, err := d.birth(ctx, entry, endpoint, r.spec.Model); err != nil {
		return err
	}
	return nil
}

// turn speaks setup words and waits for rest. why is non-empty when the
// identity did not come to rest within the bound.
func (r *runner) turn(ctx context.Context, d *dash, words string, bound time.Duration) (why string, err error) {
	_, why, err = r.taskTurn(ctx, d, words, bound)
	return why, err
}

// taskTurn speaks one turn's words and waits, within the bound, for the
// reply and then for rest. A turn that runs past the bound is not an
// error: it is what the identity did, and why says so.
func (r *runner) taskTurn(ctx context.Context, d *dash, words string, bound time.Duration) (*interaction.Location, string, error) {
	tctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	ref, err := d.chat(tctx, words)
	if errors.Is(err, context.DeadlineExceeded) && ref != nil {
		return ref, fmt.Sprintf("no reply within %s", bound), nil
	}
	if err != nil {
		return nil, "", err
	}
	if err := d.rest(tctx, r.poll, r.settle); errors.Is(err, context.DeadlineExceeded) {
		return ref, fmt.Sprintf("not at rest within %s", bound), nil
	} else if err != nil {
		return nil, "", err
	}
	return ref, "", nil
}

func (r *runner) pluginsUp(ctx context.Context, d *dash, roles []bound) error {
	if len(roles) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, startWait)
	defer cancel()
	for {
		active, err := d.activePlugins(ctx)
		if err != nil {
			return fmt.Errorf("the plugins the task needs did not come up within %s: %w", startWait, err)
		}
		var missing []string
		for _, b := range roles {
			if !active[b.id] {
				missing = append(missing, b.role+" ("+b.id+")")
			}
		}
		if len(missing) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the plugins the task needs did not come up within %s: %s not active", startWait, strings.Join(missing, ", "))
		case <-time.After(r.poll):
		}
	}
}

// restWithin waits for rest after a birth or a start: there is no turn
// to grade, so a host that will not rest is an error.
func (r *runner) restWithin(ctx context.Context, d *dash, bound time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	return d.rest(ctx, r.poll, r.settle)
}

// appendNotRecord puts the SAFE drill's line at the ledger's tail.
func appendNotRecord(h *home) error {
	f, err := os.OpenFile(filepath.Join(h.slot, "data", "ledger.jsonl"), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(notRecord); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// passEnv is the one variable a trial's host is handed from the runner's
// environment: the one the spec names for its key.
func passEnv(s *promptsuite.ModelSpec) []string {
	if s.KeyEnv == "" {
		return nil
	}
	return []string{s.KeyEnv}
}

func provide(configDir string, s *promptsuite.ModelSpec) (string, error) {
	file := filepath.Join(configDir, "providers.json")
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("the slot's provider registry: %w", err)
	}
	var reg map[string]any
	if err := json.Unmarshal(raw, &reg); err != nil {
		return "", fmt.Errorf("the slot's provider registry: %w", err)
	}
	list, _ := reg["providers"].([]any)
	name := promptsuite.Entry[s.Kind]
	var entry map[string]any
	for _, e := range list {
		if m, ok := e.(map[string]any); ok && m["name"] == name {
			entry = m
		}
	}
	if entry == nil {
		return "", fmt.Errorf("the host's registry has no entry %q for a %s spec", name, s.Kind)
	}
	switch s.Kind {
	case promptsuite.KindAnthropic, promptsuite.KindGemini:
		entry["api_key_env"] = s.KeyEnv
	case promptsuite.KindChatGPT, promptsuite.KindClaudeSubscription:
		opts, _ := entry["credential_options"].(map[string]any)
		if opts == nil {
			opts = map[string]any{}
		}
		opts["file"] = s.CredentialFile
		entry["credential_options"] = opts
	case promptsuite.KindOpenAICompatible:
		entry["url"] = s.URL
		if s.KeyEnv != "" {
			entry["api_key_env"] = s.KeyEnv
		}
	}
	out, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeWhole(file, append(out, '\n')); err != nil {
		return "", err
	}
	endpoint, _ := entry["url"].(string)
	return endpoint, nil
}

// hostOutTail is how many lines of a failed host's output its error carries:
// the trial's directory is a test's temporary one and is gone by the time
// anyone reads the failure.
const hostOutTail = 40

// tailOf is the last n lines of the file at path, or why it could not be read.
func tailOf(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(" + err.Error() + ")"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (r *runner) movePort(h *home) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	if out, err := h.aii("dashboard-port", "-dir", h.slot, strconv.Itoa(port)); err != nil {
		return fmt.Errorf("aii dashboard-port: %w\n%s", err, out)
	}
	return nil
}
