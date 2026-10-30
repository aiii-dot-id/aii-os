package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func (a *App) modelModalitiesFor(ctx context.Context, entry providerEntry, model, baseURL string) modelModalities {
	return a.fetchModelModalities(ctx, modelModalitiesURL(entry, model, baseURL), modalityGuard, (*providerRegistry)(nil).capabilityLookup())
}

// Wait for an asynchronous cache publication, not for another network request.
func waitForModalities(t *testing.T, a *App, key string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if _, ok := a.modalities.get(key); ok {
			return
		}
		select {
		case <-deadline:
			t.Fatal("catalogue answer was not published")
		case <-tick.C:
		}
	}
}

const opusEndpoints = `{"data":{"id":"anthropic/claude-opus-5","architecture":
 {"input_modalities":["text","image","file"],"output_modalities":["text"]}}}`

// modalityServing points the package at a server under the test\'s
// control and lets the fetch past the guard that refuses loopback.
// It records the paths asked for, because WHICH model was asked about is
// the whole contract.
func modalityServing(t *testing.T, status int, body string) (string, *[]string) {
	t.Helper()
	return modalityServingFunc(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

// modalityServingFunc is the same seam with the reply under the test's
// control call by call.
func modalityServingFunc(t *testing.T, reply http.HandlerFunc) (string, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		reply(w, r)
	}))
	oldGuard := modalityGuard
	modalityGuard = func(context.Context, string) error { return nil }
	t.Cleanup(func() {
		modalityGuard = oldGuard
		srv.Close()
	})
	return srv.URL + "/models", &asked
}

// ONE MODEL, ONE CALL. The author comes from the provider entry; the
// model is the one the operator just chose.
func TestOneModelIsAskedAboutByName(t *testing.T) {
	baseURL, asked := modalityServing(t, 200, opusEndpoints)
	got := new(App).modelModalitiesFor(context.Background(),
		providerEntry{Name: "Anthropic", CatalogueAuthor: "anthropic"}, "claude-opus-5", baseURL)
	if strings.Join(got.in, ",") != "text,image,file" {
		t.Fatalf("input modalities = %v", got.in)
	}
	if len(*asked) != 1 {
		t.Fatalf("asked %d times, want exactly one", len(*asked))
	}
	if want := "/models/anthropic/claude-opus-5/endpoints"; (*asked)[0] != want {
		t.Fatalf("asked for %q, want %q", (*asked)[0], want)
	}
}

// A model id that already carries its author is used as it stands —
// that is how the aggregator\'s own entries and a host like Groq spell
// theirs, and prefixing again would name a model nobody serves.
func TestAnAuthorQualifiedModelIsNotPrefixedTwice(t *testing.T) {
	baseURL, asked := modalityServing(t, 200, opusEndpoints)
	new(App).modelModalitiesFor(context.Background(),
		providerEntry{Name: "Groq", CatalogueAuthor: "groq"}, "openai/gpt-oss-120b", baseURL)
	if want := "/models/openai/gpt-oss-120b/endpoints"; (*asked)[0] != want {
		t.Fatalf("asked for %q, want %q", (*asked)[0], want)
	}
}

// Uncatalogued means NO QUESTION IS ASKED. A provider the operator added
// themselves has no author, and inventing one would ask about a model
// that does not exist.
func TestAProviderWithNoAuthorIsNotAsked(t *testing.T) {
	baseURL, asked := modalityServing(t, 200, opusEndpoints)
	got := new(App).modelModalitiesFor(context.Background(), providerEntry{Name: "mine"}, "some-model", baseURL)
	if got.known() {
		t.Fatal("an uncatalogued provider was given modalities")
	}
	if len(*asked) != 0 {
		t.Fatalf("a request was made anyway: %v", *asked)
	}
}

// A model id is not a place to discover that a name can carry a path.
func TestASlugThatIsNotTwoPlainSegmentsIsRefused(t *testing.T) {
	for _, model := range []string{"../../etc/passwd", "a/b/c", "has space", "q?x=1"} {
		if got := modalitySlug(providerEntry{CatalogueAuthor: "anthropic"}, model); got != "" {
			t.Fatalf("%q produced slug %q", model, got)
		}
	}
}

// Every failure is unknown. An unreachable prior is a missing prior.
func TestALookupFailureIsUnknownNotTextOnly(t *testing.T) {
	baseURL, _ := modalityServing(t, 503, `{}`)
	got := new(App).modelModalitiesFor(context.Background(),
		providerEntry{CatalogueAuthor: "anthropic"}, "claude-opus-5", baseURL)
	if got.known() {
		t.Fatal("a failed lookup invented modalities")
	}
}

// The body is bounded: a public endpoint we do not own.
func TestTheLookupBodyIsBounded(t *testing.T) {
	baseURL, _ := modalityServing(t, 200, `{"data":{"x":"`+strings.Repeat("y", defaultCatalogueMaxBytes+64)+`"}}`)
	got := new(App).modelModalitiesFor(context.Background(),
		providerEntry{CatalogueAuthor: "anthropic"}, "claude-opus-5", baseURL)
	if got.known() {
		t.Fatal("an oversized body was parsed")
	}
}

// The probe keeps what the lookup said, beside what the endpoint proved,
// and the one resolver answers from both.
func TestTheProbeKeepsTheModalitiesItLookedUp(t *testing.T) {
	baseURL, _ := modalityServing(t, 200, opusEndpoints)
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "claude-opus-5", Provider: "anthropic"}
	entry := providerEntry{Name: "p", CatalogueAuthor: "anthropic", CapabilityCatalogue: &baseURL}
	if err := probeAndRun(a, cc, entry, &providerRegistry{}, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("probe: %v", err)
	}
	waitForModalities(t, a, modelModalitiesURL(entry, cc.Model, baseURL))
	got := a.modelCapabilitiesFor(&providerRegistry{}, entry, "claude-opus-5")
	if got.tools.state != capYes || got.tools.from != capFromMeasured {
		t.Fatalf("tools = %+v", got.tools)
	}
	if pdf := got.input["pdf"]; pdf.state != capYes || pdf.from != capFromCatalogue || pdf.at.IsZero() {
		t.Fatalf("the catalogue's file kind did not answer pdf: %+v", pdf)
	}
	if audio := got.input["audio"]; audio.state != capNo || audio.from != capFromCatalogue {
		t.Fatalf("a kind the catalogue's answer leaves out is not a no: %+v", audio)
	}
}

// A third party never decides a substrate.
func TestALookupFailureDoesNotRefuseASubstrate(t *testing.T) {
	baseURL, _ := modalityServing(t, 500, `{}`)
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	entry := providerEntry{Name: "p", CatalogueAuthor: "anthropic", CapabilityCatalogue: &baseURL}
	if err := probeAndRun(a, cc, entry, &providerRegistry{}, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("a third party decided a substrate: %v", err)
	}
	if got := a.modelCapabilitiesFor(&providerRegistry{}, entry, "m").input["image"]; got.state != capUnknown {
		t.Fatalf("modalities invented from a failure: %+v", got)
	}
}

