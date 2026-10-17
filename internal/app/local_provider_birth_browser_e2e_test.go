//go:build !windows

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// awaitOnPage polls the page until the predicate returns a value, and fails
// with the predicate's last look at the page when it never does.
func awaitOnPage(t *testing.T, page *cdp, what, predicate string) string {
	t.Helper()
	got, err := page.evalRetry(awaitJS(predicate, 20000), true, 30*time.Second, 3)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if strings.HasPrefix(got, "TIMEOUT") {
		t.Fatalf("%s: %s", what, got)
	}
	return got
}

// BIRTH ON A LOCAL SERVER, FROM THE PAGE: the shipped first-boot form in a
// real Chrome, over the real socket, against the real firstboot handler.
// The seed's local entry asks for the server's address; where nothing
// answers the form says so and Birth waits; where an OpenAI-compatible
// server lists its models, Birth goes and the identity is born on it. The
// address is recorded on the entry by the birth, and no public catalogue is
// asked about the local model on the way.
//
// Coverage limit, named: Blink only, as the projects journey (Gecko exposes
// no debug protocol here); the form's logic runs in both engines in
// internal/dashboard's TestBirthOnALocalServerWaitsForItsAddressToAnswer.
func TestBirthOnALocalServerFromTheFormInARealBrowser(t *testing.T) {
	chrome := needChrome(t)

	// The operator's own server: keyless, OpenAI-compatible, a slashed
	// model name a catalogue would answer to as it stands.
	founding := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"org/local-model"}]}`)
		case "/v1/chat/completions":
			var req struct {
				Messages []llm.Message `json:"messages"`
			}
			if json.NewDecoder(r.Body).Decode(&req) == nil && len(req.Messages) > 0 {
				select {
				case founding <- req.Messages[0].Content:
				default:
				}
			}
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello, I am Mira."},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	// The catalogue is a counter the egress guard admits, so a question
	// about the local model would land here rather than be refused unseen.
	var catalogue atomic.Int64
	cat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catalogue.Add(1)
		http.NotFound(w, r)
	}))
	defer cat.Close()
	oldGuard := modalityGuard
	modalityGuard = func(context.Context, string) error { return nil }
	t.Cleanup(func() { modalityGuard = oldGuard })

	dir := t.TempDir()
	t.Chdir(dir)
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{
		KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.Witness.URL = ""
	// The slot holds the seed's local entry alone, stored where nothing
	// answers, so the page's own listing probe reaches nobody.
	local := seedLocalEntry(t)
	local.URL = "http://127.0.0.1:1/v1"
	catalogueURL := cat.URL
	writeRegistry(t, dir, providerRegistry{ModelCatalogueURL: &catalogueURL, Providers: []providerEntry{local}})

	a := New(cfg)
	defer stopApp(t, a)
	root := genesistest.NewRoot(t)
	a.genesisClient, a.bootstrapText = fetchVerifiedBootstrap(t, root)
	a.ring0Content = "# Constitution\nHonesty."
	a.ring0Bundle = root.Ring0Bundle(t)
	a.ring5Content = root.Ring5(t)
	want := a.bootstrapText
	startFirstbootDashboardForTest(t, a)
	page := openInChrome(t, chrome, a.dashboard.Origin(), "/")

	// 1. The form offers the local entry; choosing it asks for the address.
	awaitOnPage(t, page, "the local entry is offered", `
    const sel = document.getElementById('fb-provider');
    const opt = [...sel.options].find(o => o.textContent === 'Local server');
    last = 'form on: ' + document.getElementById('firstboot').classList.contains('on') + ', options: ' + [...sel.options].map(o => o.textContent).join('|');
    if (!opt || !document.getElementById('firstboot').classList.contains('on')) return null;
    sel.value = opt.value; sel.dispatchEvent(new Event('change'));
    return 'chosen';`)

	// 2. The field holds the entry's address; nothing answers there, and the
	// form says so plainly.
	awaitOnPage(t, page, "a silent address is said plainly", `
    const row = document.getElementById('fb-url-row'), hint = document.getElementById('fb-hint').textContent;
    last = 'row shown: ' + !row.hidden + ', url: ' + document.getElementById('fb-url').value + ', hint: ' + hint;
    if (!row.hidden && document.getElementById('fb-url').value === 'http://127.0.0.1:1/v1' &&
        hint.startsWith('Nothing at http://127.0.0.1:1/v1 answered with its models')) return last;
    return null;`)

	// 3. Birth waits.
	said, err := page.eval(`document.getElementById('fb-birth').click(); document.getElementById('fb-result').textContent`, false, 10*time.Second)
	if err != nil || !strings.Contains(said, "Birth waits until the server at http://127.0.0.1:1/v1 lists its models") {
		t.Fatalf("Birth at a silent address was not refused on the page: %q %v", said, err)
	}
	if fileExists(cfg.Identity.LedgerPath) {
		t.Fatal("Birth at a silent address minted a ledger")
	}

	// 4. The operator types where the server is; it lists its model.
	if _, err := page.eval(`const u = document.getElementById('fb-url'); u.value = '`+server.URL+`/v1'; u.dispatchEvent(new Event('change')); 'typed'`, false, 10*time.Second); err != nil {
		t.Fatalf("type the address: %v", err)
	}
	awaitOnPage(t, page, "the typed address lists its model", `
    const m = document.getElementById('fb-model');
    last = 'model: ' + m.value + ', hint: ' + document.getElementById('fb-hint').textContent;
    return m.value === 'org/local-model' ? last : null;`)

	// 5. Birth goes, to that address, and the identity is born on it.
	if _, err := page.eval(`document.getElementById('fb-birth').click(); 'clicked'`, false, 10*time.Second); err != nil {
		t.Fatalf("click Birth: %v", err)
	}
	awaitOnPage(t, page, "the identity is born and speaks", `
    const text = document.body.innerText;
    last = 'form on: ' + document.getElementById('firstboot').classList.contains('on') + ', result: ' + document.getElementById('fb-result').textContent;
    return text.includes('Hello, I am Mira.') && !document.getElementById('firstboot').classList.contains('on') ? last : null;`)

	select {
	case got := <-founding:
		if got != want {
			t.Fatalf("the founding prompt was not the verified bootstrap: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the local server never received the founding prompt")
	}
	if !fileExists(cfg.Identity.LedgerPath) {
		t.Fatal("no ledger after the birth the page showed")
	}
	reg, err := loadProvidersFile(filepath.Join(dir, "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(reg, "Local server"); e == nil || e.URL != server.URL+"/v1" || !e.Local || e.DefaultModel != "org/local-model" || !e.Default {
		t.Fatalf("the birth did not record the address on the local entry: %+v", e)
	}
	raw, err := os.ReadFile(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		LLM struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"llm"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil || saved.LLM.Provider != "Local server" || saved.LLM.Model != "org/local-model" {
		t.Fatalf("the substrate pointer does not name the local server: %+v %v", saved.LLM, err)
	}
	if n := catalogue.Load(); n != 0 {
		t.Fatalf("the catalogue was asked %d time(s) about a local model", n)
	}
}
