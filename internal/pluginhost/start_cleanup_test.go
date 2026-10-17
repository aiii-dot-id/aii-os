//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// WHAT A REFUSED START GIVES BACK, AND WHAT A STARTED ONE IS GIVEN
// (T37 follow-up). Staged.Start stands an activation up in steps — the
// binding, the wall, the tools — and a refusal at any step must take back
// everything the steps before it made: the binding closed, the channel
// (the child) closed, the runtime root's pin released. And a native
// child is held to what its package declared: a profile's real
// readiness, a voice family's resident session with its audio pair.
// Each test here fails when one of those is missing.
//
// A native package's child is the supervisor suite's fakechild, run by
// path with no arguments; a declared model named "<mode>.fakechild-mode"
// chooses its mode, and it names that mode on its first line. Whether a
// refusal closed the child's channel is read from what the HOST relays
// of the child's stderr: a channel the host closes reaches the child as
// EOF on its stdin, and the child says "fakechild: eof-exit" before it
// exits. A pid would not do: on Linux the contained child runs in its
// own PID namespace, and the pid it can report is not one the host can
// ask about.

// modeModel is the declared model that chooses the fakechild's mode.
func modeModel(t *testing.T, mode string) *modelFixture {
	t.Helper()
	return newModelFixture(t, mode+".fakechild-mode", 64)
}

// nativePackageAs is nativePackageWithFiles with the manifest's top-level
// fields (a plugin_family) and, when given, the interface it declares. A
// capability_envelope among the top-level fields is declared on the
// variant too, as the SDK emits it.
func nativePackageAs(t *testing.T, id string, child []byte, root *packagetest.Role, extra map[string][]byte, top map[string]any, iface *packagetest.InterfaceSpec) string {
	t.Helper()
	vid := hostVariantID()
	caps, _ := top["capability_envelope"].([]string)
	if iface == nil {
		iface = &packagetest.InterfaceSpec{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}
	}
	files := map[string][]byte{
		iface.SchemaFile:             []byte(`{"interface":"` + iface.ID + `","v":1}`),
		"variants/" + vid + "/child": child,
	}
	for name, raw := range extra {
		files[name] = raw
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0", []packagetest.InterfaceSpec{*iface},
		[]packagetest.VariantSpec{{ID: vid, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + vid + "/child", Capabilities: caps}},
		files, top)
	spec := packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	return writePkg(t, spec)
}

// nativeRig is one native package's activation inputs: a runtime root
// it runs from (pinned in the store while it runs), a declared model that
// is its mode file, and, when profiled, an accelerator profile naming that
// model with the given startup allowance.
type nativeRig struct {
	pkg   string
	opts  *Options
	root  string
	store *plugstore.Writer
}

// pinned says an activation holds the rig's root: the store's collector
// would be refused it.
func (r *nativeRig) pinned(t *testing.T) bool {
	t.Helper()
	h, err := r.store.HoldExclusive(plugstore.Entry{Kind: plugstore.DirRoots, ID: filepath.Base(r.root)})
	if errors.Is(err, plugstore.ErrPinned) {
		return true
	}
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	return false
}

func newNativeRig(t *testing.T, id, mode string, startupMS int64, top map[string]any) *nativeRig {
	t.Helper()
	return newNativeRigWith(t, id, mode, startupMS, top, nil)
}

