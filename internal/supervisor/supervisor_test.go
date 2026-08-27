package supervisor

// The process-boundary battery, hostile-first, against a REAL child
// process: testdata/fakechild speaks the D1-1 stdio contract — exactly
// what a native T3 plugin child speaks — with one misbehavior per
// mode. Every promise the supervisor makes to the threat model (A8:
// crash = restart, identity survives; §7: out-of-band telemetry) is
// proven here against a live process, not a mock.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

var fakechildBin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "supervisor-fakechild")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	fakechildBin = filepath.Join(tmp, "fakechild")
	if runtime.GOOS == "windows" {
		fakechildBin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", fakechildBin, "./testdata/fakechild").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build fakechild: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// captureLog is a race-safe telemetry sink.
type captureLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captureLog) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *captureLog) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func newCapture() (*captureLog, *log.Logger) {
	c := &captureLog{}
	return c, log.New(c, "", 0)
}

func childSpec(mode string, lg *log.Logger) Spec {
	return Spec{
		PluginID:  "org.example." + mode,
		Argv:      []string{fakechildBin, mode},
		Env:       []string{"SEV_PLUGIN_SOCKET=stdio:"},
		ReadyMark: "child-ready",
		Backoff:   Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 3},
		Log:       lg,
	}
}

func mustStart(t *testing.T, spec Spec, d Dispatcher) *Supervisor {
	t.Helper()
	s, err := Start(spec, d)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

const testReq = `{"jsonrpc":"2.0","id":"h1","method":"invoke.call","params":{"n":1}}`

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestInvokeRoundTripAndCleanEOFShutdown(t *testing.T) {
	capture, lg := newCapture()
	s := mustStart(t, childSpec("respond", lg), nil)

	for i := 0; i < 3; i++ {
		resp, err := s.Invoke(context.Background(), []byte(testReq))
		if err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
		if !strings.Contains(string(resp), `"answered":true`) || !strings.Contains(string(resp), `"id":"h1"`) {
			t.Fatalf("response must echo the id and carry the result: %s", resp)
		}
	}

	// Clean shutdown: stdin close is the D1-1 disconnect; the child
	// exits 0 on EOF and no restart may fire.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if state, _ := s.State(); state != StateStopped {
		t.Fatalf("state after Close = %s, want stopped", state)
	}
	waitFor(t, "eof telemetry", func() bool { return strings.Contains(capture.String(), "eof-exit") })
	if s.Restarts() != 0 {
		t.Fatalf("a clean shutdown must not count restarts, got %d", s.Restarts())
	}
	if _, err := s.Invoke(context.Background(), []byte(testReq)); err == nil {
		t.Fatal("invoke after Close must refuse typed")
	}
}

func TestStderrTelemetryCarriesPluginPrefix(t *testing.T) {
	capture, lg := newCapture()
	s := mustStart(t, childSpec("respond", lg), nil)
	waitFor(t, "prefixed ready line", func() bool {
		return strings.Contains(capture.String(), "plugin org.example.respond: fakechild: child-ready")
	})
	_ = s.Close()
}

// The A8 anchor: the child crashes; telemetry lands out-of-band with
// the plugin prefix; the supervisor restarts with backoff; the SAME
// supervisor serves invocations again — the identity never went down.
func TestCrashRestartsAndServesAgain(t *testing.T) {
	capture, lg := newCapture()
	s := mustStart(t, childSpec("crash-after-respond", lg), nil)

	resp, err := s.Invoke(context.Background(), []byte(testReq))
	if err != nil || !strings.Contains(string(resp), "answered") {
		t.Fatalf("first invoke must answer: %v %s", err, resp)
	}

	// The child dies right after answering; the next invocations fail
	// typed until the restart lands, then serve again.
	waitFor(t, "restart to land", func() bool {
		got, ierr := s.Invoke(context.Background(), []byte(testReq))
		return ierr == nil && strings.Contains(string(got), "answered")
	})
	if s.Restarts() < 1 {
		t.Fatalf("restarts = %d, want >= 1", s.Restarts())
	}
	logText := capture.String()
	for _, want := range []string{
		"plugin org.example.crash-after-respond: fakechild: crashing after first answer", // §7 out-of-band, prefixed
		"child exited code=7",
		"restart 1/3",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("host log must carry %q; got:\n%s", want, logText)
		}
	}
}

