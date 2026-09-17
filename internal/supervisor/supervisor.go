// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

// .
// .
type State string

const (
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateRestarting State = "restarting"
	StateStopped    State = "stopped"
)

// .
// .
// .
const (
	// .
	// .
	// .
	DefaultBackoffInitial = 250 * time.Millisecond
	// .
	// .
	// .
	DefaultBackoffMax = 8 * time.Second
	// .
	// .
	// .
	// .
	// .
	// .
	DefaultMaxRestarts = 5
	// .
	// .
	DefaultRestartWindow = 10 * time.Minute
	// .
	// .
	// .
	// .
	DefaultReadyTimeout = 30 * time.Second
	// .
	// .
	// .
	closeGraceEOF = 3 * time.Second
	// .
	// .
	closeGraceTerm = 2 * time.Second
	// .
	// .
	// .
	stderrTailLines = 8
	// .
	// .
	// .
	maxStderrLineBytes = 4096
)

// .
// .
// .
// .
// .
// .
type Dispatcher interface {
	Dispatch(ctx context.Context, method string, params []byte) (reply []byte, err error)
}

// .
type Backoff struct {
	Initial     time.Duration
	Max         time.Duration
	MaxRestarts int
	// .
	Window time.Duration
}

func (b Backoff) window() time.Duration {
	if b.Window > 0 {
		return b.Window
	}
	return DefaultRestartWindow
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

// .
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

// .
type Spec struct {
	// .
	// .
	// .
	// .
	// .
	SessionMode bool

	// .
	// .
	PluginID string
	// .
	// .
	Argv []string
	// .
	// .
	ArgvContainment Containment
	// .
	// .
	Env []string
	// .
	// .
	// .
	// .
	ReadyMark string
	// .
	ReadyTimeout time.Duration
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	RLimitASBytes uint64
	// .
	Backoff Backoff
	// .
	// .
	// .
	// .
	// .
	ExtraFiles []*os.File
	// .
	// .
	// .
	Artifact io.Closer

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	AudioPair bool
	// .
	// .
	// .
	// .
	AppContainer *AppContainer
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	VerifyArtifact func() error
	// .
	// .
	ExitMeaning func(code int) string
	// .
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

// .
// .
// .
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

// .
type Supervisor struct {
	spec       Spec
	dispatcher Dispatcher

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	invokeGate chan struct{}

	mu         sync.Mutex
	state      State
	stopReason error
	child      *child
	restarts   int
	// .
	// .
	restartTimes []time.Time
	gen          int
	spawning     chan struct{}
	artifactOnce sync.Once
	artifactErr  error
}

// .
type child struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	// .
	// .
	// .
	// .
	contained  func() error
	isolation  Containment
	cleanupErr error
	frames     chan []byte
	pumps      sync.WaitGroup

	abandonOnce sync.Once
	abandon     chan struct{}

	readErrMu sync.Mutex
	readErr   error

	exited chan struct{}
	// .
	// .
	// .
	audioIn  *os.File
	audioOut *os.File
	// .
	// .
	// .
	stdoutEOF atomic.Bool
	exitErr   error
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	cancelled atomic.Bool

	readyOnce sync.Once
	ready     chan struct{}
	readyLine string

	tailMu sync.Mutex
	tail   []string
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

// .
// .
// .
// .
func Start(spec Spec, dispatcher Dispatcher) (*Supervisor, error) {
	if len(spec.Argv) == 0 {
		return nil, fmt.Errorf("supervisor: %s: empty argv", spec.PluginID)
	}
	s := &Supervisor{spec: spec, dispatcher: dispatcher, state: StateStarting, invokeGate: make(chan struct{}, 1)}
	if err := s.spawnAndAwaitReady(); err != nil {
		s.mu.Lock()
		s.state = StateStopped
		s.stopReason = err
		s.mu.Unlock()
		return nil, err
	}
	return s, nil
}

// .
func (s *Supervisor) State() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.stopReason
}

// .
// .
// .
// .
// .
// .
func (s *Supervisor) SessionChannels() (frames <-chan []byte, stdin io.Writer, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateRunning || s.child == nil {
		return nil, nil, false
	}
	// .
	// .
	// .
	// .
	select {
	case <-s.child.exited:
		return nil, nil, false
	default:
	}
	if s.child.stdoutEOF.Load() {
		return nil, nil, false
	}
	return s.child.frames, s.child.stdin, true
}

// .
// .
// .
// .
func (s *Supervisor) AudioPair() (in io.WriteCloser, out io.ReadCloser, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateRunning || s.child == nil || s.child.audioIn == nil {
		return nil, nil, false
	}
	select {
	case <-s.child.exited:
		return nil, nil, false
	default:
	}
	if s.child.stdoutEOF.Load() {
		return nil, nil, false
	}
	return s.child.audioIn, s.child.audioOut, true
}

// .
// .
// .
// .
func (s *Supervisor) Exited() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.child == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return s.child.exited
}

