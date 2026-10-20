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
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func (a *App) modelModalitiesFor(ctx context.Context, entry providerEntry, model, baseURL string) modelModalities {
	return a.fetchModelModalities(ctx, modelModalitiesURL(entry, model, baseURL), modalityGuard)
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
	baseURL, _ := modalityServing(t, 200, `{"data":{"x":"`+strings.Repeat("y", modalityMaxBytes+64)+`"}}`)
	got := new(App).modelModalitiesFor(context.Background(),
		providerEntry{CatalogueAuthor: "anthropic"}, "claude-opus-5", baseURL)
	if got.known() {
		t.Fatal("an oversized body was parsed")
	}
}

// The probe keeps what the lookup said, beside what the endpoint proved.
func TestTheProbeKeepsTheModalitiesItLookedUp(t *testing.T) {
	baseURL, _ := modalityServing(t, 200, opusEndpoints)
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "claude-opus-5", Provider: "anthropic"}
	if err := probeAndRun(a, cc, providerEntry{Name: "p", CatalogueAuthor: "anthropic"}, &providerRegistry{ModelCatalogueURL: &baseURL}, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("probe: %v", err)
	}
	waitForModalities(t, a, modelModalitiesURL(providerEntry{CatalogueAuthor: "anthropic"}, cc.Model, baseURL))
	got := a.substrateCapabilityFor("p", "claude-opus-5")
	if got.toolCalls != capYes {
		t.Fatalf("toolCalls = %v", got.toolCalls)
	}
	if strings.Join(got.inputModalities, ",") != "text,image,file" {
		t.Fatalf("modalities = %v", got.inputModalities)
	}
}

// A third party never decides a substrate.
func TestALookupFailureDoesNotRefuseASubstrate(t *testing.T) {
	baseURL, _ := modalityServing(t, 500, `{}`)
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := probeAndRun(a, cc, providerEntry{Name: "p", CatalogueAuthor: "anthropic"}, &providerRegistry{ModelCatalogueURL: &baseURL}, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("a third party decided a substrate: %v", err)
	}
	if got := a.substrateCapabilityFor("p", "m"); got.inputModalities != nil {
		t.Fatalf("modalities invented from a failure: %v", got.inputModalities)
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
	a.modalities.age(modalityMemoTTL + time.Minute)
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
	for _, tc := range []struct{ base, want string }{
		{firstURL + "/", "text,image,file"}, {secondURL, "audio"}, {"", ""},
	} {
		data, err := json.Marshal(map[string]any{
			"model_catalogue_url": tc.base,
			"providers":           []map[string]string{{"name": "fixture", "api_type": "anthropic", "url": srv.URL, "catalogue_author": "anthropic"}},
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
		if reg.ModelCatalogueURL == nil || *reg.ModelCatalogueURL != tc.base {
			t.Fatal("provider edit lost the configured catalogue address")
		}
		if err := probeAndRun(a, cc, reg.Providers[0], reg, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
			t.Fatal(err)
		}
		if tc.want != "" {
			waitForModalities(t, a, modelModalitiesURL(reg.Providers[0], cc.Model, reg.modelCatalogueURL()))
		}
		got := a.substrateCapabilityFor("fixture", cc.Model)
		if strings.Join(got.inputModalities, ",") != tc.want {
			t.Fatalf("catalogue %q: got %v, want %q", tc.base, got.inputModalities, tc.want)
		}
	}
	if len(*firstAsked) != 1 || len(*secondAsked) != 1 {
		t.Fatalf("lookup counts: first=%d second=%d", len(*firstAsked), len(*secondAsked))
	}
}

func TestOmittedModelCatalogueUsesJSONDefaultWithoutPersistingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var shipped providerRegistry
	if err := json.Unmarshal(embeddedProviders, &shipped); err != nil {
		t.Fatal(err)
	}
	if shipped.ModelCatalogueURL == nil || *shipped.ModelCatalogueURL == "" {
		t.Fatal("shipped JSON has no catalogue address")
	}
	if reg.modelCatalogueURL() != *shipped.ModelCatalogueURL {
		t.Fatal("omitted address did not inherit shipped JSON")
	}
	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if _, exists := saved["model_catalogue_url"]; exists {
		t.Fatal("save froze the default as an explicit override")
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
			entry := providerEntry{Name: "first", CatalogueAuthor: "author"}
			done := make(chan error, 1)
			go func() {
				done <- probeAndRun(a, cc, entry, &providerRegistry{ModelCatalogueURL: &baseURL}, 5)
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
			if got := a.substrateCapabilityFor("first", cc.Model); got.toolCalls != capYes || got.inputModalities != nil {
				t.Fatalf("ready provider with unanswered metadata: %+v", got)
			}
			if laterSelection {
				disabled := ""
				if err := probeAndRun(a, cc, providerEntry{Name: "second"}, &providerRegistry{ModelCatalogueURL: &disabled}, 5); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			waitForModalities(t, a, modelModalitiesURL(entry, cc.Model, baseURL))
			if laterSelection {
				if got := a.substrateCapabilityFor("second", cc.Model); got.toolCalls != capYes || got.inputModalities != nil {
					t.Fatalf("late answer overwrote newer selection: %+v", got)
				}
				if got := a.substrateCapabilityFor("first", cc.Model); got.inputModalities != nil {
					t.Fatal("old selection regained a capability snapshot")
				}
			} else if got := a.substrateCapabilityFor("first", cc.Model); strings.Join(got.inputModalities, ",") != "text,image,file" {
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
			err := probeAndRun(a, cc, providerEntry{Name: "p", CatalogueAuthor: "author"}, &providerRegistry{ModelCatalogueURL: &baseURL}, 5)
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

func TestSelectedModelCapabilitiesUseNativeDataBeforeTheCatalogue(t *testing.T) {
	for _, tc := range []struct {
		name, data    string
		wantCatalogue int
		want          *bool
	}{
		{"positive", `{"data":[{"id":"m","architecture":{"input_modalities":["text","image"]}}]}`, 0, boolp(true)},
		{"negative", `{"data":[{"id":"m","capabilities":{"vision":false}}]}`, 0, boolp(false)},
		{"partial", `{"data":[{"id":"m","context_length":32768}]}`, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, asked := modalityServing(t, http.StatusOK, opusEndpoints)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, tc.data) }))
			t.Cleanup(srv.Close)
			a := probeApp(t)
			e := providerEntry{Name: "native", URL: srv.URL, APIType: "openai", CatalogueAuthor: "fixture"}
			a.refreshSelectedModalities(t.Context(), e, "m", &providerRegistry{Providers: []providerEntry{e}, ModelCatalogueURL: &base}, modalityGuard, (*App).discoverMetaForEntry)
			if len(*asked) != tc.wantCatalogue {
				t.Fatalf("catalogue requests=%d, want %d", len(*asked), tc.wantCatalogue)
			}
			got := a.discoveredImageSupport(e, "m")
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("native support=%v, want %v", got, tc.want)
			}
		})
	}
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
	e := providerEntry{Name: "native", URL: srv.URL, APIType: "openai", CatalogueAuthor: "fixture"}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	a.refreshSelectedModalities(ctx, e, "m", &providerRegistry{Providers: []providerEntry{e}, ModelCatalogueURL: &base}, modalityGuard, (*App).discoverMetaForEntry)
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
	if a.discoveredImageSupport(e, "m") != nil {
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
				e := providerEntry{Name: "fixture", APIType: "openai", URL: "https://provider.example/v1", CatalogueAuthor: "fixture"}
				writeTestProviders(t, dir, e)
				a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
				wantBound := modalityTimeout
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		key: probeKey(e), state: probeOK, checkedAt: time.Now().Add(-2 * providerStatusTTL),
		meta: map[string]modelMeta{"m": {Images: boolp(true)}},
	}}
	reg := &providerRegistry{Providers: []providerEntry{e}}
	a.refreshSelectedModalities(t.Context(), e, "m", reg, modalityGuard, (*App).discoverMetaForEntry)
	if got := a.discoveredImageSupport(e, "m"); got == nil || *got {
		t.Fatal("selection retained an expired positive over the provider's new negative")
	}
	a.providers.runtime.mu.Lock()
	st := a.providers.runtime.provStatus[e.Name]
	st.checkedAt = time.Now().Add(-2 * providerStatusTTL)
	a.providers.runtime.provStatus[e.Name] = st
	a.providers.runtime.mu.Unlock()
	a.refreshSelectedModalities(t.Context(), e, "m", reg, modalityGuard, (*App).discoverMetaForEntry)
	if got := a.discoveredImageSupport(e, "m"); got == nil || *got {
		t.Fatal("a failed refresh erased or inverted a successful native answer")
	}
	if requests.Load() != 2 {
		t.Fatalf("native requests=%d, want 2", requests.Load())
	}
}

