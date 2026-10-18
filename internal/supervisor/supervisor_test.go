package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

var fakechildBin string

func TestMain(m *testing.M) {
	flag.Parse()
	if flag.Lookup("test.list").Value.String() != "" {
		os.Exit(m.Run())
	}
	started := time.Now()
	tmp, err := os.MkdirTemp("", "supervisor-fakechild")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakechildBin = filepath.Join(tmp, "fakechild")
	if runtime.GOOS == "windows" {
		fakechildBin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", fakechildBin, "./testdata/fakechild").CombinedOutput()
	code := 0
	if err != nil {
		fmt.Fprintf(os.Stderr, "build fakechild: %v\n%s", err, out)
		code = 1
	}
	fmt.Fprintf(os.Stderr, "test preparation package=supervisor helpers=1 elapsed=%.3fs exit=%d\n", time.Since(started).Seconds(), code)
	if code == 0 {
		code = m.Run()
	}
	if err := os.RemoveAll(tmp); err != nil {
		fmt.Fprintf(os.Stderr, "remove supervisor test binary: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// Re-enter the actual TestMain, with only its Go build replaced. No helper
// is launched: the selected test below is the controlled pass/fail boundary.
func TestSupervisorPreparationKeepsDiscoveryCheapAndCleansUp(t *testing.T) {
	if probe := os.Getenv("AII_PREPARATION_PROBE"); probe != "" {
		if probe == "fail" {
			t.Fatal("preparation fixture test failure")
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("the build stand-in is a POSIX shell; native Windows runs the real helpers")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, selection, buildExit, probe string
		wantBuilds, wantExit              int
	}{
		{"discovery", "-test.list=^TestSupervisorPreparationKeepsDiscoveryCheapAndCleansUp$", "7", "pass", 0, 0},
		{"pass", "-test.run=^TestSupervisorPreparationKeepsDiscoveryCheapAndCleansUp$", "0", "pass", 1, 0},
		{"test failure", "-test.run=^TestSupervisorPreparationKeepsDiscoveryCheapAndCleansUp$", "0", "fail", 1, 1},
		{"build failure", "-test.run=^TestSupervisorPreparationKeepsDiscoveryCheapAndCleansUp$", "7", "pass", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, temporary := t.TempDir(), t.TempDir()
			log := filepath.Join(root, "build.log")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AII_PREPARATION_LOG\"\n" +
				"if [ -d \"$3\" ]; then touch \"$3/partial\"; else touch \"$3\"; fi\n" +
				"echo preparation-fixture-build >&2\nexit \"$AII_PREPARATION_EXIT\"\n"
			if err := os.WriteFile(filepath.Join(root, "go"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, tc.selection, "-test.v")
			cmd.WaitDelay = 5 * time.Second
			cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TMPDIR="+temporary, "AII_PREPARATION_LOG="+log,
				"AII_PREPARATION_EXIT="+tc.buildExit, "AII_PREPARATION_PROBE="+tc.probe)
			output, err := cmd.CombinedOutput()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("run TestMain: %v\n%s", err, output)
			}
			if ctx.Err() != nil || code != tc.wantExit {
				t.Fatalf("TestMain exit=%d, want %d (deadline=%v):\n%s", code, tc.wantExit, ctx.Err(), output)
			}
			builds, err := os.ReadFile(log)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if got := strings.Count(string(builds), "\n"); got != tc.wantBuilds {
				t.Errorf("Go build calls=%d, want %d: %s", got, tc.wantBuilds, builds)
			}
			if tc.wantBuilds != 0 {
				for _, helper := range []string{"./testdata/fakechild"} {
					if !strings.Contains(string(builds), helper) {
						t.Errorf("missing helper %s in %s", helper, builds)
					}
				}
				if !strings.Contains(string(output), "test preparation package=supervisor ") {
					t.Errorf("preparation timing missing: %s", output)
				}
			} else if !strings.Contains(string(output), "TestSupervisorPreparationKeepsDiscoveryCheapAndCleansUp") {
				t.Errorf("discovery lost the test name: %s", output)
			}
			if tc.buildExit != "0" && tc.wantBuilds != 0 && !strings.Contains(string(output), "preparation-fixture-build") {
				t.Errorf("build diagnostic lost: %s", output)
			}
			if tc.probe == "fail" && !strings.Contains(string(output), "preparation fixture test failure") {
				t.Errorf("test diagnostic lost: %s", output)
			}
			left, err := os.ReadDir(temporary)
			if err != nil || len(left) != 0 {
				t.Errorf("TestMain left owned files: %v (read error=%v)", left, err)
			}
		})
	}
}

func childSpec(mode string) Spec {
	return Spec{
		PluginID:  "org.example." + mode,
		Argv:      []string{fakechildBin, mode},
		Env:       []string{"SEV_PLUGIN_SOCKET=stdio:"},
		ReadyMark: "child-ready",
		Backoff:   Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 3},
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
	capture := logsink.CaptureForTest(t)
	s := mustStart(t, childSpec("respond"), nil)

	for i := 0; i < 3; i++ {
		resp, err := s.Invoke(context.Background(), []byte(testReq))
		if err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
		if !strings.Contains(string(resp), `"answered":true`) || !strings.Contains(string(resp), `"id":"h1"`) {
			t.Fatalf("response must echo the id and carry the result: %s", resp)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if state, _ := s.State(); state != StateStopped {
		t.Fatalf("state after Close = %s, want stopped", state)
	}
	waitFor(t, "eof telemetry", func() bool {
		return strings.Contains(capture.String(), "plugins.session: plugin org.example.respond: fakechild: eof-exit")
	})
	if s.Restarts() != 0 {
		t.Fatalf("a clean shutdown must not count restarts, got %d", s.Restarts())
	}
	if _, err := s.Invoke(context.Background(), []byte(testReq)); err == nil {
		t.Fatal("invoke after Close must refuse typed")
	}
}

func TestStderrTelemetryCarriesPluginPrefix(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	s := mustStart(t, childSpec("respond"), nil)
	waitFor(t, "prefixed ready line", func() bool {
		return strings.Contains(capture.String(), "plugins.session: plugin org.example.respond: fakechild: child-ready")
	})
	_ = s.Close()
}

// The A8 anchor: the child crashes; telemetry lands out-of-band with
// the plugin prefix; the supervisor restarts with backoff; the SAME
// supervisor serves invocations again — the identity never went down.
func TestCrashRestartsAndServesAgain(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	s := mustStart(t, childSpec("crash-after-respond"), nil)

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
		"plugins.session: plugin org.example.crash-after-respond: fakechild: crashing after first answer", // §7 out-of-band, prefixed
		"plugins.end: plugin org.example.crash-after-respond: child exited code=7",
		"plugins.end: plugin org.example.crash-after-respond: restart 1/3",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("host log must carry %q; got:\n%s", want, logText)
		}
	}
	// The reaper's own line says why the child died, whatever level the
	// relay of its stderr is logged at: its last words, on the same line.
	var died string
	for _, line := range strings.Split(logText, "\n") {
		if strings.Contains(line, "child exited code=7") {
			died = line
			break
		}
	}
	if !strings.Contains(died, "; stderr tail: ") || !strings.Contains(died, "fakechild: crashing after first answer") {
		t.Fatalf("the death's line must name the child's last words; got %q", died)
	}
}

// The restart ceiling: a crash-looping child is deactivated with a
// typed reason after the bounded restarts, never a flap-forever.
func TestMaxRestartCeilingDeactivatesTyped(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	spec := childSpec("crash")
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

	waitFor(t, "the operator log shows the deactivation", func() bool {
		return strings.Contains(capture.String(), "plugins.refusal: plugin org.example.crash: DEACTIVATED: ")
	})
}

func TestUpstreamHostcallBridged(t *testing.T) {
	var got struct {
		method string
		params string
	}
	d := dispatcherFunc(func(ctx context.Context, method string, params []byte) ([]byte, error) {
		got.method, got.params = method, string(params)
		return []byte(`{"success":true,"ok":true,"status":"succeeded","operation_result":{"value":"bridged"}}`), nil
	})
	s := mustStart(t, childSpec("hostcall"), d)

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
	s := mustStart(t, childSpec("hostcall"), nil)
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
	capture := logsink.CaptureForTest(t)
	d := dispatcherFunc(func(ctx context.Context, method string, params []byte) ([]byte, error) {
		return nil, errors.New("store exploded")
	})
	s := mustStart(t, childSpec("hostcall"), d)
	resp, err := s.Invoke(context.Background(), []byte(testReq))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(string(resp), `-32603`) {
		t.Fatalf("internal failure must answer -32603: %s", resp)
	}
	if !regexp.MustCompile(`plugins\.error: plugin org\.example\.hostcall: upstream \S+ dispatch failed: store exploded`).MatchString(capture.String()) {
		t.Fatalf("the cause belongs in the host log:\n%s", capture.String())
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
	s := mustStart(t, childSpec("badid"), nil)
	resp, err := s.Invoke(context.Background(), []byte(testReq))
	if err != nil {
		t.Fatalf("the supervisor must not judge response envelopes: %v", err)
	}
	if !strings.Contains(string(resp), "not-the-id") {
		t.Fatalf("the child's bytes must arrive verbatim, got %s", resp)
	}
}

func TestOversizeFramesRefusedBothDirections(t *testing.T) {
	// Host→child: refused before a byte is written — the stream stays
	// clean and the channel stays up.
	s := mustStart(t, childSpec("respond"), nil)
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
	s2 := mustStart(t, childSpec("bigframe"), nil)
	_, err := s2.Invoke(context.Background(), []byte(testReq))
	var ioErr *ChildIOError
	if !errors.As(err, &ioErr) || !errors.Is(ioErr.Err, bbb.ErrFrameTooLarge) {
		t.Fatalf("oversize inbound must surface ErrFrameTooLarge through the typed error, got %v", err)
	}
}

func TestRunawayChildDeadlineKillAndRestart(t *testing.T) {
	s := mustStart(t, childSpec("sleep"), nil)
	first := s.Pid()

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
	waitFor(t, "revival after the deadline kill", func() bool {
		st, _ := s.State()
		return st == StateRunning && s.Pid() != 0 && s.Pid() != first
	})
	if n := s.Restarts(); n != 0 {
		t.Fatalf("a host-requested kill was counted as a crash: restarts=%d", n)
	}
}

// The kill escalation: a child that ignores both EOF and SIGTERM still
// dies within the bounded graces.
func TestCloseEscalatesToKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal semantics differ; the escalation collapses to kill on windows by design")
	}
	s := mustStart(t, childSpec("ignore-term"), nil)
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
	capture := logsink.CaptureForTest(t)
	tampered := false
	var mu sync.Mutex
	spec := childSpec("crash")
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

	evidence := regexp.MustCompile(`plugins\.refusal: plugin org\.example\.crash: DEACTIVATED: .*digest mismatch`)
	waitFor(t, "the tamper evidence in the host log", func() bool {
		return evidence.MatchString(capture.String())
	})
}

// Exit-code taxonomy: the worker's documented codes render in the
// telemetry and in typed errors.
func TestWorkerExitTaxonomySurfaces(t *testing.T) {
	spec := childSpec("failstart")
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
}

// The RLIMIT_AS envelope, Linux: an over-envelope allocation dies; the
// same child un-enveloped survives. Skipped elsewhere — the no-op
// platforms record their gap honestly in rlimit_other.go.
func TestRLimitASEnvelopeOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RLIMIT_AS enforcement is the Linux mechanism (prlimit); other platforms are documented no-ops")
	}
	capture := logsink.CaptureForTest(t)

	// Control: un-enveloped, the sparse 4 GiB reservation succeeds.
	free := childSpec("alloc")
	s, err := Start(free, nil)
	if err != nil {
		t.Fatalf("un-enveloped alloc child must start: %v", err)
	}
	waitFor(t, "alloc-ok", func() bool {
		return strings.Contains(capture.String(), "plugins.session: plugin org.example.alloc: fakechild: alloc-ok")
	})
	_ = s.Close()

	// Enveloped at 1 GiB: the reservation must die. The child never
	// reaches alloc-ok; it exits (Go runtime OOM) either before or
	// after readiness — both surface as a start failure or a stopped
	// supervisor, never a served allocation.
	capture.Reset()
	enveloped := childSpec("alloc")
	enveloped.RLimitASBytes = 1 << 30
	enveloped.Backoff = Backoff{Initial: 20 * time.Millisecond, Max: 50 * time.Millisecond, MaxRestarts: 1}
	s2, err := Start(enveloped, nil)
	if err == nil {
		t.Cleanup(func() { _ = s2.Close() })
		waitFor(t, "enveloped child to die", func() bool {
			state, _ := s2.State()
			return state == StateStopped
		})
		_ = s2.Close()
	}
	if strings.Contains(capture.String(), "alloc-ok") {
		t.Fatalf("the envelope must prevent the over-limit allocation:\n%s", capture.String())
	}
	// If the child allocated before prlimit, admission now refuses and reaps
	// it with a typed reason instead of logging a successful envelope.
	if err != nil {
		var refused *SpawnRefusedError
		var exited *ChildExitError
		if !errors.As(err, &refused) && !errors.As(err, &exited) {
			t.Fatalf("over-limit startup must refuse or report the child's exit: %v", err)
		}
	} else if !strings.Contains(capture.String(), "plugins.start: plugin org.example.alloc: resource envelope: RLIMIT_AS 1073741824 bytes applied") {
		t.Fatalf("a successfully started child's envelope application must be logged:\n%s", capture.String())
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

func TestRetirementPendingNamesContainmentOnlyWhereThereIsAny(t *testing.T) {
	s := &Supervisor{spec: Spec{PluginID: "id.test.plugin"}}
	if got := s.retirementPending(&child{}, errors.New("stuck")); got != nil {
		t.Fatalf("an uncontained child was reported as a containment failure: %v", got)
	}
	contained := &child{contained: func() error { return nil }}
	got := s.retirementPending(contained, errors.New("stuck"))
	var cce *ContainmentCleanupError
	if !errors.As(got, &cce) {
		t.Fatalf("a contained child's pending retirement was not named: %v", got)
	}
}

func TestCalibrationInvokeGenerationRefusesReplacement(t *testing.T) {
	s := mustStart(t, childSpec("respond"), nil)
	old := s.Generation()
	if old == 0 {
		t.Fatal("missing initial generation")
	}
	if _, err := s.InvokeGeneration(t.Context(), []byte(testReq), old+1); !errors.Is(err, ErrGenerationChanged) {
		t.Fatal(err)
	}
	if _, err := s.InvokeGeneration(t.Context(), []byte(testReq), old); err != nil {
		t.Fatal(err)
	}
	if killed, err := s.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	waitFor(t, "new stream", func() bool { g := s.Generation(); return g != 0 && g != old })
	if _, err := s.InvokeGeneration(t.Context(), []byte(testReq), old); !errors.Is(err, ErrGenerationChanged) {
		t.Fatal(err)
	}
	if _, err := s.InvokeGeneration(t.Context(), []byte(testReq), s.Generation()); err != nil {
		t.Fatal(err)
	}
}
