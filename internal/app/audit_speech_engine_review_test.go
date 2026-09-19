// .
// .
// .
// .
// .
// .

package app

import (
	"context"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func auditLiveEngine(t *testing.T, id string) *pluginhost.ActivePlugin {
	t.Helper()
	frames := make(chan []byte)
	c := supervisor.NewSessionClientFrames(frames, io.Discard, nil, 32)
	v := pluginhost.NewVoiceSession(c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("fake transport failed to retire")
		}
		select {
		case <-v.Done():
		case <-time.After(2 * time.Second):
			t.Error("voice observer failed to retire")
		}
	})
	return &pluginhost.ActivePlugin{ID: id, Title: "Installed Engine", Voice: v}
}
func TestAuditDefaultEngineKeepsNetworkChoices(t *testing.T) {
	reg := &providerRegistry{}
	engines := []dashboard.SpeechService{testEngine()}
	services := speechServices(reg)
	if len(services) == 0 {
		t.Fatal("control: providers registry must offer network choices")
	}
	st := speechState(Config{}, reg, nil, engines)
	if dest := os.Getenv("AIII_AUDIT_SPEECH_STATE"); dest != "" {
		data, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(dest, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if len(st.Services) <= 1 {
		t.Errorf("both halves default to installed engine and hide every %d network alternatives", len(services))
	}
}
func TestAuditLocalEngineCanBeSavedWithoutProviderEntry(t *testing.T) {
	for _, direction := range []string{"stt", "tts"} {
		for _, broken := range []bool{false, true} {
			t.Run(direction+map[bool]string{false: "-valid-registry", true: "-broken-registry"}[broken], func(t *testing.T) {
				dir := t.TempDir()
				cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
				a := New(cfg)
				defer a.bgCancel()
				ap := auditLiveEngine(t, "id.test.voice")
				a.plugins = []*pluginhost.ActivePlugin{ap}
				data := []byte(`{"providers":[]}`)
				if broken {
					data = []byte(`{`)
				}
				if err := os.WriteFile(filepath.Join(dir, "providers.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				engines := a.speechEngines()
				if len(engines) != 1 {
					t.Fatal("control: engine not offered")
				}
				persisted := false
				_, err := a.applyConfigChangeWith(map[string]interface{}{"speech." + direction + ".provider": ap.ID}, func(*Config) (bool, error) { persisted = true; return true, nil })
				if err != nil || !persisted {
					t.Errorf("offered installed engine cannot be selected: persisted=%v error=%v", persisted, err)
				}
			})
		}
	}
}
func TestAuditInstalledEngineSampleDoesNotResolveAsCloudService(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	defer a.bgCancel()
	a.plugins = []*pluginhost.ActivePlugin{auditLiveEngine(t, "id.test.voice")}
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(`{"providers":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := a.speakMint(dashboard.SpeakText{Provider: "id.test.voice", Sample: true})
	if err != nil && strings.Contains(err.Error(), "not a speech service") {
		t.Errorf("enabled local-engine sample is sent to the cloud-only resolver: %v", err)
	}
}
func TestAuditEveryInstalledEngineRemainsSelectable(t *testing.T) {
	a := New(&Config{})
	defer a.bgCancel()
	a.plugins = []*pluginhost.ActivePlugin{auditLiveEngine(t, "id.test.first"), auditLiveEngine(t, "id.test.second")}
	got := a.speechEngines()
	if len(got) != 2 {
		t.Errorf("%d live installed engines offered for %d active engines", len(got), len(a.plugins))
	}
}
func TestAuditScopedSecretRetainsGrantedHandleChoices(t *testing.T) {
	const id = "id.test.voice"
	a := New(&Config{Plugins: PluginsConfig{Grants: map[string]broker.Grant{id: {CredentialHandles: []string{"synthetic-handle"}}}, Settings: map[string]map[string]interface{}{id: {"recognizer_credential": "synthetic-handle"}}}})
	defer a.bgCancel()
	p := &pluginhost.ActivePlugin{ID: id, Settings: []pluginhost.SettingDecl{{Key: "recognizer_credential", Type: "secret", Title: "Credential", Scope: pluginhost.ScopeHearing}}}
	a.plugins = []*pluginhost.ActivePlugin{p}
	before := a.pluginViews(a.cfg)
	if len(before) != 1 || len(before[0].Settings[0].Handles) != 1 {
		t.Fatal("control: original plugin view must retain granted handle")
	}
	after := a.speechScopedSettings(p)
	if len(after) != 1 || len(after[0].Handles) != 1 {
		t.Errorf("moving the scoped credential setting dropped its granted choices: %+v", after)
	}
}

// .
// .
func TestAuditExportActualCloudAndInstalledEngineStatus(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json"), Speech: SpeechConfig{STT: STTConfig{Provider: "Deepgram", Model: "nova-3"}}})
	defer a.bgCancel()
	a.plugins = []*pluginhost.ActivePlugin{auditLiveEngine(t, "id.test.voice")}
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(`{"providers":[{"name":"Deepgram","url":"http://127.0.0.1:1","api_key":"synthetic"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	state, reason, source := a.VoiceStatus()
	if state != "cloud" || !a.VoiceEngine() {
		t.Fatalf("expected actual cloud selection beside installed engine; state=%s reason=%s engine=%v", state, reason, a.VoiceEngine())
	}
	if path := os.Getenv("AIII_AUDIT_SPEECH_STATE"); path != "" {
		data, _ := json.Marshal(map[string]interface{}{"voice_state": state, "voice_engine": a.VoiceEngine(), "voice_source": source})
		if err := os.WriteFile(path+".status.json", data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
