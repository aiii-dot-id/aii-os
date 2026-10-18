package pluginhost

// The supervised-mode battery: the SAME promises the in-process
// harness proved, now across the real process boundary — a built
// worker binary, real packages, a real Registry, real broker bindings.
// The anchor denial posture must be byte-for-byte the in-process one
// (the supervised twins below), because the mode must never widen
// anything: it only moves the wall behind a process.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

var (
	workerBin    string
	fakechildBin string
	aiiBin       string
)

// storageJunctionChild marks the child the Windows junction test runs itself
// in (storage_alias_windows_test.go).
const storageJunctionChild = "AII_STORAGE_JUNCTION_CHILD"

func TestMain(m *testing.M) {
	flag.Parse()
	if flag.Lookup("test.list").Value.String() != "" {
		os.Exit(m.Run())
	}

	if os.Getenv(storageJunctionChild) == "1" {
		os.Exit(m.Run())
	}
	started := time.Now()
	tmp, err := os.MkdirTemp("", "pluginhost-supervised")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	workerBin = filepath.Join(tmp, "aii-plugin-worker")
	fakechildBin = filepath.Join(tmp, "fakechild")
	aiiBin = filepath.Join(tmp, "aii")
	if runtime.GOOS == "windows" {
		workerBin += ".exe"
		fakechildBin += ".exe"
		aiiBin += ".exe"
	}
	code := 0
	// Go owns the build graph and concurrency. Each process still owns its
	// helpers; only test-name discovery avoids preparing executables.
	out, err := exec.Command("go", "build", "-o", tmp+string(os.PathSeparator),
		"../../cmd/aii-plugin-worker", "../supervisor/testdata/fakechild", "../../cmd/aii").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build pluginhost helpers: %v\n%s", err, out)
		code = 1
	}
	fmt.Fprintf(os.Stderr, "test preparation package=pluginhost helpers=3 elapsed=%.3fs exit=%d\n", time.Since(started).Seconds(), code)
	if code == 0 {
		code = m.Run()
	}
	if err := os.RemoveAll(tmp); err != nil {
		fmt.Fprintf(os.Stderr, "remove pluginhost test binaries: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// Re-enter the actual TestMain, with only its Go build replaced. No helper
// is launched: the selected test below is the controlled pass/fail boundary.
func TestPluginhostPreparationKeepsDiscoveryCheapAndCleansUp(t *testing.T) {
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
		{"discovery", "-test.list=^TestPluginhostPreparationKeepsDiscoveryCheapAndCleansUp$", "7", "pass", 0, 0},
		{"pass", "-test.run=^TestPluginhostPreparationKeepsDiscoveryCheapAndCleansUp$", "0", "pass", 1, 0},
		{"test failure", "-test.run=^TestPluginhostPreparationKeepsDiscoveryCheapAndCleansUp$", "0", "fail", 1, 1},
		{"build failure", "-test.run=^TestPluginhostPreparationKeepsDiscoveryCheapAndCleansUp$", "7", "pass", 1, 1},
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
				for _, helper := range []string{"../../cmd/aii-plugin-worker", "../supervisor/testdata/fakechild", "../../cmd/aii"} {
					if !strings.Contains(string(builds), helper) {
						t.Errorf("missing helper %s in %s", helper, builds)
					}
				}
				if !strings.Contains(string(output), "test preparation package=pluginhost ") {
					t.Errorf("preparation timing missing: %s", output)
				}
			} else if !strings.Contains(string(output), "TestPluginhostPreparationKeepsDiscoveryCheapAndCleansUp") {
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

func supervisedOpts(extra Options) *Options {
	opts := extra
	opts.WorkerBinary = workerBin
	return &opts
}

func waitUntil(cond func() bool) bool {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func pollUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	if !waitUntil(cond) {
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestTheDaemonReExecutedIsTheSupervisedWorker(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.selfworker", "0.1.0", []string{"ping"}, fixtureWasm(t, "responder.wasm")))
	// "plugin-worker" mirrors app.WorkerSubcommand; app imports this
	// package, so the name is repeated here rather than imported.
	opts := Options{WorkerBinary: aiiBin, WorkerArgs: []string{"plugin-worker"}}
	ap, err := Activate(context.Background(), pkg, reg, &opts)
	if err != nil {
		t.Fatalf("Activate through the daemon binary: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	if ap.Mode != ModeSupervised || ap.SupervisedPid() == 0 {
		t.Fatalf("mode = %s pid = %d: the daemon re-executed must be a live supervised child", ap.Mode, ap.SupervisedPid())
	}
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]any{"probe": true})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("dispatch through the daemon's own worker lane must work: %v %+v", err, res)
	}
	if !strings.Contains(capture.String(), "plugins.session: plugin org.example.selfworker: aii-plugin-worker: event=ready") {
		t.Fatalf("the ready banner must come from the re-executed daemon:\n%s", capture.String())
	}
}

// The supervised echo E2E: activation → real child process → tool
// dispatch through the process boundary → deactivation cleans up.
func TestSupervisedResponderE2E(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.supresponder", "0.1.0", []string{"ping"}, fixtureWasm(t, "responder.wasm")))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{}))
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if ap.Mode != ModeSupervised {
		t.Fatalf("mode = %s, want supervised (worker binary resolved)", ap.Mode)
	}
	if ap.SupervisedPid() == 0 {
		t.Fatal("a supervised activation must have a live child")
	}
	artifactDir := ap.artifactDir

	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]any{"probe": true})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("dispatch through the process boundary must work: %v %+v", err, res)
	}
	if !strings.Contains(capture.String(), "plugins.session: plugin org.example.supresponder: aii-plugin-worker: event=ready") {
		t.Fatalf("the worker banner must land in the host log with the plugin prefix:\n%s", capture.String())
	}

	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if _, ok := reg.Get(ap.ToolNames[0]); ok {
		t.Fatal("Deactivate must deregister the tools")
	}
	if _, err := os.Stat(artifactDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the extracted artifact dir must be removed, got %v", err)
	}
	pollUntil(t, "clean child exit in log", func() bool {
		return strings.Contains(capture.String(), "plugins.session: plugin org.example.supresponder: aii-plugin-worker: event=shutdown reason=stdin-eof")
	})
}

