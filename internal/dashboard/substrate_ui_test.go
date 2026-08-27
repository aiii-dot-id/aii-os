package dashboard

import (
	"strings"
	"testing"
)

// The live regression this pins had two UI halves: one native select held
// every model from every provider (hundreds on OpenRouter), and Save claimed
// success before the server answered. The picker is now two bounded controls
// with searchable model suggestions, and only a matching config response can
// report success.
func TestSubstratePickerIsSearchableAndAcknowledged(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	shell := read("static/index.html")
	for _, required := range []string{
		`id="chat-provider"`, `id="chat-model"`,
		`<select id="chat-provider"`, `<select id="chat-model"`,
		`id="chat-substrate-apply"`, `id="chat-substrate-status"`,
	} {
		if !strings.Contains(shell, required) {
			t.Errorf("chat substrate picker is missing %s", required)
		}
	}
	if strings.Contains(shell, `list="chat-model-list"`) {
		t.Fatal("the retired datalist picker returned; the picker is now bounded selects (512af66)")
	}

	chat := read("static/views/chat.js")
	for _, required := range []string{
		"Checking real inference", "acceptSubstrateConfig", "rejectSubstrateConfig",
		"current provider remains active",
	} {
		if !strings.Contains(chat, required) {
			t.Errorf("chat substrate transaction is missing %q", required)
		}
	}
	if strings.Contains(chat, "filter(p => p.status === 'ok'") {
		t.Fatal("catalogue status still hides configured providers before the real inference gate")
	}
	// The correlation moved into pending.js (claim matches the request id
	// and clears), so this asserts the call that carries it rather than
	// the comparison that used to be written out here.
	if !strings.Contains(chat, "substrate.claim(requestID)") {
		t.Fatal("substrate acknowledgement is not correlated to the request")
	}

	settings := read("static/views/settings.js")
	if strings.Contains(settings, "if (!S.providers.length) query('providers')") {
		t.Fatal("an empty provider registry still causes a query/render loop")
	}
	if strings.Contains(settings, "n.classList.add('show')") {
		t.Fatal("Settings still announces saved before a server acknowledgement")
	}
	for _, required := range []string{"acceptSettingsConfig", "rejectSettingsConfig", "<datalist", "providerModels(p)"} {
		if !strings.Contains(settings, required) {
			t.Errorf("Settings substrate transaction is missing %q", required)
		}
	}
	if !strings.Contains(settings, "p.api_key || ''") {
		t.Fatal("a rejected provider save does not retain the operator's typed key for retry")
	}
	// Both saves correlate through pending.js now: claim() matches the
	// request id and clears in one move, so the assertion follows the
	// call rather than the comparison it replaced.
	for _, required := range []string{"prov.claim(requestID)", "config.claim(requestID)"} {
		if !strings.Contains(settings, required) {
			t.Fatalf("settings acknowledgement is missing %q", required)
		}
	}
	if strings.Contains(settings, "credential_options") {
		t.Fatal("file-owned credential request facts must not round-trip through the browser")
	}

	ws := read("static/ws.js")
	for _, required := range []string{
		"msg.request_id", "acceptDiscoveryResponse(msg.request_id, msg.provider)",
		"substrateConnectionLost()", "settingsConnectionLost()", "firstbootConnectionLost()",
	} {
		if !strings.Contains(ws, required) {
			t.Fatalf("WebSocket correlation is missing %q", required)
		}
	}
}
