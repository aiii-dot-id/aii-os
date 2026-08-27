// Package supervisor is the process boundary of the plugin plane —
// build-order step 5 (docs/PLUGIN_FRAMEWORK.md §15; threat model
// boundary 2: the process boundary isolates the worker from the
// resident, A8: a worker crash must never take the identity down).
//
// It is deliberately a process babysitter, not an orchestration
// framework: spawn a child speaking framed BBB on its standard streams
// (DELTA_D1 D1-1), relay one invocation at a time, forward the child's
// upstream guest hostcalls to a Dispatcher, capture stderr as
// out-of-band crash telemetry (threat model §7 — a crashed worker
// cannot report its own crash), restart with bounded backoff, and stop
// at a ceiling. Four states — starting, running, restarting, stopped —
// and nothing else: no queues, no health protocol beyond process
// liveness plus the child's own load-time admission (DELTA_D1 N-7:
// readiness IS lifecycle progress; no ping method exists).
//
// Transport (D1-1, adopted): the host writes request frames to the
// child's stdin and reads response/request frames from its stdout,
// 4-byte big-endian length prefix, 1 MiB ceiling BOTH directions (the
// host is stricter than its 16 MiB server bound with children it
// spawned — D1-1 rule 2). stderr carries no frames. EOF is disconnect;
// an oversize or desynced stream has no resync and the child is killed
// and restarted (AUDIT §2.2, N-8).
//
// Full duplex on one stdio pair: both directions carry requests. A
// frame WITH a "method" member is a request from the child (a guest
// hostcall forwarded upstream by the worker's -forward mode); any
// other frame is the child's answer to the host's one outstanding
// request — the daemon's own classification rule (rpc.c:3433-3441 via
// AUDIT §6.4), collapsed to the single-outstanding-request discipline.
// The answer is handed up VERBATIM: on the worker lane it carries the
// guest's raw plugin-invoke bytes, and the caller's decode seam is the
// single judge of the response contract (id echo, one of result|error)
// for both walls alike. Upstream, ids echo byte-form verbatim (AUDIT
// finding F-1): the host echoes the child's request id raw, and the
// worker's forward dispatcher enforces the echo on the replies it
// awaits. Nesting is the point: while one host request is in flight
// downstream, guest hostcalls flow upstream and are answered before
// the downstream response arrives; the worker's module lock guarantees
// one guest invocation at a time, so no deeper interleaving exists to
// support.
package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

// State is the supervisor's lifecycle position. Exactly four states —
// the KISS boundary this package refuses to grow past.
type State string

const (
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateRestarting State = "restarting"
	StateStopped    State = "stopped"
)

// Config-shaped lifecycle constants. Durations are policy; each names
// its WHY so an operator config field can absorb it later without
// archaeology (§10 resource-envelope work).
const (
	// DefaultBackoffInitial is the first restart delay: long enough
	// that a crash-looping child cannot burn a core respawning, short
	// enough that a one-off crash is invisible to the operator.
	DefaultBackoffInitial = 250 * time.Millisecond
	// DefaultBackoffMax caps exponential growth: past ~8s a plugin is
	// effectively down anyway and longer waits only delay the ceiling
	// verdict the operator needs to see.
	DefaultBackoffMax = 8 * time.Second
	// DefaultMaxRestarts is the lifetime restart ceiling: five
	// consecutive lives is generosity for a transient fault and proof
	// of a persistent one — after it the plugin deactivates with a
	// typed reason instead of flapping forever.
	DefaultMaxRestarts = 5
	// DefaultReadyTimeout bounds the wait for the child's readiness
	// mark: load-time admission runs guest code (version/smoke
	// exports), so it gets the same order of protection as an invoke —
	// a looping guest must not hang activation.
	DefaultReadyTimeout = 30 * time.Second
	// closeGraceEOF is how long a closing supervisor waits after
	// closing stdin (EOF = the D1-1 clean disconnect) before
	// escalating: the child only needs to notice EOF and exit.
	closeGraceEOF = 3 * time.Second
	// closeGraceTerm is the SIGTERM-to-SIGKILL grace: a child that
	// ignored EOF gets one polite signal's worth of time.
	closeGraceTerm = 2 * time.Second
	// stderrTailLines is how many trailing stderr lines a child error
	// carries as evidence — enough to see the fatal line and its
	// context, never an unbounded dump.
	stderrTailLines = 8
	// maxStderrLineBytes bounds one captured stderr line: telemetry is
	// for operator eyes, and a child must not be able to balloon the
	// host log with a single line.
	maxStderrLineBytes = 4096
)

// Dispatcher answers the child's upstream guest hostcalls — the same
// contract as pluginworker.HostDispatcher so a broker.Binding plugs
// into both the in-process wall and this process boundary unchanged:
// method is the WIT kebab import name, params the method-params bytes,
// reply the result-object bytes on success or the JSON-RPC
// error-object bytes on denial; err is internal failure only.
type Dispatcher interface {
	Dispatch(ctx context.Context, method string, params []byte) (reply []byte, err error)
}