// The anchor denial, supervised twin (TestCallerGuestSurfacesQuarantineDenial):
// zero grants means the guest's hostcall crosses the process boundary
// and STILL meets the audited POLICY_DENY — the deny-all posture is
// mode-independent.
func TestSupervisedCallerKeepsQuarantineDenial(t *testing.T) {
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.supcaller", "0.1.0", []string{"ping"}, fixtureWasm(t, "caller.wasm")))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{}))
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	defer ap.Deactivate(context.Background())
	if ap.Mode != ModeSupervised {
		t.Fatalf("mode = %s, want supervised", ap.Mode)
	}

	_, err = reg.Execute(context.Background(), ap.ToolNames[0], nil)
	var rce *ResponseContractError
	if !errors.As(err, &rce) {
		t.Fatalf("want the relayed deny-all shape, got %v", err)
	}
	for _, evidence := range []string{"POLICY_DENY", "-32000"} {
		if !strings.Contains(rce.Got, evidence) {
			t.Fatalf("the supervised posture must surface the same audited denial (%s), got %q", evidence, rce.Got)
		}
	}
}

// The supervised broker twin (TestKVPairThroughActivationTempAtT0):
// guest kv hostcalls forwarded upstream into a REAL broker binding —
// same three-ring evaluation, receipts injected, temp RING4 dying with
// the activation — now across the process boundary.
func TestSupervisedKVForwardedToRealBroker(t *testing.T) {
	const id = "org.example.supkv"
	guest := wasmgen.CannedCaller(
		[]byte(`{"operation":"kv.put","target":{"key":"greeting"},"arguments":{"value":"hello-supervised"}}`),
		[]byte(`{"operation":"kv.get","target":{"key":"greeting"},"arguments":{}}`),
	)
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
	if err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t)
	pkg := writePkg(t, brokerPkgSpec(id, "0.1.0", []string{"roundtrip"}, guest, nil))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{Broker: h}))
	if err != nil {
		t.Fatal(err)
	}
	if ap.Mode != ModeSupervised {
		t.Fatalf("mode = %s, want supervised", ap.Mode)
	}

	res, err := reg.Execute(context.Background(), ap.ToolNames[0], nil)
	if err != nil || res.Error != "" {
		t.Fatalf("the forwarded kv roundtrip must succeed: %v %+v", err, res)
	}
	for _, want := range []string{`"stored":true`, `"hello-supervised"`, `"scope":"temp"`, `"host_authored":true`} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("relayed replies must carry %s, got %s", want, res.Output)
		}
	}
	scope := storedTempKVScope(t, st, id, "greeting")
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.PluginKVGet(scope, "greeting"); err != nil || found {
		t.Fatalf("temp RING4 must die with the activation, supervised too: found=%v err=%v", found, err)
	}
	recs, _ := st.PluginReceipts(id)
	if len(recs) != 2 {
		t.Fatalf("put+get → two host-authored receipts, got %d", len(recs))
	}
}

