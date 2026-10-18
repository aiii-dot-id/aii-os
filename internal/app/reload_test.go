package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

func TestReloadAppliesSandboxRootsLive(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "ReloadTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfgPath := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.LLM = withTestProvider(t, dir, "test", "https://x", "m", "sk-x")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.SourcePath = cfgPath
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer stopApp(t, app)

	granted := t.TempDir()
	if err := os.WriteFile(filepath.Join(granted, "hello.txt"), []byte("reachable"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate an operator editing config.json by hand.
	cfg2 := *cfg
	cfg2.Tools.ExtraRoots = []string{granted}
	b2, _ := json.Marshal(&cfg2)
	if err := os.WriteFile(cfgPath, b2, 0o644); err != nil {
		t.Fatal(err)
	}

	app.reloadConfig()

	// Enforcement: reachable, live.
	res, err := app.toolReg.Execute(t.Context(), "read", map[string]any{"file_path": filepath.Join(granted, "hello.txt")})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "reachable") {
		t.Fatalf("granted root must be readable after reload: %v %+v", err, res)
	}

	// Knowledge: her floor names the root, by its real path. The system's
	// temporary directory can be spelled another way than that (the short
	// form of a long user name in a Windows desktop session), and the floor
	// names the place itself.
	realGranted, err := filepath.EvalSymlinks(granted)
	if err != nil {
		t.Fatal(err)
	}
	floor := app.rings.GetContent(ring.Ring5)
	if !strings.Contains(floor, realGranted) && !strings.Contains(floor, granted) {
		t.Fatalf("Ring 5 floor must name the granted root — enforcement without knowledge is the bug this fixes: %s is not in it", realGranted)
	}
	if !strings.Contains(floor, "Granted roots") {
		t.Fatal("floor must carry the granted-roots section")
	}
}

func TestReloadDoesNotActivateSupersededFile(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "old", URL: "https://old.example", DefaultModel: "m1", Default: true},
		providerEntry{Name: "new", URL: server.URL, DefaultModel: "m2"},
	)
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: "old", Model: "m1", TimeoutSeconds: 5, Retries: -1}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live.Store(true)
	a.bgCtx = t.Context()
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "m1"}))

	candidate := *cfg
	candidate.LLM.Provider, candidate.LLM.Model = "new", "m2"
	if _, err := saveConfig(&candidate); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		a.reloadConfig()
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("reload probe did not start")
	}

	newer := candidate
	newer.LLM.Model = "m3"
	if _, err := saveConfig(&newer); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if got := a.configSnapshot().LLM.Model; got != "m1" {
		t.Fatalf("superseded reload activated %q", got)
	}
	if got := a.llmSwap.Current().ModelName(); got != "m1" {
		t.Fatalf("superseded reload swapped client to %q", got)
	}
	persisted, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.LLM.Model != "m3" {
		t.Fatalf("reload overwrote newer file with %q", persisted.LLM.Model)
	}
}

func TestReloadTransportTuningDoesNotProbe(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "provider unavailable", http.StatusTooManyRequests)
	}))
	defer server.Close()

	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{
		Name: "provider", URL: server.URL, APIKey: "key", DefaultModel: "model", Default: true,
	})
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: "provider", Model: "model", TimeoutSeconds: 5, Retries: -1}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live.Store(true)
	a.bgCtx = t.Context()
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "model"}))

	edited := *cfg
	edited.LLM.TimeoutSeconds = 10
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()

	if got := requests.Load(); got != 0 {
		t.Fatalf("timeout-only reload made %d inference requests", got)
	}
	if got := a.configSnapshot().LLM.TimeoutSeconds; got != 10 {
		t.Fatalf("timeout = %d, want 10", got)
	}
}

func TestReloadPreservesRestartEditOnNextSave(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live.Store(true)

	edited := *cfg
	edited.Dashboard.Port = 9191
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if got := a.configSnapshot().Dashboard.Port; got != 9191 {
		t.Fatalf("restart-required file edit was not retained in desired config: %d", got)
	}
	if _, err := a.applyConfigChange(map[string]any{"updates.automatic": true}); err != nil {
		t.Fatal(err)
	}
	persisted, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Dashboard.Port != 9191 {
		t.Fatalf("later save overwrote restart-required edit with port %d", persisted.Dashboard.Port)
	}
}

// THE VOICE MODE'S REVISION IS THE HOST'S COUNTER, NOT THE FILE'S. A file
// edited by hand carries no revision; a reload that published its zero
// sent every open page a lower revision than the one it held, and the
// pages — rightly — kept the old pair. A changed pair advances the
// counter as the door does; an unchanged pair keeps it.
func TestReloadKeepsTheVoiceModeRevisionMonotonic(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{Name: "provider", URL: "http://127.0.0.1:9", APIKey: "key", DefaultModel: "model", Default: true})
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: "provider", Model: "model", TimeoutSeconds: 5, Retries: -1}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live.Store(true)
	a.bgCtx = t.Context()
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "model"}))
	for i := 0; i < 8; i++ { // the door advances the counter to 8
		voiceModeDoor(t, a, listenInteractive, []string{speakOn, speakOff}[i%2])
	}
	if _, _, rev := a.VoiceMode(); rev != 8 {
		t.Fatalf("the fixture door left the revision at %d, want 8", rev)
	}

	edited := a.configSnapshot()
	edited.Speech.Mode = VoiceModeConfig{Listen: listenOff, Speak: speakOff} // no revision, as a hand edit has none
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if listen, speak, rev := a.VoiceMode(); listen != listenOff || speak != speakOff || rev != 9 {
		t.Fatalf("a reload of a changed pair gave (%s, %s, rev %d); want (off, off, rev 9)", listen, speak, rev)
	}

	// The same pair written again (revision 0 in the file) keeps 9.
	edited = a.configSnapshot()
	edited.Speech.Mode = VoiceModeConfig{Listen: listenOff, Speak: speakOff}
	edited.LLM.TimeoutSeconds = 7 // something else changed, so the reload runs
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if _, _, rev := a.VoiceMode(); rev != 9 {
		t.Fatalf("a reload of an unchanged pair moved the revision to %d, want 9", rev)
	}
	// A file ahead of the host is taken.
	edited = a.configSnapshot()
	edited.Speech.Mode = VoiceModeConfig{Listen: listenMeeting, Speak: speakOff, Revision: 40}
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if listen, _, rev := a.VoiceMode(); listen != listenMeeting || rev != 40 {
		t.Fatalf("a reload of a file ahead of the host gave (%s, rev %d); want (meeting, rev 40)", listen, rev)
	}
}