// Backoff is the restart policy. Zero values take the defaults above.
type Backoff struct {
	Initial     time.Duration
	Max         time.Duration
	MaxRestarts int
}

func (b Backoff) initial() time.Duration {
	if b.Initial > 0 {
		return b.Initial
	}
	return DefaultBackoffInitial
}
func (b Backoff) max() time.Duration {
	if b.Max > 0 {
		return b.Max
	}
	return DefaultBackoffMax
}
func (b Backoff) maxRestarts() int {
	if b.MaxRestarts > 0 {
		return b.MaxRestarts
	}
	return DefaultMaxRestarts
}

// delay computes the bounded exponential backoff for restart n (1-based).
func (b Backoff) delay(n int) time.Duration {
	d := b.initial()
	for i := 1; i < n; i++ {
		d *= 2
		if d >= b.max() {
			return b.max()
		}
	}
	if d > b.max() {
		return b.max()
	}
	return d
}

// Spec describes one supervised child.
type Spec struct {
	// PluginID prefixes every out-of-band telemetry line (threat model
	// §7) and every typed error.
	PluginID string
	// Argv is the child command line; Argv[0] must be an absolute path
	// (agent processes have no stable cwd to resolve against).
	Argv []string
	// Env entries are appended to the parent environment (D1-1 rule 4:
	// SEV_PLUGIN_SOCKET=stdio: for native children).
	Env []string
	// ReadyMark is a substring of the stderr line that marks child
	// readiness — the worker's documented "event=ready" banner. Empty
	// means spawn success is readiness (native children carry no
	// banner contract yet; their admission protocol is step-6 work).
	ReadyMark string
	// ReadyTimeout bounds the readiness wait; 0 = DefaultReadyTimeout.
	ReadyTimeout time.Duration
	// RLimitASBytes applies an address-space ceiling to NATIVE
	// children (Linux prlimit; Windows folds it into the one
	// containment job — see rlimit_linux.go / contain_windows.go;
	// documented no-op elsewhere). Never set for wasm children:
	// their envelope is the worker's -memory-max flag, enforced
	// in-process by wazero — an address-space cap on the worker's Go
	// runtime would kill the warden for the prisoner's sins.
	RLimitASBytes uint64
	// Backoff is the restart policy.
	Backoff Backoff
	// VerifyArtifact runs before EVERY spawn — first and restarts.
	// This extends the verified-bytes-are-loaded-bytes discipline
	// across the restart loop: the extracted artifact lives on disk
	// between spawns, and a same-user adversary (threat model A5, the
	// lesson already paid for) can rewrite a disk file the host will
	// re-exec. A verification failure STOPS the supervisor typed —
	// tampering is a refusal, never a retry.
	VerifyArtifact func() error
	// ExitMeaning renders a child exit code for telemetry. nil = the
	// bare number. The worker lane passes WorkerExitMeaning.
	ExitMeaning func(code int) string
	// Log receives the child's out-of-band telemetry. nil = log.Default().
	Log *log.Logger
}

func (s Spec) logger() *log.Logger {
	if s.Log != nil {
		return s.Log
	}
	return log.Default()
}

func (s Spec) readyTimeout() time.Duration {
	if s.ReadyTimeout > 0 {
		return s.ReadyTimeout
	}
	return DefaultReadyTimeout
}

// WorkerExitMeaning is the exit-code taxonomy cmd/aii-plugin-worker
// documents — the supervisor's restart signal, surfaced verbatim so an
// operator reading the host log sees the worker's own contract.
func WorkerExitMeaning(code int) string {
	switch code {
	case 0:
		return "clean shutdown (stdin EOF)"
	case 1:
		return "usage error"
	case 2:
		return "module load/admission failure"
	case 3:
		return "fatal invocation failure (trap, timeout, resource kill, ABI or frame-budget violation)"
	case 4:
		return "stream failure (framing violation or broken pipe)"
	case -1:
		return "killed by signal (no exit code)"
	}
	return "unrecognized exit code"
}

// Supervisor runs one child under the process boundary.
type Supervisor struct {
	spec       Spec
	dispatcher Dispatcher

	// invokeMu serializes host→child invocations: one in flight, the
	// same guarantee the in-process module lock gives (ADR-033 bounded
	// reentrancy) — the wire discipline depends on it.
	invokeMu sync.Mutex

	mu         sync.Mutex
	state      State
	stopReason error
	child      *child
	restarts   int
	gen        int // spawn generation; a monitor acts only on its own child
}