// .
// .
// .
func (s *Supervisor) Dispatcher() Dispatcher { return s.dispatcher }

// .
// .
// .
// .
func (s *Supervisor) ReadyLine() string {
	s.mu.Lock()
	c := s.child
	s.mu.Unlock()
	if c == nil {
		return ""
	}
	c.tailMu.Lock()
	defer c.tailMu.Unlock()
	return c.readyLine
}

func (s *Supervisor) Restarts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restarts
}

// .
// .
func (s *Supervisor) Pid() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.child != nil && s.child.cmd.Process != nil {
		select {
		case <-s.child.exited:
			return 0
		default:
		}
		return s.child.cmd.Process.Pid
	}
	return 0
}

// .
// .
// .
// .
// .
// .
// .
var applyLimit = applyAddressSpaceLimit

func (s *Supervisor) spawnAndAwaitReady() (spawnErr error) {
	s.mu.Lock()
	if s.state == StateStopped {
		s.mu.Unlock()
		return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: errClosed}
	}
	done := make(chan struct{})
	s.spawning = done
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		var cleanup *ContainmentCleanupError
		if errors.As(spawnErr, &cleanup) {
			s.state, s.stopReason = StateStopped, spawnErr
		}
		if s.spawning == done {
			close(done)
			s.spawning = nil
		}
		s.mu.Unlock()
	}()
	// .
	// .
	// .
	// .
	if nc := hostcap.Can(hostcap.NativeChild); !nc.Available {
		return &SpawnRefusedError{PluginID: s.spec.PluginID,
			Cause: fmt.Errorf("this host cannot run native children: %s", nc.Reason)}
	}
	if s.spec.VerifyArtifact != nil {
		if err := s.spec.VerifyArtifact(); err != nil {
			// .
			// .
			// .
			return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: err}
		}
	}

	cmd := exec.Command(s.spec.Argv[0], s.spec.Argv[1:]...)
	cmd.ExtraFiles = s.spec.ExtraFiles
	env := s.spec.Env
	var pair *audioPair
	if s.spec.AudioPair {
		var err error
		if pair, env, err = prepareAudioPair(cmd, env, s.spec.ExtraFiles); err != nil {
			return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: fmt.Errorf("audio pair: %w", err)}
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	cmd.Env = env
	// .
	// .
	var stdin io.WriteCloser
	var stdout, stderr io.ReadCloser
	var walled *launched
	if s.spec.AppContainer != nil {
		// .
		// .
		// .
		var lerr error
		if walled, lerr = launchContained(cmd, s.spec.AppContainer, s.spec.RLimitASBytes); lerr != nil {
			pair.release(true)
			return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: lerr}
		}
		stdin, stdout, stderr = walled.stdin, walled.stdout, walled.stderr
	} else {
		var err error
		if stdin, err = cmd.StdinPipe(); err != nil {
			pair.release(true)
			return fmt.Errorf("supervisor: %s: stdin pipe: %w", s.spec.PluginID, err)
		}
		if stdout, err = cmd.StdoutPipe(); err != nil {
			pair.release(true)
			return fmt.Errorf("supervisor: %s: stdout pipe: %w", s.spec.PluginID, err)
		}
		if stderr, err = cmd.StderrPipe(); err != nil {
			pair.release(true)
			return fmt.Errorf("supervisor: %s: stderr pipe: %w", s.spec.PluginID, err)
		}
		if err := cmd.Start(); err != nil {
			pair.release(true)
			return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: err}
		}
	}
	// .
	// .
	pair.release(false)
	hostIn, hostOut := pair.ends()

	c := &child{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		frames:  make(chan []byte, 4),
		audioIn: hostIn, audioOut: hostOut,
		abandon: make(chan struct{}),
		exited:  make(chan struct{}),
		ready:   make(chan struct{}),
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var contained func() error
	var cmsg string
	var cerr error
	c.isolation = s.spec.ArgvContainment
	if walled != nil {
		// .
		contained, cmsg = walled.contained, walled.containment.Description
		c.isolation = walled.containment
	} else {
		contained, cmsg, cerr = containProcess(cmd.Process.Pid, s.spec.RLimitASBytes)
	}
	if cerr != nil {
		// .
		// .
		// .
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: cerr}
	}
	if cmsg != "" {
		s.spec.logger().Printf("plugin %s: native child %s", s.spec.PluginID, cmsg)
	}
	c.contained = contained
	if s.spec.RLimitASBytes > 0 {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		msg, err := applyLimit(cmd.Process.Pid, s.spec.RLimitASBytes)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if c.contained != nil {
				err = errors.Join(err, c.contained())
			}
			return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: err}
		}
		if msg != "" {
			s.spec.logger().Printf("plugin %s: resource envelope: %s", s.spec.PluginID, msg)
		}
	}

	s.mu.Lock()
	if s.state == StateStopped {
		// .
		// .
		// .
		// .
		// .
		// .
		s.mu.Unlock()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		var cleanupErr error
		if c.contained != nil {
			cleanupErr = c.contained()
		}
		if cleanupErr != nil {
			return &ContainmentCleanupError{PluginID: s.spec.PluginID, Err: cleanupErr}
		}
		return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: errors.Join(errClosed, cleanupErr)}
	}
	s.gen++
	gen := s.gen
	s.child = c
	close(done)
	s.spawning = nil
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

	// .
	// .
	// .
	if s.spec.ReadyMark == "" {
		c.markReady()
	}
	select {
	case <-c.ready:
	case <-c.exited:
		if c.cleanupErr != nil {
			return c.cleanupErr
		}
		code := exitCode(c.exitErr)
		return &ChildExitError{
			PluginID: s.spec.PluginID, Code: code,
			Meaning: s.exitMeaning(code), Phase: "start",
			StderrTail: c.tailLines(),
		}
	case <-time.After(s.spec.readyTimeout()):
		s.kill(c, false)
		select {
		case <-c.exited:
			if c.cleanupErr != nil {
				return c.cleanupErr
			}
		default:
			if pending := s.retirementPending(c, fmt.Errorf("startup deadline exceeded; child retirement still pending")); pending != nil {
				return pending
			}
		}
		return &ChildExitError{
			PluginID: s.spec.PluginID, Code: -1,
			Meaning: fmt.Sprintf("no readiness mark within %s; killed", s.spec.readyTimeout()),
			Phase:   "start", StderrTail: c.tailLines(),
		}
	}

	// .
	// .
	// .
	s.mu.Lock()
	if s.state == StateStopped {
		// .
		// .
		// .
		s.mu.Unlock()
		return &SpawnRefusedError{PluginID: s.spec.PluginID, Cause: errClosed}
	}
	select {
	case <-c.exited:
		s.mu.Unlock()
		if c.cleanupErr != nil {
			return c.cleanupErr
		}
		code := exitCode(c.exitErr)
		return &ChildExitError{PluginID: s.spec.PluginID, Code: code,
			Meaning: s.exitMeaning(code), Phase: "start", StderrTail: c.tailLines()}
	default:
	}
	s.state = StateRunning
	s.mu.Unlock()
	return nil
}

