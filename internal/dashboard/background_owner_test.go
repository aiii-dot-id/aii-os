package dashboard

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Slow handler work belongs to the application lifecycle, not to a goroutine
// detached from the WebSocket handler. Holding the supplied job proves the
// callback and its eventual status read cannot run until the owner runs it.
func TestSlowConfigWorkUsesTheApplicationBackgroundOwner(t *testing.T) {
	jobs := make(chan func(), 1)
	var called atomic.Bool
	h := &WSHandler{
		Background: func(work func()) bool { jobs <- work; return true },
		GetStats:   func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		SetConfig: func(map[string]any) (*ConfigState, error) {
			called.Store(true)
			return &ConfigState{}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = drainUntil(t, conn, "status")
	sendMsg(t, conn, ClientMessage{RequestID: "cfg", Type: "config_set", Config: map[string]any{"llm.provider": "next"}})

	var work func()
	select {
	case work = <-jobs:
	case <-time.After(2 * time.Second):
		t.Fatal("the slow config callback bypassed the application background owner")
	}
	if called.Load() {
		t.Fatal("the callback ran before its lifecycle owner ran it")
	}
	work()
	if !called.Load() {
		t.Fatal("the owned callback did not run")
	}
	if m := drainUntil(t, conn, "config"); m.RequestID != "cfg" {
		t.Fatalf("owned callback reply lost its request: %+v", m)
	}
}

func TestSlowWorkIsRefusedAfterTheApplicationStartsStopping(t *testing.T) {
	h := &WSHandler{
		Background: func(func()) bool { return false },
		GetStats:   func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		SetConfig: func(map[string]any) (*ConfigState, error) {
			t.Fatal("a refused background job ran")
			return nil, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = drainUntil(t, conn, "status")
	sendMsg(t, conn, ClientMessage{RequestID: "cfg", Type: "config_set", Config: map[string]any{"llm.provider": "next"}})
	if m := drainUntil(t, conn, "error"); m.RequestID != "cfg" || m.Message != "identity is stopping" {
		t.Fatalf("stopping refusal = %+v", m)
	}
}

// Shutdown cancels detached turns and joins their final status work. A turn
// callback that has already started is allowed to finish, but the server may
// not report shutdown complete while that callback can still read app state.
func TestShutdownWaitsForADetachedTurn(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h := &WSHandler{
		HandleMessage: func(context.Context, string) (string, error) {
			close(entered)
			<-release
			return "done", nil
		},
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conn := dialWS(t, addr)
	_ = drainUntil(t, conn, "status")
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "wait"})
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- s.Shutdown(context.Background()) }()
	select {
	case err := <-stopped:
		t.Fatalf("shutdown returned while the detached turn was live: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not join the finished turn")
	}
}

func TestEveryOffLoopArmRefusesAlikeWhenTheApplicationIsStopping(t *testing.T) {
	ran := func(name string) { t.Errorf("%s ran although its background job was refused", name) }
	h := &WSHandler{
		Background:     func(func()) bool { return false },
		GetStats:       func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		DiscoverModels: func(string, string, string) ([]string, bool, error) { ran("discover"); return nil, false, nil },
		SpeechLists: func(string, string, string, string, string) (SpeechLists, error) {
			ran("speech_lists")
			return SpeechLists{}, nil
		},
		PluginAct:       func(PluginAction) error { ran("plugin"); return nil },
		CatalogRefresh:  func() error { ran("catalog_refresh"); return nil },
		SetConfig:       func(map[string]any) (*ConfigState, error) { ran("config_set"); return nil, nil },
		PublicNameClaim: func() (PublicNameState, error) { ran("public_name_claim"); return PublicNameState{}, nil },
		SignInProvider:  func(string) (string, error) { ran("provider_signin"); return "", nil },
		SignInProfile:   func(string) (string, error) { ran("profile_signin"); return "", nil },
		DisconnectProfile: func(string) error {
			ran("profile_disconnect")
			return nil
		},
		SetEffort:      func(string) error { ran("effort_set"); return nil },
		ClearPluginKey: func(string, string) error { ran("plugin_key_clear"); return nil },
		SettingChoices: func(string, string) ([]SettingChoice, error) { ran("plugin_setting_choices"); return nil, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = drainUntil(t, conn, "status")

	key := &PluginKeyRequest{Plugin: "p", Key: "k"}
	for _, m := range []ClientMessage{
		{Type: "query", Query: "discover", Provider: "x"},
		{Type: "query", Query: "speech_lists", Provider: "x"},
		{Type: "plugin", Plugin: &PluginAction{Action: "install", ID: "p"}},
		{Type: "catalog_refresh"},
		{Type: "config_set", Config: map[string]any{"llm.provider": "next"}},
		{Type: "public_name_claim"},
		{Type: "provider_signin", Provider: "x"},
		{Type: "profile_signin", Profile: "x"},
		{Type: "profile_disconnect", Profile: "x"},
		{Type: "effort_set", Effort: "high"},
		{Type: "plugin_key_clear", PluginKey: key},
		{Type: "plugin_setting_choices", PluginKey: key},
	} {
		label := m.Type + "/" + m.Query
		m.RequestID = "req-" + label
		sendMsg(t, conn, m)
		if got := drainUntil(t, conn, "error"); got.RequestID != m.RequestID || got.Message != "identity is stopping" {
			t.Errorf("%s refused as %q under request %q", label, got.Message, got.RequestID)
		}
	}
}
