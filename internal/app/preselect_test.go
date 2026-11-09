package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// What the birth form opens on is a NOW-fact about this machine: a
// subscription credential that is present and working beats a stored
// default, because it births with nothing to paste.
func TestPreselectPrefersAWorkingSubscription(t *testing.T) {
	mk := func() []dashboard.ProviderInfo {
		return []dashboard.ProviderInfo{
			{Name: "Claude (Max/Pro)", Credential: "claude-code", Status: "no_credential"},
			{Name: "ChatGPT (Plus/Pro)", Credential: "codex", Status: "no_credential"},
			{Name: "Acme", Default: true, Status: "ok"},
		}
	}

	// Neither subscription present: the stored default stands.
	out := mk()
	markPreselect(out, nil)
	if !out[2].Preselect || out[0].Preselect || out[1].Preselect {
		t.Fatalf("with no usable credential the registry default must win, got %+v", out)
	}

	// ChatGPT alone: it wins over the stored default.
	out = mk()
	out[1].Status = "ok"
	markPreselect(out, nil)
	if !out[1].Preselect || out[2].Preselect {
		t.Fatalf("a working credential must beat the stored default, got %+v", out)
	}
	if out[1].PreselectWhy == "" {
		t.Fatal("the operator must be told WHY it was chosen")
	}

	// Both present: registry order decides, and it puts Anthropic first —
	// the public documented API on its native dialect, ahead of a private
	// backend.
	out = mk()
	out[0].Status, out[1].Status = "ok", "ok"
	markPreselect(out, nil)
	if !out[0].Preselect || out[1].Preselect {
		t.Fatalf("Anthropic first when both are available, got %+v", out)
	}

	// Exactly one, always.
	for _, o := range [][]dashboard.ProviderInfo{mk(), out} {
		n := 0
		for _, p := range o {
			if p.Preselect {
				n++
			}
		}
		if n > 1 {
			t.Fatalf("at most one preselection, got %d", n)
		}
	}
}

func TestAProviderThatWantsAKeyIsNotCalledUnreachable(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusUnauthorized:       probeAuthRequired,
		http.StatusForbidden:          probeAuthRequired,
		http.StatusServiceUnavailable: probeUnreachable,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "refused", status)
		}))
		a := &App{}
		probe := a.probeOne(providerEntry{Name: "keyed", URL: server.URL})
		server.Close()
		if probe.state != want {
			t.Errorf("a provider answering %d to a caller with no key is %q (%s); want %q", status, probe.state, probe.reason, want)
		}
		if want == probeAuthRequired && probe.reason != "" {
			t.Errorf("a provider that wants a key carries a failure's reason: %q", probe.reason)
		}
	}
}

// A KEY THE PROVIDER REFUSES IS SAID AS REFUSED. A provider shown a key that
// answers 401 or 403 is not unreachable: it answered, and the key is what to
// change, where the page used to read "not answering". One that answers
// 410 is still unreachable, and its reason says the answer.
func TestAKeyTheProviderRefusesIsSaidAsRefused(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusUnauthorized: probeKeyRefused,
		http.StatusForbidden:    probeKeyRefused,
		http.StatusGone:         probeUnreachable,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "refused", status)
		}))
		a := &App{}
		probe := a.probeOne(providerEntry{Name: "keyed", URL: server.URL, APIKey: "a-key-it-refuses"})
		server.Close()
		if probe.state != want {
			t.Errorf("a provider answering %d to a key is %q (%s); want %q", status, probe.state, probe.reason, want)
		}
		if probe.reason == "" || !strings.Contains(probe.reason, strconv.Itoa(status)) {
			t.Errorf("the %d is not in the reason the row shows: %q", status, probe.reason)
		}
	}
}