// The shipped registry carries an author for the providers whose models
// a catalogue publishes — the fill, not a Go table.
func TestTheShippedRegistryNamesItsCatalogueAuthors(t *testing.T) {
	reg := embeddedRegistry()
	want := map[string]string{
		"Anthropic": "anthropic", "OpenAI": "openai", "zAI": "z-ai",
		"Google Gemini": "google", "DeepSeek": "deepseek",
	}
	got := map[string]string{}
	for _, e := range reg.Providers {
		if e.CatalogueAuthor != "" {
			got[e.Name] = e.CatalogueAuthor
		}
	}
	for name, author := range want {
		if got[name] != author {
			t.Errorf("%s catalogue_author = %q, want %q", name, got[name], author)
		}
	}
}

// A vendor fill is not the operator\'s edit: a shipped author must not
// invalidate every cached provider probe.
func TestTheCatalogueAuthorIsNotPartOfTheProbeKey(t *testing.T) {
	a := providerEntry{Name: "p", URL: "https://x"}
	b := a
	b.CatalogueAuthor = "anthropic"
	if probeKey(a) != probeKey(b) {
		t.Fatal("a vendor-supplied author changed the probe key")
	}
}

// age back-dates everything remembered, so a test can reach the far side
// of the TTL without spending it. It lives here because it is a thing
// tests need and the runtime never does.
func (m *modalityMemo) age(by time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.by {
		e.at = e.at.Add(-by)
		m.by[k] = e
	}
}

// A SECOND LOOK AT THE SAME MODEL ASKS NOTHING. Trying one substrate,
// then another, then coming back is how a choice actually gets made; it
// is not a reason to ask a public catalogue the same question again.
func TestTheSameModelIsNotAskedAboutTwice(t *testing.T) {
	baseURL, asked := modalityServing(t, 200, opusEndpoints)
	a, entry := new(App), providerEntry{Name: "Anthropic", CatalogueAuthor: "anthropic"}
	first := a.modelModalitiesFor(context.Background(), entry, "claude-opus-5", baseURL)
	second := a.modelModalitiesFor(context.Background(), entry, "claude-opus-5", baseURL)
	if !first.known() {
		t.Fatal("the first lookup answered nothing; the test proves nothing")
	}
	if strings.Join(second.in, ",") != strings.Join(first.in, ",") {
		t.Fatalf("remembered %v, first said %v", second.in, first.in)
	}
	if len(*asked) != 1 {
		t.Fatalf("asked %d times, want exactly one", len(*asked))
	}
}

// A DIFFERENT MODEL IS A DIFFERENT QUESTION: the memo is keyed by the
// model asked about, so remembering one never answers for another.
func TestEachModelIsRememberedSeparately(t *testing.T) {
	baseURL, asked := modalityServing(t, 200, opusEndpoints)
	a, entry := new(App), providerEntry{CatalogueAuthor: "anthropic"}
	a.modelModalitiesFor(context.Background(), entry, "claude-opus-5", baseURL)
	a.modelModalitiesFor(context.Background(), entry, "claude-haiku-4.5", baseURL)
	if len(*asked) != 2 {
		t.Fatalf("asked %v, want one question per model", *asked)
	}
}

// A FAILURE IS NEVER REMEMBERED. Keeping one would turn a single
// unreachable moment into a lasting verdict about the model — the exact
// mistake the lookup itself refuses to make.
func TestAFailedLookupIsNotRemembered(t *testing.T) {
	var n atomic.Int32
	baseURL, asked := modalityServingFunc(t, func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		io.WriteString(w, opusEndpoints)
	})
	a, entry := new(App), providerEntry{CatalogueAuthor: "anthropic"}
	if got := a.modelModalitiesFor(context.Background(), entry, "claude-opus-5", baseURL); got.known() {
		t.Fatal("a failed lookup invented modalities")
	}
	if got := a.modelModalitiesFor(context.Background(), entry, "claude-opus-5", baseURL); !got.known() {
		t.Fatal("the failure was kept: the model now has no modalities for as long as the process lives")
	}
	if len(*asked) != 2 {
		t.Fatalf("asked %d times, want the second question asked", len(*asked))
	}
}

// A REMEMBERED ANSWER EXPIRES, because a catalogue's answer about a name
// already in service can change under it. The memo is a convenience
// spanning one sitting, not a record.
func TestARememberedAnswerExpires(t *testing.T) {
	baseURL, asked := modalityServing(t, 200, opusEndpoints)
	a, entry := new(App), providerEntry{CatalogueAuthor: "anthropic"}
	a.modelModalitiesFor(context.Background(), entry, "claude-opus-5", baseURL)
	a.modalities.age(defaultCapabilityKeep + time.Minute)
	a.modelModalitiesFor(context.Background(), entry, "claude-opus-5", baseURL)
	if len(*asked) != 2 {
		t.Fatalf("asked %d times, want an expired answer asked again", len(*asked))
	}
}

// The memo has a CEILING, because its keys come from provider model
// lists and not from us.
func TestTheMemoDoesNotGrowWithoutBound(t *testing.T) {
	var m modalityMemo
	for i := 0; i < modalityMemoMax*2+1; i++ {
		m.put(fmt.Sprintf("author/model-%d", i), modelModalities{in: []string{"text"}})
	}
	if len(m.by) > modalityMemoMax {
		t.Fatalf("memo holds %d entries, ceiling is %d", len(m.by), modalityMemoMax)
	}
}

func TestAPartialCatalogueAnswerDoesNotRetractAnUnansweredCapability(t *testing.T) {
	var memo modalityMemo
	memo.put("one-source/model", modelModalities{in: []string{"text", "image"}, out: []string{"text"}})
	memo.put("one-source/model", modelModalities{out: []string{"audio"}})
	got := memo.last("one-source/model")
	if !slices.Contains(got.in, "image") || !slices.Equal(got.out, []string{"audio"}) {
		t.Fatalf("partial update lost a known input or kept the superseded output: %+v", got)
	}
	memo.put("another-source/model", modelModalities{out: []string{"text"}})
	if memo.last("another-source/model").in != nil {
		t.Fatal("an unrelated source inherited the input evidence")
	}
}

func TestAFullMemoDoesNotDiscardOtherModelsWhenOneAnswerChanges(t *testing.T) {
	var memo modalityMemo
	for i := 0; i < modalityMemoMax; i++ {
		memo.put(fmt.Sprint(i), modelModalities{in: []string{"text", "image"}})
	}
	memo.put("0", modelModalities{in: []string{"text"}})
	if len(memo.by) != modalityMemoMax || !slices.Contains(memo.last("1").in, "image") {
		t.Fatal("updating an answer at capacity erased another model's image support")
	}
}

