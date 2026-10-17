//go:build acceptance

// Command acceptance is the pilot's runner. It has two verbs and they
// never meet:
//
//	drive  sends each case of a frozen manifest's set to a DISPOSABLE
//	       identity through its dashboard socket, waits for the identity
//	       to come to rest, and writes one observation line per attempt.
//	       It judges nothing.
//	score  joins the manifest, a human judge's verdicts and the driver's
//	       observations through internal/acceptance, which refuses an
//	       incomplete run.
//
// The build tag keeps it out of the default suite, so a green host gate
// says nothing about a live run: that is its own recorded invocation,
//
//	AII_ACCEPTANCE_DISPOSABLE=1 AII_ROOT=… AII_SLOT=… \
//	  go run -tags acceptance ./test/acceptance drive -manifest m.json -set development -out obs.jsonl
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/acceptance"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// Exit statuses.
const (
	exitPass    = 0 // drive: every case observed; score: the run passes
	exitFail    = 1 // drive: the run stopped; score: the run does not pass
	exitRefused = 2 // a refusal, an incomplete run, or a usage error
)

// restPoll is how often an attempt looks for rest, and restSettle how
// many consecutive looks must find it: one quiet look can fall in the
// gap between a turn ending and the work it queued being claimed.
const (
	restPoll   = time.Second
	restSettle = 2
	// probeWait bounds one answer from the dashboard, apart from the
	// attempt's own bound: a socket that stops answering is a failed
	// observer, not a busy identity.
	probeWait = 30 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: acceptance drive|score [flags]")
		return exitRefused
	}
	switch args[0] {
	case "drive":
		return runDrive(args[1:], getenv, stderr)
	case "score":
		return runScore(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "acceptance: unknown verb %q (drive or score)\n", args[0])
	return exitRefused
}

