package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

const keptSecret = "sk-stored-q9-fixture"

func keyedProviderEntry() providerEntry {
	return providerEntry{Name: "Keyed", APIType: "openai", URL: "http://127.0.0.1:1/v1",
		APIKey: keptSecret, DefaultModel: "m", Default: true}
}

// providerPage is a dashboard serving the provider hooks as the live
// handler wires them (wireProviderHooks), reached over a real socket: the
// frames below are decoded by the server, not by the test.
func providerPage(t *testing.T) (*App, *websocket.Conn) {
	t.Helper()
	a := newProvidersApp(t)
	h := &dashboard.WSHandler{}
	a.wireProviderHooks(h)
	srv := dashboard.New("127.0.0.1", 0, h)
	if _, err := srv.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	return a, dialWSS(t, srv.Origin())
}

// pageFrame sends one frame and returns the raw bytes of the first
// "providers" frame answering it: the one wearing id, or for a query
// (whose answer wears none) the first.
func pageFrame(t *testing.T, conn *websocket.Conn, id, frame string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("no providers frame answered %s: %v", frame, err)
		}
		var m struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
			Message   string `json:"message"`
		}
		if json.Unmarshal(data, &m) != nil || m.RequestID != id {
			continue
		}
		switch m.Type {
		case "error":
			t.Fatalf("%s was refused: %s", frame, m.Message)
		case "providers":
			return data
		}
	}
}

func storedKey(t *testing.T, a *App) string {
	t.Helper()
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.Providers {
		if e.Name == "Keyed" {
			return e.APIKey
		}
	}
	t.Fatal("the saved entry is gone")
	return ""
}

func TestAProviderSaveClearsTheStoredKeyOnlyWhenItSaysSo(t *testing.T) {
	a, conn := providerPage(t)
	const entry = `"name":"Keyed","api_type":"openai","endpoint":"http://127.0.0.1:1/v1","default_model":"m","default":true,"chat":true`
	for _, c := range []struct {
		name, key string
		want      string
	}{
		// Red before Q9: no has_key read as has_key:false.
		{"a save that says nothing of the key keeps it", ``, keptSecret},
		// Control: the card's keep box unchecked, as the page sends it.
		{"has_key false with no new key clears it", `,"api_key":"","has_key":false`, ""},
		{"has_key true with no new key keeps it", `,"api_key":"","has_key":true`, keptSecret},
		{"a new key replaces it, keep box unchecked", `,"api_key":"sk-new","has_key":false`, "sk-new"},
		{"a new key replaces it, keep box checked", `,"api_key":"sk-new","has_key":true`, "sk-new"},
		{"a new key replaces it, has_key absent", `,"api_key":"sk-new"`, "sk-new"},
		{"clear_key clears it", `,"clear_key":true`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			writeTestProviders(t, ".", keyedProviderEntry())
			pageFrame(t, conn, "save", `{"type":"provider_set","request_id":"save","entry":{`+entry+c.key+`}}`)
			if got := storedKey(t, a); got != c.want {
				t.Fatalf("stored key after the save is %q, want %q", got, c.want)
			}
		})
	}
}

// THE PROVIDER LIST SENT TO THE BROWSER NEVER CARRIES A KEY. The listing a
// page asks for and the one a save answers with are read as raw frames:
// no api_key field anywhere in them, and the stored secret nowhere in
// their bytes. The control is that the same frames name the keyed entry
// and say a key is stored, and that providers.json holds it, so the
// absence is the server's withholding, not an empty fixture.
func TestTheProviderListSentToTheBrowserCarriesNoKey(t *testing.T) {
	_, conn := providerPage(t)
	writeTestProviders(t, ".", keyedProviderEntry())
	if file, err := os.ReadFile("providers.json"); err != nil || !bytes.Contains(file, []byte(keptSecret)) {
		t.Fatalf("control: providers.json does not hold the key (%v)", err)
	}
	frames := map[string][]byte{
		"the listing a page asks for": pageFrame(t, conn, "", `{"type":"query","query":"providers"}`),
		"the listing a save answers with": pageFrame(t, conn, "save",
			`{"type":"provider_set","request_id":"save","entry":{"name":"Keyed","api_type":"openai","endpoint":"http://127.0.0.1:1/v1","default_model":"m","default":true,"chat":true,"has_key":true}}`),
	}
	for name, frame := range frames {
		var m struct {
			Providers []map[string]any `json:"providers"`
		}
		if err := json.Unmarshal(frame, &m); err != nil {
			t.Fatal(err)
		}
		keyed := false
		for _, p := range m.Providers {
			if p["name"] == "Keyed" && p["has_key"] == true {
				keyed = true
			}
		}
		if !keyed {
			t.Fatalf("control: %s does not say the keyed entry has a key: %s", name, frame)
		}
		if namesField(m.Providers, "api_key") {
			t.Fatalf("%s carries an api_key field: %s", name, frame)
		}
		if bytes.Contains(frame, []byte(keptSecret)) {
			t.Fatalf("%s carries the stored key: %s", name, frame)
		}
	}
}

// namesField reports whether a decoded JSON value holds an object field of
// this name at any depth.
func namesField(v any, field string) bool {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			if k == field || namesField(child, field) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if namesField(child, field) {
				return true
			}
		}
	case []map[string]any:
		for _, child := range v {
			if namesField(child, field) {
				return true
			}
		}
	}
	return false
}