func TestModelCatalogueConfigurationReachesTheProbeAndSurvivesEdits(t *testing.T) {
	firstURL, firstAsked := modalityServing(t, 200, opusEndpoints)
	secondURL, secondAsked := modalityServing(t, 200, strings.ReplaceAll(opusEndpoints, `"text","image","file"`, `"audio"`))
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "fixture", Model: "claude-opus-5", Provider: "anthropic"}
	path := filepath.Join(t.TempDir(), "providers.json")
	for _, tc := range []struct{ base, kind string }{
		{firstURL + "/", "image"}, {secondURL, "audio"}, {"", ""},
	} {
		data, err := json.Marshal(map[string]any{
			"providers": []map[string]string{{"name": "fixture", "api_type": "anthropic", "url": srv.URL, "catalogue_author": "anthropic", "capability_catalogue": tc.base}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		reg, err := loadProvidersFile(path)
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := candidateRegistry(reg, func(r *providerRegistry) error {
			r.Providers[0].DefaultModel = "a-provider-edit"
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := saveProvidersFile(path, candidate); err != nil {
			t.Fatal(err)
		}
		reg, err = loadProvidersFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := reg.Providers[0].CapabilityCatalogue; got == nil || *got != tc.base {
			t.Fatal("provider edit lost the configured catalogue address")
		}
		if err := probeAndRun(a, cc, reg.Providers[0], reg, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
			t.Fatal(err)
		}
		if tc.kind == "" {
			if url := modelModalitiesURL(reg.Providers[0], cc.Model, catalogueFor(reg.Providers[0])); url != "" {
				t.Fatalf("a catalogue turned off was given an address: %s", url)
			}
			continue
		}
		waitForModalities(t, a, modelModalitiesURL(reg.Providers[0], cc.Model, catalogueFor(reg.Providers[0])))
		if got := a.modelCapabilitiesFor(reg, reg.Providers[0], cc.Model).input[tc.kind]; got.state != capYes || got.from != capFromCatalogue {
			t.Fatalf("catalogue %q: %s = %+v", tc.base, tc.kind, got)
		}
	}
	if len(*firstAsked) != 1 || len(*secondAsked) != 1 {
		t.Fatalf("lookup counts: first=%d second=%d", len(*firstAsked), len(*secondAsked))
	}
}

// THE SHIPPED FILE CARRIES TODAY'S CHOICE AS DATA, lent to an entry of a
// shipped name whose line says nothing, and never written back as the
// operator's. A file that still states the earlier file-wide address lends
// that one instead, and its "" lends none.
func TestOmittedModelCatalogueUsesJSONDefaultWithoutPersistingIt(t *testing.T) {
	shipped := entryNamed(embeddedRegistry(), "OpenRouter")
	if shipped == nil || shipped.CapabilityCatalogue == nil || *shipped.CapabilityCatalogue == "" {
		t.Fatal("the shipped OpenRouter entry names no catalogue")
	}
	for _, tc := range []struct{ top, want string }{
		{"", *shipped.CapabilityCatalogue},
		{`"model_catalogue_url":"https://mirror.example/api/v1/models",`, "https://mirror.example/api/v1/models"},
		{`"model_catalogue_url":"",`, ""},
	} {
		path := filepath.Join(t.TempDir(), "providers.json")
		if err := os.WriteFile(path, []byte(`{`+tc.top+`"providers":[{"name":"OpenRouter","url":"https://openrouter.ai/api/v1"}]}`), 0600); err != nil {
			t.Fatal(err)
		}
		reg, err := loadProvidersFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := catalogueFor(reg.Providers[0]); got != tc.want {
			t.Fatalf("%s: lent catalogue %q, want %q", tc.top, got, tc.want)
		}
		if _, err := saveProvidersFile(path, reg); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "capability_catalogue") {
			t.Fatalf("save froze the lent catalogue as an explicit override: %s", raw)
		}
	}
}

func TestOptionalCatalogueDoesNotDelayProviderAcceptance(t *testing.T) {
	for _, laterSelection := range []bool{false, true} {
		t.Run(fmt.Sprintf("later-selection=%v", laterSelection), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			baseURL, _ := modalityServingFunc(t, func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				select {
				case <-release:
					io.WriteString(w, opusEndpoints)
				case <-r.Context().Done():
				}
			})
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			srv := substrateProbeServer(t, 200, `{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
			a := probeApp(t)
			a.bgCtx = t.Context()
			cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "fixture", Model: "m", Provider: "anthropic"}
			entry := providerEntry{Name: "first", CatalogueAuthor: "author", CapabilityCatalogue: &baseURL}
			done := make(chan error, 1)
			go func() {
				done <- probeAndRun(a, cc, entry, &providerRegistry{}, 5)
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("catalogue not queried")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("ready provider is waiting for the optional catalogue")
			}
			if got := a.modelCapabilitiesFor(&providerRegistry{}, entry, cc.Model); got.tools.state != capYes || got.input["image"].state != capUnknown {
				t.Fatalf("ready provider with unanswered metadata: %+v", got)
			}
			disabled := ""
			second := providerEntry{Name: "second", CatalogueAuthor: "author", CapabilityCatalogue: &disabled}
			if laterSelection {
				if err := probeAndRun(a, cc, second, &providerRegistry{}, 5); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			waitForModalities(t, a, modelModalitiesURL(entry, cc.Model, baseURL))
			if laterSelection {
				if got := a.modelCapabilitiesFor(&providerRegistry{}, second, cc.Model); got.tools.state != capYes || got.input["image"].state != capUnknown {
					t.Fatalf("late answer overwrote newer selection: %+v", got)
				}
			}
			if got := a.modelCapabilitiesFor(&providerRegistry{}, entry, cc.Model).input["image"]; got.state != capYes || got.from != capFromCatalogue {
				t.Fatalf("late answer was lost: %+v", got)
			}
		})
	}
}

func TestOptionalCatalogueStopsOnShutdownOrProbeFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("refusal=%v", fail), func(t *testing.T) {
			entered, stopped := make(chan struct{}), make(chan struct{})
			baseURL, _ := modalityServingFunc(t, func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-r.Context().Done()
				close(stopped)
			})
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-entered:
				case <-r.Context().Done():
					return
				}
				if fail {
					w.WriteHeader(401)
				}
				io.WriteString(w, `{"content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`)
			}))
			t.Cleanup(srv.Close)
			a := probeApp(t)
			a.bgCtx = ctx
			cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "fixture", Model: "m", Provider: "anthropic", Retries: -1}
			err := probeAndRun(a, cc, providerEntry{Name: "p", CatalogueAuthor: "author", CapabilityCatalogue: &baseURL}, &providerRegistry{}, 5)
			if (err != nil) != fail {
				t.Fatalf("probe refusal=%v: %v", fail, err)
			}
			if !fail {
				cancel()
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("catalogue lookup survived cancellation")
			}
		})
	}
}