// .
// .
// .
// .
func (s *Supervisor) pumpFrames(c *child, stdout io.Reader) {
	defer func() {
		c.stdoutEOF.Store(true)
		close(c.frames)
	}()
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

// .
// .
// .
// .
// .
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
			c.tailMu.Lock()
			if c.readyLine == "" {
				c.readyLine = line
			}
			c.tailMu.Unlock()
			c.markReady()
		}
	}
	for {
		b, err := br.ReadByte()
		if err == nil {
			if b == '\n' {
				flush()
			} else if len(buf) < maxStderrLineBytes {
				// .
				// .
				// .
				buf = append(buf, b)
			}
			continue
		}
		flush()
		return
	}
}

// .
// .
func (s *Supervisor) reap(c *child, gen int) {
	// .
	// .
	// .
	state, err := c.cmd.Process.Wait()
	if err == nil {
		c.cmd.ProcessState = state
		if !state.Success() {
			err = &exec.ExitError{ProcessState: state}
		}
	}
	if c.contained != nil {
		// .
		// .
		// .
		// .
		if err := c.contained(); err != nil {
			c.cleanupErr = &ContainmentCleanupError{PluginID: s.spec.PluginID, Err: err}
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
	// .
	// .
	// .
	if c.audioIn != nil {
		_ = c.audioIn.Close()
	}
	if c.audioOut != nil {
		_ = c.audioOut.Close()
	}
	_ = c.stdin.Close()
	c.exitErr = err
	close(c.exited)

	s.mu.Lock()
	if s.gen != gen {
		s.mu.Unlock()
		return
	}
	if c.cleanupErr != nil {
		s.state = StateStopped
		s.stopReason = c.cleanupErr
		s.spec.logger().Printf("plugin %s: %v", s.spec.PluginID, s.stopReason)
		s.mu.Unlock()
		return
	}
	if s.state == StateStopped {
		// .
		s.mu.Unlock()
		return
	}
	code := exitCode(err)
	s.spec.logger().Printf("plugin %s: child exited code=%d meaning=%q restarts=%d",
		s.spec.PluginID, code, s.exitMeaning(code), s.restarts)

	if s.state == StateStarting {
		// .
		s.mu.Unlock()
		return
	}
	if c.cancelled.Load() {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		n := s.restarts
		s.state = StateRestarting
		s.child = nil
		s.mu.Unlock()
		s.spec.logger().Printf("plugin %s: child exited on the host's cancel of a call; reviving now, not counted (restarts=%d)",
			s.spec.PluginID, n)
		go s.restart(n)
		return
	}

	now := time.Now()
	s.restartTimes = restartsWithin(s.restartTimes, now.Add(-s.spec.Backoff.window()))
	if len(s.restartTimes) >= s.spec.Backoff.maxRestarts() {
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
	s.restartTimes = append(s.restartTimes, now)
	n := s.restarts
	s.state = StateRestarting
	s.child = nil
	s.mu.Unlock()

	delay := s.spec.Backoff.delay(n)
	s.spec.logger().Printf("plugin %s: restart %d/%d in %s",
		s.spec.PluginID, n, s.spec.Backoff.maxRestarts(), delay)
	time.AfterFunc(delay, func() { s.restart(n) })
}

// .
// .
func restartsWithin(ts []time.Time, cutoff time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if !t.Before(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

// .
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
			// .
			// .
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
		return ee.ExitCode()
	}
	return -1
}

// .
// .
// .
// .
func (s *Supervisor) kill(c *child, cancelled bool) {
	if cancelled {
		c.cancelled.Store(true)
	}
	c.abandonNow()
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	s.awaitReaped(c)
}

// .
// .
// .
// .
func (s *Supervisor) Invoke(ctx context.Context, frame []byte) ([]byte, error) {
	// .
	// .
	// .
	select {
	case s.invokeGate <- struct{}{}:
		defer func() { <-s.invokeGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.spec.SessionMode {
		return nil, fmt.Errorf("supervisor: %s runs as a resident session — Invoke is not available on the session lane", s.spec.PluginID)
	}

	s.mu.Lock()
	if s.state != StateRunning || s.child == nil {
		state, reason := s.state, s.stopReason
		s.mu.Unlock()
		return nil, &UnavailableError{PluginID: s.spec.PluginID, State: state, Reason: reason}
	}
	c := s.child
	s.mu.Unlock()

	if len(frame) > bbb.MaxControlFrameBytes {
		// .
		// .
		// .
		return nil, fmt.Errorf("supervisor: %s: request frame is %d bytes, over the %d-byte plugin-side ceiling: %w",
			s.spec.PluginID, len(frame), bbb.MaxControlFrameBytes, bbb.ErrFrameTooLarge)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	wrote := make(chan error, 1)
	go func() { wrote <- bbb.WriteFrame(c.stdin, frame, bbb.MaxControlFrameBytes) }()
	select {
	case err := <-wrote:
		if err != nil {
			// .
			// .
			// .
			// .
			s.kill(c, false)
			return nil, &ChildIOError{PluginID: s.spec.PluginID, Op: "write", Err: err}
		}
	case <-ctx.Done():
		// .
		// .
		// .
		// .
		// .
		s.kill(c, true)
		return nil, ctx.Err()
	}

	for {
		// .
		// .
		// .
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
				// .
				// .
				// .
				// .
				// .
				s.kill(c, true)
				return nil, &InvokeTimeoutError{PluginID: s.spec.PluginID, Err: ctx.Err()}
			}
		}
		if !ok {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			readErr := c.readError()
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			if errors.Is(readErr, bbb.ErrFrameTooLarge) {
				s.kill(c, false)
				return nil, &ChildIOError{PluginID: s.spec.PluginID, Op: "read", Err: readErr}
			}
			select {
			case <-c.exited:
				code := exitCode(c.exitErr)
				return nil, &ChildExitError{PluginID: s.spec.PluginID, Code: code,
					Meaning: s.exitMeaning(code), Phase: "invoke", StderrTail: c.tailLines()}
			case <-time.After(closeGraceEOF):
			}
			s.kill(c, false)
			return nil, &ChildIOError{PluginID: s.spec.PluginID, Op: "read", Err: readErr}
		}
		done, resp, err := s.consumeFrame(ctx, c, payload)
		if err != nil {
			s.kill(c, false)
			return nil, err
		}
		if done {
			return resp, nil
		}
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
func (s *Supervisor) answerUpstream(ctx context.Context, c *child, members map[string]json.RawMessage) error {
	var method string
	if err := json.Unmarshal(members["method"], &method); err != nil {
		return &ProtocolError{PluginID: s.spec.PluginID,
			Requirement: "request method must be a string", Evidence: excerpt(members["method"])}
	}
	idRaw, hasID := members["id"]
	if !hasID {
		// .
		// .
		// .
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
		// .
		// .
		reply = mustJSON(rpcErrorObject{Code: -32601, Message: "method not found"})
	case s.dispatcher == nil:
		// .
		// .
		// .
		reply = mustJSON(rpcErrorObject{
			Code:    -32000,
			Message: fmt.Sprintf("no capability broker attached to this worker; %s denied", importName),
			Data:    &rpcErrorData{ReasonCode: "POLICY_DENY"},
		})
	default:
		var derr error
		reply, derr = s.dispatcher.Dispatch(ctx, importName, params)
		if derr != nil {
			// .
			// .
			// .
			s.spec.logger().Printf("plugin %s: upstream %s dispatch failed: %v", s.spec.PluginID, method, derr)
			reply = mustJSON(rpcErrorObject{Code: -32603, Message: "host dispatch failed"})
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
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

// .
// .
// .
func (s *Supervisor) Close() error { return s.CloseContext(context.Background()) }

// .
// .
// .
// .
// .
// .
func (s *Supervisor) CloseContext(ctx context.Context) error {
	s.mu.Lock()
	if s.state == StateStopped {
		c, err := s.child, s.stopReason
		s.mu.Unlock()
		// .
		if c != nil {
			if !s.awaitReaped(c) {
				return fmt.Errorf("supervisor: %s: containment retirement still pending", s.spec.PluginID)
			}
			if c.cleanupErr != nil {
				return c.cleanupErr
			}
			return s.releaseArtifact()
		}
		var cleanup *ContainmentCleanupError
		if errors.As(err, &cleanup) {
			return err
		}
		return s.releaseArtifact()
	}
	s.state = StateStopped
	if s.stopReason == nil {
		s.stopReason = errClosed
	}
	c := s.child
	s.mu.Unlock()

	if c == nil {
		return s.releaseArtifact()
	}
	c.abandonNow()
	_ = c.stdin.Close()
	select {
	case <-c.exited:
		if c.cleanupErr != nil {
			return c.cleanupErr
		}
		return s.releaseArtifact()
	case <-time.After(closeGraceEOF):
	case <-ctx.Done():
	}
	signalTerm(c.cmd)
	select {
	case <-c.exited:
		if c.cleanupErr != nil {
			return c.cleanupErr
		}
		return s.releaseArtifact()
	case <-time.After(closeGraceTerm):
	case <-ctx.Done():
	}
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	if !s.awaitReaped(c) {
		return fmt.Errorf("supervisor: %s: child survived SIGKILL and is orphaned", s.spec.PluginID)
	}
	if c.cleanupErr != nil {
		return c.cleanupErr
	}
	return s.releaseArtifact()
}

// .
// .
// .
// .
// .
// .
// .
var closeGraceKill = 5 * time.Second

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

// .

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

// .
// .
// .
// .
// .
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

// .
func excerpt(b []byte) string {
	const max = 256
	if len(b) > max {
		return string(b[:max]) + fmt.Sprintf("… (%d bytes)", len(b))
	}
	return string(b)
}

// .
// .
// .
// .
// .
// .
// .
func (s *Supervisor) retirementPending(c *child, err error) error {
	if c != nil && c.contained != nil {
		return &ContainmentCleanupError{PluginID: s.spec.PluginID, Err: err}
	}
	return nil
}

func (s *Supervisor) releaseArtifact() error {
	// .
	// .
	s.mu.Lock()
	done := s.spawning
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(closeGraceKill):
			return &ContainmentCleanupError{PluginID: s.spec.PluginID, Err: fmt.Errorf("in-flight spawn retirement still pending")}
		}
	}
	s.mu.Lock()
	reason := s.stopReason
	s.mu.Unlock()
	var cleanup *ContainmentCleanupError
	if errors.As(reason, &cleanup) {
		return reason
	}
	s.artifactOnce.Do(func() {
		if s.spec.Artifact != nil {
			s.artifactErr = s.spec.Artifact.Close()
		}
	})
	return s.artifactErr
}
