package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// skelTone is 0.3 s of a quiet tone at 16 kHz, mono, as a WAV.
func skelTone() []byte {
	const n = 4800
	pcm := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(math.Round(3000*math.Sin(2*math.Pi*440*float64(i)/16000)))))
	}
	return append(audio.WAVHeader(audio.Format{Rate: 16000, Channels: 1}, uint32(len(pcm))), pcm...)
}

// checkedEngineApp boots an application with the engine installed,
// carrying validation and its recording, and returns it. settled waits for
// its plugins to come up, as most tests do; a test whose subject is a
// plugin that does not come up boots without.
func checkedEngineApp(t *testing.T, id, validation string, settled bool) *App {
	t.Helper()
	engine := sdkVoiceEngine(t)
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "SessionChecks", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
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
	pkg := voicePackageWith(t, dir, id, "0.1.0", engine, root, map[string][]byte{
		packagefmt.ValidationFile: []byte(validation),
		"checks/tone.wav":         skelTone(),
	})
	installPluginDir(t, dir, id, pkg)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := New(&Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T3", PlatformRoot: rootPath, WorkerBinary: exe},
		Agency:     defaultConfig().Agency,
	})
	start := startLiveForTest
	if !settled {
		start = func(a *App) error {
			_ = store.SeedForTest(a.configSnapshot().Identity.DBPath)
			return startLiveUnsettled(a)
		}
	}
	if err := start(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })
	return app
}

// instanceOf is the facility's word on one plugin id.
func instanceOf(a *App, id string) *pluginfacility.InstanceView {
	a.pluginMu.Lock()
	f := a.facility
	a.pluginMu.Unlock()
	if f == nil {
		return nil
	}
	for _, v := range f.Snapshot().Instances {
		if v.ID == id {
			return &v
		}
	}
	return nil
}

// heardOrTurned counts the records holding s: the heard history and the
// conversation's turns.
func heardOrTurned(t *testing.T, a *App, s string) int {
	t.Helper()
	n := len(historyRows(t, a, s))
	turns, _ := a.store.RecentTurns(50)
	for _, tr := range turns {
		if raw, _ := json.Marshal(tr); strings.Contains(string(raw), s) {
			n++
		}
	}
	return n
}

// privateFiles counts the files in the plugins' private storage — the
// host's links to each plugin's directory followed, so what is counted is
// what a plugin wrote there.
func privateFiles(t *testing.T, a *App) int {
	t.Helper()
	n := 0
	var walk func(root string)
	walk = func(root string) {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			switch {
			case err != nil:
			case d.Type()&os.ModeSymlink != 0:
				if target, terr := filepath.EvalSymlinks(p); terr == nil {
					walk(target)
				}
			case d.Type().IsRegular():
				t.Logf("private storage holds %s", p)
				n++
			}
			return nil
		})
	}
	walk(a.pluginOpts.PluginDataDir)
	return n
}