// Native metadata fills only the questions it answered, kind by kind, and
// outranks the catalogue on each. The catalogue is asked while any kind is
// unanswered, and not when the provider's row answers every one.
func TestSelectedModelCapabilitiesUseNativeDataBeforeTheCatalogue(t *testing.T) {
	for _, tc := range []struct {
		name, data    string
		wantCatalogue int
		want          *bool
	}{
		{"positive", `{"data":[{"id":"m","architecture":{"input_modalities":["text","image"]}}]}`, 1, boolp(true)},
		{"negative", `{"data":[{"id":"m","capabilities":{"vision":false}}]}`, 1, boolp(false)},
		{"partial", `{"data":[{"id":"m","context_length":32768}]}`, 1, nil},
		{"complete", `{"data":[{"id":"m","architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},"supported_parameters":["tools"]}]}`, 0, boolp(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, asked := modalityServing(t, http.StatusOK, opusEndpoints)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, tc.data) }))
			t.Cleanup(srv.Close)
			a := probeApp(t)
			e := providerEntry{Name: "native", URL: srv.URL, APIType: "openai", CatalogueAuthor: "fixture", CapabilityCatalogue: &base}
			reg := &providerRegistry{Providers: []providerEntry{e}}
			a.refreshSelectedModalities(t.Context(), e, "m", reg, modalityGuard, (*App).discoverMetaForEntry)
			if len(*asked) != tc.wantCatalogue {
				t.Fatalf("catalogue requests=%d, want %d", len(*asked), tc.wantCatalogue)
			}
			got := nativeImage(a, e, "m")
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("native support=%v, want %v", got, tc.want)
			}
			image := a.modelCapabilitiesFor(reg, e, "m").input["image"]
			if tc.want != nil && (image.from != capFromProvider || (image.state == capYes) != *tc.want) {
				t.Fatalf("the catalogue outranked the provider: %+v", image)
			}
			if tc.want == nil && (image.from != capFromCatalogue || image.state != capYes) {
				t.Fatalf("an unanswered kind was not left to the catalogue: %+v", image)
			}
		})
	}
	// A row that answers every kind leaves nothing to ask.
	base, asked := modalityServing(t, http.StatusOK, opusEndpoints)
	a := probeApp(t)
	e := providerEntry{Name: "native", URL: "https://provider.example/v1", APIType: "openai", CatalogueAuthor: "fixture", CapabilityCatalogue: &base}
	all := modelMeta{Input: map[string]bool{}, Output: map[string]bool{}, Tools: boolp(true)}
	for _, k := range inputKinds {
		all.Input[k] = true
	}
	for _, k := range outputKinds {
		all.Output[k] = k == "text"
	}
	a.providers.runtime.provStatus = map[string]providerProbe{e.Name: {
		key: probeKey(e), state: probeOK, checkedAt: time.Now(), models: []string{"m"}, meta: map[string]modelMeta{"m": all},
	}}
	a.refreshSelectedModalities(t.Context(), e, "m", &providerRegistry{Providers: []providerEntry{e}}, modalityGuard, (*App).discoverMetaForEntry)
	if len(*asked) != 0 {
		t.Fatalf("a complete provider answer still asked the catalogue %d time(s)", len(*asked))
	}
}

// nativeImage is what the endpoint's own list said of pictures, nil for
// nothing.
func nativeImage(a *App, e providerEntry, model string) *bool {
	row, _ := a.discoveredCapabilities(e, model)
	return saidOf(row.Input, "image")
}

// A live chat endpoint need not implement a responsive model list. Native
// discovery timing out must leave a real opportunity for the optional
// catalogue, rather than handing it an already cancelled context.
func TestNativeDiscoveryTimeoutLeavesTimeForTheSelectedCatalogue(t *testing.T) {
	base, asked := modalityServing(t, http.StatusOK, opusEndpoints)
	entered, stopped := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(stopped)
	}))
	t.Cleanup(srv.Close)
	a := probeApp(t)
	e := providerEntry{Name: "native", URL: srv.URL, APIType: "openai", CatalogueAuthor: "fixture", CapabilityCatalogue: &base}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	a.refreshSelectedModalities(ctx, e, "m", &providerRegistry{Providers: []providerEntry{e}}, modalityGuard, (*App).discoverMetaForEntry)
	select {
	case <-entered:
	default:
		t.Fatal("the native model list was not asked first")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the timed-out native request was not cancelled")
	}
	if len(*asked) != 1 {
		t.Fatalf("catalogue requests=%d after native timeout, want 1", len(*asked))
	}
	if got := a.modalities.last(modelModalitiesURL(e, "m", base)); !slices.Contains(got.in, "image") {
		t.Fatalf("native timeout prevented the catalogue answer: %v", got.in)
	}
	if _, ok := a.discoveredCapabilities(e, "m"); ok {
		t.Fatal("a native timeout was recorded as a capability answer")
	}
}

// Test the startup wrapper's actual budget without spending wall-clock
// seconds. Cancelling the native leg must not cancel the catalogue leg;
// shutdown still owns and joins the whole optional background operation.
func TestStartupAndSelectionReserveTheCatalogueLegAfterNativeTimeout(t *testing.T) {
	oldDiscovery, oldGuard := selectedModelDiscovery, modalityGuard
	t.Cleanup(func() { selectedModelDiscovery, modalityGuard = oldDiscovery, oldGuard })
	for _, selection := range []bool{false, true} {
		t.Run(fmt.Sprintf("selection=%v", selection), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				started := time.Now()
				var nativeBound time.Duration
				selectedModelDiscovery = func(_ *App, ctx context.Context, _ providerEntry, _ string) ([]string, map[string]modelMeta, error) {
					deadline, ok := ctx.Deadline()
					if !ok {
						t.Error("native discovery has no deadline")
						return nil, nil, fmt.Errorf("missing native deadline")
					}
					nativeBound = time.Until(deadline)
					<-ctx.Done()
					return nil, nil, ctx.Err()
				}
				catalogue := make(chan error, 1)
				modalityGuard = func(ctx context.Context, _ string) error {
					catalogue <- ctx.Err()
					return fmt.Errorf("synthetic catalogue stop")
				}
				dir := t.TempDir()
				e := providerEntry{Name: "fixture", APIType: "openai", URL: "https://provider.example/v1", CatalogueAuthor: "fixture", CapabilityCatalogue: &testCatalogue}
				writeTestProviders(t, dir, e)
				a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
				wantBound := defaultCapabilityTimeout
				if selection {
					wantBound = time.Second
					client := substrateProbeCall(func(context.Context, []llm.Message, llm.ChatOptions) (*llm.Response, error) {
						return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "OK"}}}}, nil
					})
					_, err := a.probeSubstrate(client, llm.ClientConfig{APIKey: "fixture", Model: "model"}, e, &providerRegistry{Providers: []providerEntry{e}}, 1)
					if err != nil {
						t.Fatal(err)
					}
					if time.Since(started) != 0 {
						t.Fatal("optional metadata delayed provider acceptance")
					}
				} else {
					a.startSelectedModalities(e, "model")
				}
				if err := <-catalogue; err != nil {
					t.Errorf("catalogue inherited the native timeout: %v", err)
				}
				if nativeBound != wantBound || time.Since(started) != wantBound {
					t.Errorf("native timeout=%s, elapsed=%s, want %s each", nativeBound, time.Since(started), wantBound)
				}
				if err := a.Shutdown(t.Context()); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestSelectionRefreshesOldNativeCapabilitiesAndPreservesThemOnFailure(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			// A local entry's server is also asked its own window routes
			// (askLocalWindows); this one has none, and they are not the
			// list's requests this test counts.
			http.NotFound(w, r)
			return
		}
		if requests.Add(1) == 1 {
			io.WriteString(w, `{"data":[{"id":"m","capabilities":{"vision":false}}]}`)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	a := probeApp(t)
	e := providerEntry{Name: "native", URL: srv.URL, APIType: "openai", Local: true}
	a.providers.runtime.provStatus = map[string]providerProbe{e.Name: {
		key: probeKey(e), state: probeOK, checkedAt: time.Now().Add(-2 * defaultModelListCacheSeconds * time.Second),
		meta: map[string]modelMeta{"m": {Input: imageKinds(boolp(true))}},
	}}
	reg := &providerRegistry{Providers: []providerEntry{e}}
	a.refreshSelectedModalities(t.Context(), e, "m", reg, modalityGuard, (*App).discoverMetaForEntry)
	if got := nativeImage(a, e, "m"); got == nil || *got {
		t.Fatal("selection retained an expired positive over the provider's new negative")
	}
	a.providers.runtime.mu.Lock()
	st := a.providers.runtime.provStatus[e.Name]
	st.checkedAt = time.Now().Add(-2 * defaultModelListCacheSeconds * time.Second)
	a.providers.runtime.provStatus[e.Name] = st
	a.providers.runtime.mu.Unlock()
	a.refreshSelectedModalities(t.Context(), e, "m", reg, modalityGuard, (*App).discoverMetaForEntry)
	if got := nativeImage(a, e, "m"); got == nil || *got {
		t.Fatal("a failed refresh erased or inverted a successful native answer")
	}
	if requests.Load() != 2 {
		t.Fatalf("native requests=%d, want 2", requests.Load())
	}
}