// child is one spawned process with its stream pumps.
type child struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	// contained releases per-child containment resources (the Windows
	// job handle) once the child is REAPED — closing earlier lifts the
	// limits; never closing leaked one handle per activation (D20).
	// Idempotent; nil on platforms whose containment wraps argv.
	contained func()
	frames    chan []byte // frames the reader pump pulled off stdout
	pumps     sync.WaitGroup

	abandonOnce sync.Once
	abandon     chan struct{} // closed at teardown so pumps never leak

	readErrMu sync.Mutex
	readErr   error // first stdout framing error, set before frames closes

	exited  chan struct{} // closed once the process and streams are reaped
	exitErr error         // process result, valid after exited

	readyOnce sync.Once
	ready     chan struct{} // closed when the ReadyMark line is seen

	tailMu sync.Mutex
	tail   []string // last stderrTailLines lines, evidence for typed errors
}

func (c *child) abandonNow() { c.abandonOnce.Do(func() { close(c.abandon) }) }

func (c *child) markReady() { c.readyOnce.Do(func() { close(c.ready) }) }

func (c *child) noteRead(err error) {
	c.readErrMu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	c.readErrMu.Unlock()
}

func (c *child) readError() error {
	c.readErrMu.Lock()
	defer c.readErrMu.Unlock()
	return c.readErr
}

func (c *child) noteLine(line string) {
	c.tailMu.Lock()
	c.tail = append(c.tail, line)
	if len(c.tail) > stderrTailLines {
		c.tail = c.tail[len(c.tail)-stderrTailLines:]
	}
	c.tailMu.Unlock()
}

func (c *child) tailLines() []string {
	c.tailMu.Lock()
	defer c.tailMu.Unlock()
	return append([]string(nil), c.tail...)
}

// Start spawns the child and waits for readiness. On failure the
// supervisor is stopped and the error carries the exit taxonomy and
// stderr evidence — activation must fail atomically, exactly like the
// in-process Load path.
func Start(spec Spec, dispatcher Dispatcher) (*Supervisor, error) {
	if len(spec.Argv) == 0 {
		return nil, fmt.Errorf("supervisor: %s: empty argv", spec.PluginID)
	}
	s := &Supervisor{spec: spec, dispatcher: dispatcher, state: StateStarting}
	if err := s.spawnAndAwaitReady(); err != nil {
		s.mu.Lock()
		s.state = StateStopped
		s.stopReason = err
		s.mu.Unlock()
		return nil, err
	}
	return s, nil
}

// State reports the current lifecycle position and, when stopped, why.
func (s *Supervisor) State() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.stopReason
}

// Restarts reports how many times the child has been respawned.
func (s *Supervisor) Restarts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restarts
}

// Pid reports the live child's pid, 0 when no child is running
// (telemetry and tests; never authority).
func (s *Supervisor) Pid() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.child != nil && s.child.cmd.Process != nil {
		return s.child.cmd.Process.Pid
	}
	return 0
}

// spawnAndAwaitReady verifies the artifact, spawns, and waits for the
// readiness mark. Caller state transitions are its responsibility on
// success (running) — on error the caller decides (stop vs another
// restart).
// applyLimit is applyAddressSpaceLimit behind a swappable name. A
// refused envelope must ground the plugin, and no test can make the
// kernel refuse one on demand — the seam is how that stays proven.
var applyLimit = applyAddressSpaceLimit

