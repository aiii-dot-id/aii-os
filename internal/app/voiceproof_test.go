package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// sessionControls are the resident lane's seven controls — what a voice
// package declares as its methods (the SDK's DeclareSession).
var sessionControls = []string{
	"speech.session.open", "speech.session.synthesize", "speech.session.cancel_synthesis",
	"speech.session.stop_playback", "speech.session.finish_input", "speech.session.close", "speech.session.status",
}

// sdkVoiceEngine builds the kit's voice-skel engine — an SDK-built
// resident speech engine with no models and no audio — from the checkout
// AII_PLUGIN_SDK_DIR names, else the aii-plugin-sdk checkout beside this
// repository's, and returns its bytes. No other location is guessed: a
// host whose checkout sits elsewhere names it. Without the checkout,
// or on a host that cannot establish the native sandbox, the proof
// cannot run and says so; the gate on the build host refuses that skip.
func sdkVoiceEngine(t *testing.T) []byte {
	t.Helper()
	kit := os.Getenv("AII_PLUGIN_SDK_DIR")
	if kit == "" {
		sibling := filepath.Join("..", "..", "..", "aii-plugin-sdk")
		if _, err := os.Stat(filepath.Join(sibling, "examples", "voice-skel", "main.go")); err == nil {
			kit, _ = filepath.Abs(sibling)
		}
	}
	if kit == "" {
		t.Skip("no aii-plugin-sdk checkout (set AII_PLUGIN_SDK_DIR): the SDK-built engine cannot be built here")
	}
	if runtime.GOOS == "linux" {
		if out, err := exec.Command("bwrap", "--unshare-all", "--die-with-parent",
			"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--", "/bin/true").CombinedOutput(); err != nil {
			t.Skipf("this host cannot establish the bwrap sandbox — user namespaces, or a private /proc inside them, are unavailable (capability absent, not product failure): %v — %s", err, bytes.TrimSpace(out))
		}
	}
	bin := filepath.Join(t.TempDir(), "voice-skel")
	build := exec.Command("go", "build", "-o", bin, "./examples/voice-skel/")
	build.Dir = kit
	build.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false", "CGO_ENABLED=0", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the SDK engine from %s: %v\n%s", kit, err, out)
	}
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// voicePackage builds one release of the engine as a voice_interface
// package for THIS host and signs it at the platform tier.
func voicePackage(t *testing.T, dir, id, ver string, engine []byte, root *packagetest.Role) string {
	t.Helper()
	return voicePackageWith(t, dir, id, ver, engine, root, nil)
}