// A missing credential is not a provider outage. Saying "unreachable"
// when Claude Code simply is not signed in reads as though Anthropic
// were down, and tells the operator nothing they can act on.
func TestCredentialAbsenceIsNotAProviderOutage(t *testing.T) {
	a := &App{}
	probe := a.probeOne(providerEntry{
		Name: "subscription", URL: "https://example.com", Credential: "file:" + filepath.Join(t.TempDir(), "missing.json"),
	})
	if probe.state != "no_credential" || probe.reason == "" {
		t.Fatalf("missing owner credential = %+v, want actionable no_credential", probe)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	_, _, err := discoverModelsWith(t.Context(), "", server.URL, "", false, nil, nil)
	if err == nil || errors.Is(err, errCredentialUnavailable) {
		t.Fatalf("provider refusal was misclassified as local credential failure: %v", err)
	}
}

// A MODEL ON THIS COMPUTER THAT ANSWERS is recommended when no
// subscription works: the first-run design says so, and the page drew the
// row only for a preselection the server never made for a local entry
// It must answer with a model to think with; a
// working subscription still comes first; a stopped server is not it.
func TestPreselectRecommendsALocalModelThatAnswers(t *testing.T) {
	mk := func() []dashboard.ProviderInfo {
		return []dashboard.ProviderInfo{
			{Name: "Claude (Max/Pro)", Chat: true, Credential: "claude-code", Status: "no_credential"},
			{Name: "Acme", Chat: true, Default: true, Status: "ok"},
			{Name: "Ollama", Chat: true, Local: true, Status: "unreachable"},
			{Name: "LM Studio", Chat: true, Local: true, Status: "ok", Models: []string{"qwen3-8b"}},
		}
	}
	live := [][]string{nil, nil, nil, {"qwen3-8b"}}
	out := mk()
	markPreselect(out, live)
	if !out[3].Preselect || out[1].Preselect || out[2].Preselect {
		t.Fatalf("an answering local model must be recommended over the stored default, got %+v", out)
	}
	// Its configured name is not a model it lists: no live model, no
	// recommendation, though Models still holds the name.
	out = mk()
	markPreselect(out, [][]string{nil, nil, nil, nil})
	if out[3].Preselect || !out[1].Preselect {
		t.Fatalf("a local server with no model to think with is not recommended, got %+v", out)
	}
	out = mk()
	out[0].Status = "ok"
	markPreselect(out, live)
	if !out[0].Preselect || out[3].Preselect {
		t.Fatalf("a working subscription still comes first, got %+v", out)
	}
}

// The usual addresses are looked at because they are shipped entries: the
// probe that fills Status asks every chat entry, so a new identity finds
// Ollama, LM Studio, or a llama.cpp or MLX server at its default address.
func TestTheShippedLocalEntriesAreTheUsualAddresses(t *testing.T) {
	want := map[string]string{
		"Ollama":       "http://127.0.0.1:11434/v1",
		"LM Studio":    "http://127.0.0.1:1234/v1",
		"Local server": "http://127.0.0.1:8080/v1",
	}
	for _, e := range embeddedRegistry().Providers {
		if url, ok := want[e.Name]; ok {
			if e.URL != url || !e.Local || !chatProvider(e) {
				t.Errorf("%s ships as %s, local %v, chat %v; want %s, local, chat", e.Name, e.URL, e.Local, chatProvider(e), url)
			}
			delete(want, e.Name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("not shipped: %v", want)
	}
}

// End to end on the server half: an entry marked local whose address lists
// a model is probed and recommended through the same rows the page reads.
func TestALocalServerThatListsAModelIsRecommendedThroughTheRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"llama3.2","object":"model"}]}`))
	}))
	t.Cleanup(server.Close)
	a := &App{}
	rows := a.providerInfos(&providerRegistry{Providers: []providerEntry{
		{Name: "Acme", APIType: "openai", URL: "http://127.0.0.1:9/v1", Default: true},
		{Name: "Ollama", APIType: "openai", URL: server.URL + "/v1", Local: true},
	}})
	if len(rows) != 2 || !rows[1].Preselect || rows[0].Preselect || rows[1].Status != probeOK {
		t.Fatalf("the answering local entry must be the recommendation: %+v", rows)
	}
}

// A LOCAL SERVER THAT LISTS NOTHING IS NOT RECOMMENDED FOR A NAME ITS ENTRY
// HOLDS. A default model in the configuration is merged into the models the
// page reads; the server answering with an empty list still offers none,
// so the stored default is the recommendation.
func TestALocalServerThatListsNothingIsNotRecommendedForItsConfiguredName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(server.Close)
	a := &App{}
	rows := a.providerInfos(&providerRegistry{Providers: []providerEntry{
		{Name: "Acme", APIType: "openai", URL: "http://127.0.0.1:9/v1", Default: true},
		{Name: "Ollama", APIType: "openai", URL: server.URL + "/v1", Local: true, DefaultModel: "gone"},
	}})
	if len(rows) != 2 || rows[1].Status != probeOK || rows[1].Preselect || !rows[0].Preselect {
		t.Fatalf("a local server that lists no model was recommended for its configured name: %+v", rows)
	}
}

// THE RECOMMENDATION NAMES A MODEL THE SERVER LISTS. An entry's default is
// named when its server lists it; otherwise the first it lists is, so the
// page never recommends, or leaves chosen, a name only the configuration
// holds.
func TestALocalRecommendationNamesAModelItsServerLists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"live","object":"model"},{"id":"also","object":"model"}]}`))
	}))
	t.Cleanup(server.Close)
	a := &App{}
	for _, c := range []struct{ def, want string }{{"gone", "live"}, {"also", "also"}, {"", "live"}} {
		rows := a.providerInfos(&providerRegistry{Providers: []providerEntry{
			{Name: "Acme", APIType: "openai", URL: "http://127.0.0.1:9/v1", Default: true},
			{Name: "Ollama", APIType: "openai", URL: server.URL + "/v1", Local: true, DefaultModel: c.def},
		}})
		if len(rows) != 2 || !rows[1].Preselect || rows[1].PreselectModel != c.want {
			t.Fatalf("default %q: the recommendation names %q, want %q: %+v", c.def, rows[1].PreselectModel, c.want, rows[1])
		}
	}
}