// The restart ceiling: a crash-looping child is deactivated with a
// typed reason after the bounded restarts, never a flap-forever.
func TestMaxRestartCeilingDeactivatesTyped(t *testing.T) {
	capture, lg := newCapture()
	spec := childSpec("crash", lg)
	s := mustStart(t, spec, nil)

	waitFor(t, "restart ceiling", func() bool {
		state, _ := s.State()
		return state == StateStopped
	})
	_, reason := s.State()
	var ceiling *RestartCeilingError
	if !errors.As(reason, &ceiling) || ceiling.Restarts != 3 {
		t.Fatalf("stop reason must be the typed ceiling with the count, got %v", reason)
	}
	var unavailable *UnavailableError
	if _, err := s.Invoke(context.Background(), []byte(testReq)); !errors.As(err, &unavailable) {
		t.Fatalf("invoke after ceiling must be UnavailableError carrying the reason, got %v", err)
	} else if !errors.As(unavailable.Reason, &ceiling) {
		t.Fatalf("the unavailable reason must carry the ceiling, got %v", unavailable.Reason)
	}
	if !strings.Contains(capture.String(), "DEACTIVATED") {
		t.Fatalf("the operator log must show the deactivation:\n%s", capture.String())
	}
}

// The forwarding bridge: the child sends a nested upstream request
// mid-invocation; the dispatcher (broker seam) answers; the child's
// final response embeds what came back — the whole point of D1-1's
// duplex nesting, proven over a real process boundary.
func TestUpstreamHostcallBridged(t *testing.T) {
	_, lg := newCapture()
	var got struct {
		method string
		params string
	}
	d := dispatcherFunc(func(ctx context.Context, method string, params []byte) ([]byte, error) {
		got.method, got.params = method, string(params)
		return []byte(`{"success":true,"ok":true,"status":"succeeded","operation_result":{"value":"bridged"}}`), nil
	})
	s := mustStart(t, childSpec("hostcall", lg), d)

	resp, err := s.Invoke(context.Background(), []byte(testReq))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got.method != "invoke-call" {
		t.Fatalf("the bridge must hand the dispatcher the WIT kebab name, got %q", got.method)
	}
	if !strings.Contains(got.params, `"operation":"kv.get"`) {
		t.Fatalf("params must pass through verbatim, got %s", got.params)
	}
	// The child embedded the full response envelope it received; the
	// result member must carry the dispatcher's reply verbatim under
	// the child's echoed upstream id.
	for _, want := range []string{`"id":"c1"`, `"value":"bridged"`, `"upstream"`} {
		if !strings.Contains(string(resp), want) {
			t.Fatalf("final response must show the bridged reply (%s): %s", want, resp)
		}
	}
}

// No dispatcher = the audited deny-all, across the process boundary:
// the upstream call is answered -32000 POLICY_DENY, byte-shape of the
// in-process stub.
func TestUpstreamDenyAllWithoutDispatcher(t *testing.T) {
	_, lg := newCapture()
	s := mustStart(t, childSpec("hostcall", lg), nil)
	resp, err := s.Invoke(context.Background(), []byte(testReq))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	for _, want := range []string{`"error"`, `-32000`, `POLICY_DENY`, `invoke-call`} {
		if !strings.Contains(string(resp), want) {
			t.Fatalf("the child must have received the audited denial (%s): %s", want, resp)
		}
	}
}

// A dispatcher internal failure answers the daemon's -32603 INTERNAL —
// the connection survives (C parity: handler failure is an error
// response, not connection death).
func TestUpstreamDispatcherFailureAnswersInternal(t *testing.T) {
	capture, lg := newCapture()
	d := dispatcherFunc(func(ctx context.Context, method string, params []byte) ([]byte, error) {
		return nil, errors.New("store exploded")
	})
	s := mustStart(t, childSpec("hostcall", lg), d)
	resp, err := s.Invoke(context.Background(), []byte(testReq))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(string(resp), `-32603`) {
		t.Fatalf("internal failure must answer -32603: %s", resp)
	}
	if !strings.Contains(capture.String(), "store exploded") {
		t.Fatal("the cause belongs in the host log")
	}
	// The channel survived: a second invocation still works.
	if _, err := s.Invoke(context.Background(), []byte(testReq)); err != nil {
		t.Fatalf("the connection must survive an internal dispatch failure: %v", err)
	}
}