func TestPartialNativeListsKeepOnlyUnansweredSameEndpointCapabilities(t *testing.T) {
	for _, support := range []bool{false, true} {
		previous := providerProbe{key: "endpoint-a", meta: map[string]modelMeta{
			"m":       {Input: map[string]bool{"image": support, "pdf": !support}, Output: map[string]bool{"audio": support}, Tools: boolp(support)},
			"removed": {Input: imageKinds(boolp(true))},
		}}
		fresh := providerProbe{key: "endpoint-a", state: probeOK, models: []string{"m"}, meta: map[string]modelMeta{"m": {Context: 12345}}}
		got := retainNativeCapabilities(fresh, previous)
		m := got.meta["m"]
		if m.Input["image"] != support || m.Input["pdf"] != !support || m.Output["audio"] != support || m.Tools == nil || *m.Tools != support || m.Context != 12345 {
			t.Fatalf("partial native list erased support=%v or fresh context: %+v", support, m)
		}
		if _, exists := got.meta["removed"]; exists {
			t.Fatal("a removed model was resurrected by retained metadata")
		}
		other := retainNativeCapabilities(providerProbe{key: "endpoint-b", state: probeOK, models: []string{"m"}}, previous)
		if other.meta["m"].answers() {
			t.Fatal("another endpoint inherited the capability")
		}
		replaced := retainNativeCapabilities(providerProbe{key: "endpoint-a", state: probeOK, models: []string{"m"}, meta: map[string]modelMeta{"m": {Input: imageKinds(boolp(!support))}}}, previous)
		if replaced.meta["m"].Input["image"] == support || replaced.meta["m"].Input["pdf"] != !support {
			t.Fatal("explicit native replacement was ignored, or took an unanswered kind with it")
		}
	}
}

func TestAFailedEventRefreshDoesNotEraseTheLastCatalogueAnswer(t *testing.T) {
	base, asked := modalityServing(t, http.StatusServiceUnavailable, `{}`)
	a := probeApp(t)
	e := providerEntry{Name: "public", CatalogueAuthor: "fixture", CapabilityCatalogue: &base}
	key := modelModalitiesURL(e, "m", base)
	a.modalities.put(key, modelModalities{in: []string{"text", "image"}})
	a.modalities.age(defaultCapabilityKeep + time.Hour)
	a.refreshSelectedModalities(t.Context(), e, "m", &providerRegistry{}, modalityGuard, (*App).discoverMetaForEntry)
	if got := a.modalities.last(key); !slices.Contains(got.in, "image") {
		t.Fatalf("last good answer was erased: %v", got.in)
	}
	if len(*asked) != 1 {
		t.Fatalf("refresh requests=%d, want 1", len(*asked))
	}
}

