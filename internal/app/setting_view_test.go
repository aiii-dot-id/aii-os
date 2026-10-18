package app

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// A SETTING MOVED TO SETTINGS → SPEECH IS THE SAME SETTING. The Speech
// card drew an engine's scoped settings from its own copy of the view the
// plugin's card builds, and the copy had fallen behind: a string whose
// choices the plugin looks up came without choices_from, so the card drew
// a bare text box; a secret came without key_kept, key_host and key_not,
// so the card drew a title and nothing to paste into; an account came
// without its offers. The plugin's own card drew each one right. One
// function now builds the view for both; this holds them equal field for
// field, for every kind a package can declare.
func TestTheSpeechCardShowsASettingAsItsPluginCardDoes(t *testing.T) {
	const id = "id.test.voice"
	dir := t.TempDir()
	minimum, maximum := 0.5, 2.0
	decls := []pluginhost.SettingDecl{
		{Key: "language", Type: pluginhost.SettingString, Title: "Language", Default: "en", Scope: pluginhost.ScopeHearing},
		{Key: "model", Type: pluginhost.SettingString, Title: "Model", ChoicesFrom: "list.models", Scope: pluginhost.ScopeHearing},
		{Key: "voice", Type: pluginhost.SettingEnum, Title: "Voice", Values: []string{"ash", "elm"}, Labels: map[string]string{"ash": "Ash"}, Default: "ash", Scope: pluginhost.ScopeSpeaking},
		{Key: "rate", Type: pluginhost.SettingNumber, Title: "Rate", Minimum: &minimum, Maximum: &maximum, Default: 1.0, Scope: pluginhost.ScopeSpeaking},
		{Key: "beam", Type: pluginhost.SettingInteger, Title: "Beam", Required: true, Scope: pluginhost.ScopeHearing},
		{Key: "punctuate", Type: pluginhost.SettingBoolean, Title: "Punctuate", Default: true, Scope: pluginhost.ScopeHearing},
		{Key: "api_key", Type: pluginhost.SettingSecret, Title: "API key", Scope: pluginhost.ScopeHearing},
		{Key: "account", Type: pluginhost.SettingSecret, Title: "Account", Scope: pluginhost.ScopeSpeaking,
			OAuth: &pluginhost.SettingOAuthHint{Provider: "google", Services: []string{"speech"}}},
	}
	cfg := &Config{
		SourcePath: filepath.Join(dir, "config.json"),
		Identity:   IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")},
		Plugins: PluginsConfig{
			Grants: map[string]broker.Grant{id: {CredentialHandles: []string{keyProfileName(id, "api_key")}}},
			Settings: map[string]map[string]any{id: {
				"voice":   "oak", // no longer offered: Invalid, with the default in effect
				"rate":    1.5,
				"model":   "big-v2",
				"api_key": keyProfileName(id, "api_key"),
				"account": "work",
			}},
		},
	}
	a := New(cfg)
	defer a.bgCancel()
	credentials, err := a.credentialsDir()
	if err != nil {
		t.Fatal(err)
	}
	name := keyProfileName(id, "api_key")
	cfg.Plugins.AuthProfiles = map[string]broker.AuthProfile{
		name:   {Scheme: "bearer", SecretFile: filepath.Join(credentials, name+".key")},
		"work": {Scheme: broker.SchemeOAuth2, Provider: "google"},
	}
	ap := &pluginhost.ActivePlugin{ID: id, Settings: decls, Capabilities: []string{"net.outbound:api.engine.test:443"}}
	setPlugins(a, []*pluginhost.ActivePlugin{ap})

	views := a.pluginViews(a.cfg)
	if len(views) != 1 {
		t.Fatalf("one plugin card: %d", len(views))
	}
	card := map[string]dashboard.PluginSettingView{}
	for _, s := range views[0].Settings {
		card[s.Key] = s
	}
	speech := map[string]dashboard.PluginSettingView{}
	for _, s := range a.speechScopedSettings(ap) {
		speech[s.Key] = s
	}
	// The control half: the fixture reaches every field the card can say,
	// so an equality below is a comparison of something, not of zeroes.
	if !card["model"].ChoicesFrom || !card["api_key"].KeyKept || card["api_key"].KeyHost != "api.engine.test:443" ||
		card["account"].Account == nil || len(card["account"].Account.Offers) != 1 || card["voice"].Invalid == "" || len(card["api_key"].Handles) != 1 {
		t.Fatalf("the fixture does not reach every field of the plugin card's view: %+v", card)
	}
	for _, d := range decls {
		want, got := card[d.Key], speech[d.Key]
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)
		if string(wantJSON) != string(gotJSON) {
			t.Errorf("%s (%s): the Speech card shows\n  %s\nwhere the plugin's card shows\n  %s", d.Key, d.Type, gotJSON, wantJSON)
		}
	}
}