func (s *Supervisor) spawnAndAwaitReady() error {
	// TOPOLOGY FIRST (hostcap, 2026-08-26): native children are a
	// capability, not an assumption — mobile T3 is in-process wasm and
	// must never reach an exec. Refusing here is the same fail-closed
	// shape as a missing bwrap, with the topology's own reason.
	if nc := hostcap.Can(hostcap.NativeChild); !nc.Available {
		return &SpawnRefusedError{PluginID: s.spec.PluginID,
			Cause: fmt.Errorf("this host cannot run native children: %s", nc.Reason)}
	}
	if s.spec.VerifyArtifact != nil {
		if err := s.spec.VerifyArtifact(); err != nil {
			// Tampering between spawns is A5, not a crash: refuse and
			// STOP — retrying an artifact that fails verification
			// would be retrying the attack.
			return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: err}
		}
	}

	cmd := exec.Command(s.spec.Argv[0], s.spec.Argv[1:]...)
	// EXACTLY what the spec names, never os.Environ().
	//
	// The daemon's environment is where secrets live: broker.go serves
	// secret.read by reading os.Getenv(profile.SecretEnv). Handing that
	// same environment to a child let it read the identical variable
	// WITHOUT asking the broker — a capability decision routed around,
	// not made. Canon is explicit that this one is not waivable by
	// trust: "proc.spawn and secret.read require explicit operator
	// policy.json admission regardless of trust tier. T3 defaults do
	// NOT automatically admit these" (PLUGIN_SYSTEM_DESIGN.md §2.1).
	// First-party signing proves provenance, not that a bug in signed
	// code cannot hand an attacker every key the daemon holds.
	//
	// Nothing needs the inheritance: no child binary in this tree reads
	// any variable but the SEV_ pair the spec sets, and a Go process is
	// fine with an empty environment. An empty PATH is a feature here —
	// spawning is a capability too.
	cmd.Env = s.spec.Env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("supervisor: %s: stdin pipe: %w", s.spec.PluginID, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("supervisor: %s: stdout pipe: %w", s.spec.PluginID, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("supervisor: %s: stderr pipe: %w", s.spec.PluginID, err)
	}
	if err := cmd.Start(); err != nil {
		return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: err}
	}

	c := &child{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		frames:  make(chan []byte, 4),
		abandon: make(chan struct{}),
		exited:  make(chan struct{}),
		ready:   make(chan struct{}),
	}
	// POST-SPAWN CONTAINMENT, where the platform contains a process
	// rather than a command line. Linux and macOS wrap argv and are
	// already contained before this runs; Windows applies a job object
	// here. See contain_windows.go for the mechanism and contain_other.go
	// for why the other four need nothing at this point.
	//
	// A mechanism that exists and refuses is a refusal, exactly as it is
	// for the envelope below: containment the operator was told about
	// and did not get is worse than containment they were told they did
	// not have.
	contained, cmsg, cerr := containProcess(cmd.Process.Pid, s.spec.RLimitASBytes)
	if cerr != nil {
		// Refusal must REAP, not just kill: Wait releases the process
		// table entry and the three pipes — without it every refusal
		// left a zombie and three FDs behind (D18, Sev 2026-08-26).
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: cerr}
	}
	if cmsg != "" {
		s.spec.logger().Printf("plugin %s: native child %s", s.spec.PluginID, cmsg)
	}
	c.contained = contained
	if s.spec.RLimitASBytes > 0 {
		// Post-spawn envelope: see rlimit_linux.go / rlimit_windows.go for
		// the mechanisms and rlimit_other.go for the honest no-op record.
		//
		// A MECHANISM THAT EXISTS AND REFUSES IS A REFUSAL. The operator
		// configured a ceiling; if the platform can enforce one and
		// would not, running the child anyway gives them a limit they
		// believe in and do not have. Logged-and-continue was fail-open.
		// A platform with no mechanism at all is a different fact, is
		// recorded as one, and does not ground the plugin.
		msg, err := applyLimit(cmd.Process.Pid, s.spec.RLimitASBytes)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait() // reap: no zombie, no leaked pipes (D18)
			if c.contained != nil {
				c.contained()
			}
			return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: err}
		}
		if msg != "" {
			s.spec.logger().Printf("plugin %s: resource envelope: %s", s.spec.PluginID, msg)
		}
	}

	s.mu.Lock()
	if s.state == StateStopped {
		// Close won while we were spawning: it saw no child (nothing
		// published yet) and returned. This child must not exist, and
		// only WE can see it — kill, reap, release (D04, Sev
		// 2026-08-26: a closed plugin could resurrect through exactly
		// this window).
		s.mu.Unlock()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if c.contained != nil {
			c.contained()
		}
		return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: errClosed}
	}
	s.gen++
	gen := s.gen
	s.child = c
	s.mu.Unlock()

	c.pumps.Add(2)
	go func() {
		defer c.pumps.Done()
		defer stdout.Close()
		s.pumpFrames(c, stdout)
	}()
	go func() {
		defer c.pumps.Done()
		defer stderr.Close()
		s.pumpStderr(c, stderr)
	}()
	go s.reap(c, gen)

	// Readiness: the mark on stderr (worker banner), or bare spawn
	// success when no mark is contracted (N-7: readiness is lifecycle
	// progress, never a ping method).
	if s.spec.ReadyMark == "" {
		c.markReady()
	}
	select {
	case <-c.ready:
	case <-c.exited:
		code := exitCode(c.exitErr)
		return &ChildExitError{
			PluginID: s.spec.PluginID, Code: code,
			Meaning: s.exitMeaning(code), Phase: "start",
			StderrTail: c.tailLines(),
		}
	case <-time.After(s.spec.readyTimeout()):
		s.kill(c)
		return &ChildExitError{
			PluginID: s.spec.PluginID, Code: -1,
			Meaning: fmt.Sprintf("no readiness mark within %s; killed", s.spec.readyTimeout()),
			Phase:   "start", StderrTail: c.tailLines(),
		}
	}

	// Promotion is atomic with the death check: the reaper defers to
	// the starting phase, so a child that died between readiness and
	// promotion must be caught HERE or its restart would be lost.
	s.mu.Lock()
	if s.state == StateStopped {
		// Close raced us after publication: it holds this child and
		// owns the teardown. Promoting would flip a closed supervisor
		// back to running (D04).
		s.mu.Unlock()
		return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: errClosed}
	}
	select {
	case <-c.exited:
		s.mu.Unlock()
		code := exitCode(c.exitErr)
		return &ChildExitError{PluginID: s.spec.PluginID, Code: code,
			Meaning: s.exitMeaning(code), Phase: "start", StderrTail: c.tailLines()}
	default:
	}
	s.state = StateRunning
	s.mu.Unlock()
	return nil
}