func TestPartialNativeListsKeepOnlyUnansweredSameEndpointCapabilities(t *testing.T) {
	for _, support := range []bool{false, true} {
		previous := providerProbe{key: "endpoint-a", meta: map[string]modelMeta{"m": {Images: boolp(support)}, "removed": {Images: boolp(true)}}}
		fresh := providerProbe{key: "endpoint-a", state: probeOK, models: []string{"m"}, meta: map[string]modelMeta{"m": {Context: 12345}}}
		got := retainNativeImages(fresh, previous)
		if got.meta["m"].Images == nil || *got.meta["m"].Images != support || got.meta["m"].Context != 12345 {
			t.Fatalf("partial native list erased support=%v or fresh context: %+v", support, got.meta["m"])
		}
		if _, exists := got.meta["removed"]; exists {
			t.Fatal("a removed model was resurrected by retained metadata")
		}
		other := retainNativeImages(providerProbe{key: "endpoint-b", state: probeOK, models: []string{"m"}}, previous)
		if other.meta["m"].Images != nil {
			t.Fatal("another endpoint inherited the capability")
		}
		replaced := retainNativeImages(providerProbe{key: "endpoint-a", state: probeOK, models: []string{"m"}, meta: map[string]modelMeta{"m": {Images: boolp(!support)}}}, previous)
		if *replaced.meta["m"].Images == support {
			t.Fatal("explicit native replacement was ignored")
		}
	}
}

func TestAFailedEventRefreshDoesNotEraseTheLastCatalogueAnswer(t *testing.T) {
	base, asked := modalityServing(t, http.StatusServiceUnavailable, `{}`)
	a := probeApp(t)
	e := providerEntry{Name: "public", CatalogueAuthor: "fixture"}
	key := modelModalitiesURL(e, "m", base)
	a.modalities.put(key, modelModalities{in: []string{"text", "image"}})
	a.modalities.age(modalityMemoTTL + time.Hour)
	a.refreshSelectedModalities(t.Context(), e, "m", &providerRegistry{ModelCatalogueURL: &base}, modalityGuard, (*App).discoverMetaForEntry)
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
	e := providerEntry{Name: "local", Local: true, URL: srv.URL, APIType: "openai"}
	a.refreshSelectedModalities(t.Context(), e, "private/alias", &providerRegistry{Providers: []providerEntry{e}, ModelCatalogueURL: &base}, modalityGuard, (*App).discoverMetaForEntry)
	if len(*asked) != 0 {
		t.Fatalf("a local model name was disclosed: %v", *asked)
	}
	if got := a.discoveredImageSupport(e, "private/alias"); got != nil {
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
