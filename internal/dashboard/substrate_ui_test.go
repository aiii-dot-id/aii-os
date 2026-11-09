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
	// THE MODEL IS CHOSEN IN SETTINGS ALONE (step 4): Chat carries no
	// picker, and the one in Settings → Model is the transaction below.
	shell := read("static/index.html")
	for _, gone := range []string{`id="chat-substrate-wrap"`, `id="chat-provider"`, `id="chat-model"`, `list="chat-model-list"`} {
		if strings.Contains(shell, gone) {
			t.Errorf("Chat still carries a model picker: %s", gone)
		}
	}
	if strings.Contains(read("static/views/chat.js"), "llm.provider") {
		t.Error("Chat still saves the model it thinks with")
	}

	// Settings is a group of modules (settingsGroup). The guard reads them as
	// one text, so a line that moved between them is still held, and every
	// absence below is checked in all of them.
	var settings string
	for _, p := range settingsGroup(t) {
		settings += read(p) + "\n"
	}
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
	// Key retention is a browser behavior, not a requirement to serialize
	// the password into HTML. TestSettingsProviderDraftStaysWithItsOwnerAndSecretNeverEntersMarkup
	// proves refusal/retry, explicit cancellation and absence from markup/storage.
	// Both saves correlate through pending.js now: claim() matches the
	// request id and clears in one move, so the assertion follows the
	// call rather than the comparison it replaced.
	for _, required := range []string{"prov.claim(requestID)", "config.claim(requestID)"} {
		if !strings.Contains(settings, required) {
			t.Fatalf("settings acknowledgement is missing %q", required)
		}
	}
	// The page may name the file's key in its one sentence that tells the
	// operator where an address is named (the line under the CREDENTIAL
	// select); that sentence is taken out, and nothing else of the page
	// may name the field at all, in any form.
	const credentialWords = "name that address on the entry in providers.json (credential_options.base_url)."
	if !strings.Contains(settings, credentialWords) {
		t.Fatalf("the CREDENTIAL line's sentence is not on the page as this guard knows it: %q", credentialWords)
	}
	if strings.Contains(strings.Replace(settings, credentialWords, "", 1), "credential_options") {
		t.Fatal("file-owned credential request facts must not round-trip through the browser")
	}

	ws := read("static/ws.js")
	for _, required := range []string{
		"msg.request_id", "acceptDiscoveryResponse(msg.request_id, msg.provider)",
		"settingsConnectionLost()", "firstbootConnectionLost()",
	} {
		if !strings.Contains(ws, required) {
			t.Fatalf("WebSocket correlation is missing %q", required)
		}
	}
}