func TestALocalModelSelectionNeverAsksThePublicCatalogue(t *testing.T) {
	base, asked := modalityServing(t, http.StatusOK, opusEndpoints)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"data":[{"id":"private/alias"}]}`) }))
	t.Cleanup(srv.Close)
	a := probeApp(t)
	// Whatever the entry's line says: the loader refuses it, and an entry
	// built anywhere else is not asked either.
	e := providerEntry{Name: "local", Local: true, URL: srv.URL, APIType: "openai", CapabilityCatalogue: &base}
	a.refreshSelectedModalities(t.Context(), e, "private/alias", &providerRegistry{Providers: []providerEntry{e}, ModelCatalogueURL: &base}, modalityGuard, (*App).discoverMetaForEntry)
	if len(*asked) != 0 {
		t.Fatalf("a local model name was disclosed: %v", *asked)
	}
	if got := nativeImage(a, e, "private/alias"); got != nil {
		t.Fatalf("missing metadata invented an answer: %v", *got)
	}
}

func TestShutdownCancelsAndJoinsSelectedCapabilityDiscovery(t *testing.T) {
	old := selectedModelDiscovery
	t.Cleanup(func() { selectedModelDiscovery = old })
	synctest.Test(t, func(t *testing.T) {
		entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		selectedModelDiscovery = func(_ *App, ctx context.Context, _ providerEntry, _ string) ([]string, map[string]modelMeta, error) {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-release
			return nil, nil, ctx.Err()
		}
		dir := t.TempDir()
		e := providerEntry{Name: "local", Local: true, APIType: "openai", URL: "http://127.0.0.1:1/v1"}
		writeTestProviders(t, dir, e)
		a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
		a.startSelectedModalities(e, "private-model")
		<-entered
		done := make(chan struct{})
		go func() { _ = a.Shutdown(context.Background()); close(done) }()
		<-cancelled
		synctest.Wait()
		early := false
		select {
		case <-done:
			early = true
		default:
		}
		close(release)
		<-done
		if early {
			t.Fatal("shutdown returned before its capability-discovery goroutine finished")
		}
	})
}

func TestEachKindIsAnsweredByTheHighestSourceThatSaysAnything(t *testing.T) {
	const model = "claude-opus-5"
	tiers := []string{capFromOperator, capFromProvider, capFromCatalogue, capFromShipped}
	cat := "https://catalogue.example/api/v1/models"
	entry := providerEntry{Name: "p", URL: "https://p.example/v1", CatalogueAuthor: "anthropic", CapabilityCatalogue: &cat}
	for _, side := range []string{"input", "output"} {
		kinds := map[string][]string{"input": inputKinds, "output": outputKinds}[side]
		for _, kind := range kinds {
			row := embeddedRegistry().ModelCapabilities[model]
			shipped := map[string]*bool{"input": row.takes(kind), "output": row.gives(kind)}[side]
			for top := range len(tiers) + 1 {
				for _, says := range []bool{true, false} {
					// What the tier says, and whether it says anything.
					word := func(tier int) (bool, bool) { return says == (tier == top), tier >= top }
					one := func(v bool) map[string]bool { return map[string]bool{kind: v} }
					a := probeApp(t)
					reg := &providerRegistry{}
					if v, ok := word(0); ok {
						row := modelCapability{Input: one(v)}
						if side == "output" {
							row = modelCapability{Output: one(v)}
						}
						reg.ModelCapabilities = map[string]modelCapability{model: row}
					}
					if v, ok := word(1); ok {
						row := modelMeta{Input: one(v)}
						if side == "output" {
							row = modelMeta{Output: one(v)}
						}
						a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {key: probeKey(entry), state: probeOK, meta: map[string]modelMeta{model: row}}}
					}
					if v, ok := word(2); ok {
						list := []string{"x"} // an answer that leaves the kind out
						if v {
							list = []string{catalogueKinds[kind]}
						}
						mods := modelModalities{in: []string{"x"}, out: list}
						if side == "input" {
							mods = modelModalities{in: list, out: []string{"x"}}
						}
						a.modalities.put(modelModalitiesURL(entry, model, cat), mods)
					}
					got := a.modelCapabilitiesFor(reg, entry, model)
					answer := map[string]map[string]capAnswer{"input": got.input, "output": got.output}[side][kind]
					wantState, wantFrom := capUnknown, ""
					switch {
					case top < len(tiers)-1:
						wantState, wantFrom = capNo, tiers[top]
						if says {
							wantState = capYes
						}
					case shipped != nil:
						wantState, wantFrom = capStateOf(shipped), capFromShipped
					}
					if top == len(tiers)-1 && shipped != nil && *shipped != says {
						continue // the shipped row says what it says
					}
					if answer.state != wantState || answer.from != wantFrom {
						t.Fatalf("%s.%s, top source %d saying %v: got %s from %q, want %s from %q", side, kind, top, says, answer.state, answer.from, wantState, wantFrom)
					}
				}
			}
		}
	}
}

// Tools are answered in the same order, with what the selection measured on
// this endpoint first among the provider's answers.
func TestToolsAreAnsweredByTheOperatorThenTheMeasurementThenTheList(t *testing.T) {
	a := probeApp(t)
	entry := providerEntry{Name: "p", URL: "https://p.example/v1"}
	reg := &providerRegistry{}
	if got := a.modelCapabilitiesFor(reg, entry, "m").tools; got.state != capUnknown {
		t.Fatalf("nobody said, got %+v", got)
	}
	a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {key: probeKey(entry), state: probeOK, meta: map[string]modelMeta{"m": {Tools: boolp(false)}}}}
	if got := a.modelCapabilitiesFor(reg, entry, "m").tools; got.state != capNo || got.from != capFromProvider {
		t.Fatalf("the list's no, got %+v", got)
	}
	a.setSubstrateCapability(substrateCapability{provider: "p", model: "m", toolCalls: capYes, checkedAt: time.Now()})
	if got := a.modelCapabilitiesFor(reg, entry, "m").tools; got.state != capYes || got.from != capFromMeasured || got.at.IsZero() {
		t.Fatalf("the measurement did not outrank the list: %+v", got)
	}
	reg.ModelCapabilities = map[string]modelCapability{"m": {Tools: boolp(false)}}
	if got := a.modelCapabilitiesFor(reg, entry, "m").tools; got.state != capNo || got.from != capFromOperator {
		t.Fatalf("the operator's no did not outrank the measurement: %+v", got)
	}
}

// "images" IS THE OLD SPELLING OF input.image, still read: a row that says
// it is the operator's answer about pictures. A row that says both,
// differently, is refused by its path, and so is a kind outside either
// vocabulary, by name.
func TestTheOldSpellingIsReadAndABadRowIsRefusedByItsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	load := func(rows string) (*providerRegistry, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"providers":[],"model_capabilities":`+rows+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return loadProvidersFile(path)
	}
	reg, err := load(`{"m":{"images":false}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := probeApp(t).modelCapabilitiesFor(reg, providerEntry{Name: "p"}, "m").input["image"]; got.state != capNo || got.from != capFromOperator {
		t.Fatalf("images: false was not read as the operator's input.image: %+v", got)
	}
	if _, err := load(`{"m":{"images":true,"input":{"image":true}}}`); err != nil {
		t.Fatalf("two spellings that agree were refused: %v", err)
	}
	for rows, want := range map[string]string{
		`{"m":{"images":true,"input":{"image":false}}}`: `model_capabilities["m"]: "images" and "input.image" disagree`,
		`{"m":{"input":{"document":true}}}`:             `model_capabilities["m"].input: "document" is not a kind (want image, pdf, audio, video)`,
		`{"m":{"output":{"pdf":true}}}`:                 `model_capabilities["m"].output: "pdf" is not a kind (want text, image, audio, video)`,
	} {
		if _, err := load(rows); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: got %v, want a refusal naming %s", rows, err, want)
		}
	}
}

// THE SOURCES ARE SETTINGS, and two are refused by name at load: a local
// entry that names a catalogue (its model names never leave the machine,
// whatever the setting says) and a list away from the entry's address
// (its credential would follow). The entry is set aside as broken with
// the reason; the others are used.
func TestACapabilitySourceAnEntryMayNotUseIsRefusedByName(t *testing.T) {
	a := newProvidersApp(t)
	for body, want := range map[string]string{
		`{"name":"metal","url":"http://127.0.0.1:9/v1","local":true,"models":["m"],"capability_catalogue":"https://openrouter.ai/api/v1/models"}`: `provider "metal" is local, so its model names never go to a public catalogue: remove its capability_catalogue "https://openrouter.ai/api/v1/models"`,
		`{"name":"metal","url":"https://api.example/v1","models":["m"],"capability_list":"https://elsewhere.example/api/v1/models"}`:              `provider "metal" has capability_list "https://elsewhere.example/api/v1/models" away from its own address "https://api.example/v1"; its credential goes only there`,
	} {
		writeProvidersFor(t, a, `{"providers":[{"name":"kept","url":"https://kept.example/v1","models":["m"]},`+body+`]}`)
		reg, err := a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		if len(reg.Providers) != 1 || reg.Providers[0].Name != "kept" || len(reg.broken) != 1 || !strings.Contains(reg.broken[0].reason, want) {
			t.Fatalf("got providers %v, broken %+v; want the entry set aside naming %s", reg.Providers, reg.broken, want)
		}
	}
	writeProvidersFor(t, a, `{"providers":[{"name":"metal","url":"http://127.0.0.1:9/v1","local":true,"models":["m"],"capability_list":"http://127.0.0.1:9/api/v1/models","capability_catalogue":""}]}`)
	if reg, err := a.loadProviders(); err != nil || len(reg.broken) != 0 {
		t.Fatalf("a local entry's own list at its own address was refused: %v %+v", err, reg)
	}
}

// The shipped file carries the release's choice as data: each provider's
// own list, and a public catalogue for OpenRouter's entry alone, which is
// that catalogue's own provider.
func TestTheShippedRegistryAsksOpenRoutersCatalogueForOpenRouterAlone(t *testing.T) {
	for _, e := range embeddedRegistry().Providers {
		if got := catalogueFor(e); (got != "") != (e.Name == "OpenRouter") {
			t.Errorf("%s asks catalogue %q", e.Name, got)
		}
		if e.Local && e.CapabilityCatalogue != nil {
			t.Errorf("%s is local and names a catalogue", e.Name)
		}
	}
}

// A CAPABILITY LIST IS ASKED IN PLACE OF THE MODEL LIST for what a model
// takes, and only for that: the models the provider serves stay the
// model list's. "" asks no list at all.
func TestACapabilityListIsAskedForCapabilitiesAlone(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"data":[{"id":"m","capabilities":{"vision":true}},{"id":"other"}]}`)
	}))
	t.Cleanup(srv.Close)
	list := srv.URL + "/api/v1/models"
	a := probeApp(t)
	e := providerEntry{Name: "p", URL: srv.URL + "/v1", APIType: "openai", APIKey: "fixture-key", Local: true, CapabilityList: &list}
	a.providers.runtime.provStatus = map[string]providerProbe{e.Name: {key: probeKey(e), state: probeOK, checkedAt: time.Now(), models: []string{"m", "n"}}}
	a.refreshSelectedModalities(t.Context(), e, "m", &providerRegistry{Providers: []providerEntry{e}}, modalityGuard, func(*App, context.Context, providerEntry, string) ([]string, map[string]modelMeta, error) {
		t.Fatal("the model list was asked for capabilities the entry's own list answers")
		return nil, nil, nil
	})
	mu.Lock()
	once := slices.Equal(asked, []string{"/api/v1/models"})
	asked = nil
	mu.Unlock()
	if !once {
		t.Fatal("the capability list was not asked exactly once")
	}
	if got := a.modelCapabilitiesFor(&providerRegistry{}, e, "m").input["image"]; got.state != capYes || got.from != capFromProvider {
		t.Fatalf("the capability list's answer was not the provider's: %+v", got)
	}
	if st := a.providers.runtime.provStatus[e.Name]; !slices.Equal(st.models, []string{"m", "n"}) {
		t.Fatalf("the capability list replaced the models the provider serves: %v", st.models)
	}
	off := ""
	e.CapabilityList = &off
	a.refreshSelectedModalities(t.Context(), e, "m", &providerRegistry{Providers: []providerEntry{e}}, modalityGuard, func(*App, context.Context, providerEntry, string) ([]string, map[string]modelMeta, error) {
		t.Fatal("a list turned off was asked")
		return nil, nil, nil
	})
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 0 {
		t.Fatalf("a list turned off was asked: %v", asked)
	}
}