// A RESIDENT ENGINE WHOSE SESSION CHECKS PASS IS COMMITTED, AND WHAT THEY
// HEARD IS NO ONE'S. It hears its recording's tail and speaks a sentence
// long and loud enough; the card says its publisher's checks passed. The
// engine's words in the check — "the tail, finalized" — are in no heard
// record and no turn, and nothing reached its private storage (the
// speaker store the engine keeps there; C4). The operator's first open on
// it succeeds.
func TestAnEngineWhoseSessionChecksPassServesAndWhatTheyHeardIsNoOnes(t *testing.T) {
	const id = "com.example.voice-skel"
	app := checkedEngineApp(t, id, `{"checks":[
		{"name":"hears its tail","sets":["*"],"lane":"session","input":{"audio":"checks/tone.wav"},"expect":{"events":{"order":["session_ready","transcript_final","input_finished"],"transcript_contains":["the tail, finalized"]}},"within_ms":10000},
		{"name":"speaks a sentence","sets":["*"],"lane":"session","input":{"text":"The local service is ready."},"expect":{"events":{"order":["session_ready","synthesis_start","synthesis_end"],"audio_at_least":{"ms":100,"rms_dbfs":-40}}},"within_ms":10000}]}`, true)
	voiceWait(t, "the engine serves after its checks", 30*time.Second, func() bool {
		ap := app.activePlugin(id)
		return ap != nil && ap.Voice.Load() != nil
	})
	inst := instanceOf(app, id)
	var verdict *pluginfacility.ChecksVerdict
	for _, a := range inst.Activations {
		if a.Role == pluginfacility.RoleActive {
			verdict = a.Checks
		}
	}
	if verdict == nil || verdict.State != pluginfacility.ChecksPassed {
		t.Fatalf("the card says its publisher's checks passed: %+v", verdict)
	}
	plane := app.AudioPlane()
	if err := plane.Register(&audio.Endpoint{ID: "spk", Label: "capture", Sink: audio.NewCaptureSink(mono16k)}); err != nil {
		t.Fatal(err)
	}
	// The operator's first open starts the application's observer of the
	// engine, which reads whatever the engine's handle holds: a check's
	// words left there would be read now.
	vo, err := app.OpenVoiceSession(context.Background(), "", "spk", "output", nil)
	if err != nil {
		t.Fatalf("the operator's first open on the engine its checks ran on: %v", err)
	}
	if !strings.HasPrefix(vo.ID(), "vs-") {
		t.Fatalf("the operator's session is the application's: %q", vo.ID())
	}
	time.Sleep(500 * time.Millisecond) // the observer drains what it was handed
	if n := heardOrTurned(t, app, "finalized"); n != 0 {
		t.Fatalf("a check's words reached %d heard records or turns", n)
	}
	if n := privateFiles(t, app); n != 0 {
		t.Fatalf("a check left %d files in the plugin's private storage", n)
	}
}

// A SESSION CHECK THAT FAILS PASSES ITS SET OVER, AND THE CARD SAYS SO IN
// THE PUBLISHER'S WORDS: the engine never says what the check listens
// for; its one set is not used on this machine, nothing serves, and the
// refusal names the check and what the engine said instead. Its words are
// no one's either.
func TestASessionCheckThatFailsPassesTheSetOverWithItsWords(t *testing.T) {
	const id = "com.example.voice-skel"
	app := checkedEngineApp(t, id, `{"checks":[
		{"name":"hears cobalt lantern","sets":["*"],"lane":"session","input":{"audio":"checks/tone.wav"},"expect":{"events":{"transcript_contains":["cobalt lantern"]}},"within_ms":10000}]}`, false)
	var ref *pluginfacility.Refusal
	voiceWait(t, "the set refused at its checks", 30*time.Second, func() bool {
		if inst := instanceOf(app, id); inst != nil && inst.Refusal != nil {
			ref = inst.Refusal
			return true
		}
		return false
	})
	if ref.Stage != pluginfacility.StageValidation && !strings.Contains(ref.Error(), "its publisher's checks failed") {
		t.Fatalf("refused at its checks: %v", ref)
	}
	if !strings.Contains(refusalReason(ref)+selectionWords(instanceOf(app, id)), `"hears cobalt lantern": its transcript lacks "cobalt lantern"`) {
		t.Fatalf("the card says which check failed and how, in the publisher's words: %s %s", refusalReason(ref), selectionWords(instanceOf(app, id)))
	}
	if app.activePlugin(id) != nil {
		t.Fatal("a set whose checks failed for good must not serve")
	}
	if n := heardOrTurned(t, app, "finalized"); n != 0 {
		t.Fatalf("a check's words reached %d heard records or turns", n)
	}
}

// selectionWords is every reason the card gives for the sets of the
// instance's newest activation.
func selectionWords(inst *pluginfacility.InstanceView) string {
	if inst == nil {
		return ""
	}
	var out []string
	for _, a := range inst.Activations {
		if a.Selection == nil {
			continue
		}
		for _, s := range a.Selection.Sets {
			for _, r := range s.Reasons {
				out = append(out, r.Words)
			}
		}
	}
	return strings.Join(out, " | ")
}
