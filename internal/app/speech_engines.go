package app

import (
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
func (a *App) speechEngines() []dashboard.SpeechService {
	// .
	// .
	// .
	// .
	// .
	var out []dashboard.SpeechService
	for _, p := range a.voicePlugins() {
		title := strings.TrimSpace(p.Title)
		if title == "" {
			title = p.ID
		}
		out = append(out, dashboard.SpeechService{
			Name: p.ID, Title: title, Plugin: true, Added: true,
			// .
			// .
			Speech:   &dashboard.ProviderSpeech{STT: &dashboard.SpeechOffer{}, TTS: &dashboard.SpeechOffer{}},
			Settings: a.speechScopedSettings(p),
		})
	}
	return out
}

// .
// .
// .
// .
func (a *App) speechScopedSettings(p *pluginhost.ActivePlugin) []dashboard.PluginSettingView {
	cfg := a.configSnapshot()
	values := cfg.Plugins.Settings[p.ID]
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var handles []string
	if g, ok := cfg.Plugins.Grants[p.ID]; ok {
		handles = append(handles, g.CredentialHandles...)
	}
	effective := pluginhost.EffectiveSettings(p.Settings, values)
	var out []dashboard.PluginSettingView
	for _, d := range p.Settings {
		if d.Scope != pluginhost.ScopeHearing && d.Scope != pluginhost.ScopeSpeaking {
			continue
		}
		sv := dashboard.PluginSettingView{Key: d.Key, Type: d.Type, Title: d.Title, Description: d.Description,
			Default: d.Default, Values: d.Values, Labels: d.Labels, Required: d.Required,
			Minimum: d.Minimum, Maximum: d.Maximum, Scope: d.Scope}
		if d.OAuth != nil {
			sv.OAuth = &dashboard.SettingOAuthHintView{Provider: d.OAuth.Provider, Services: append([]string(nil), d.OAuth.Services...)}
		}
		if d.Type == pluginhost.SettingSecret {
			sv.Handles = append([]string(nil), handles...)
		}
		if v, ok := values[d.Key]; ok {
			sv.Value = v
		}
		if e, ok := effective[d.Key]; ok {
			sv.Effective = e
		}
		sv.Invalid = pluginhost.StoredInvalid(d, values)
		out = append(out, sv)
	}
	return out
}

// .
// .
// .
// .
// .
// .
// .
// .
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
