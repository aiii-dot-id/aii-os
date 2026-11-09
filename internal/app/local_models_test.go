package app

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// A local model is offered only when one is available, and these tests
// check what counts as a local model.

func localTestServer(t *testing.T, status int, models ...string) (*httptest.Server, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		ids := make([]string, len(models))
		for i, m := range models {
			ids[i] = `{"id":"` + m + `"}`
		}
		_, _ = w.Write([]byte(`{"data":[` + strings.Join(ids, ",") + `]}`))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	return srv, port
}

// deadPort is a loopback port nothing listens on.
func deadPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func localApp(t *testing.T, dir string, entries ...providerEntry) *App {
	t.Helper()
	writeTestProviders(t, dir, append([]providerEntry{{Name: "active", URL: "http://active.invalid", DefaultModel: "main", Default: true}}, entries...)...)
	return &App{
		cfg: &Config{
			SourcePath: filepath.Join(dir, "config.json"),
			LLM:        LLMConfig{Provider: "active", Model: "main"},
			Prompt:     PromptConfig{MaxTokens: 32000},
			Agency:     AgencyConfig{PreferLocalForRoles: true},
		},
		composer: prompt.New(ring.NewManager(), 32000),
		llmSwap:  newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: "http://active.invalid", Model: "main"})),
	}
}

// WHAT THE PAGE SAYS SUB-AGENTS WILL USE IS WHAT THEY USE. The page's line
// and the router ask one chooser; a dead entry first in the file shadows
// neither; an answering entry with no model is said as a problem, and the
// router then falls back to the main model.
func TestThePageAndTheRouterChooseTheSameLocalModel(t *testing.T) {
	srv, _ := localTestServer(t, http.StatusOK, "metal-model")
	dead := providerEntry{Name: "Local server", APIType: "openai", URL: "http://127.0.0.1:" + strconv.Itoa(deadPort(t)) + "/v1", Local: true, DefaultModel: "seed-model"}
	mine := providerEntry{Name: "Metal", APIType: "openai", URL: srv.URL + "/v1", Local: true, DefaultModel: "metal-model"}

	a := localApp(t, t.TempDir(), dead, mine)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	page := a.localModelState(reg)
	route := a.resolveRunTarget("review")
	if page.Marked != 2 || page.Provider != "Metal" || page.Model != "metal-model" || page.Problem != "" {
		t.Fatalf("page = %+v, want Metal · metal-model of 2 marked", page)
	}
	if route.provider != page.Provider || route.modelID != page.Model || route.fellBack() {
		t.Fatalf("router chose %q · %q (fallback=%v), page says %q · %q", route.provider, route.modelID, route.fellBack(), page.Provider, page.Model)
	}

	mine.DefaultModel = ""
	a = localApp(t, t.TempDir(), dead, mine)
	reg, _ = a.loadProviders()
	page = a.localModelState(reg)
	route = a.resolveRunTarget("review")
	if page.Provider != "Metal" || page.Model != "" || page.Problem == "" {
		t.Fatalf("an answering entry with no model: page = %+v, want its problem said", page)
	}
	if !route.fellBack() || route.modelID != "main" {
		t.Fatalf("an answering entry with no model: router used %q (fallback=%v), want the main model", route.modelID, route.fellBack())
	}

	a = localApp(t, t.TempDir(), dead)
	reg, _ = a.loadProviders()
	if page = a.localModelState(reg); page.Marked != 1 || page.Provider != "" {
		t.Fatalf("nothing answering: page = %+v, want 1 marked and none answering", page)
	}
}

// THE LOOK ASKS ONLY ITS PORTS, ONLY ON THIS MACHINE, and says what it
// found: a listing server with its models, one that wants a key as such,
// one already in the file by its entry's name, and a silent port not at all.
func TestLookForLocalServersAsksOnlyItsPortsOnThisMachine(t *testing.T) {
	_, open := localTestServer(t, http.StatusOK, "qwen", "llama")
	_, keyed := localTestServer(t, http.StatusUnauthorized)
	_, known := localTestServer(t, http.StatusOK, "kept")
	silent := deadPort(t)
	ports := localServerPorts
	t.Cleanup(func() { localServerPorts = ports })
	localServerPorts = []localPort{{open, "Open"}, {keyed, "Keyed"}, {known, "Known"}, {silent, "Silent"}}

	a := localApp(t, t.TempDir(), providerEntry{Name: "Mine", APIType: "openai", URL: "http://localhost:" + strconv.Itoa(known) + "/v1", Local: true, DefaultModel: "kept"})
	found := a.lookForLocalServers()
	if len(found) != 3 {
		t.Fatalf("found %d servers, want 3 (open, keyed, known): %+v", len(found), found)
	}
	byPort := map[int]int{}
	for i, f := range found {
		byPort[f.Port] = i
		if !strings.HasPrefix(f.Address, "http://127.0.0.1:") {
			t.Errorf("port %d was asked at %q, not on this machine", f.Port, f.Address)
		}
	}
	if f := found[byPort[open]]; f.Usual != "Open" || len(f.Models) != 2 || f.NeedsKey || f.Entry != "" {
		t.Errorf("open server: %+v", f)
	}
	if f := found[byPort[keyed]]; !f.NeedsKey || len(f.Models) != 0 {
		t.Errorf("keyed server: %+v, want needs_key", f)
	}
	if f := found[byPort[known]]; f.Entry != "Mine" {
		t.Errorf("a server already in the file: %+v, want entry Mine", f)
	}
	if _, ok := byPort[silent]; ok {
		t.Errorf("a silent port was listed")
	}
}