// pumpFrames owns the child's stdout: it reads frames under the 1 MiB
// plugin-side ceiling (D1-1 rule 2) and hands them to the in-flight
// invocation. On any framing error the stream is dead by definition
// (no resync exists — AUDIT §2.2): record and close.
func (s *Supervisor) pumpFrames(c *child, stdout io.Reader) {
	defer close(c.frames)
	for {
		payload, err := bbb.ReadFrame(stdout, bbb.MaxControlFrameBytes)
		if err != nil {
			c.noteRead(err)
			return
		}
		select {
		case c.frames <- payload:
		case <-c.abandon:
			return
		}
	}
}

// pumpStderr owns the child's stderr: every line lands in the host log
// with the plugin id prefix — the out-of-band telemetry of threat
// model §7 (the child cannot report its own death; this pump plus the
// reaper is who does). Lines are length-bounded; the readiness mark is
// watched here too.
func (s *Supervisor) pumpStderr(c *child, stderr io.Reader) {
	lg := s.spec.logger()
	br := bufio.NewReader(stderr)
	buf := make([]byte, 0, 256)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		line := string(buf)
		buf = buf[:0]
		c.noteLine(line)
		lg.Printf("plugin %s: %s", s.spec.PluginID, line)
		if s.spec.ReadyMark != "" && strings.Contains(line, s.spec.ReadyMark) {
			c.markReady()
		}
	}
	for {
		b, err := br.ReadByte()
		if err == nil {
			if b == '\n' {
				flush()
			} else if len(buf) < maxStderrLineBytes {
				// Over-length bytes are dropped, never buffered: a
				// hostile child cannot balloon the host through one
				// endless line.
				buf = append(buf, b)
			}
			continue
		}
		flush()
		return
	}
}

// reap waits for the child and drives the restart policy. Only the
// generation that spawned the child acts on its death.
func (s *Supervisor) reap(c *child, gen int) {
	// Cmd.Wait closes its pipes as soon as the process exits. Reap the
	// process directly, then drain them so a final response cannot lose
	// a race to the exit notification.
	state, err := c.cmd.Process.Wait()
	if err == nil {
		c.cmd.ProcessState = state
		if !state.Success() {
			err = &exec.ExitError{ProcessState: state}
		}
	}
	drained := make(chan struct{})
	go func() {
		c.pumps.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(closeGraceEOF):
		c.abandonNow()
		_ = c.stdout.Close()
		_ = c.stderr.Close()
		<-drained
	}
	_ = c.stdin.Close()
	c.exitErr = err
	close(c.exited)
	if c.contained != nil {
		// The job handle outlives the child ON PURPOSE until here:
		// kill-on-close makes this close the reaper of any descendants
		// the direct kill missed, and closing per-child ends the
		// handle-per-activation leak (D20, Sev 2026-08-26).
		c.contained()
	}

	s.mu.Lock()
	if s.gen != gen || s.state == StateStopped {
		// A newer child exists, or Close owns the teardown.
		s.mu.Unlock()
		return
	}
	code := exitCode(err)
	s.spec.logger().Printf("plugin %s: child exited code=%d meaning=%q restarts=%d",
		s.spec.PluginID, code, s.exitMeaning(code), s.restarts)

	if s.state == StateStarting {
		// Start owns this failure; it is watching c.exited itself.
		s.mu.Unlock()
		return
	}

	if s.restarts >= s.spec.Backoff.maxRestarts() {
		s.state = StateStopped
		s.stopReason = &RestartCeilingError{
			PluginID: s.spec.PluginID, Restarts: s.restarts,
			Last: &ChildExitError{PluginID: s.spec.PluginID, Code: code,
				Meaning: s.exitMeaning(code), Phase: "run", StderrTail: c.tailLines()},
		}
		s.spec.logger().Printf("plugin %s: DEACTIVATED: %v", s.spec.PluginID, s.stopReason)
		s.mu.Unlock()
		return
	}
	s.restarts++
	n := s.restarts
	s.state = StateRestarting
	s.child = nil
	s.mu.Unlock()

	delay := s.spec.Backoff.delay(n)
	s.spec.logger().Printf("plugin %s: restart %d/%d in %s",
		s.spec.PluginID, n, s.spec.Backoff.maxRestarts(), delay)
	time.AfterFunc(delay, func() { s.restart(n) })
}