// voicePackageWith is voicePackage carrying more install-root members.
func voicePackageWith(t *testing.T, dir, id, ver string, engine []byte, root *packagetest.Role, extra map[string][]byte) string {
	t.Helper()
	vid := packagefmt.HostPlatform() + "-" + packagefmt.HostArch() + "-native"
	files := map[string][]byte{
		"interfaces/speech.session.v1.schema.json": []byte(`{"interface":"speech.session","v":1}`),
		"variants/" + vid + "/plugin":              engine,
	}
	for name, raw := range extra {
		files[name] = raw
	}
	manifest := packagetest.BuildManifestJSON(id, ver,
		[]packagetest.InterfaceSpec{{ID: "speech.session", Version: 1, SchemaFile: "interfaces/speech.session.v1.schema.json", Methods: sessionControls}},
		[]packagetest.VariantSpec{{ID: vid, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + vid + "/plugin"}},
		files, map[string]any{"plugin_family": "voice_interface"})
	spec := packagetest.PackageSpec{Root: id + "-" + ver, Manifest: manifest, InstallFiles: files}
	if err := root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+"-"+ver+".aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// observed collects a session's critical events as the application's
// consumer.
type observed struct {
	mu     sync.Mutex
	events []pluginhost.Event
}

func observe(v *pluginhost.VoiceSession) *observed {
	o := &observed{}
	go func() {
		for ev := range v.Observe() {
			o.mu.Lock()
			o.events = append(o.events, ev)
			o.mu.Unlock()
		}
	}()
	return o
}

func (o *observed) count(typ string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, ev := range o.events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// countFor counts the events of one type that name one synthesis.
func (o *observed) countFor(typ, synthesisID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, ev := range o.events {
		if ev.Type != typ {
			continue
		}
		var body struct {
			SynthesisID string `json:"synthesis_id"`
		}
		_ = json.Unmarshal(ev.Raw, &body)
		if body.SynthesisID == synthesisID {
			n++
		}
	}
	return n
}

func (o *observed) texts(typ string) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []string
	for _, ev := range o.events {
		if ev.Type == typ {
			var body struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(ev.Raw, &body)
			out = append(out, body.Text)
		}
	}
	return out
}

func voiceWait(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("within %v: %s", within, what)
}

func fired(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// V-P: the resident path proven with an SDK-BUILT, T3-SIGNED package.
// The kit's voice-skel engine — built from the sibling checkout, signed
// at the platform tier with a test root, activated by the application on
// the real native lane — is driven through sessions while the
// application updates it, drains it, saturates it, loses it, and shuts
// down. No speech model is involved; the engine keeps the contract the
// models would.
func TestSDKBuiltVoiceEngineSurvivesUpdateDrainSaturationLossAndShutdown(t *testing.T) {
	engine := sdkVoiceEngine(t)
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "VoiceProof", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	// The platform root this application pins, and its snapshot.
	root, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
	if err != nil {
		t.Fatal(err)
	}
	rootRaw, _ := json.Marshal(root.Env)
	rootPath := filepath.Join(dir, "platform-root.json")
	if err := os.WriteFile(rootPath, rootRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(dir, "trust")
	if err := os.MkdirAll(trust, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), status, 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "com.example.voice-skel"
	v1 := voicePackage(t, dir, id, "0.1.0", engine, root)
	v2 := voicePackage(t, dir, id, "0.2.0", engine, root)
	v3 := voicePackage(t, dir, id, "0.3.0", engine, root)
	installPluginDir(t, dir, id, v1)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T3", PlatformRoot: rootPath, WorkerBinary: exe},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stopApp(t, app)
		}
	}()
	ctx := context.Background()
	retiring := func() int {
		app.pluginMu.Lock()
		defer app.pluginMu.Unlock()
		return len(app.retiring)
	}

	// ---- 1. Activated at T3 on the native lane; a session opens. ----
	var ap1 *pluginhost.ActivePlugin
	voiceWait(t, "the engine activates with a session surface", 20*time.Second, func() bool {
		ap1 = app.activePlugin(id)
		return ap1 != nil && ap1.Voice.Load() != nil
	})
	if v, n := activeRelease(app, id); v != "0.1.0" || n != 1 {
		t.Fatalf("running release 0.1.0 alone, got %q n=%d", v, n)
	}
	// The session is opened WITH AUDIO on the application's plane: a
	// live source (a microphone stand-in) and a capture sink, bound
	// exclusively to s1 and carried to the SDK-built engine on its
	// inherited pair; the engine mirrors every frame back.
	plane := app.AudioPlane()
	mic := &liveSource{f: mono16k, every: 2 * time.Millisecond, total: 16000 * 600}
	spk := audio.NewCaptureSink(mono16k)
	if err := plane.Register(&audio.Endpoint{ID: "mic", Label: "live", Source: mic}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "spk", Label: "capture", Sink: spk}); err != nil {
		t.Fatal(err)
	}
	b1, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ap1.Voice.Load().OpenWithAudio(ctx, "s1", b1, map[string]any{"test_close_delay_ms": 1200, "test_synth_ms": 60000}); err != nil {
		t.Fatalf("open with audio: %v", err)
	}
	o1 := observe(ap1.Voice.Load())
	voiceWait(t, "the engine reports the session ready", 5*time.Second, func() bool { return o1.count("session_ready") == 1 })
	_, pump1 := ap1.Voice.Load().Audio()
	voiceWait(t, "session audio flows through the SDK-built engine and back", 10*time.Second, func() bool { return pump1.Received() >= 3200 })
	if err := ap1.Voice.Load().Synthesize(ctx, "g1", "the reply the operator is hearing"); err != nil {
		t.Fatal(err)
	}
	if _, err := ap1.Voice.Load().Status(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ap1.Voice.Load().Label(); got != "Speaking" {
		t.Fatalf("label while playback plays = %q, want Speaking", got)
	}

	// ---- 2. The update lands beneath the open session: the candidate
	// serves, the predecessor is pinned and still owned. ----
	swapPackage(t, dir, id, v2)
	app.rescanPlugins(ctx)
	awaitPluginVersion(t, app, id, "0.2.0")
	if v, n := activeRelease(app, id); v != "0.2.0" || n != 1 {
		t.Fatalf("exactly the new release is active, got %q n=%d", v, n)
	}
	// THE SUCCESSOR SERVES BEFORE THE PREDECESSOR RETIRES, deliberately:
	// the id must never look like it is serving nothing. So the
	// predecessor being handed over as pinned is the step AFTER the
	// redirect, not the same instant, and this waits for it.
	voiceWait(t, "the pinned predecessor to be handed over", 10*time.Second, func() bool { return retiring() == 1 })
	if !ap1.Pinned() || retiring() != 1 {
		t.Fatalf("the predecessor with an open session is pinned and retiring: pinned=%v retiring=%d", ap1.Pinned(), retiring())
	}
	if got := ap1.Voice.Load().Label(); got != "Speaking" {
		t.Fatalf("the predecessor still speaks after the update: %q", got)
	}
	// Endpoint exclusivity: the candidate cannot seize the live
	// microphone and speaker while the predecessor's session holds them.
	if _, err := plane.Bind("s2", "mic", "spk", true); !errors.Is(err, audio.ErrEndpointBusy) {
		t.Fatalf("a shadow must never seize a live session's endpoints: %v", err)
	}

	// ---- 3. Drain: the close is ACKNOWLEDGED while playback continues;
	// the pin holds; the engine's session_end releases it and the
	// application stops the predecessor. ----
	// The operator stops speaking: the cutoff is where the delivery
	// stands, exact, and the engine receives no sample past it.
	cutoff, err := ap1.Voice.Load().FinishInputNow(ctx, "in1")
	if err != nil {
		t.Fatal(err)
	}
	// THE SPEAKER CARRIES TWO STREAMS: the input's echo and the reply's
	// own audio, each under an id the engine chose, never shared. The
	// echo is the one that ends at the cutoff.
	echo := func() (uint32, bool) {
		for _, st := range spk.Streams() {
			if spk.StreamEnd(st) == cutoff {
				return st, true
			}
		}
		return 0, false
	}
	voiceWait(t, "the input tail through the cutoff comes back from the engine", 10*time.Second, func() bool { _, ok := echo(); return ok })
	echoStream, _ := echo()
	if got := int64(len(spk.StreamPCM(echoStream)) / 2); got != cutoff {
		t.Fatalf("the engine received %d samples for a cutoff at %d", got, cutoff)
	}
	if streams := spk.Streams(); len(streams) < 2 {
		t.Fatalf("the reply's audio must be its own output stream beside the echo, got streams %v", streams)
	} else {
		for _, st := range streams {
			if st != echoStream && len(spk.StreamPCM(st)) == 0 {
				t.Fatalf("the reply's stream %d carried no audio", st)
			}
		}
	}
	if err := ap1.Voice.Load().Close(ctx, "drain", "update"); err != nil {
		t.Fatalf("drain close: %v", err)
	}
	if _, err := ap1.Voice.Load().Status(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ap1.Voice.Load().Label(); got != "Draining" {
		t.Fatalf("label after close admission with playback playing = %q, want Draining", got)
	}
	if fired(ap1.PinReleased(), 300*time.Millisecond) || !ap1.Pinned() || retiring() != 1 {
		t.Fatal("a close that was merely admitted must not release the pin or stop the predecessor")
	}
	// The operator interrupts; the synthesis resolves cancelled and the
	// drain can end — after the engine's delay, on ITS word.
	if err := ap1.Voice.Load().Interrupt(ctx, "g1", "operator"); err != nil {
		t.Fatal(err)
	}
	voiceWait(t, "the synthesis resolves as cancelled", 5*time.Second, func() bool { return o1.countFor("synthesis_cancelled", "g1") == 1 })
	if o1.countFor("synthesis_end", "g1") != 0 {
		t.Fatal("a cancelled synthesis must not also end")
	}
	if fired(ap1.Voice.Load().Done(), 500*time.Millisecond) {
		t.Fatal("the session ended before the engine's drain delay — closure was invented")
	}
	// THE DRAIN WAITS FOR THE PAGE'S RECEIPT:
	// no session_end until the fenced reply's terminal playback report —
	// not after the engine's delay, not on its synthesis END. This proof's
	// speaker is the capture sink, so the proof files the report a page
	// would: what the sink holds of the reply's stream is what its client
	// rendered — client evidence, never acoustic proof.
	if fired(ap1.Voice.Load().Done(), 1500*time.Millisecond) {
		t.Fatal("the session ended without the reply's playback receipt — a drain without render evidence")
	}
	reply := func() (uint32, bool) {
		for _, st := range spk.Streams() {
			if st != echoStream && spk.StreamEnd(st) >= 0 {
				return st, true
			}
		}
		return 0, false
	}
	voiceWait(t, "the fenced reply's stream ends at the sink", 5*time.Second, func() bool { _, ok := reply(); return ok })
	replyStream, _ := reply()
	// EVERY STREAM THE ENGINE INTRODUCED IS ONE THE PAGE REPORTS — the
	// echo too. It was named on the control lane like the reply, so it is
	// a generation with a receipt owed, and a page that played it files
	// one: drained, every sample rendered. The engine used to refuse that
	// report (it named the stream and kept no record for it), and a drain
	// cannot honestly end while a stream it delivered is unaccounted for.
	if err := ap1.Voice.Load().PlaybackReportFor(ctx, "s1", pluginhost.PlaybackReport{Stream: echoStream, Rendered: int64(len(spk.StreamPCM(echoStream)) / 2), Rate: 16000, Channels: 1, Terminal: true, Outcome: "drained"}); err != nil {
		t.Fatalf("the echo's playback receipt: %v", err)
	}
	if fired(ap1.Voice.Load().Done(), 300*time.Millisecond) {
		t.Fatal("the session ended on the echo's receipt alone — the reply's is still owed")
	}
	if err := ap1.Voice.Load().PlaybackReportFor(ctx, "s1", pluginhost.PlaybackReport{Stream: replyStream, Rendered: int64(len(spk.StreamPCM(replyStream)) / 2), Rate: 16000, Channels: 1, Terminal: true, Outcome: "stopped"}); err != nil {
		t.Fatalf("the reply's playback receipt: %v", err)
	}
	voiceWait(t, "the engine's playback observation of the receipt", 5*time.Second, func() bool { return o1.countFor("playback_observation", "g1") == 1 })
	if !fired(ap1.PinReleased(), 5*time.Second) {
		t.Fatal("the engine's session_end must release the pin")
	}
	if ap1.Pinned() || ap1.Voice.Load().Label() != "Closed" {
		t.Fatalf("after session_end: pinned=%v label=%q", ap1.Pinned(), ap1.Voice.Load().Label())
	}
	// The endpoints went back with the engine's word, and are free.
	if !fired(b1.Released(), time.Second) {
		t.Fatal("session_end must give the endpoints back")
	}
	if free, err := plane.Bind("s2", "mic", "spk", true); err != nil {
		t.Fatalf("after release the endpoints are free: %v", err)
	} else {
		free.Release()
	}

	// ---- 4. Saturation on the candidate, at a forced boundary: the
	// application drains transcripts and deliberately never the
	// telemetry stream, so the burst must overflow it — telemetry is
	// shed and counted, every transcript arrives in order, nothing
	// faults, and the session stays whole. ----
	ap2 := app.activePlugin(id)
	if ap2 == nil || ap2 == ap1 || ap2.Voice.Load() == nil {
		t.Fatal("the candidate carries its own session surface")
	}
	if err := ap2.Voice.Load().Open(ctx, "s2", map[string]any{"test_telemetry_burst": 4000, "test_critical_burst": 25}); err != nil {
		t.Fatalf("open on the candidate: %v", err)
	}
	o2 := observe(ap2.Voice.Load())
	voiceWait(t, "every critical event reaches the application", 10*time.Second, func() bool { return o2.count("transcript_final") == 25 })
	if ap2.Voice.Load().TelemetryDropped() == 0 {
		t.Fatal("the telemetry stream was never drained: the burst must have overflowed it and been counted")
	}
	for i, text := range o2.texts("transcript_final") {
		if want := "utterance " + strconv.Itoa(i+1); text != want {
			t.Fatalf("transcript %d = %q, want %q — critical events arrive complete and in order", i, text, want)
		}
	}
	if ap2.Voice.Load().Faulted() || !ap2.Voice.Load().IsOpen() {
		t.Fatalf("a telemetry burst must not fault or close the session: faulted=%v (%s) open=%v", ap2.Voice.Load().Faulted(), ap2.Voice.Load().FaultReason(), ap2.Voice.Load().IsOpen())
	}
	if _, err := ap2.Voice.Load().Status(ctx); err != nil {
		t.Fatalf("status after the burst: %v", err)
	}
	t.Logf("saturation: %d telemetry events dropped at the undrained telemetry stream, %d shed by the transport, %d sequence gaps seen, 25/25 transcripts delivered", ap2.Voice.Load().TelemetryDropped(), ap2.Voice.Load().Dropped(), ap2.Voice.Load().Gaps())
	if err := ap2.Voice.Load().Close(ctx, "abort", "done"); err != nil {
		t.Fatal(err)
	}
	if !fired(ap2.Voice.Load().Done(), 5*time.Second) || ap2.Voice.Load().Label() != "Closed" {
		t.Fatalf("an abort ends with the session's cancellation: label=%q", ap2.Voice.Load().Label())
	}
	voiceWait(t, "the session's cancellation reaches the observer", 5*time.Second, func() bool { return o2.count("cancellation") == 1 })

	// ---- 5. Transport loss: the child dies with a session open. The
	// session is FAILED, a new open on the dead handle is refused, and
	// a rebind to the restarted child serves again. ----
	deadVoice := ap2.Voice.Load()
	if err := deadVoice.Open(ctx, "s3", map[string]any{"test_die_after_ms": 200}); err != nil {
		t.Fatalf("open before the death: %v", err)
	}
	if !fired(deadVoice.Done(), 10*time.Second) {
		t.Fatal("the application must learn the session died")
	}
	if !deadVoice.Faulted() || deadVoice.Label() != "Failed" {
		t.Fatalf("a dead transport is a FAILED session: faulted=%v label=%q", deadVoice.Faulted(), deadVoice.Label())
	}
	if err := deadVoice.Open(ctx, "s4", nil); err == nil {
		t.Fatal("a dead handle must refuse a new session")
	}
	voiceWait(t, "the restarted child automatically serves", 15*time.Second, func() bool { v := ap2.Voice.Load(); return v != deadVoice && v.Alive() })
	if err := ap2.Voice.Load().Open(ctx, "s4", nil); err != nil {
		t.Fatalf("open after rebind: %v", err)
	}
	if _, err := ap2.Voice.Load().Status(ctx); err != nil {
		t.Fatalf("status after rebind: %v", err)
	}
	if err := ap2.Voice.Load().Close(ctx, "abort", "done"); err != nil {
		t.Fatal(err)
	}
	if !fired(ap2.Voice.Load().Done(), 5*time.Second) {
		t.Fatal("the rebound session closes on the engine's word")
	}

	// ---- 6. An UNTRUSTED predecessor: its critical stream overflows
	// (the application never drains it), so the host can no longer trust
	// the session — but that is not the engine's word. The pin holds; the
	// application asks the engine to abort; the engine's cancellation
	// releases the pin and the predecessor is stopped. ----
	if err := ap2.Voice.Load().Open(ctx, "s5", map[string]any{"test_critical_burst": 1100}); err != nil {
		t.Fatal(err)
	}
	voiceWait(t, "the undrained critical stream overflows into a host-side fault", 10*time.Second, func() bool { return ap2.Voice.Load().Faulted() })
	if !ap2.Voice.Load().IsOpen() || fired(ap2.PinReleased(), 200*time.Millisecond) {
		t.Fatal("a host-side fault must not close the session or release its pin")
	}
	swapPackage(t, dir, id, v3)
	app.rescanPlugins(ctx)
	awaitPluginVersion(t, app, id, "0.3.0")
	if v, _ := activeRelease(app, id); v != "0.3.0" {
		t.Fatalf("release after the second update = %q", v)
	}
	// NOT "retiring() == 0" ALONE: zero is also what it reads BEFORE the
	// predecessor has been handed over at all, and the successor is
	// published as serving before that handover. Waiting on the pin
	// itself waits for the thing the step is about.
	voiceWait(t, "the application resolves the untrusted predecessor through the engine and stops it", 20*time.Second,
		func() bool { return !ap2.Pinned() && retiring() == 0 })
	if ap2.Pinned() || ap2.Voice.Load().Label() != "Closed" && ap2.Voice.Load().Label() != "Failed" {
		t.Fatalf("after the abort: pinned=%v label=%q", ap2.Pinned(), ap2.Voice.Load().Label())
	}

	// ---- 7. The operator's act through the application: a session
	// opened on the active engine bound to the plane's endpoints, the
	// engine's final transcript recorded through the heard path, the
	// release finishing the input at the exact sample, the engine's
	// word ending it and the endpoints returning. This release's event
	// stream has no other consumer: the application's observer is the
	// one reader, as in production. ----
	ap3 := app.activePlugin(id)
	if ap3 == nil || ap3.Voice.Load() == nil {
		t.Fatal("the third release carries its own session surface")
	}
	if !app.VoiceEngine() {
		t.Fatal("an active engine is a speech engine")
	}
	opMark := len(spk.Frames()) // what the speaker held before this session spoke
	vh, err := app.OpenVoiceSession(ctx, "mic", "spk", "meeting", nil)
	if err != nil {
		t.Fatalf("open a voice session: %v", err)
	}
	if got := vh.ID(); !strings.HasPrefix(got, "vs-") || len(got) != 35 {
		t.Fatalf("durable voice reference must not be a process-local counter: %q", got)
	}
	if in, out := ap3.Voice.Load().EngineFormats(); in != mono16k || out != mono16k {
		t.Fatalf("the SDK-built engine answered the open with the formats it speaks: %s / %s", in, out)
	}
	_, opPump := ap3.Voice.Load().Audio()
	voiceWait(t, "the operator's audio flows through the engine", 10*time.Second, func() bool { return opPump.Received() >= 3200 })
	if err := vh.Finish(ctx, opPump.Delivered()); err != nil {
		t.Fatalf("finish: %v", err)
	}
	turnsMentioning := func(s string) int {
		turns, _ := app.store.RecentTurns(10)
		n := 0
		for _, tr := range turns {
			if raw, _ := json.Marshal(tr); strings.Contains(string(raw), s) {
				n++
			}
		}
		return n
	}
	voiceWait(t, "the engine's final transcript is recorded", 10*time.Second, func() bool { return turnsMentioning("the tail, finalized") > 0 })
	// THE PAGE'S RECEIPT FOR WHAT IT PLAYED. The engine introduced the
	// echo's stream on the control lane, so it is a generation whose
	// terminal playback report the drain waits for — and the page files
	// one for every stream it rendered. This proof's speaker is the
	// capture sink, so the proof files the report a page would, through
	// the application's own handle: client evidence, never acoustic proof.
	pageReceipt := func(what string, mark int, pump *audio.Pump, report func(context.Context, dashboard.PlaybackReport) error) {
		t.Helper()
		var echoOut uint32
		voiceWait(t, what, 10*time.Second, func() bool {
			frames := spk.Frames()
			if n := len(frames); n > mark && frames[n-1].Kind == audio.KindEnd {
				echoOut = frames[n-1].Stream
				return true
			}
			return false
		})
		played, _ := pump.OutputStream(echoOut)
		if err := report(ctx, dashboard.PlaybackReport{Stream: echoOut, Rendered: played.Written, Rate: 16000, Channels: 1, Terminal: true, Outcome: "drained"}); err != nil {
			t.Fatalf("the page's receipt for the echo it played: %v", err)
		}
	}
	pageReceipt("the echo of the operator's audio ends at the speaker", opMark, opPump, vh.PlaybackReport)
	// THE HOST ORDERS THE DRAIN (voice-platform item 3): after Finish, the
	// final transcript's resolution — recorded, no reply, in meeting mode —
	// is what requests the drain-close. The application never drains ahead
	// of it; nothing else ends this session, so the engine's word arriving
	// here IS the proof the host drained after the final resolved.
	if !fired(vh.Done(), 5*time.Second) || !fired(vh.Released(), 5*time.Second) {
		t.Fatal("the host's drain after the final transcript, then the engine's word, must end the session and return the endpoints")
	}

	// ---- 7b. TYPED WORDS, THE ENGINE'S VOICE. Earbuds with a speech engine:
	// a session opened on the page's SPEAKER ALONE — no microphone bound, no
	// input named, the engine told so in the contract's words and
	// confirming it — and a reply to words nobody spoke is synthesized
	// through it, heard whole at the speaker, receipted by the page, and
	// the drain close that follows needs no input boundary at all. ----
	outMark := len(spk.Frames())
	vo, err := app.OpenVoiceSession(ctx, "", "spk", "output", nil)
	if err != nil {
		t.Fatalf("open an output-only voice session: %v", err)
	}
	if !ap3.Voice.Load().OutputOnly() {
		t.Fatal("the driver must know this session has no input")
	}
	for _, ep := range plane.Endpoints() {
		if ep.ID == "mic" && ep.BoundTo != "" {
			t.Fatalf("an output-only session holds the microphone: bound to %q", ep.BoundTo)
		}
	}
	_, outPump := ap3.Voice.Load().Audio()
	app.settleVoice(ctx, "a reply to words that were typed")
	if !app.voiceReplyShown.Swap(false) {
		t.Fatal("the typed reply was not taken by the engine's voice")
	}
	pageReceipt("the typed reply ends at the speaker", outMark, outPump, vo.PlaybackReport)
	if got := len(spk.Frames()) - outMark; got < 2 {
		t.Fatalf("the typed reply carried no audio to the speaker: %d frame(s)", got)
	}
	if err := vo.Close(ctx, "drain"); err != nil {
		t.Fatalf("a drain close with no input boundary: %v", err)
	}
	if !fired(vo.Done(), 10*time.Second) || !fired(vo.Released(), 5*time.Second) {
		t.Fatal("the engine's word must end the output-only session and return the speaker")
	}
	if ap3.Voice.Load().Faulted() {
		t.Fatalf("nothing about an output-only session is a fault: %s", ap3.Voice.Load().FaultReason())
	}

	callIn := &liveSource{f: mono16k, every: 2 * time.Millisecond, total: 16000 * 600}
	if err := plane.Register(&audio.Endpoint{ID: "call-in", Label: "a call", Remote: true, Source: callIn}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "call-out", Label: "a call", Remote: true, Sink: audio.NewCaptureSink(mono16k)}); err != nil {
		t.Fatal(err)
	}
	remote, err := app.OpenVoiceSession(ctx, "call-in", "call-out", "meeting", nil)
	if err != nil {
		t.Fatalf("in NORMAL a remote endpoint is legitimate: %v", err)
	}
	recorded := turnsMentioning("the tail, finalized")
	app.enterSafe("the proof's SAFE")
	if !fired(remote.Done(), 10*time.Second) || !fired(remote.Released(), 5*time.Second) {
		t.Fatal("SAFE beginning mid-session must abort the session on the remote endpoint and return its endpoints")
	}
	if _, err := app.OpenVoiceSession(ctx, "call-in", "call-out", "meeting", nil); !errors.Is(err, audio.ErrSafe) {
		t.Fatalf("under SAFE a remote endpoint is refused at entry: %v", err)
	}
	safeMark := len(spk.Frames())
	safe, err := app.OpenVoiceSession(ctx, "mic", "spk", "meeting", nil)
	if err != nil {
		t.Fatalf("under SAFE the contained engine on the host's endpoints is allowed: %v", err)
	}
	_, safePump := ap3.Voice.Load().Audio()
	voiceWait(t, "audio flows through the contained engine under SAFE", 10*time.Second, func() bool { return safePump.Received() >= 3200 })
	if err := safe.Finish(ctx, safePump.Delivered()); err != nil {
		t.Fatalf("finish under SAFE: %v", err)
	}
	voiceWait(t, "the engine's transcript reaches the observer and is dropped under SAFE", 10*time.Second, func() bool { return app.voiceSafeDropped.Load() > 0 })
	if n := turnsMentioning("the tail, finalized"); n != recorded {
		t.Fatalf("under SAFE the engine's transcript is recorded by no one: %d turns, was %d", n, recorded)
	}
	// The page under SAFE still says what it played; the receipt is
	// playback evidence, not a record of what was heard.
	pageReceipt("the echo under SAFE ends at the speaker", safeMark, safePump, safe.PlaybackReport)
	// Under SAFE the final is withheld from every record — and still resolves
	// the Finish it answers: the host orders the drain with that explicit
	// outcome, and the engine's word ends the session.
	if !fired(safe.Done(), 10*time.Second) || !fired(safe.Released(), 5*time.Second) {
		t.Fatal("the host's drain after the SAFE-withheld final, then the engine's word, must end the session and return the endpoints")
	}

	// ---- 9. Shutdown with a session open: Stop ends every lane. ----
	// Simulate the process-local ordinal restarting. The newly opened session
	// must not alias the earlier voice annotation, even with the same ordinal.
	app.voiceSeq.Store(0)
	vh2, err := app.OpenVoiceSession(ctx, "mic", "spk", "meeting", nil)
	if err != nil {
		t.Fatalf("open a second voice session: %v", err)
	}
	if vh2.ID() == vh.ID() {
		t.Fatal("reset ordinal reused a durable voice reference")
	}
	if !ap3.Pinned() {
		t.Fatal("an open session pins the running release")
	}
	stopApp(t, app)
	stopped = true
	if retiring() != 0 {
		t.Fatal("Stop must take every retiring predecessor")
	}
	if !fired(vh2.Done(), 5*time.Second) {
		t.Fatal("after Stop every session lane has ended")
	}
}

var mono16k = audio.Format{Rate: 16000, Channels: 1}

// liveSource is a microphone stand-in: one 20 ms frame every tick under
// its own sample clock.
type liveSource struct {
	f     audio.Format
	next  int64
	seq   uint32
	every time.Duration
	total int64
	ended bool
}

func (s *liveSource) Format() audio.Format { return s.f }
func (s *liveSource) Read(ctx context.Context) (audio.Frame, error) {
	if s.ended {
		return audio.Frame{}, io.EOF
	}
	if s.next >= s.total {
		s.ended = true
		s.seq++
		return audio.Frame{Kind: audio.KindEnd, Stream: 1, Seq: s.seq, Start: s.next}, nil
	}
	select {
	case <-time.After(s.every):
	case <-ctx.Done():
		return audio.Frame{}, ctx.Err()
	}
	s.seq++
	pcm := make([]byte, 640)
	for i := range pcm {
		pcm[i] = byte(i)
	}
	fr := audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: s.seq, Start: s.next, PCM: pcm}
	s.next += 320
	return fr, nil
}