// ADDING NEEDS A MODEL THE SERVER LISTS, marks the entry local, gives routing
// that model, and refuses a port it does not look on or a server already in
// the file.
func TestAddLocalServerMarksItLocalWithTheChosenModel(t *testing.T) {
	_, port := localTestServer(t, http.StatusOK, "qwen", "llama")
	ports := localServerPorts
	t.Cleanup(func() { localServerPorts = ports })
	localServerPorts = []localPort{{port, "Test"}}

	a := localApp(t, t.TempDir())
	if err := a.addLocalServer(port, "not-listed"); err == nil {
		t.Fatal("added with a model the server does not list")
	}
	if err := a.addLocalServer(port, ""); err == nil {
		t.Fatal("added with no model")
	}
	if err := a.addLocalServer(port+1, "qwen"); err == nil {
		t.Fatal("added a port the look does not ask")
	}
	if err := a.addLocalServer(port, "llama"); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	e := entryNamed(reg, "Local server, port "+strconv.Itoa(port))
	if e == nil || !e.Local || e.DefaultModel != "llama" || e.Default {
		t.Fatalf("added entry = %+v, want local, default model llama, not the default provider", e)
	}
	if err := a.addLocalServer(port, "qwen"); err == nil || !strings.Contains(err.Error(), "already points") {
		t.Fatalf("a second add of the same server: %v", err)
	}
	if page := a.localModelState(reg); page.Provider != e.Name || page.Model != "llama" {
		t.Fatalf("after adding, page = %+v", page)
	}
}

// THE CARD MARKS AN ENTRY LOCAL. An oMLX server answering on 8081, a port
// the look does not ask, exists only as an entry added by hand. Marked from the card, beside its address, it
// appears to the checkbox. A save that says nothing of local — a page from
// before the box — keeps the mark; one that says false clears it. The
// saves are decoded from the page's JSON, so the wire's reading is in it.
func TestTheCardMarksAHandAddedEntryLocal(t *testing.T) {
	srv, _ := localTestServer(t, http.StatusOK, "omlx-model")
	hand := providerEntry{Name: "oMLX", APIType: "openai", URL: srv.URL + "/v1", DefaultModel: "omlx-model"}
	a := localApp(t, t.TempDir(), hand)

	save := func(local string) {
		t.Helper()
		var edit dashboard.ProviderEdit
		frame := `{"name":"oMLX","api_type":"openai","endpoint":"` + srv.URL + `/v1","default_model":"omlx-model","chat":true` + local + `}`
		if err := json.Unmarshal([]byte(frame), &edit); err != nil {
			t.Fatal(err)
		}
		if err := a.setProviderInfo(edit); err != nil {
			t.Fatal(err)
		}
	}
	shown := func() *dashboard.LocalModelState {
		t.Helper()
		reg, err := a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		return a.localModelState(reg)
	}

	if page := shown(); page.Marked != 0 || page.Provider != "" {
		t.Fatalf("before marking: page = %+v, want nothing local", page)
	}
	save(`,"local":true`)
	if page := shown(); page.Marked != 1 || page.Provider != "oMLX" || page.Model != "omlx-model" {
		t.Fatalf("marked from the card: page = %+v, want oMLX · omlx-model", page)
	}
	save(``)
	if page := shown(); page.Provider != "oMLX" {
		t.Fatalf("a save that said nothing of local unmarked it: page = %+v", page)
	}
	save(`,"local":false`)
	if page := shown(); page.Marked != 0 {
		t.Fatalf("unchecked on the card: page = %+v, want nothing local", page)
	}
}