// restart is the deferred half of the restart policy.
func (s *Supervisor) restart(n int) {
	s.mu.Lock()
	if s.state != StateRestarting || s.restarts != n {
		s.mu.Unlock()
		return
	}
	s.state = StateStarting
	s.mu.Unlock()

	if err := s.spawnAndAwaitReady(); err != nil {
		s.mu.Lock()
		if s.state == StateStopped {
			s.mu.Unlock()
			return
		}
		var refused *SpawnRefusedError
		if errors.As(err, &refused) {
			// Artifact verification or exec refusal: stop, never retry
			// (the WHY in Spec.VerifyArtifact).
			s.state = StateStopped
			s.stopReason = err
			s.spec.logger().Printf("plugin %s: DEACTIVATED: %v", s.spec.PluginID, err)
			s.mu.Unlock()
			return
		}
		if s.restarts >= s.spec.Backoff.maxRestarts() {
			s.state = StateStopped
			s.stopReason = &RestartCeilingError{PluginID: s.spec.PluginID, Restarts: s.restarts, Last: err}
			s.spec.logger().Printf("plugin %s: DEACTIVATED: %v", s.spec.PluginID, s.stopReason)
			s.mu.Unlock()
			return
		}
		s.restarts++
		next := s.restarts
		s.state = StateRestarting
		s.mu.Unlock()
		delay := s.spec.Backoff.delay(next)
		s.spec.logger().Printf("plugin %s: restart %d/%d in %s (previous respawn failed: %v)",
			s.spec.PluginID, next, s.spec.Backoff.maxRestarts(), delay, err)
		time.AfterFunc(delay, func() { s.restart(next) })
	}
}

func (s *Supervisor) exitMeaning(code int) string {
	if s.spec.ExitMeaning != nil {
		return s.spec.ExitMeaning(code)
	}
	return fmt.Sprintf("exit code %d", code)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode() // -1 when signal-killed
	}
	return -1
}

// kill tears the child down hard (deadline exceeded, protocol breach —
// the N-8 model: no in-band cancel exists; kill and let the restart
// policy decide).
func (s *Supervisor) kill(c *child) {
	c.abandonNow()
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	s.awaitReaped(c)
}

// Invoke writes one request frame to the child and returns the child's
// response frame. While the request is in flight it answers the
// child's upstream requests (forwarded guest hostcalls) through the
// Dispatcher — the nesting D1-1 exists for. One invocation at a time.
func (s *Supervisor) Invoke(ctx context.Context, frame []byte) ([]byte, error) {
	s.invokeMu.Lock()
	defer s.invokeMu.Unlock()

	s.mu.Lock()
	if s.state != StateRunning || s.child == nil {
		state, reason := s.state, s.stopReason
		s.mu.Unlock()
		return nil, &UnavailableError{PluginID: s.spec.PluginID, State: state, Reason: reason}
	}
	c := s.child
	s.mu.Unlock()

	if len(frame) > bbb.MaxControlFrameBytes {
		// Refused BEFORE any byte is written (the audited send rule:
		// posix.c:179-180 fails before write) — the stream stays
		// clean and the channel stays up.
		return nil, fmt.Errorf("supervisor: %s: request frame is %d bytes, over the %d-byte plugin-side ceiling: %w",
			s.spec.PluginID, len(frame), bbb.MaxControlFrameBytes, bbb.ErrFrameTooLarge)
	}
	if err := bbb.WriteFrame(c.stdin, frame, bbb.MaxControlFrameBytes); err != nil {
		// A mid-frame write failure poisons the stream (a partial
		// frame cannot be resynced): kill; the reaper restarts.
		s.kill(c)
		return nil, &ChildIOError{PluginID: s.spec.PluginID, Op: "write", Err: err}
	}

	for {
		// Bias toward delivered frames: a response the pump already
		// queued must win over a concurrently-observed exit, or a
		// child that answered and then died would lose its answer.
		var payload []byte
		var ok bool
		select {
		case payload, ok = <-c.frames:
		default:
			select {
			case payload, ok = <-c.frames:
			case <-c.exited:
				code := exitCode(c.exitErr)
				return nil, &ChildExitError{PluginID: s.spec.PluginID, Code: code,
					Meaning: s.exitMeaning(code), Phase: "invoke", StderrTail: c.tailLines()}
			case <-ctx.Done():
				// The adopted cancellation model (DELTA_D1 N-8): no
				// daemon→plugin cancel method exists; deadline
				// exceeded means kill the child and let the restart
				// policy revive it. The identity survives; the call
				// reports honestly.
				s.kill(c)
				return nil, &InvokeTimeoutError{PluginID: s.spec.PluginID, Err: ctx.Err()}
			}
		}
		if !ok {
			// The stdout stream ended. EOF here almost always means
			// the child is dying (a trapped guest: fatal stderr line,
			// then exit) — prefer the exit report with its taxonomy
			// and stderr evidence over a bare read error, so wait
			// briefly for the reap. A child that closed stdout but
			// lives is hostile: killed, reported as the IO fault it
			// is.
			readErr := c.readError()
			// A RECORDED PROTOCOL REFUSAL OUTRANKS THE EXIT (external
			// review P2, 2026-08-26 — the race the suite showed once):
			// when the reader already named the specific fault (an
			// oversize frame is a deliberate refusal, not a death
			// rattle), a child that happens to win the reap race must
			// not relabel it as a generic exit. The exit taxonomy is
			// for deaths WITHOUT a better explanation.
			if errors.Is(readErr, bbb.ErrFrameTooLarge) {
				s.kill(c)
				return nil, &ChildIOError{PluginID: s.spec.PluginID, Op: "read", Err: readErr}
			}
			select {
			case <-c.exited:
				code := exitCode(c.exitErr)
				return nil, &ChildExitError{PluginID: s.spec.PluginID, Code: code,
					Meaning: s.exitMeaning(code), Phase: "invoke", StderrTail: c.tailLines()}
			case <-time.After(closeGraceEOF):
			}
			s.kill(c)
			return nil, &ChildIOError{PluginID: s.spec.PluginID, Op: "read", Err: readErr}
		}
		done, resp, err := s.consumeFrame(ctx, c, payload)
		if err != nil {
			s.kill(c)
			return nil, err
		}
		if done {
			return resp, nil
		}
	}
}