// newNativeRigWith is newNativeRig with the set's declared profile changed
// by declare before it is signed.
func newNativeRigWith(t *testing.T, id, mode string, startupMS int64, top map[string]any, declare func(*AcceleratorProfile)) *nativeRig {
	t.Helper()
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := sharedPlatformRoots(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	model := newModelFixture(t, "weights.bin", 64)
	if mode != "" {
		model = modeModel(t, mode)
	}
	models, err := json.Marshal([]ModelDecl{model.decl})
	if err != nil {
		t.Fatal(err)
	}
	profile := AcceleratorProfile{OS: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Backend: "cpu", Precision: "int8",
		Models: []string{model.decl.Name}, MemoryBytes: 1 << 20, SessionLimit: 1, Fallback: "none", StartupMS: msPtr(startupMS)}
	if declare != nil {
		declare(&profile)
	}
	accel, err := json.Marshal(map[string]AcceleratorProfile{hostVariantID(): profile})
	if err != nil {
		t.Fatal(err)
	}
	rt := newRuntimeFixtureWith(t, "# "+id+"\n")
	pkg := nativePackageAs(t, id, child, role, map[string][]byte{ModelsFile: models, AcceleratorFile: accel, RuntimesFile: runtimesJSON(t, rt)}, top, nil)
	opts := supervisedOpts(Options{Roots: roots})
	store := useStore(t, opts, byURL(map[string]ModelFetcher{model.decl.URL: model.fetch, rt.decl.URL: rt.fetch}))
	opts.NativePlaces = func() ([]*firewall.Rule, error) { return []*firewall.Rule{nativePlace(store.Dir)}, nil }
	root, _ := store.Root(plugstore.RootKey(hostVariantID(), rt.decl.InventorySHA256, "child", digestOf(child)))
	return &nativeRig{pkg: pkg, opts: opts, root: root, store: store}
}

// requireChannelClosed fails unless the host relayed, from the child of
// plugin id, first that it started in mode — so the observation is of
// this child, never vacuous — and then that it read EOF on its stdin:
// the host closed its channel.
func requireChannelClosed(t *testing.T, capture *logsink.Capture, id, mode, what string) {
	t.Helper()
	child := "plugin " + id + ": fakechild: "
	if !waitUntil(func() bool { return capture.Contains(child+"pid=") && capture.Contains("mode="+mode) }) {
		t.Fatalf("%s: the host never relayed the child's first line (mode %s); its log:\n%s", what, mode, capture.String())
	}
	if !waitUntil(func() bool { return capture.Contains(child + "eof-exit") }) {
		t.Fatalf("%s: the child never read EOF on its stdin — its channel was never closed; its log:\n%s", what, capture.String())
	}
}

// requireReadinessLogged fails unless the host logged plugin id's
// readiness once, as the fields it read from the child's line: exactly
// want, so a field the line did not carry is not in the log.
func requireReadinessLogged(t *testing.T, capture *logsink.Capture, id, want string) {
	t.Helper()
	prefix := "plugins.start: plugin " + id + ": native child ready: "
	var got []string
	for _, line := range strings.Split(capture.String(), "\n") {
		if i := strings.Index(line, prefix); i >= 0 {
			got = append(got, line[i+len(prefix):])
		}
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the readiness log for %s is %q, want one line %q; the log:\n%s", id, got, want, capture.String())
	}
}

// A PROFILE MAKES READINESS A REPORT, AND THE REPORT MUST BE REAL. A
// child whose event=ready line lacks the fields a profile requires is
// refused, typed — and the child it started is stopped and the runtime
// root's pin given back. Its control half: the same package whose child
// reports real readiness is admitted and keeps the report.
func TestANativeProfileHoldsItsChildToRealReadiness(t *testing.T) {
	t.Run("real readiness is admitted", func(t *testing.T) {
		const id = "org.example.readiness-real"
		rig := newNativeRig(t, id, "ready-real", 20000, nil)
		capture := logsink.CaptureForTest(t)
		ap, err := Activate(context.Background(), rig.pkg, newRegistry(t), rig.opts)
		if err != nil {
			t.Fatalf("a child reporting real readiness is admitted: %v", err)
		}
		if ap.Readiness == nil || !ap.Readiness.Real() || ap.Readiness.ModelsLoaded != 1 || ap.Readiness.Accelerator != "cpu" {
			_ = ap.Deactivate(context.Background())
			t.Fatalf("the activation keeps the child's real report: %+v", ap.Readiness)
		}
		// A line that names no kit (a plugin built before the kit wrote
		// one) is logged with none: nothing is filled in for it.
		requireReadinessLogged(t, capture, id, "models_loaded=1 accelerator=cpu probe_ms=3")
		if strings.Contains(capture.String(), "plugin "+id+": fakechild: eof-exit") {
			t.Fatal("a running child has not read EOF")
		}
		// THE OBSERVATION CAN SEE A CLOSE on this platform: an ordinary
		// deactivation closes the channel, and the child says so.
		if err := ap.Deactivate(context.Background()); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
		requireChannelClosed(t, capture, id, "ready-real", "a deactivation")
	})
	t.Run("the kit the line names is named in the readiness log", func(t *testing.T) {
		const id = "org.example.readiness-sdk"
		rig := newNativeRig(t, id, "ready-sdk", 20000, nil)
		capture := logsink.CaptureForTest(t)
		ap, err := Activate(context.Background(), rig.pkg, newRegistry(t), rig.opts)
		if err != nil {
			t.Fatalf("a child reporting real readiness and its kit is admitted: %v", err)
		}
		defer func() { _ = ap.Deactivate(context.Background()) }()
		const sdk = "v0.1.15-0.20261008000000-0123456789ab" // fakechild's readySDK
		if ap.Readiness == nil || !ap.Readiness.Real() || ap.Readiness.SDK != sdk {
			t.Fatalf("the activation keeps the kit the child named: %+v", ap.Readiness)
		}
		requireReadinessLogged(t, capture, id, "models_loaded=1 accelerator=cpu probe_ms=3 sdk="+sdk)
	})
	t.Run("a mark without the fields is refused and stopped", func(t *testing.T) {
		const id = "org.example.readiness-unreal"
		rig := newNativeRig(t, id, "ready-unreal", 20000, nil)
		capture := logsink.CaptureForTest(t)
		ap, err := Activate(context.Background(), rig.pkg, newRegistry(t), rig.opts)
		if ap != nil {
			_ = ap.Deactivate(context.Background())
			t.Fatal("a child that reported the mark without real readiness was admitted")
		}
		var notReady *ReadinessError
		if !errors.As(err, &notReady) || notReady.PluginID != id || !strings.Contains(notReady.Line, "event=ready") {
			t.Fatalf("refused as a ReadinessError naming the line it got: %v", err)
		}

		var unready pluginfacility.Unready
		if !errors.As(err, &unready) || !strings.Contains(unready.Unready(), "corrected release") {
			t.Fatalf("the refusal does not present itself as readiness: %v", err)
		}
		requireChannelClosed(t, capture, id, "ready-unreal", "a readiness refusal")
		if rig.pinned(t) {
			t.Fatal("a readiness refusal gives the runtime root's pin back")
		}
	})
}

// A NATIVE START THAT FAILS GIVES ITS RUNTIME ROOT BACK. The pin is
// taken before the child is started; a child that never reports ready
// within its allowance fails the start, and the pin must not outlive it.
func TestAFailedNativeStartReleasesItsRuntimeRoot(t *testing.T) {
	const id = "org.example.failed-native-start"
	rig := newNativeRig(t, id, "", 50, nil) // no mode file: the child never says event=ready
	ap, err := Activate(context.Background(), rig.pkg, newRegistry(t), rig.opts)
	if ap != nil {
		_ = ap.Deactivate(context.Background())
		t.Fatal("a child that never reports ready was activated")
	}
	var exit *supervisor.ChildExitError
	if !errors.As(err, &exit) || exit.Phase != "start" {
		t.Fatalf("the start fails on the readiness deadline: %v", err)
	}

	var late *supervisor.NotReadyError
	var unready pluginfacility.Unready
	if !errors.As(err, &late) || late.Allowance != 50*time.Millisecond || !errors.As(err, &unready) ||
		!strings.Contains(unready.Unready(), "plugins.resources."+id+".startup_timeout_ms") || !strings.Contains(unready.Unready(), "Try again") {
		t.Fatalf("the missed deadline is not a readiness failure with its remedy: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(rig.root, "child")); serr != nil {
		t.Fatalf("the root was published (and pinned) before the start: %v", serr)
	}
	if rig.pinned(t) {
		t.Fatal("a failed native start releases the runtime root's pin")
	}
}

// AN ACTIVATION REFUSED AFTER ITS WALL STOOD CLOSES ITS CHANNEL. The
// child is running and ready when the activation is refused — here a
// package claiming the channel-adapter family without the interface,
// checked once its operations exist — and the refusal must stop the
// child and release what it ran from.
func TestAnAbortedNativeActivationClosesItsChannel(t *testing.T) {
	const id = "org.example.aborted-native"
	rig := newNativeRig(t, id, "ready-real", 20000, map[string]any{"plugin_family": "channel_adapter"})
	capture := logsink.CaptureForTest(t)
	ap, err := Activate(context.Background(), rig.pkg, newRegistry(t), rig.opts)
	if ap != nil {
		_ = ap.Deactivate(context.Background())
		t.Fatal("a channel-adapter family without the channel interface was activated")
	}
	var contract *ChannelContractError
	if !errors.As(err, &contract) {
		t.Fatalf("refused by the channel contract, after the wall: %v", err)
	}
	requireChannelClosed(t, capture, id, "ready-real", "an aborted activation")
	if rig.pinned(t) {
		t.Fatal("an aborted activation releases the runtime root's pin")
	}
}

// A VOICE ENGINE IS A RESIDENT SESSION WITH ITS AUDIO PAIR, decided by
// the family its package declares. Activated through Start, its child
// runs on the session lane — the request/response lane refuses — and
// the session handle carries the spawn's audio pair.
func TestAVoiceFamilyEngineStartsAsAResidentSessionWithItsAudioPair(t *testing.T) {
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := sharedPlatformRoots(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.voice-family"
	model := modeModel(t, "session-audio")
	models, err := json.Marshal([]ModelDecl{model.decl})
	if err != nil {
		t.Fatal(err)
	}
	controls := []string{"speech.session.open", "speech.session.synthesize", "speech.session.cancel_synthesis",
		"speech.session.stop_playback", "speech.session.finish_input", "speech.session.close", "speech.session.status"}
	pkg := nativePackageAs(t, id, child, role, map[string][]byte{ModelsFile: models},
		map[string]any{"plugin_family": "voice_interface"},
		&packagetest.InterfaceSpec{ID: "speech.session", Version: 1, SchemaFile: "interfaces/speech.session.v1.schema.json", Methods: controls})
	opts := supervisedOpts(Options{Roots: roots})
	useStore(t, opts, model.fetch)
	ap, err := ActivateShadow(context.Background(), pkg, newRegistry(t), opts)
	if err != nil {
		t.Fatalf("activate the voice engine: %v", err)
	}
	defer ap.CloseQuiet(context.Background())
	v := ap.Voice.Load()
	if v == nil {
		t.Fatal("a voice-family activation binds its session lane")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := ap.sup.Invoke(ctx, []byte(`{"jsonrpc":"2.0","id":"h1","method":"invoke.call","params":{}}`)); err == nil || !strings.Contains(err.Error(), "resident session") {
		t.Fatalf("the child runs as a resident session, so the invocation lane refuses: %v", err)
	}
	if _, _, ok := ap.sup.AudioPair(); !ok {
		t.Fatal("the voice engine's spawn has its audio pair")
	}
	v.mu.Lock()
	paired := v.pair != nil
	v.mu.Unlock()
	if !paired {
		t.Fatal("the session handle carries the spawn's audio pair")
	}
}

func TestANativeCallPastItsDeadlineLeavesItsActivationRetirable(t *testing.T) {
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := sharedPlatformRoots(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.nested-deaf"
	model := modeModel(t, "hostcall-deaf")
	models, err := json.Marshal([]ModelDecl{model.decl})
	if err != nil {
		t.Fatal(err)
	}
	pkg := nativePackageAs(t, id, child, role, map[string][]byte{ModelsFile: models},
		map[string]any{"capability_envelope": []string{"ring4.kv"}}, nil)
	st := newBrokerStore(t)
	// The value the child asks for (fakechild: kv.get "probe").
	if err := st.PluginKVPut(store.PluginScope{PluginID: id}, "probe", strings.Repeat("x", 600<<10), false, 4, 1<<20); err != nil {
		t.Fatal(err)
	}
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
	if err != nil {
		t.Fatal(err)
	}
	opts := supervisedOpts(Options{Roots: roots, Broker: h})
	useStore(t, opts, model.fetch)
	reg := newRegistry(t)
	ap, err := Activate(context.Background(), pkg, reg, opts)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	name, err := toolName(id, "ping")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	returned := make(chan error, 1)
	go func() {
		res, err := reg.Execute(ctx, name, nil)
		if err == nil && res.Error == "" {
			err = errors.New("the call succeeded: " + res.Output)
		}
		returned <- err
	}()
	select {
	case err := <-returned:
		var cut *supervisor.InvokeTimeoutError
		if !errors.As(err, &cut) {
			t.Fatalf("the call must end as the host's cut at its deadline: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("a call with a 500ms deadline is still in flight after %v", time.Since(start))
	}
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatalf("after a call past its deadline the activation must retire: %v", err)
	}
}
