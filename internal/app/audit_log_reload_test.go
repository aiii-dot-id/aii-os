package app

import (
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuditSupersededReloadKeepsLogLevels(t *testing.T) {
	restoreLevels(t)
	t.Setenv(logsink.EnvDirective, "")
	logsink.SetLevels(slog.LevelInfo, map[string]slog.Level{"audit": slog.LevelDebug})
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	// .
	defer server.Close()
	defer unblock()
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{Name: "old", URL: "https://old.example", DefaultModel: "m1", Default: true}, providerEntry{Name: "new", URL: server.URL, DefaultModel: "m2"})
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: "old", Model: "m1", TimeoutSeconds: 5, Retries: -1}
	cfg.Logs.Level = "info"
	cfg.Logs.Detail = map[string]string{"audit": "debug"}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live = true
	a.bgCtx = t.Context()
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "m1"}))
	candidate := *cfg
	candidate.LLM.Provider, candidate.LLM.Model = "new", "m2"
	candidate.Logs.Level = "error"
	candidate.Logs.Detail = map[string]string{"audit": "error"}
	if _, err := saveConfig(&candidate); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); a.reloadConfig() }()
	defer func() { unblock(); <-done }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("loopback candidate probe never started")
	}
	newer := candidate
	newer.LLM.Model = "m3"
	newer.Logs.Level = "warn"
	if _, err := saveConfig(&newer); err != nil {
		t.Fatal(err)
	}
	unblock()
	<-done
	if got := a.configSnapshot(); got.LLM.Model != "m1" || got.Logs.Level != "info" {
		t.Fatalf("superseded config activated: %v %v", got.LLM.Model, got.Logs.Level)
	}
	if got := a.llmSwap.Current().ModelName(); got != "m1" {
		t.Fatalf("superseded client activated: %s", got)
	}
	persisted, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.LLM.Model != "m3" || persisted.Logs.Level != "warn" {
		t.Fatal("newer persisted config overwritten")
	}
	def, cats := logsink.Levels()
	if def != slog.LevelInfo || cats["audit"] != slog.LevelDebug {
		t.Fatalf("rejected candidate changed live levels: default=%v audit=%v; config and client stayed old", def, cats["audit"])
	}
}

func TestAuditAcceptedLogReloadIsLive(t *testing.T) {
	restoreLevels(t)
	t.Setenv(logsink.EnvDirective, "")
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Logs.Level = "info"
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live = true
	a.bgCtx = t.Context()
	fresh := *cfg
	fresh.Logs.Level = "debug"
	fresh.Logs.Detail = map[string]string{"audit": "trace"}
	if _, err := saveConfig(&fresh); err != nil {
		t.Fatal(err)
	}
	capture := logsink.CaptureForTest(t)
	a.reloadConfig()
	def, cats := logsink.Levels()
	if def != slog.LevelDebug || cats["audit"] != logsink.LevelTrace || a.configSnapshot().Logs.Level != "debug" {
		t.Fatalf("accepted reload not applied: %v %v", def, cats)
	}
	if strings.Contains(capture.String(), "SAVED FOR NEXT BOOT") {
		t.Fatalf("logging-only live change announced as restart-only: %s", capture.String())
	}
}