// Non-request frames hand up VERBATIM — even a lawless one with the
// wrong id. Judging the response contract (id echo, one of
// result|error) is the caller's single decode seam, so both walls
// behave byte-identically; the pluginhost battery proves that judge
// refuses this exact frame (decodeReply's "id must echo" rule).
func TestNonRequestFramesHandUpVerbatim(t *testing.T) {
	_, lg := newCapture()
	s := mustStart(t, childSpec("badid", lg), nil)
	resp, err := s.Invoke(context.Background(), []byte(testReq))
	if err != nil {
		t.Fatalf("the supervisor must not judge response envelopes: %v", err)
	}
	if !strings.Contains(string(resp), "not-the-id") {
		t.Fatalf("the child's bytes must arrive verbatim, got %s", resp)
	}
}

// Oversize frames refuse in BOTH directions (D1-1 rule 2: 1 MiB both
// ways; the host is stricter than its server bound with its own
// children).
func TestOversizeFramesRefusedBothDirections(t *testing.T) {
	_, lg := newCapture()

	// Host→child: refused before a byte is written — the stream stays
	// clean and the channel stays up.
	s := mustStart(t, childSpec("respond", lg), nil)
	big := make([]byte, bbb.MaxControlFrameBytes+1)
	if _, err := s.Invoke(context.Background(), big); !errors.Is(err, bbb.ErrFrameTooLarge) {
		t.Fatalf("oversize outbound must refuse with ErrFrameTooLarge, got %v", err)
	}
	if resp, err := s.Invoke(context.Background(), []byte(testReq)); err != nil || !strings.Contains(string(resp), "answered") {
		t.Fatalf("the channel must survive a refused outbound frame: %v", err)
	}
	_ = s.Close()

	// Child→host: a declared length over the ceiling kills the stream
	// (no resync exists) and the invocation fails typed.
	s2 := mustStart(t, childSpec("bigframe", lg), nil)
	_, err := s2.Invoke(context.Background(), []byte(testReq))
	var ioErr *ChildIOError
	if !errors.As(err, &ioErr) || !errors.Is(ioErr.Err, bbb.ErrFrameTooLarge) {
		t.Fatalf("oversize inbound must surface ErrFrameTooLarge through the typed error, got %v", err)
	}
}

// The runaway child: the invocation deadline kills it (N-8 — no
// in-band cancel exists) and the restart policy revives it.
func TestRunawayChildDeadlineKillAndRestart(t *testing.T) {
	_, lg := newCapture()
	s := mustStart(t, childSpec("sleep", lg), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.Invoke(ctx, []byte(testReq))
	var terr *InvokeTimeoutError
	if !errors.As(err, &terr) {
		t.Fatalf("want InvokeTimeoutError, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("deadline kill took %v, want prompt", elapsed)
	}
	waitFor(t, "restart after deadline kill", func() bool { return s.Restarts() >= 1 })
}

// The kill escalation: a child that ignores both EOF and SIGTERM still
// dies within the bounded graces.
func TestCloseEscalatesToKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal semantics differ; the escalation collapses to kill on windows by design")
	}
	_, lg := newCapture()
	s := mustStart(t, childSpec("ignore-term", lg), nil)
	start := time.Now()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < closeGraceEOF {
		t.Fatalf("escalation must give the EOF grace first, took %v", elapsed)
	}
	if elapsed > closeGraceEOF+closeGraceTerm+5*time.Second {
		t.Fatalf("kill escalation too slow: %v", elapsed)
	}
}

// Artifact re-verification runs before EVERY spawn: tampering between
// restarts is a typed STOP, never a retry (A5 across the restart
// loop).
func TestArtifactTamperBetweenRestartsStopsTyped(t *testing.T) {
	capture, lg := newCapture()
	tampered := false
	var mu sync.Mutex
	spec := childSpec("crash", lg)
	spec.VerifyArtifact = func() error {
		mu.Lock()
		defer mu.Unlock()
		if tampered {
			return errors.New("digest mismatch: artifact changed on disk")
		}
		return nil
	}
	s := mustStart(t, spec, nil)
	mu.Lock()
	tampered = true
	mu.Unlock()

	waitFor(t, "tamper stop", func() bool {
		state, _ := s.State()
		return state == StateStopped
	})
	_, reason := s.State()
	var refused *SpawnRefusedError
	if !errors.As(reason, &refused) {
		t.Fatalf("tamper must stop with SpawnRefusedError, got %v", reason)
	}
	if !strings.Contains(capture.String(), "digest mismatch") {
		t.Fatal("the tamper evidence belongs in the host log")
	}
}