// consumeFrame handles one frame read while a request is in flight:
// a frame WITH a "method" member is an upstream guest hostcall —
// dispatched and answered, staying in flight (the daemon's own
// request/response classification, rpc.c:3433-3441). ANY other frame
// is the child's answer to the one outstanding request and is handed
// up VERBATIM: on the worker lane the response frame carries the
// guest's raw plugin-invoke bytes (envelope-or-not, exactly what the
// in-process wall returns), and judging them — id echo, one of
// result|error — is the caller's single decode seam. One judge for
// both walls is what keeps the supervised mode byte-identical in
// behavior to the in-process mode; the F-1 echo rule is enforced
// there, and by the worker's own forward dispatcher for the upstream
// direction.
func (s *Supervisor) consumeFrame(ctx context.Context, c *child, payload []byte) (done bool, resp []byte, err error) {
	var members map[string]json.RawMessage
	if uerr := json.Unmarshal(payload, &members); uerr == nil {
		if _, isRequest := members["method"]; isRequest {
			if herr := s.answerUpstream(ctx, c, members); herr != nil {
				return false, nil, herr
			}
			return false, nil, nil
		}
	}
	return true, payload, nil
}

// answerUpstream serves one child→host request: map the wire method to
// the WIT import name, dispatch through the broker seam (or the
// deny-all default), classify the reply, and write the response
// envelope echoing the child's id byte-form verbatim.
func (s *Supervisor) answerUpstream(ctx context.Context, c *child, members map[string]json.RawMessage) error {
	var method string
	if err := json.Unmarshal(members["method"], &method); err != nil {
		return &ProtocolError{PluginID: s.spec.PluginID,
			Requirement: "request method must be a string", Evidence: excerpt(members["method"])}
	}
	idRaw, hasID := members["id"]
	if !hasID {
		// The daemon answers id-less requests with "id":null rather
		// than treating them as notifications (AUDIT finding F-8) —
		// adopted.
		idRaw = json.RawMessage("null")
	}
	params := members["params"]
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}

	var reply []byte
	importName, known := bbb.ImportForMethod(method)
	switch {
	case !known:
		// Outside the eight-function plugin surface: the audited
		// method-not-found error (AUDIT §6, rpc.c:3312-3314).
		reply = mustJSON(rpcErrorObject{Code: -32601, Message: "method not found"})
	case s.dispatcher == nil:
		// The deny-all posture, byte-for-byte the worker stub's
		// denial (hostbbb.go): absence of a broker binding IS the
		// quarantine, across the process boundary too.
		reply = mustJSON(rpcErrorObject{
			Code:    -32000,
			Message: fmt.Sprintf("no capability broker attached to this worker; %s denied", importName),
			Data:    &rpcErrorData{ReasonCode: "POLICY_DENY"},
		})
	default:
		var derr error
		reply, derr = s.dispatcher.Dispatch(ctx, importName, params)
		if derr != nil {
			// Host-side internal failure: the daemon's -32603 INTERNAL
			// answer (AUDIT §8) — the connection survives; the guest
			// sees a classified failure, and the host log the cause.
			s.spec.logger().Printf("plugin %s: upstream %s dispatch failed: %v", s.spec.PluginID, method, derr)
			reply = mustJSON(rpcErrorObject{Code: -32603, Message: "host dispatch failed"})
		}
	}

	// The dispatcher reply is either a result object or a JSON-RPC
	// error object (the ADR-033 Decision 6 mapping the in-process wall
	// hands guests raw). On the wire that distinction is the envelope
	// member: an error object is recognizable by its required numeric
	// "code" — the same classification the SDK applies to the raw
	// bytes.
	envelope := map[string]json.RawMessage{
		"jsonrpc": json.RawMessage(`"2.0"`),
		"id":      idRaw,
	}
	if isErrorObject(reply) {
		envelope["error"] = reply
	} else {
		envelope["result"] = reply
	}
	frame, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("supervisor: %s: encode upstream response: %w", s.spec.PluginID, err)
	}
	if err := bbb.WriteFrame(c.stdin, frame, bbb.MaxControlFrameBytes); err != nil {
		return &ChildIOError{PluginID: s.spec.PluginID, Op: "write upstream response", Err: err}
	}
	return nil
}