// AN ENTRY'S CREDENTIAL GOES ONLY TO ITS OWN ADDRESS: a capability list
// elsewhere is never sent its key, even for an entry the loader never saw.
func TestACapabilityListElsewhereIsNeverSentTheKey(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { asked.Add(1) }))
	t.Cleanup(srv.Close)
	e := providerEntry{Name: "p", URL: "https://api.example/v1", APIType: "openai", APIKey: "fixture-key"}
	if _, _, err := probeApp(t).discoverMetaAt(t.Context(), e, "", srv.URL+"/models", "m"); err == nil || asked.Load() != 0 {
		t.Fatalf("a list away from the entry's address was asked (%d) or not refused: %v", asked.Load(), err)
	}
}

// THE LOOKUP'S BOUNDS ARE SETTINGS of providers.json: how long a
// catalogue's answer is kept and the most it may be reach the lookup, a
// field left out takes the shipped file's, and a bound outside its range
// refuses the file by its path.
func TestTheCapabilityLookupsBoundsAreSettings(t *testing.T) {
	shipped := embeddedRegistry().CapabilityLookup
	if shipped.TimeoutSeconds != 8 || shipped.KeepHours != 6 || shipped.CatalogueMaxBytes != defaultCatalogueMaxBytes {
		t.Fatalf("the shipped file does not carry the defaults as data: %+v", shipped)
	}
	path := filepath.Join(t.TempDir(), "providers.json")
	load := func(lookup string) (*providerRegistry, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"capability_lookup":`+lookup+`,"providers":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return loadProvidersFile(path)
	}
	reg, err := load(`{"keep_hours":1,"catalogue_max_bytes":4096}`)
	if err != nil {
		t.Fatal(err)
	}
	lk := reg.capabilityLookup()
	if lk.keep() != time.Hour || lk.catalogueMaxBytes() != 4096 || lk.timeout() != 8*time.Second {
		t.Fatalf("the file's bounds, over the shipped ones: %+v", lk)
	}
	big := `{"data":{"architecture":{"input_modalities":["text","image"]},"x":"` + strings.Repeat("y", 5000) + `"}}`
	baseURL, asked := modalityServing(t, 200, big)
	a, entry := new(App), providerEntry{CatalogueAuthor: "anthropic"}
	if got := a.fetchModelModalities(t.Context(), modelModalitiesURL(entry, "m", baseURL), modalityGuard, lk); got.known() {
		t.Fatal("an answer past the file's catalogue_max_bytes was read")
	}
	small, _ := modalityServing(t, 200, opusEndpoints)
	url := modelModalitiesURL(entry, "m", small)
	a.fetchModelModalities(t.Context(), url, modalityGuard, lk)
	a.modalities.age(2 * time.Hour)
	if _, kept := a.modalities.get(url); kept {
		t.Fatal("an answer was reused past the file's keep_hours")
	}
	if len(*asked) != 1 {
		t.Fatalf("asked %d times", len(*asked))
	}
	for lookup, want := range map[string]string{
		`{"timeout_seconds":500}`:      "capability_lookup.timeout_seconds: 500 is outside 1 to 120",
		`{"keep_hours":-1}`:            "capability_lookup.keep_hours: -1 is outside 1 to 720",
		`{"catalogue_max_bytes":1024}`: "capability_lookup.catalogue_max_bytes: 1024 is outside 4096 to 16777216",
	} {
		if _, err := load(lookup); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want a refusal naming %s", lookup, err, want)
		}
	}
}

func TestTheWindowIsAFactOfTheOneResolver(t *testing.T) {
	const model = "claude-haiku-4-5" // shipped with a max_output_tokens of its own
	a := probeApp(t)
	entry := providerEntry{Name: "p", URL: "https://p.example/v1"}
	a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {key: probeKey(entry), state: probeOK, models: []string{model},
		meta: map[string]modelMeta{model: {Context: 150000, MaxOut: 32000}}}}
	reg := &providerRegistry{ModelCapabilities: map[string]modelCapability{model: {ContextLength: 100000}}}
	got := a.modelCapabilitiesFor(reg, entry, model)
	if len(got.windows) < 2 || got.windows[0] != (capFigure{value: 100000, from: capFromOperator}) || got.windows[1] != (capFigure{value: 150000, from: capFromProvider}) {
		t.Fatalf("windows by source: %+v", got.windows)
	}
	shipped := embeddedRegistry().ModelCapabilities[model].MaxOutputTokens
	if said(got.outLimits, capFromProvider) != 32000 || said(got.outLimits, capFromShipped) != shipped || shipped == 0 {
		t.Fatalf("output limits by source: %+v (shipped %d)", got.outLimits, shipped)
	}

	dir := t.TempDir()
	listed := providerEntry{Name: "listed", APIType: "openai", URL: "https://listed.example/v1", APIKey: "k", DefaultModel: "m", MaxOutputTokens: 4096}
	writeTestProviders(t, dir, listed)
	block := LLMConfig{Provider: "listed", Model: "m"}
	r := resolverApp(dir, block)
	r.providers.runtime.provStatus = map[string]providerProbe{"listed": {key: probeKey(listed), state: probeOK, checkedAt: time.Now(), models: []string{"m"},
		meta: map[string]modelMeta{"m": {Context: 64000}}}}
	loaded, err := r.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if _, e, err := r.resolveLLMConfig(Config{LLM: block}, loaded); err != nil || e.ContextLength != 64000 {
		t.Fatalf("this endpoint's published window did not fill the entry's: %d %v", e.ContextLength, err)
	}
	moved := listed
	moved.URL = "https://elsewhere.example/v1"
	writeTestProviders(t, dir, moved)
	if loaded, err = r.loadProviders(); err != nil {
		t.Fatal(err)
	}
	if _, e, err := r.resolveLLMConfig(Config{LLM: block}, loaded); err != nil || e.ContextLength == 64000 {
		t.Fatalf("a window the entry's former endpoint published filled the moved entry's: %d %v", e.ContextLength, err)
	}
}