func runDrive(args []string, getenv func(string) string, stderr io.Writer) int {
	fs := flag.NewFlagSet("drive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifest := fs.String("manifest", "", "the frozen manifest")
	set := fs.String("set", "", "development or heldout")
	out := fs.String("out", "", "the observations file to create (never overwritten)")
	if err := fs.Parse(args); err != nil {
		return exitRefused
	}
	if *manifest == "" || *set == "" || *out == "" {
		fmt.Fprintln(stderr, "drive: -manifest, -set and -out are required")
		return exitRefused
	}
	// The slot is refused before anything else is read.
	t, err := resolveTarget(getenv, getenv(liveIdentitiesEnv))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	m, err := acceptance.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	cases, err := m.Cases(*set)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	st, err := store.OpenReadOnly(t.dbPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFail
	}
	defer st.Close()
	// Evidence is never overwritten: a second run is a second file.
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	defer f.Close()
	d := &driver{target: t, obs: observer{st}, timeout: time.Duration(m.AttemptTimeoutSeconds) * time.Second, poll: restPoll, log: stderr}
	fmt.Fprintf(stderr, "acceptance: manifest %s, set %s, %d case(s), slot %s\n", m.SHA256, *set, len(cases), t.slotDir)
	if err := d.drive(context.Background(), cases, f); err != nil {
		fmt.Fprintf(stderr, "acceptance: the run stopped: %v — the observations written so far are kept, and a report over them is refused as incomplete\n", err)
		return exitFail
	}
	return exitPass
}

func runScore(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifest := fs.String("manifest", "", "the frozen manifest")
	set := fs.String("set", "", "development or heldout")
	verdicts := fs.String("verdicts", "", "the judge's verdicts (JSON Lines)")
	observations := fs.String("observations", "", "the driver's observations (JSON Lines)")
	if err := fs.Parse(args); err != nil {
		return exitRefused
	}
	m, err := acceptance.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	vs, err := acceptance.LoadVerdicts(*verdicts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	obs, err := acceptance.LoadObservations(*observations)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	r, err := acceptance.Score(m, *set, vs, obs)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		fmt.Fprintln(stderr, err)
		return exitRefused
	}
	if !r.Pass {
		return exitFail
	}
	return exitPass
}

// driver runs attempts one at a time against one slot.
type driver struct {
	target  target
	obs     observer
	timeout time.Duration
	poll    time.Duration
	log     io.Writer
}

// drive attempts every case in order and writes each observation as it
// is made, synced, so a run that stops keeps what it saw.
func (d *driver) drive(ctx context.Context, cases []acceptance.Case, out *os.File) error {
	enc := json.NewEncoder(out)
	for _, c := range cases {
		o, err := d.attempt(ctx, c)
		if err != nil {
			return fmt.Errorf("case %s: %w", c.ID(), err)
		}
		if err := enc.Encode(o); err != nil {
			return err
		}
		if err := out.Sync(); err != nil {
			return err
		}
		fmt.Fprintf(d.log, "acceptance: %s delivery=%s quiescent=%v usage_complete=%v unattributed=%v\n",
			o.AttemptID, o.Delivery, o.Quiescent, o.UsageComplete, o.Unattributed)
	}
	return nil
}

// attempt sends one case and watches the identity until it comes to
// rest or the bound runs out. An error means the driver could not
// observe — a failed socket or database read — and ends the run; a
// timeout is an observation (quiescent false), never an error and never
// a success.
func (d *driver) attempt(ctx context.Context, c acceptance.Case) (acceptance.Observation, error) {
	s, err := connect(ctx, d.target)
	if err != nil {
		return acceptance.Observation{}, err
	}
	defer s.close()

	// Rest before the attempt, so its window holds its own work. When
	// the identity will not settle the attempt still runs, and its
	// window is declared unattributed.
	settled, err := d.awaitRest(ctx, s, nil, time.Now().Add(d.timeout))
	if err != nil {
		return acceptance.Observation{}, err
	}
	before, err := d.obs.alarmIDs()
	if err != nil {
		return acceptance.Observation{}, err
	}

	start := time.Now()
	deadline := start.Add(d.timeout)
	o := acceptance.Observation{
		AttemptID:      fmt.Sprintf("%s-%d", c.ID(), start.UnixMilli()),
		VariantID:      c.VariantID,
		NegativeCaseID: c.NegativeCaseID,
		StartMS:        start.UnixMilli(),
		Unattributed:   !settled,
	}
	cctx, cancel := context.WithDeadline(ctx, deadline)
	o.Delivery, err = s.chat(cctx, o.AttemptID, c.Input)
	cancel()
	if err != nil {
		return acceptance.Observation{}, err
	}
	if time.Now().Before(deadline) {
		if o.Quiescent, err = d.awaitRest(ctx, s, before, deadline); err != nil {
			return acceptance.Observation{}, err
		}
	}
	o.EndMS = time.Now().UnixMilli()

	if o.Usage, o.UsageGaps, err = d.obs.usage(o.StartMS, o.EndMS); err != nil {
		return acceptance.Observation{}, err
	}
	if !o.Quiescent {
		o.UsageGaps = append(o.UsageGaps, "the attempt did not come to rest within its bound — work after end_ms is outside the window")
	}
	o.UsageComplete = len(o.UsageGaps) == 0
	return o, nil
}

// awaitRest polls until the identity is at rest on restSettle
// consecutive looks — no turn running (the dashboard's probe), no open
// work session, nothing pending or claimed in the work queue, and no
// alarm armed since before (when given) — or until deadline. It reports
// false at the deadline and an error only when it could not look.
func (d *driver) awaitRest(ctx context.Context, s *session, before map[string]bool, deadline time.Time) (bool, error) {
	quiet := 0
	for {
		pctx, cancel := context.WithTimeout(ctx, probeWait)
		active, err := s.turnActive(pctx)
		cancel()
		if err != nil {
			return false, fmt.Errorf("ask the dashboard whether a turn runs: %w", err)
		}
		busy := active
		if !busy {
			if busy, err = d.obs.busy(before); err != nil {
				return false, err
			}
		}
		if busy {
			quiet = 0
		} else {
			quiet++
			if quiet == restSettle {
				return true, nil
			}
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(min(d.poll, time.Until(deadline))):
		}
	}
}