// Close shuts the child down cleanly: close stdin (EOF = disconnect,
// connection lifetime = process lifetime — D1-1 rule 3), then escalate
// SIGTERM, then kill. Idempotent; the supervisor ends stopped.
func (s *Supervisor) Close() error { return s.CloseContext(context.Background()) }

// CloseContext is Close bounded by the caller's context: each grace
// ends EARLY when the context does, escalating to the next signal —
// ten seconds of fixed graces used to ignore a five-second deadline
// entirely (D24, Sev 2026-08-26). The KILL is always sent and the
// reap always awaited (bounded by closeGraceKill): an unreaped child
// is a leak whatever the caller's deadline was.
func (s *Supervisor) CloseContext(ctx context.Context) error {
	s.mu.Lock()
	if s.state == StateStopped {
		s.mu.Unlock()
		return nil
	}
	s.state = StateStopped
	if s.stopReason == nil {
		s.stopReason = errClosed
	}
	c := s.child
	s.child = nil
	s.mu.Unlock()

	if c == nil {
		return nil
	}
	c.abandonNow()
	_ = c.stdin.Close()
	select {
	case <-c.exited:
		return nil
	case <-time.After(closeGraceEOF):
	case <-ctx.Done():
	}
	signalTerm(c.cmd)
	select {
	case <-c.exited:
		return nil
	case <-time.After(closeGraceTerm):
	case <-ctx.Done():
	}
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if !s.awaitReaped(c) {
		return fmt.Errorf("supervisor: %s: child survived SIGKILL and is orphaned", s.spec.PluginID)
	}
	return nil
}

// closeGraceKill bounds the wait AFTER SIGKILL. SIGKILL cannot be
// refused, but it is not instant either: a child parked in
// uninterruptible I/O — a hung FUSE mount, a dead NFS read — stays in D
// state until that I/O returns, and until then it is not reaped. A wait
// with no bound turns one stuck plugin into a stuck shutdown of the
// whole application. Variable so tests can shrink it; no kernel refuses
// a SIGKILL on demand.
var closeGraceKill = 5 * time.Second

// awaitReaped waits for the child to be reaped, bounded, and reports
// whether it actually exited.
//
// false means the process is still out there and now orphaned. That is
// a fact the operator needs — the alternative was to keep waiting and
// tell them nothing, forever.
//
// Process.Kill's own error stays ignored on purpose: "process already
// finished" is the common case (the child exited between the liveness
// check and the signal) and is not a problem. Whether it was REAPED is
// the question that matters, and this is what answers it.
func (s *Supervisor) awaitReaped(c *child) bool {
	select {
	case <-c.exited:
		return true
	case <-time.After(closeGraceKill):
		pid := -1
		if c.cmd != nil && c.cmd.Process != nil {
			pid = c.cmd.Process.Pid
		}
		s.spec.logger().Printf("plugin %s: SIGKILL did not reap pid %d within %s — abandoning the wait; the process is unkillable (uninterruptible I/O) and is now ORPHANED",
			s.spec.PluginID, pid, closeGraceKill)
		return false
	}
}

var errClosed = errors.New("supervisor closed")

// --- small helpers ---

type rpcErrorObject struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    *rpcErrorData `json:"data,omitempty"`
}

type rpcErrorData struct {
	ReasonCode string `json:"reasonCode"`
}

func mustJSON(v interface{}) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("supervisor: static marshal failed: %v", err))
	}
	return raw
}

// isErrorObject reports whether reply is a JSON-RPC error object: a
// JSON object whose "code" member is a number. The broker's two reply
// shapes are disjoint on exactly this member (rpcError requires code;
// result objects never carry one), and the SDK's own classification
// reads the same field.
func isErrorObject(reply []byte) bool {
	var probe struct {
		Code json.RawMessage `json:"code"`
	}
	if err := json.Unmarshal(reply, &probe); err != nil {
		return false
	}
	if len(probe.Code) == 0 {
		return false
	}
	var n float64
	return json.Unmarshal(probe.Code, &n) == nil
}

// excerpt bounds evidence carried inside errors.
func excerpt(b []byte) string {
	const max = 256
	if len(b) > max {
		return string(b[:max]) + fmt.Sprintf("… (%d bytes)", len(b))
	}
	return string(b)
}