// WHAT THE OPERATOR ASKS OF A MODEL IS NOT THE ENDPOINT. A window, an
// output allocation, the model chosen or a sampling setting changed by
// hand keeps what the endpoint published; its address does not.
func TestABudgetEditKeepsWhatTheEndpointPublished(t *testing.T) {
	temp := 0.2
	a := providerEntry{Name: "p", URL: "https://x/v1", APIKey: "k"}
	b := a
	b.ContextLength, b.MaxOutputTokens, b.DefaultModel, b.ReasoningEffort, b.Temperature = 64000, 1024, "m2", "high", &temp
	if probeKey(a) != probeKey(b) {
		t.Fatal("a budget or sampling edit cleared what the endpoint published")
	}
	b.URL = "https://y/v1"
	if probeKey(a) == probeKey(b) {
		t.Fatal("another address kept this one's evidence")
	}
}

// The catalogue says whether a model takes tools (one endpoint that does
// is enough), and its answer is kept for as long as the catalogue says,
// else six hours.
func TestTheCatalogueSaysToolsAndHowLongItsAnswerKeeps(t *testing.T) {
	baseURL, _ := modalityServingFunc(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		io.WriteString(w, `{"data":{"architecture":{"input_modalities":["text"]},"endpoints":[{"supported_parameters":["max_tokens"]},{"supported_parameters":["tools"]}]}}`)
	})
	a, entry := new(App), providerEntry{CatalogueAuthor: "anthropic"}
	got := a.modelModalitiesFor(context.Background(), entry, "claude-opus-5", baseURL)
	if got.tools == nil || !*got.tools || got.ttl != time.Minute {
		t.Fatalf("tools %v, kept for %s", got.tools, got.ttl)
	}
	key := modelModalitiesURL(entry, "claude-opus-5", baseURL)
	a.modalities.age(2 * time.Minute)
	if _, ok := a.modalities.get(key); ok {
		t.Fatal("an answer was kept past the age the catalogue gave it")
	}
	if maxAge("no-cache") != 0 || maxAge("max-age=0") != 0 || maxAge("max-age=x") != 0 {
		t.Fatal("a header that names no age gave one")
	}
}

// A LIST THAT ANSWERS ONE MODEL AT A TIME is asked for the one chosen:
// "{model}" in the address stands for it (the DashScope API's ?model=),
// and an Ollama server's /api/show is asked in a POST's body.
func TestACapabilityListIsAskedForTheChosenModel(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		asked = append(asked, r.Method+" "+r.URL.RequestURI()+" "+string(body))
		mu.Unlock()
		if r.URL.Path == "/api/show" {
			io.WriteString(w, `{"capabilities":["completion","vision","tools"]}`)
			return
		}
		io.WriteString(w, `{"output":{"models":[{"model":"qwen x","inference_metadata":{"request_modality":["Text","Image"],"response_modality":["Text"]},"features":["function-calling"]}]}}`)
	}))
	t.Cleanup(srv.Close)
	for _, tc := range []struct{ list, model, want string }{
		{srv.URL + "/api/v1/models?model={model}", "qwen x", "GET /api/v1/models?model=qwen+x "},
		{srv.URL + "/api/show", "llava", `POST /api/show {"model":"llava"}`},
	} {
		a := probeApp(t)
		e := providerEntry{Name: "p", URL: srv.URL + "/v1", APIType: "openai", Local: true, CapabilityList: &tc.list}
		a.refreshSelectedModalities(t.Context(), e, tc.model, &providerRegistry{Providers: []providerEntry{e}}, modalityGuard, (*App).discoverMetaForEntry)
		mu.Lock()
		got := slices.Clone(asked)
		asked = nil
		mu.Unlock()
		if !slices.Equal(got, []string{tc.want}) {
			t.Fatalf("asked %q, want %q", got, tc.want)
		}
		caps := a.modelCapabilitiesFor(&providerRegistry{}, e, tc.model)
		if caps.input["image"].state != capYes || caps.tools.state != capYes || caps.output["text"].state != capYes {
			t.Fatalf("%s: read %+v", tc.list, caps)
		}
	}
}

// AN OPERATOR'S images LINE ROUND-TRIPS. A save of an unrelated setting
// writes the row back as the file spelled it, so 0.1.22 and older, whose
// loader knows images and refuses input, still load the file; a row
// whose answer changed is written in the new spelling.
func TestAnImagesLineIsWrittenBackAsTheFileSpelledIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"p","url":"https://p.example/v1","models":["m"]}],`+
		`"model_capabilities":{"m":{"images":false},"both":{"images":true,"input":{"image":true,"pdf":true}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := candidateRegistry(reg, func(r *providerRegistry) error {
		r.Providers[0].DefaultModel = "m"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveProvidersFile(path, candidate); err != nil {
		t.Fatal(err)
	}
	var saved struct {
		ModelCapabilities map[string]json.RawMessage `json:"model_capabilities"`
	}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if got := string(saved.ModelCapabilities["m"]); !strings.Contains(got, `"images":false`) && !strings.Contains(got, `"images": false`) || strings.Contains(got, "input") {
		t.Fatalf("an unrelated save rewrote the operator's images line: %s", got)
	}
	if got := string(saved.ModelCapabilities["both"]); !strings.Contains(got, "images") || !strings.Contains(got, `"pdf"`) || !strings.Contains(got, `"image"`) {
		t.Fatalf("a row that said both lost one: %s", got)
	}
	reg, err = loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := probeApp(t).modelCapabilitiesFor(reg, reg.Providers[0], "m").input["image"]; got.state != capNo || got.from != capFromOperator {
		t.Fatalf("the round-tripped line no longer answers: %+v", got)
	}
	row := reg.ModelCapabilities["m"]
	row.Input = map[string]bool{"image": true}
	reg.ModelCapabilities["m"] = row
	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if got := string(saved.ModelCapabilities["m"]); strings.Contains(got, "images") || !strings.Contains(got, `"image"`) {
		t.Fatalf("a changed answer was written in the old spelling: %s", got)
	}
}
