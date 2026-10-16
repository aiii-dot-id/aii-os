package app

import (
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func (a *App) speechEngines() []dashboard.SpeechService {

	var out []dashboard.SpeechService
	for _, p := range a.voicePlugins() {
		title := strings.TrimSpace(p.Title)
		if title == "" {
			title = p.ID
		}
		out = append(out, dashboard.SpeechService{
			Name: p.ID, Title: title, Plugin: true, Added: true,

			Speech:   &dashboard.ProviderSpeech{STT: &dashboard.SpeechOffer{}, TTS: &dashboard.SpeechOffer{}},
			Settings: a.speechScopedSettings(p),
		})
	}
	return out
}

func (a *App) speechScopedSettings(p *pluginhost.ActivePlugin) []dashboard.PluginSettingView {
	cfg := a.configSnapshot()
	return a.settingViews(&cfg, p, sync.OnceValues(a.oauthContracts), func(d pluginhost.SettingDecl) bool {

		return d.Scope == pluginhost.ScopeHearing || d.Scope == pluginhost.ScopeSpeaking
	})
}

func speechEngineServes(provider string, engines []dashboard.SpeechService) (id string, serves, byDefault bool) {
	if len(engines) == 0 {
		return "", false, false
	}
	chosen := strings.TrimSpace(provider)
	if chosen == "" {
		return engines[0].Name, true, true
	}
	for _, e := range engines {
		if e.Name == chosen {
			return e.Name, true, false
		}
	}
	return "", false, false
}

func (a *App) speechEngineFor(provider string) *pluginhost.ActivePlugin {
	live := a.voicePlugins()
	offered := make([]dashboard.SpeechService, len(live))
	for i, p := range live {
		offered[i] = dashboard.SpeechService{Name: p.ID}
	}
	id, serves, _ := speechEngineServes(provider, offered)
	if !serves {
		return nil
	}
	for _, p := range live {
		if p.ID == id {
			return p
		}
	}
	return nil
}
