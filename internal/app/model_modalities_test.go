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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func (a *App) modelModalitiesFor(ctx context.Context, entry providerEntry, model, baseURL string) modelModalities {
	return a.fetchModelModalities(ctx, modelModalitiesURL(entry, model, baseURL), modalityGuard)
}

// .
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

// .
// .
// .
// .
func modalityServing(t *testing.T, status int, body string) (string, *[]string) {
	t.Helper()
	return modalityServingFunc(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

// .
// .
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

// .
// .
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

// .
// .
// .
func TestAnAuthorQualifiedModelIsNotPrefixedTwice(t *testing.T) {
	baseURL, asked := modalityServing(t, 200, opusEndpoints)
	new(App).modelModalitiesFor(context.Background(),
		providerEntry{Name: "Groq", CatalogueAuthor: "groq"}, "openai/gpt-oss-120b", baseURL)
	if want := "/models/openai/gpt-oss-120b/endpoints"; (*asked)[0] != want {
		t.Fatalf("asked for %q, want %q", (*asked)[0], want)
	}
}

// .
// .
// .
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

// .
func TestASlugThatIsNotTwoPlainSegmentsIsRefused(t *testing.T) {
	for _, model := range []string{"../../etc/passwd", "a/b/c", "has space", "q?x=1"} {
		if got := modalitySlug(providerEntry{CatalogueAuthor: "anthropic"}, model); got != "" {
			t.Fatalf("%q produced slug %q", model, got)
		}
	}
}

// .
func TestALookupFailureIsUnknownNotTextOnly(t *testing.T) {
	baseURL, _ := modalityServing(t, 503, `{}`)
	got := new(App).modelModalitiesFor(context.Background(),
		providerEntry{CatalogueAuthor: "anthropic"}, "claude-opus-5", baseURL)
	if got.known() {
		t.Fatal("a failed lookup invented modalities")
	}
}

// .
func TestTheLookupBodyIsBounded(t *testing.T) {
	baseURL, _ := modalityServing(t, 200, `{"data":{"x":"`+strings.Repeat("y", modalityMaxBytes+64)+`"}}`)
	got := new(App).modelModalitiesFor(context.Background(),
		providerEntry{CatalogueAuthor: "anthropic"}, "claude-opus-5", baseURL)
	if got.known() {
		t.Fatal("an oversized body was parsed")
	}
}

// .
func TestTheProbeKeepsTheModalitiesItLookedUp(t *testing.T) {
	baseURL, _ := modalityServing(t, 200, opusEndpoints)
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "claude-opus-5", Provider: "anthropic"}
	if err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "p", CatalogueAuthor: "anthropic"}, &providerRegistry{ModelCatalogueURL: &baseURL}, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
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

// .
func TestALookupFailureDoesNotRefuseASubstrate(t *testing.T) {
	baseURL, _ := modalityServing(t, 500, `{}`)
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "p", CatalogueAuthor: "anthropic"}, &providerRegistry{ModelCatalogueURL: &baseURL}, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("a third party decided a substrate: %v", err)
	}
	if got := a.substrateCapabilityFor("p", "m"); got.inputModalities != nil {
		t.Fatalf("modalities invented from a failure: %v", got.inputModalities)
	}
}

// .
// .
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

// .
// .
func TestTheCatalogueAuthorIsNotPartOfTheProbeKey(t *testing.T) {
	a := providerEntry{Name: "p", URL: "https://x"}
	b := a
	b.CatalogueAuthor = "anthropic"
	if probeKey(a) != probeKey(b) {
		t.Fatal("a vendor-supplied author changed the probe key")
	}
}

// .
// .
// .
func (m *modalityMemo) age(by time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.by {
		e.at = e.at.Add(-by)
		m.by[k] = e
	}
}

// .
// .
// .
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

// .
// .
func TestEachModelIsRememberedSeparately(t *testing.T) {
	baseURL, asked := modalityServing(t, 200, opusEndpoints)
	a, entry := new(App), providerEntry{CatalogueAuthor: "anthropic"}
	a.modelModalitiesFor(context.Background(), entry, "claude-opus-5", baseURL)
	a.modelModalitiesFor(context.Background(), entry, "claude-haiku-4.5", baseURL)
	if len(*asked) != 2 {
		t.Fatalf("asked %v, want one question per model", *asked)
	}
}

// .
// .
// .
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

// .
// .
// .
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

// .
// .
func TestTheMemoDoesNotGrowWithoutBound(t *testing.T) {
	var m modalityMemo
	for i := 0; i < modalityMemoMax*2+1; i++ {
		m.put(fmt.Sprintf("author/model-%d", i), modelModalities{in: []string{"text"}})
	}
	if len(m.by) > modalityMemoMax {
		t.Fatalf("memo holds %d entries, ceiling is %d", len(m.by), modalityMemoMax)
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
		if err := a.probeSubstrate(llm.New(&cc), cc, reg.Providers[0], reg, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
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
				done <- a.probeSubstrate(llm.New(&cc), cc, entry, &providerRegistry{ModelCatalogueURL: &baseURL}, 5)
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
				if err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "second"}, &providerRegistry{ModelCatalogueURL: &disabled}, 5); err != nil {
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
			err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "p", CatalogueAuthor: "author"}, &providerRegistry{ModelCatalogueURL: &baseURL}, 5)
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