// The signed net twin (TestSignedT1NetFetchE2E): a publisher-signed
// package, an operator grant, the egress guard, the H3 observation —
// the whole three-ring path with the guest behind the process
// boundary, receipt injected into what the identity sees.
func TestSupervisedSignedT1NetFetchE2E(t *testing.T) {
	const id = "org.example.supfetcher"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"weather":"supervised-aurora"}`)
	}))
	defer ts.Close()
	host, port := serverHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	target := ts.URL + "/v1/report"

	guest := wasmgen.CannedCaller([]byte(fmt.Sprintf(
		`{"operation":"http.get","target":{"url":%q},"arguments":{}}`, target)))
	spec := brokerPkgSpec(id, "1.0.0", []string{"fetch"}, guest, []string{"net.outbound:" + hostPort})
	if err := signer(t).SignT1(&spec); err != nil {
		t.Fatal(err)
	}

	st := newBrokerStore(t)
	reg := newRegistry(t)
	h, err := broker.New(broker.Config{
		Store:        st,
		Grants:       map[string]broker.Grant{id: {Hosts: []string{hostPort}}},
		Guard:        guardAdmitting(ts),
		ObserveFetch: reg.NotifyFetch,
	})
	if err != nil {
		t.Fatal(err)
	}
	ap, err := Activate(context.Background(), writePkg(t, spec), reg,
		supervisedOpts(Options{Roots: signerRoots(t), Broker: h}))
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Deactivate(context.Background())
	if ap.Tier != packagefmt.TierT1 || ap.Mode != ModeSupervised {
		t.Fatalf("want T1 supervised, got %s %s", ap.Tier, ap.Mode)
	}

	var observed []string
	reg.ObserveFetches(func(u string) { observed = append(observed, u) })

	res, err := reg.Execute(context.Background(), ap.ToolNames[0], nil)
	if err != nil || res.Error != "" {
		t.Fatalf("the granted fetch must perform across the boundary: %v %+v", err, res)
	}
	for _, want := range []string{`"weather":"supervised-aurora"`, `"host_authored":true`, `"status":"succeeded"`} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output must carry the relayed result and injected receipt (%s): %s", want, res.Output)
		}
	}
	recs, _ := st.PluginReceipts(id)
	if len(recs) != 1 || !recs[0].Success || recs[0].Target != target {
		t.Fatalf("one success receipt for the real effect, got %+v", recs)
	}
	if len(observed) != 1 || observed[0] != target {
		t.Fatalf("the brokered fetch must feed ObserveFetches (H3), got %v", observed)
	}
}

// A crashing guest: the worker dies (exit 3), telemetry lands with the
// plugin prefix, the supervisor restarts with backoff, and the SAME
// registered tool dispatches into the fresh child — registrations
// derive statically from the signed manifest, so a restarted child
// re-passes admission and the identity's tool surface never moved.
func TestSupervisedCrashTelemetryRestartAndRecovery(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.supcrash", "0.1.0", []string{"ping"}, fixtureWasm(t, "responder.wasm")))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{}))
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Deactivate(context.Background())

	// Kill the child out from under the harness — the hard crash.
	pid := ap.SupervisedPid()
	if pid == 0 {
		t.Fatal("no child pid")
	}
	if killed, err := ap.sup.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}

	// The tool must come back without any re-registration: poll until
	// dispatch succeeds against the restarted child.
	pollUntil(t, "tool dispatch after restart", func() bool {
		res, rerr := reg.Execute(context.Background(), ap.ToolNames[0], nil)
		return rerr == nil && res.Error == "" && strings.Contains(res.Output, `"echoed":true`)
	})
	if ap.Restarts() < 1 {
		t.Fatalf("restarts = %d, want >= 1", ap.Restarts())
	}
	logText := capture.String()
	for _, want := range []string{"plugins.end: plugin org.example.supcrash: child exited", "plugins.end: plugin org.example.supcrash: restart 1/"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("host log must carry %q:\n%s", want, logText)
		}
	}
}

// A trapping guest is out-of-band telemetry too: the worker's fatal
// stderr line arrives prefixed, the tool call fails typed with the
// worker's documented exit taxonomy.
func TestSupervisedTrapSurfacesTaxonomy(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.suptrap", "0.1.0", []string{"ping"}, fixtureWasm(t, "trap.wasm")))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{}))
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Deactivate(context.Background())

	_, err = reg.Execute(context.Background(), ap.ToolNames[0], nil)
	var exitErr *supervisor.ChildExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("a trapped guest must surface the child exit typed with code 3, got %v", err)
	}
	if !strings.Contains(exitErr.Meaning, "fatal invocation failure") {
		t.Fatalf("the worker taxonomy must render, got %q", exitErr.Meaning)
	}
	pollUntil(t, "prefixed fatal telemetry", func() bool {
		return strings.Contains(capture.String(), "plugins.session: plugin org.example.suptrap: aii-plugin-worker: event=fatal stage=invoke")
	})
}

func TestSupervisedRunawayDeadlineKill(t *testing.T) {
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.suploop", "0.1.0", []string{"ping"}, fixtureWasm(t, "loop.wasm")))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{}))
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Deactivate(context.Background())
	first := ap.SupervisedPid()
	if first == 0 {
		t.Fatal("no child pid")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = reg.Execute(ctx, ap.ToolNames[0], nil)
	var terr *supervisor.InvokeTimeoutError
	if !errors.As(err, &terr) {
		t.Fatalf("want the typed deadline kill, got %v", err)
	}
	pollUntil(t, "revival after the deadline kill", func() bool {
		pid := ap.SupervisedPid()
		return pid != 0 && pid != first
	})
	if n := ap.Restarts(); n != 0 {
		t.Fatalf("a host-requested kill was counted as a crash: restarts=%d", n)
	}
}

func TestNativeT3LaneRunsVerifiedArtifact(t *testing.T) {
	skipWhereNativeIsRefused(t)
	// This proof LAUNCHES the child contained, so it asks whether this
	// host can build the sandbox — not whether bwrap is on PATH, which
	// skipped it on every darwin run, where sandbox-exec contains.
	skipWhereTheSandboxCannotBeEstablished(t)
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	res := &packagefmt.Result{
		Tier: packagefmt.TierT3,
		Manifest: &packagefmt.Manifest{
			ID: "org.example.native",
			Variants: []packagefmt.Variant{{
				VariantID: "native", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
				Topology: packagefmt.HostTopology(), ExecutionRuntime: "native_t3_component",
				AdmissionProfile: "platform_reserved", Entrypoint: "variants/native/child",
			}},
		},
		FileDigests: map[string]string{"variants/native/child": digestOf(raw)},
	}
	capture := logsink.CaptureForTest(t)
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		opts := &Options{NativePlaces: func() ([]*firewall.Rule, error) { return []*firewall.Rule{nativePlace("relative")}, nil }}
		sup, dir, _, err := startSupervisedNativeWith(context.Background(), res, &res.Manifest.Variants[0], raw, nil,
			opts, nil, startupAllowance(opts, res.Manifest.ID, nil), "", nil)
		if sup != nil {
			sup.Close()
		}
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		if !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("native launch ignored its protected paths: %v", err)
		}
	}
	// A place the app cannot name refuses the child (H6: another identity's
	// slot is never left unmasked).
	unnamed := errors.New("the machine service's state does not read")
	refusing := &Options{NativePlaces: func() ([]*firewall.Rule, error) { return nil, unnamed }}
	if sup, dir, _, err := startSupervisedNativeWith(context.Background(), res, &res.Manifest.Variants[0], raw, nil,
		refusing, nil, startupAllowance(refusing, res.Manifest.ID, nil), "", nil); !errors.Is(err, unnamed) {
		if sup != nil {
			sup.Close()
		}
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		t.Fatalf("a native child was started with a place its app could not name: %v", err)
	}
	opts := &Options{NativePlaces: func() ([]*firewall.Rule, error) {
		return []*firewall.Rule{nativePlace(os.TempDir())}, nil
	}}
	sup, dir, _, serr := startSupervisedNativeWith(context.Background(), res, &res.Manifest.Variants[0], raw, nil,
		opts, nil, startupAllowance(opts, res.Manifest.ID, nil), "", nil)
	if serr != nil {
		t.Fatalf("native lane start: %v", serr)
	}
	defer os.RemoveAll(dir)
	defer sup.Close()

	resp, err := sup.Invoke(context.Background(), []byte(`{"jsonrpc":"2.0","id":"h1","method":"invoke.call","params":{}}`))
	if err != nil || !strings.Contains(string(resp), `"answered":true`) {
		t.Fatalf("the native child must answer over stdio: %v %s", err, resp)
	}
	pollUntil(t, "stdio endpoint in child env", func() bool {
		return regexp.MustCompile(`plugins\.session: plugin org\.example\.native: fakechild: child-ready .*socket=stdio:`).MatchString(capture.String())
	})

	artifact := filepath.Join(dir, "artifact")
	if runtime.GOOS == "windows" {
		// Windows binds the verified executable with a sharing lock for the
		// whole activation. The attempted swap itself must fail (32 is
		// ERROR_SHARING_VIOLATION), before the restart verification seam.
		if err := os.Remove(artifact + ".exe"); !errors.Is(err, syscall.Errno(32)) {
			t.Fatalf("live Windows image replacement must be denied by its binding: %v", err)
		}
		return
	}
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, _ = sup.KillChild()
	pollUntil(t, "tamper stop", func() bool {
		state, _ := sup.State()
		return state == supervisor.StateStopped
	})
	_, reason := sup.State()
	var refused *supervisor.SpawnRefusedError
	if !errors.As(reason, &refused) {
		t.Fatalf("a tampered artifact must stop the supervisor typed, got %v", reason)
	}
	var digestErr *EntrypointDigestError
	if !errors.As(reason, &digestErr) {
		t.Fatalf("the refusal must carry the digest evidence, got %v", reason)
	}
}

func digestOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// A configured native readiness deadline reaches the actual supervisor. This
// child deliberately never emits event=ready, so waiting the default 30 seconds
// would contradict the operator's configured deadline.
func TestNativeConfiguredReadyDeadlineIsEnforced(t *testing.T) {
	skipWhereTheSandboxCannotBeEstablished(t)
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.startup-deadline"
	v := packagefmt.Variant{VariantID: "native", Entrypoint: "variants/native/child" + exeSuffix}
	res := &packagefmt.Result{Tier: packagefmt.TierT3, Manifest: &packagefmt.Manifest{ID: id}, FileDigests: map[string]string{v.Entrypoint: digestOf(raw)}}
	opts := &Options{ReadyTimeout: map[string]time.Duration{id: 25 * time.Millisecond}}
	profile := &AcceleratorProfile{Backend: "cpu"}
	started := time.Now()
	sup, dir, _, err := startSupervisedNativeWith(context.Background(), res, &v, raw, nil, opts, profile, startupAllowance(opts, id, profile), "", nil)
	if sup != nil {
		defer sup.Close()
	}
	defer os.RemoveAll(dir)
	var exit *supervisor.ChildExitError
	if !errors.As(err, &exit) || exit.Phase != "start" || !strings.Contains(exit.Meaning, "25ms") {
		t.Fatalf("configured startup refusal: %v", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("configured readiness deadline was not enforced")
	}
}

func TestAReadinessRefusalNamesItsAllowance(t *testing.T) {
	skipWhereTheSandboxCannotBeEstablished(t)
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.startup-provenance"
	v := packagefmt.Variant{VariantID: "native", Entrypoint: "variants/native/child" + exeSuffix}
	res := &packagefmt.Result{Tier: packagefmt.TierT3, Manifest: &packagefmt.Manifest{ID: id}, FileDigests: map[string]string{v.Entrypoint: digestOf(raw)}}
	opts := &Options{ReadyTimeout: map[string]time.Duration{id: 25 * time.Millisecond}, StartupCeiling: 20 * time.Millisecond}
	profile := &AcceleratorProfile{Backend: "cpu"}
	sup, dir, _, err := startSupervisedNativeWith(context.Background(), res, &v, raw, nil, opts, profile, startupAllowance(opts, id, profile), "", nil)
	if sup != nil {
		defer sup.Close()
	}
	defer os.RemoveAll(dir)
	var exit *supervisor.ChildExitError
	if !errors.As(err, &exit) || exit.Phase != "start" {
		t.Fatalf("a child that never reports ready is refused at start: %v", err)
	}
	if !strings.Contains(exit.Meaning, "within 20ms") || !strings.Contains(exit.Meaning, "you set 25ms; capped at the ceiling 20ms") {
		t.Fatalf("the refusal must name the allowance and where it came from: %s", exit.Meaning)
	}
}