// Exit-code taxonomy: the worker's documented codes render in the
// telemetry and in typed errors.
func TestWorkerExitTaxonomySurfaces(t *testing.T) {
	capture, lg := newCapture()
	spec := childSpec("failstart", lg)
	spec.ExitMeaning = WorkerExitMeaning
	_, err := Start(spec, nil)
	var exitErr *ChildExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("failstart must surface the exit code, got %v", err)
	}
	if exitErr.Meaning != WorkerExitMeaning(2) || !strings.Contains(exitErr.Meaning, "admission") {
		t.Fatalf("the taxonomy must render, got %q", exitErr.Meaning)
	}
	if len(exitErr.StderrTail) == 0 || !strings.Contains(strings.Join(exitErr.StderrTail, " "), "refusing to start") {
		t.Fatalf("the stderr evidence must ride the error, got %v", exitErr.StderrTail)
	}
	_ = capture
}

// The RLIMIT_AS envelope, Linux: an over-envelope allocation dies; the
// same child un-enveloped survives. Skipped elsewhere — the no-op
// platforms record their gap honestly in rlimit_other.go.
func TestRLimitASEnvelopeOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RLIMIT_AS enforcement is the Linux mechanism (prlimit); other platforms are documented no-ops")
	}
	capture, lg := newCapture()

	// Control: un-enveloped, the sparse 4 GiB reservation succeeds.
	free := childSpec("alloc", lg)
	s, err := Start(free, nil)
	if err != nil {
		t.Fatalf("un-enveloped alloc child must start: %v", err)
	}
	waitFor(t, "alloc-ok", func() bool { return strings.Contains(capture.String(), "alloc-ok") })
	_ = s.Close()

	// Enveloped at 1 GiB: the reservation must die. The child never
	// reaches alloc-ok; it exits (Go runtime OOM) either before or
	// after readiness — both surface as a start failure or a stopped
	// supervisor, never a served allocation.
	capture2, lg2 := newCapture()
	enveloped := childSpec("alloc", lg2)
	enveloped.RLimitASBytes = 1 << 30
	enveloped.Backoff = Backoff{Initial: 20 * time.Millisecond, Max: 50 * time.Millisecond, MaxRestarts: 1}
	s2, err := Start(enveloped, nil)
	if err == nil {
		waitFor(t, "enveloped child to die", func() bool {
			state, _ := s2.State()
			return state == StateStopped
		})
		_ = s2.Close()
	}
	if strings.Contains(capture2.String(), "alloc-ok") {
		t.Fatalf("the envelope must prevent the over-limit allocation:\n%s", capture2.String())
	}
	if !strings.Contains(capture2.String(), "RLIMIT_AS 1073741824 bytes applied") {
		t.Fatalf("the envelope application must be logged:\n%s", capture2.String())
	}
}

// dispatcherFunc adapts a func to the Dispatcher seam.
type dispatcherFunc func(ctx context.Context, method string, params []byte) ([]byte, error)

func (f dispatcherFunc) Dispatch(ctx context.Context, method string, params []byte) ([]byte, error) {
	return f(ctx, method, params)
}

// Sanity: the upstream envelope classification — error objects ride
// the error member, results the result member.
func TestErrorObjectClassification(t *testing.T) {
	if !isErrorObject([]byte(`{"code":-32000,"message":"denied","data":{"reasonCode":"POLICY_DENY"}}`)) {
		t.Fatal("an error object must classify as one")
	}
	for _, notErr := range []string{
		`{"success":true,"ok":true,"status":"succeeded"}`,
		`{"success":false,"status":"denied","reasonCode":"POLICY_DENY"}`,
		`{"code":"not-a-number"}`,
		`[1,2]`,
	} {
		if isErrorObject([]byte(notErr)) {
			t.Fatalf("%s must not classify as an error object", notErr)
		}
	}
	var raw json.RawMessage = []byte(`"x"`)
	_ = raw
}
