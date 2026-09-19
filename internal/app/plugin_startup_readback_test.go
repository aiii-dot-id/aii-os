package app

import (
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
// .
// .
func TestTheStartupAllowanceAndSettingScopeReachThePage(t *testing.T) {
	a := &App{}
	a.plugins = []*pluginhost.ActivePlugin{{
		ID: "id.example.engine", Version: "1.0.0", Family: "tool",
		Startup: &pluginhost.StartupAllowance{
			Effective: 60 * time.Second, Requested: 180 * time.Second,
			Ceiling: 300 * time.Second, Source: "package", Capped: true,
		},
		Settings: []pluginhost.SettingDecl{
			{Key: "turn_pause_ms", Type: "integer", Title: "Pause", Scope: pluginhost.ScopeHearing},
			{Key: "tts_voice", Type: "string", Title: "Voice", Scope: pluginhost.ScopeSpeaking},
			{Key: "plain", Type: "string", Title: "Plain"},
		},
	}}
	views := a.pluginViews(defaultConfig())
	if len(views) != 1 {
		t.Fatalf("one active plugin, one view: %+v", views)
	}
	s := views[0].Startup
	if s == nil {
		t.Fatal("the allowance an activation ran under never reached the page")
	}
	if s.EffectiveMS != 60000 || s.RequestedMS != 180000 || s.CeilingMS != 300000 || s.Source != "package" || !s.Capped {
		t.Fatalf("the readback is not what the activation ran under: %+v", s)
	}
	scopes := map[string]string{}
	for _, sv := range views[0].Settings {
		scopes[sv.Key] = sv.Scope
	}
	if scopes["turn_pause_ms"] != "hearing" || scopes["tts_voice"] != "speaking" || scopes["plain"] != "" {
		t.Fatalf("a setting's declared half of Speech did not reach the page: %+v", scopes)
	}
}
