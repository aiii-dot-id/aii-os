package pluginhost

// The supervised-mode battery: the SAME promises the in-process
// harness proved, now across the real process boundary — a built
// worker binary, real packages, a real Registry, real broker bindings.
// The anchor denial posture must be byte-for-byte the in-process one
// (the supervised twins below), because the mode must never widen
// anything: it only moves the wall behind a process.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

var (
	workerBin    string
	fakechildBin string
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "pluginhost-supervised")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	workerBin = filepath.Join(tmp, "aii-plugin-worker")
	fakechildBin = filepath.Join(tmp, "fakechild")
	if runtime.GOOS == "windows" {
		workerBin += ".exe"
		fakechildBin += ".exe"
	}
	for _, build := range []struct{ out, pkg string }{
		{workerBin, "../../cmd/aii-plugin-worker"},
		{fakechildBin, "../supervisor/testdata/fakechild"},
	} {
		out, err := exec.Command("go", "build", "-o", build.out, build.pkg).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", build.pkg, err, out)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// logSink is a race-safe host-log capture.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func supervisedOpts(extra Options) *Options {
	opts := extra
	opts.WorkerBinary = workerBin
	return &opts
}

func pollUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The supervised echo E2E: activation → real child process → tool
// dispatch through the process boundary → deactivation cleans up.
func TestSupervisedResponderE2E(t *testing.T) {
	sink := &logSink{}
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.supresponder", "0.1.0", []string{"ping"}, fixtureWasm(t, "responder.wasm")))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{Log: log.New(sink, "", 0)}))
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

	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]interface{}{"probe": true})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("dispatch through the process boundary must work: %v %+v", err, res)
	}
	if !strings.Contains(sink.String(), "plugin org.example.supresponder: aii-plugin-worker: event=ready") {
		t.Fatalf("the worker banner must land in the host log with the plugin prefix:\n%s", sink.String())
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
		return strings.Contains(sink.String(), "event=shutdown reason=stdin-eof")
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
	if _, found, _ := st.PluginKVGet(id, "greeting"); !found {
		t.Fatal("the row must exist while activated")
	}
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := st.PluginKVGet(id, "greeting"); found {
		t.Fatal("temp RING4 must die with the activation, supervised too")
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
	sink := &logSink{}
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.supcrash", "0.1.0", []string{"ping"}, fixtureWasm(t, "responder.wasm")))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{Log: log.New(sink, "", 0)}))
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Deactivate(context.Background())

	// Kill the child out from under the harness — the hard crash.
	pid := ap.SupervisedPid()
	if pid == 0 {
		t.Fatal("no child pid")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
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
	logText := sink.String()
	for _, want := range []string{"plugin org.example.supcrash: child exited", "restart 1/"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("host log must carry %q:\n%s", want, logText)
		}
	}
}

// A trapping guest is out-of-band telemetry too: the worker's fatal
// stderr line arrives prefixed, the tool call fails typed with the
// worker's documented exit taxonomy.
func TestSupervisedTrapSurfacesTaxonomy(t *testing.T) {
	sink := &logSink{}
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.suptrap", "0.1.0", []string{"ping"}, fixtureWasm(t, "trap.wasm")))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{Log: log.New(sink, "", 0)}))
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
		return strings.Contains(sink.String(), "plugin org.example.suptrap: aii-plugin-worker: event=fatal stage=invoke")
	})
}

// The runaway child: the tool context deadline kills the child (N-8)
// and the call reports the typed timeout; the supervisor restarts.
func TestSupervisedRunawayDeadlineKill(t *testing.T) {
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.suploop", "0.1.0", []string{"ping"}, fixtureWasm(t, "loop.wasm")))
	ap, err := Activate(context.Background(), pkg, reg, supervisedOpts(Options{}))
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Deactivate(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = reg.Execute(ctx, ap.ToolNames[0], nil)
	var terr *supervisor.InvokeTimeoutError
	if !errors.As(err, &terr) {
		t.Fatalf("want the typed deadline kill, got %v", err)
	}
	pollUntil(t, "restart after deadline kill", func() bool { return ap.Restarts() >= 1 })
}

// The native T3 lane, white-box: a verified native artifact (the
// supervisor suite's fakechild — a real executable speaking D1-1
// stdio) runs as the supervised child with the stdio endpoint value in
// its environment, answers invocations, and dies cleanly. Packaging/
// signing for T3 is packagefmt's proven ground; this pins the harness
// wiring: extraction 0700, env, digest re-verify hook.
func TestNativeT3LaneRunsVerifiedArtifact(t *testing.T) {
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
	sink := &logSink{}
	sup, dir, serr := startSupervisedNative(res, &res.Manifest.Variants[0], raw, nil,
		&Options{Log: log.New(sink, "", 0)})
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
		return strings.Contains(sink.String(), "socket=stdio:")
	})

	// The re-verify hook: tamper the extracted file, kill the child —
	// the respawn must refuse typed, never re-exec tampered bytes.
	// (Remove-then-write: overwriting a running executable is ETXTBSY
	// on Linux; a same-user adversary would swap the inode exactly
	// like this.)
	artifact := filepath.Join(dir, "artifact")
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	proc, _ := os.FindProcess(sup.Pid())
	_ = proc.Kill()
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
