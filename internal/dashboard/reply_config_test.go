package dashboard

import (
	"context"
	"testing"
)

func TestTheConfigAndProviderRepliesAnswerUnderTheirRequest(t *testing.T) {
	h := &WSHandler{
		GetStats:          func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		GetConfig:         func() *ConfigState { return &ConfigState{Database: DatabaseState{Active: "live"}} },
		GetProviders:      func() ProviderDirectory { return ProviderDirectory{Providers: []ProviderInfo{{Name: "Claude"}}} },
		PluginAct:         func(PluginAction) error { return nil },
		CatalogRefresh:    func() error { return nil },
		DisconnectProfile: func(string) error { return nil },
		ClearPluginKey:    func(string, string) error { return nil },
		CompleteSignIn:    func(string, string) error { return nil },
		SetEffort:         func(string) error { return nil },
		SetProvider:       func(ProviderEdit) error { return nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = drainUntil(t, conn, "status")

	configActs := []ClientMessage{
		{Type: "plugin", Plugin: &PluginAction{Action: "install", ID: "p"}},
		{Type: "catalog_refresh"},
		{Type: "profile_disconnect", Profile: "x"},
		{Type: "plugin_key_clear", PluginKey: &PluginKeyRequest{Plugin: "p", Key: "k"}},
	}
	for _, m := range configActs {
		m.RequestID = m.Type + "-config"
		sendMsg(t, conn, m)
		got := drainUntil(t, conn, "config")
		if got.RequestID != m.RequestID {
			t.Errorf("%s: the configuration answered request %q", m.Type, got.RequestID)
		}
		if got.Config == nil || got.Config.Database.Active != "live" {
			t.Errorf("%s: configuration %+v", m.Type, got.Config)
		}
	}

	for _, m := range []ClientMessage{
		{Type: "provider_signin_complete", Provider: "Claude"},
		{Type: "effort_set", Effort: "high"},
		{Type: "provider_set", Entry: &ProviderEdit{ProviderInfo: ProviderInfo{Name: "Claude"}}},
	} {
		m.RequestID = m.Type + "-providers"
		sendMsg(t, conn, m)
		got := drainUntil(t, conn, "providers")
		if got.RequestID != m.RequestID || len(got.Providers) != 1 || got.Providers[0].Name != "Claude" || got.SkipSignInWithValidToken == nil {
			t.Errorf("%s: providers reply under request %q, %d entries", m.Type, got.RequestID, len(got.Providers))
		}
	}
}
