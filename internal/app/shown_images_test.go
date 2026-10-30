package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	stdgif "image/gif"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// shownPNG is a real picture, four pixels by three: what a file is, is
// read from what it holds.
var shownPNG = func() []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		panic(err)
	}
	return buf.Bytes()
}()

// catalogueSaysImages makes a public catalogue's word for the running
// model "takes images", with no row of the operator's: the prior.
func catalogueSaysImages(t *testing.T, app *App) {
	t.Helper()
	provider, model := app.currentProvider().Name, app.llmSwap.Current().ModelName()
	reg, err := loadProvidersFile(app.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	var entry providerEntry
	for i := range reg.Providers {
		if reg.Providers[i].Name == provider {
			reg.Providers[i].CatalogueAuthor = "fixture"
			reg.Providers[i].CapabilityCatalogue = &testCatalogue
			entry = reg.Providers[i]
		}
	}
	if _, err := saveProvidersFile(app.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	key := modelModalitiesURL(entry, model, catalogueFor(entry))
	app.modalities.put(key, modelModalities{in: []string{"text", "image"}, out: []string{"text"}})
	app.providers.runtime.mu.Lock()
	app.providers.runtime.substrateCap = substrateCapability{provider: provider, model: model, toolCalls: capYes}
	app.providers.runtime.mu.Unlock()
}

// testCatalogue is a public catalogue's address an entry names in a test.
var testCatalogue = "https://catalogue.example/api/v1/models"

// imageKinds is a provider's answer about pictures alone, nil for none.
func imageKinds(v *bool) map[string]bool {
	if v == nil {
		return nil
	}
	return map[string]bool{"image": *v}
}

// imageApp is promptCacheApp with an image in the identity's uploads
// folder and, when row is not nil, a registry row for the model that
// answers.
func imageApp(t *testing.T, row *modelCapability) (*App, *contScript, string) {
	t.Helper()
	app, obs := promptCacheApp(t)
	// The cache harness installs its scripted client after boot. Publish that
	// client with its provider, as activation does, before testing its images.
	app.adoptRuntime(app.currentProvider(), app.llmSwap.Current())
	home := app.configSnapshot().Tools.CWD
	if err := os.MkdirAll(filepath.Join(home, "uploads"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "uploads", "proof.png"), shownPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	if row != nil {
		setModelRow(t, app, "fake", *row)
	}
	return app, obs, "data:image/png;base64," + base64.StdEncoding.EncodeToString(shownPNG)
}

func setModelRow(t *testing.T, app *App, model string, row modelCapability) {
	t.Helper()
	reg, err := loadProvidersFile(app.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	if reg.ModelCapabilities == nil {
		reg.ModelCapabilities = map[string]modelCapability{}
	}
	reg.ModelCapabilities[model] = row
	if _, err := saveProvidersFile(app.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
}

func boolp(v bool) *bool { return &v }

func TestRunningImagePolicyUsesTheAdoptedProviderAndClientTogether(t *testing.T) {
	dir := t.TempDir()
	first := providerEntry{Name: "first", APIType: "openai", URL: "http://127.0.0.1:1/v1", DefaultModel: "first-model", Local: true}
	second := providerEntry{Name: "second", APIType: "openai", URL: "http://127.0.0.1:2/v1", DefaultModel: "second-model", Local: true}
	writeTestProviders(t, dir, first, second)
	a := resolverApp(dir, LLMConfig{Provider: first.Name, Model: first.DefaultModel})
	setModelRow(t, a, first.DefaultModel, modelCapability{Input: imageKinds(boolp(false))})
	setModelRow(t, a, second.DefaultModel, modelCapability{Input: imageKinds(boolp(true))})
	firstClient := llm.New(&llm.ClientConfig{Provider: first.APIType, Endpoint: first.URL, Model: first.DefaultModel})
	secondClient := llm.New(&llm.ClientConfig{Provider: second.APIType, Endpoint: second.URL, Model: second.DefaultModel})
	a.llmSwap = newSwappableLLM(firstClient)
	a.adoptRuntime(first, firstClient)

	// Stop at the real activation boundary: the new pair is published before
	// the conversation loop's swap. Its old client must not supply the model
	// for the new provider's image policy.
	a.adoptRuntime(second, secondClient)
	if a.llmSwap.Current() != firstClient {
		t.Fatal("rig: the conversation loop must still hold the first client")
	}
	if got := a.runningImagePolicy().takes; got != capYes {
		t.Fatalf("the adopted second provider/model takes images, got %s while the loop swap is still first", got)
	}
}

func TestNativeImageCapabilitiesReachAnImageRead(t *testing.T) {
	for _, tc := range []struct {
		name             string
		declared, native *bool
		want             bool
	}{
		{"native positive", nil, boolp(true), true},
		{"native negative beats catalogue", nil, boolp(false), false},
		{"operator negative beats native", boolp(false), boolp(true), false},
		{"operator positive beats native", boolp(true), boolp(false), true},
		{"partial native uses catalogue", nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var row *modelCapability
			if tc.declared != nil {
				row = &modelCapability{Images: tc.declared}
			}
			a, _, _ := imageApp(t, row)
			catalogueSaysImages(t, a)
			reg, err := a.loadProviders()
			if err != nil {
				t.Fatal(err)
			}
			entry := reg.Providers[0]
			a.providers.runtime.mu.Lock()
			a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {
				key: probeKey(entry), state: probeOK, meta: map[string]modelMeta{"fake": {Input: imageKinds(tc.native)}},
			}}
			a.providers.runtime.mu.Unlock()
			result := tools.Result{Output: "proof", Image: &tools.ResultImage{Path: "uploads/proof.png"}}
			_, picture := a.readPicture(withTurnImages(t.Context(), "", ""), result)
			if (picture != nil) != tc.want {
				t.Fatalf("image sent=%v, want %v", picture != nil, tc.want)
			}
			_, picture = a.readPicture(withTurnImages(t.Context(), entry.Name, "fake"), result)
			if (picture != nil) != tc.want {
				t.Fatalf("worker image sent=%v, want %v", picture != nil, tc.want)
			}
		})
	}
}

func TestCatalogueImageSupportDoesNotDisappearWithTime(t *testing.T) {
	base, asked := modalityServing(t, http.StatusOK, opusEndpoints)
	a, _, _ := imageApp(t, nil)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	reg.Providers[0].CapabilityCatalogue = &base
	reg.Providers[0].CatalogueAuthor = "fixture"
	if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	a.modalities.put(modelModalitiesURL(reg.Providers[0], "fake", base), modelModalities{in: []string{"text", "image"}})
	a.modalities.age(defaultCapabilityKeep + time.Hour)
	for i := 0; i < 5; i++ {
		if got := a.runningImagePolicy().takes; got != capYes {
			t.Fatalf("the clock removed image support: %s", got)
		}
	}
	if len(*asked) != 0 {
		t.Fatalf("policy reads made %d catalogue requests", len(*asked))
	}
}

func TestARoutedWorkerReadsItsOwnNativeImageEvidence(t *testing.T) {
	a, _, _ := imageApp(t, nil)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	worker := providerEntry{Name: "vision-worker", Local: true, APIType: "openai", URL: "http://127.0.0.1:1/v1"}
	reg.Providers = append(reg.Providers, worker)
	if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	a.providers.runtime.mu.Lock()
	a.providers.runtime.provStatus[worker.Name] = providerProbe{key: probeKey(worker), meta: map[string]modelMeta{"worker-model": {Input: imageKinds(boolp(true))}}}
	a.providers.runtime.mu.Unlock()
	if got := a.runningImagePolicy().takes; got != capUnknown {
		t.Fatalf("identity's unknown model borrowed worker evidence: %s", got)
	}
	_, picture := a.readPicture(withTurnImages(t.Context(), worker.Name, "worker-model"), tools.Result{Image: &tools.ResultImage{Path: "uploads/proof.png"}})
	if picture == nil {
		t.Fatal("worker was denied its own native image capability by the identity's unknown one")
	}
}

func TestNativeImageEvidenceDoesNotFollowAnEndpointEdit(t *testing.T) {
	a, _, _ := imageApp(t, nil)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	entry := reg.Providers[0]
	a.providers.runtime.mu.Lock()
	a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {
		key: probeKey(entry), state: probeOK, models: []string{"fake"},
		meta: map[string]modelMeta{"fake": {Input: imageKinds(boolp(true))}}, checkedAt: time.Now(),
	}}
	a.providers.runtime.mu.Unlock()
	fails := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	t.Cleanup(fails.Close)
	reg.Providers[0].URL = fails.URL
	if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	a.probeProviders(reg)
	if got := a.imagePolicyFor(entry.Name, "fake").takes; got != capUnknown {
		t.Fatalf("a replacement endpoint inherited image support: %s", got)
	}
	_, _, listed, _ := a.discoveredMeta(entry.Name, "fake")
	if listed != 1 {
		t.Fatal("the historical model list was lost")
	}
}

// A FIRST BOOT WRITES NOTHING THE SHIPPED REGISTRY SAYS OF MODELS into the
// operator's file: no row, no lookup bound, no entry's capability source.
// Each is lent on every load, so a release's corrections reach the
// install, and the shipped row still answers through the one resolver.
func TestAShippedImageDefaultDoesNotBecomeAnOperatorOverride(t *testing.T) {
	reg := providerRegistry{}
	if err := json.Unmarshal(scaffoldProviders(), &reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.ModelCapabilities) != 0 || reg.CapabilityLookup != (capabilityLookup{}) {
		t.Fatalf("scaffolding froze what the shipped registry says of models as the operator's: %d rows, lookup %+v", len(reg.ModelCapabilities), reg.CapabilityLookup)
	}
	for _, e := range reg.Providers {
		if e.CapabilityList != nil || e.CapabilityCatalogue != nil {
			t.Fatalf("scaffolding froze %s's capability sources as the operator's", e.Name)
		}
	}
	if got := (&App{}).modelCapabilitiesFor(&reg, providerEntry{Name: "Anthropic"}, "claude-opus-5").input["image"]; got.state != capYes || got.from != capFromShipped {
		t.Fatalf("omitting a copied image declaration removed the inherited default: %+v", got)
	}
	if embeddedRegistry().ModelCapabilities["claude-opus-5"].takes("image") == nil {
		t.Fatal("scaffolding mutated the shared embedded registry")
	}
}

func TestTheShippedImageBaselineReachesAnImageReadBelowDiscoveredFacts(t *testing.T) {
	const model = "claude-opus-5"
	for _, source := range []string{"baseline", "catalogue", "native"} {
		t.Run(source, func(t *testing.T) {
			a, _, _ := imageApp(t, nil)
			reg, err := a.loadProviders()
			if err != nil {
				t.Fatal(err)
			}
			reg.Providers[0].Local = false
			reg.Providers[0].CatalogueAuthor = "anthropic"
			reg.Providers[0].CapabilityCatalogue = &testCatalogue
			if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
				t.Fatal(err)
			}
			entry := reg.Providers[0]
			switch source {
			case "catalogue":
				a.modalities.put(modelModalitiesURL(entry, model, catalogueFor(entry)), modelModalities{in: []string{"text"}})
			case "native":
				a.providers.runtime.mu.Lock()
				a.providers.runtime.provStatus[entry.Name] = providerProbe{key: probeKey(entry), meta: map[string]modelMeta{model: {Input: imageKinds(boolp(false))}}}
				a.providers.runtime.mu.Unlock()
			}
			want := source == "baseline"
			_, picture := a.readPicture(withTurnImages(t.Context(), entry.Name, model), tools.Result{Image: &tools.ResultImage{Path: "uploads/proof.png"}})
			if (picture != nil) != want {
				t.Fatalf("%s evidence with a shipped positive baseline: image sent=%v, want %v", source, picture != nil, want)
			}
		})
	}
}

// AN ATTACHED IMAGE IS SHOWN TO A MODEL THAT TAKES IMAGES, IN THE TURN IT
// ARRIVES. The request that answers the message carries the picture
// beside its words. The next turn carries the words and the file's line,
// and no picture: the record never held the bytes.
func TestAnAttachedImageIsShownToAModelThatTakesImages(t *testing.T) {
	app, obs, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	attachedTurn(t, app, "what is this?", []string{"uploads/proof.png"})
	operatorTurn(t, app, "and the next thing")
	reqs := obs.snapshot()
	if len(reqs) < 2 {
		t.Fatalf("two turns made %d requests", len(reqs))
	}
	if strings.Count(reqs[0], dataURL) != 1 {
		t.Fatalf("the turn that answers the message does not show the picture once (%d times)", strings.Count(reqs[0], dataURL))
	}
	if !strings.Contains(reqs[0], "your operator attached a file: uploads/proof.png") {
		t.Fatal("the picture came without its file's line")
	}
	last := reqs[len(reqs)-1]
	if strings.Contains(last, "image_url") || strings.Contains(last, base64.StdEncoding.EncodeToString(shownPNG)) {
		t.Fatal("a later turn sent the picture again")
	}
	if !strings.Contains(last, "your operator attached a file: uploads/proof.png") {
		t.Fatal("a later turn lost the file's line")
	}
}

// UNKNOWN AND NO ARE THE SAME ACT: the picture is not sent, and the file's
// line still is. So is a picture over the model's bound, and a file that
// is not an image.
func TestAnImageIsNotShownWhereNobodySaidTheModelTakesOne(t *testing.T) {
	for _, c := range []struct {
		name string
		row  *modelCapability
		path string
		why  string // what the line after the file's says, "" for no line
	}{
		{"nobody has said", nil, "uploads/proof.png", "[not shown to you: uploads/proof.png, nobody has said this model takes images]"},
		{"the row says no", &modelCapability{Effort: []string{}, Images: boolp(false)}, "uploads/proof.png", "[not shown to you: uploads/proof.png, this model does not take images]"},
		{"over the row's bound", &modelCapability{Effort: []string{}, Images: boolp(true), ImageMaxBytes: 8}, "uploads/proof.png", "[not shown to you: uploads/proof.png, its "},
		{"not an image", &modelCapability{Effort: []string{}, Images: boolp(true)}, "uploads/notes.txt", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, obs, _ := imageApp(t, c.row)
			if err := os.WriteFile(filepath.Join(app.configSnapshot().Tools.CWD, "uploads", "notes.txt"), []byte("notes"), 0o600); err != nil {
				t.Fatal(err)
			}
			attachedTurn(t, app, "what is this?", []string{c.path})
			req := obs.snapshot()[0]
			if strings.Contains(req, "image_url") {
				t.Fatal("a picture was sent")
			}
			if !strings.Contains(req, "your operator attached a file: "+c.path) {
				t.Fatal("the file's line was not sent")
			}
			// A picture the model is not shown is declared with why, after
			// the file's line, and on the record where the operator reads
			// it; a file that holds no picture is not.
			declared := strings.Contains(req, "[not shown to you:")
			if declared != (c.why != "") || c.why != "" && !strings.Contains(req, c.why) {
				t.Fatalf("want %q after the file's line:\n%s", c.why, req)
			}
			if notes := pictureNotices(t, app, store.ReasonNotShown, "what is this?"); len(notes) != map[bool]int{true: 1, false: 0}[c.why != ""] {
				t.Fatalf("the record holds %v", notes)
			}
		})
	}
}

// pictureNotices are the notices of one reason beside the operator
// message whose words are given, oldest first.
func pictureNotices(t *testing.T, a *App, reason, words string) []string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT n.content FROM conversations n JOIN conversations m ON n.related_id=m.id
		WHERE n.role='system' AND n.kind='notice' AND json_extract(n.details,'$.reason_code')=? AND m.role='operator' AND m.content=? ORDER BY n.turn_seq`, reason, words)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			t.Fatal(err)
		}
		out = append(out, content)
	}
	return out
}

// THE ROW PRICES AND BOUNDS AN IMAGE, AND THE DEFAULTS STAND WHERE IT IS
// SILENT. A picture a bound leaves out is declared with the bound, in a
// line the message carries after the file's own.
func TestTheModelsRowPricesAndBoundsAnImage(t *testing.T) {
	app, _, _ := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	// decide asks which of a new message's files are sent as pictures.
	decide := func(paths ...string) ([]llm.Image, string) {
		t.Helper()
		ref, err := app.recordConversationRef(t.Context(), "operator", "look at these")
		if err != nil {
			t.Fatal(err)
		}
		row := store.ConversationTurn{ID: ref.ID, Attached: paths}
		images, err := app.picturesWith(withTurnImages(t.Context(), "", ""), &row)
		if err != nil {
			t.Fatal(err)
		}
		return images, notShownLines(row.NotShown)
	}
	if p := app.imagePolicy(t.Context()); p.takes != capYes || p.tokens != defaultImageTokens || p.maxBytes != defaultImageMaxBytes ||
		p.maxTotal != defaultImagesMaxBytes || p.maxSide != defaultImageMaxSide || p.maxCount != defaultImagesMaxCount {
		t.Fatalf("a row that says only yes: %+v", p)
	}
	setModelRow(t, app, "fake", modelCapability{Effort: []string{}, Images: boolp(true), ImageTokens: 4800, ImageMaxBytes: 1 << 20})
	if p := app.imagePolicy(t.Context()); p.tokens != 4800 || p.maxBytes != 1<<20 {
		t.Fatalf("the row's figures were not taken: %+v", p)
	}
	images, declared := decide("uploads/proof.png", "uploads/absent.png", "../outside.png")
	if len(images) != 1 || images[0].MediaType != "image/png" || images[0].Tokens != 4800 || string(images[0].Data) != string(shownPNG) || declared != "" {
		t.Fatalf("shown images: %d, declared %q", len(images), declared)
	}
	size := int64(len(shownPNG))
	for _, c := range []struct {
		name  string
		row   modelCapability
		shown int
		says  string
	}{
		{"one picture's bytes", modelCapability{ImageMaxBytes: size - 1}, 0, fmt.Sprintf("[not shown to you: uploads/proof.png, its %d bytes are past this model's bound of %d for one picture]", size, size-1)},
		{"one picture's longer side", modelCapability{ImageMaxSide: 3}, 0, "[not shown to you: uploads/proof.png, 4 by 3 pixels is past this model's bound of 3 a side]"},
		{"a request's sum", modelCapability{ImagesMaxBytes: size + 1}, 1, fmt.Sprintf("[not shown to you: uploads/proof.png, with it this turn's pictures pass the %d bytes one request carries; a later turn can read it]", size+1)},
		{"a request's count", modelCapability{ImagesMaxCount: 1}, 1, "[not shown to you: uploads/proof.png, this turn already carries 1 pictures, the most one request takes; a later turn can read it]"},
	} {
		c.row.Effort, c.row.Images = []string{}, boolp(true)
		setModelRow(t, app, "fake", c.row)
		images, declared := decide("uploads/proof.png", "uploads/proof.png")
		if len(images) != c.shown || !strings.Contains(declared, c.says) || strings.Count(declared, "[not shown to you: ") != 2-c.shown {
			t.Errorf("%s: %d picture(s) shown, declared %q; want %d and %q", c.name, len(images), declared, c.shown, c.says)
		}
	}
}

// WHAT BECOMES OF AN ATTACHED PICTURE IS ON THE RECORD, WHERE THE OPERATOR
// READS THE TURN. A picture sent to the model is one notice beside the
// message; a picture a bound left out is one notice with the bound, and
// the identity is told it from that notice, on the turn it arrives and on
// the turns after. Nothing the identity is told about a picture is told
// to the identity alone.
func TestWhatBecomesOfAnAttachedPictureIsOnTheRecord(t *testing.T) {
	t.Run("sent", func(t *testing.T) {
		app, obs, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
		attachedTurn(t, app, "what is this?", []string{"uploads/proof.png"})
		if got := pictureNotices(t, app, store.ReasonShown, "what is this?"); len(got) != 1 || got[0] != "uploads/proof.png" {
			t.Fatalf("the notice that the picture was sent: %q", got)
		}
		if got := pictureNotices(t, app, store.ReasonNotShown, "what is this?"); len(got) != 0 {
			t.Fatalf("a picture that was sent is also noted as not shown: %q", got)
		}
		if n := strings.Count(obs.snapshot()[0], dataURL); n != 1 {
			t.Fatalf("the request carries the picture %d times", n)
		}
	})
	t.Run("left out by a bound", func(t *testing.T) {
		app, obs, _ := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true), ImageMaxBytes: 8})
		attachedTurn(t, app, "what is this?", []string{"uploads/proof.png"})
		operatorTurn(t, app, "and the next thing")
		notes := pictureNotices(t, app, store.ReasonNotShown, "what is this?")
		if len(notes) != 1 || !strings.HasPrefix(notes[0], "uploads/proof.png, its ") || !strings.Contains(notes[0], "bound of 8 for one picture") {
			t.Fatalf("the notice that the picture was not sent, with the bound: %q", notes)
		}
		if got := pictureNotices(t, app, store.ReasonShown, "what is this?"); len(got) != 0 {
			t.Fatalf("a picture left out is noted as sent: %q", got)
		}
		reqs := obs.snapshot()
		line := "[your operator attached a file: uploads/proof.png]\\n[not shown to you: " + notes[0] + "]"
		for i, req := range []string{reqs[0], reqs[len(reqs)-1]} {
			if strings.Count(req, line) != 1 {
				t.Fatalf("request %d does not tell the identity what the record holds, once, after the file's line", i)
			}
		}
	})
}

// "SHOWN" IS WRITTEN OF A PICTURE A MODEL ANSWERED WITH, AND OF NO OTHER.
// A turn whose one request is never answered (the address is not found,
// the rate is refused) fails with its picture sent to nobody: the record
// says the picture was not shown and why, never that it was, and a later
// turn tells the identity so and does not send it again.
func TestAPictureWhoseRequestWasNotAnsweredIsNotOnTheRecordAsShown(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			app, _, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
			fail := true
			obs := &contScript{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				obs.add(string(body))
				if fail {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, textReply("noted"))
			}))
			t.Cleanup(srv.Close)
			app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, NoStream: true}))

			ctx, cancel := context.WithTimeout(dashboard.WithAttachments(context.Background(), []string{"uploads/proof.png"}), 60*time.Second)
			defer cancel()
			if err := app.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := app.observeChat(ctx, "what is this?", func(kind, name, args string) {}); err == nil {
				t.Fatal("the fixture provider did not fail the turn")
			}
			if first := obs.snapshot(); len(first) == 0 || !strings.Contains(first[0], dataURL) {
				t.Fatal("the fixture proves nothing: the failed request carried no picture")
			}
			if got := pictureNotices(t, app, store.ReasonShown, "what is this?"); len(got) != 0 {
				t.Fatalf("a picture nobody answered is on the record as shown: %q", got)
			}
			notes := pictureNotices(t, app, store.ReasonNotShown, "what is this?")
			if len(notes) != 1 || !strings.HasPrefix(notes[0], "uploads/proof.png, the request that carried it was not answered") {
				t.Fatalf("the record beside the message: %q", notes)
			}
			fail = false
			before := len(obs.snapshot())
			operatorTurn(t, app, "are you there?")
			later := obs.snapshot()[before:]
			if strings.Contains(later[0], dataURL) || !strings.Contains(later[0], "[not shown to you: "+notes[0]+"]") {
				t.Fatal("the next turn sent the picture again, or does not tell the identity it was not shown")
			}
		})
	}
}

// A PICTURE DELIVERED INTO A RUNNING TURN IS TOLD, NOT SHOWN, AND SAYS
// SO. Words that arrive while a turn runs are delivered at a tool
// boundary; a picture that came with them is told by its line. The
// identity is told that too, and the operator reads it on the record: the
// chip was drawn before the turn began.
func TestAPictureDeliveredIntoARunningTurnIsToldNotShown(t *testing.T) {
	app, _, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	const words = "and this picture, which came while you worked"
	steered := make(chan error, 1)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		if n == 1 {
			ok, err := app.steer(roleOperator, words, nil, dashboard.WithAttachments(t.Context(), []string{"uploads/proof.png"}))
			if err == nil && !ok {
				err = fmt.Errorf("the steer was not queued")
			}
			steered <- err
			fmt.Fprint(w, callReply("", toolCall("r1", "ls", `{"path":"."}`)))
			return
		}
		fmt.Fprint(w, textReply("noted"))
	})
	operatorTurn(t, app, "start on the first one")
	if err := <-steered; err != nil {
		t.Fatal(err)
	}
	reqs := obs.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("fixture rounds=%d", len(reqs))
	}
	text := requestText(t, reqs[1])
	if strings.Contains(reqs[1], dataURL) || !strings.Contains(text, "[your operator attached a file: uploads/proof.png]\n[not shown to you: uploads/proof.png, it came while a turn was running") {
		t.Fatalf("the delivered picture is sent, or the identity is not told it is not")
	}
	if got := pictureNotices(t, app, store.ReasonNotShown, words); len(got) != 1 || !strings.Contains(got[0], "it came while a turn was running") {
		t.Fatalf("the record beside the delivered message: %q", got)
	}
}

// refusingModel is a model whose provider refuses any request that
// carries a picture and answers any other; after is the tool call its
// first answer makes, or "" for none.
func refusingModel(t *testing.T, app *App, after string, status int, message string) *contScript {
	t.Helper()
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		if strings.Contains(string(body), "image_url") {
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":{"message":%q}}`, message)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if after != "" && n == 1 {
			_, _ = io.WriteString(w, callReply("", after))
			return
		}
		_, _ = io.WriteString(w, textReply("noted"))
	}))
	t.Cleanup(srv.Close)
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, NoStream: true}))
	return obs
}

// A REFUSED REQUEST THAT HELD A PICTURE IS SENT ONCE MORE WITHOUT IT, AND
// THE OPERATOR CAN READ THAT IT WAS. The message that carried the picture
// tells the identity; one notice in the turn tells the operator, with the
// provider's own status and words. Nothing is kept past the turn: whoever
// said the model takes images, the next turn sends a picture again, and a
// refusal of a request is not read as a statement about pictures. Generic
// request errors do not establish a model's media capabilities or write
// discoveries into the operator's providers file.
func TestAProviderThatRefusesAnImageIsAskedAgainWithoutIt(t *testing.T) {
	for _, c := range []struct {
		name string
		row  *modelCapability
	}{
		{"a catalogue said yes", nil},
		{"the operator's row said yes", &modelCapability{Effort: []string{}, Images: boolp(true)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, refusal := range []struct {
				status  int
				message string
			}{
				{http.StatusBadRequest, "the request's message ordering is invalid"},
				{http.StatusRequestEntityTooLarge, "the request body is too large"},
				{http.StatusUnsupportedMediaType, "the request has an unsupported content type"},
				{http.StatusUnprocessableEntity, "a required request field is missing"},
			} {
				t.Run(fmt.Sprintf("HTTP %d", refusal.status), func(t *testing.T) {
					app, _, _ := imageApp(t, c.row)
					obs := refusingModel(t, app, "", refusal.status, refusal.message)
					if c.row == nil {
						catalogueSaysImages(t, app)
					}
					before, err := os.ReadFile(app.providersPath())
					if err != nil {
						t.Fatal(err)
					}
					attachedTurn(t, app, "what is this?", []string{"uploads/proof.png"})
					reqs := obs.snapshot()
					n := len(reqs)
					if n != 2 || !strings.Contains(reqs[0], "image_url") || strings.Contains(reqs[1], "image_url") {
						t.Fatalf("%d requests; want the picture sent once and the turn asked again without it", n)
					}
					if !strings.Contains(reqs[1], "your operator attached a file: uploads/proof.png") ||
						!strings.Contains(reqs[1], fmt.Sprintf("the model's provider refused the request that carried the picture(s) or file(s) sent here (HTTP %d), so it was sent again without them and you were not shown them", refusal.status)) {
						t.Fatal("the second request lost the file's line, or does not say the picture was refused")
					}
					var note string
					if err := app.store.DB().QueryRow(`SELECT content FROM conversations WHERE role='system' AND kind='notice' AND json_extract(details,'$.reason_code')=?`, reasonPicturesRefused).Scan(&note); err != nil {
						t.Fatalf("the refusal is not on the record: %v", err)
					}
					if !strings.Contains(note, fmt.Sprintf("HTTP %d", refusal.status)) || !strings.Contains(note, refusal.message) || !strings.Contains(note, `"input": {"image": false}`) {
						t.Fatalf("the notice does not carry the provider's status and words and the way on: %q", note)
					}
					// The record says what happened: the picture was not shown,
					// and the identity reads that line on the turns after.
					if got := pictureNotices(t, app, store.ReasonShown, "what is this?"); len(got) != 0 {
						t.Fatalf("a picture the provider refused is on the record as shown: %q", got)
					}
					if got := pictureNotices(t, app, store.ReasonNotShown, "what is this?"); len(got) != 1 || !strings.HasPrefix(got[0], "uploads/proof.png, the request that carried it was refused by the model's provider") {
						t.Fatalf("the record beside the message: %q", got)
					}
					if p := app.imagePolicy(t.Context()); p.takes != capYes {
						t.Fatalf("a refusal of one request changed what the model is said to take: %v", p.takes)
					}
					attachedTurn(t, app, "and this one?", []string{"uploads/proof.png"})
					if after := obs.snapshot()[n:]; len(after) != 2 || !strings.Contains(after[0], "image_url") || strings.Contains(after[1], "image_url") {
						t.Fatal("the next turn did not offer the picture and retry without it: a refusal was kept past its turn")
					}
					if p := app.imagePolicy(t.Context()); p.takes != capYes {
						t.Fatalf("a later request refusal changed what the model is said to take: %v", p.takes)
					}
					if after, err := os.ReadFile(app.providersPath()); err != nil || !bytes.Equal(after, before) {
						t.Fatalf("request refusals changed the operator's providers file: %v", err)
					}
				})
			}
		})
	}
}

// A PICTURE THE IDENTITY READS DOES NOT END ITS TURN. The provider refuses
// the request that carries a picture read in the round before; the
// request is sent again without it, the boundary that carried it says so,
// and the turn is answered. A second read in that turn is told the
// provider refused one, and is not sent.
func TestAProviderThatRefusesAPictureAfterAReadDoesNotEndTheTurn(t *testing.T) {
	app, _, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	obs := refusingModel(t, app, toolCall("c1", "read", `{"file_path":"uploads/proof.png"}`), http.StatusBadRequest, "this model does not support image input")
	operatorTurn(t, app, "look at the proof you made")
	reqs := obs.snapshot()
	if len(reqs) != 3 {
		t.Fatalf("a read, a refused request and its second asking made %d requests", len(reqs))
	}
	if strings.Count(reqs[1], dataURL) != 1 || strings.Contains(reqs[2], "image_url") {
		t.Fatal("the picture was not sent once and then left out")
	}
	if !strings.Contains(reqs[2], "the model's provider refused the request that carried the picture(s) or file(s) sent here (HTTP 400)") {
		t.Fatal("the request sent again does not say the picture was refused")
	}
	turn := withTurnImages(t.Context(), "", "")
	app.imagesRefused(turn, &llm.HTTPError{StatusCode: 400}, true)
	text, picture := app.readPicture(turn, tools.Result{Output: "x is an image.", Image: &tools.ResultImage{Path: "uploads/proof.png", MediaType: "image/png"}})
	if picture != nil || !strings.Contains(text, "it is not shown to you: a request that carried pictures or files earlier in this turn was refused by the model's provider") {
		t.Fatalf("a read after the refusal, in the same turn: %q, picture=%v", text, picture != nil)
	}
}

// A REQUEST CARRIES EVERY PICTURE OF ITS TURN, SO THE TURN IS WHAT IS
// BOUNDED. With room for one picture in a request, three reads in one
// round and a fourth in the next show one picture, in every request; each
// read past the bound is told so and that a later turn can read it.
func TestPicturesReadInOneTurnAreHeldToWhatOneRequestCarries(t *testing.T) {
	app, _, dataURL := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true), ImagesMaxBytes: int64(len(shownPNG)) + 1})
	obs := &contScript{}
	read := func(id string) string { return toolCall(id, "read", `{"file_path":"uploads/proof.png"}`) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch obs.add(string(body)) {
		case 1:
			_, _ = io.WriteString(w, callReply("", read("c1"), read("c2"), read("c3")))
		case 2:
			_, _ = io.WriteString(w, callReply("", read("c4")))
		default:
			_, _ = io.WriteString(w, textReply("four looks"))
		}
	}))
	t.Cleanup(srv.Close)
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, NoStream: true}))
	operatorTurn(t, app, "look at the proof four times")
	reqs := obs.snapshot()
	if len(reqs) != 3 {
		t.Fatalf("two rounds of reads and an answer made %d requests", len(reqs))
	}
	for i, req := range reqs[1:] {
		if got := strings.Count(req, dataURL); got != 1 {
			t.Errorf("request %d carries %d pictures; one request has room for one", i+2, got)
		}
	}
	if got := strings.Count(reqs[2], "with it this turn's pictures pass the"); got != 3 {
		t.Errorf("%d of the three reads past the bound were told so", got)
	}
}

// A PICTURE IS SHOWN UNDER THE POLICY OF THE MODEL THAT READS IT. A
// sub-agent routed to another model is not answered by the identity's
// model's policy: a worker on a model nobody has spoken for is not sent
// the picture under an identity whose model takes images, and is told
// why; the identity's own turn, beside it, is sent it.
func TestASubagentsPictureIsHeldToItsOwnModel(t *testing.T) {
	app, _, _ := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	result := tools.Result{Output: "uploads/proof.png is an image.", Image: &tools.ResultImage{Path: "uploads/proof.png", MediaType: "image/png"}}
	if text, picture := app.readPicture(withTurnImages(t.Context(), "", ""), result); picture == nil || !strings.Contains(text, pictureSent) {
		t.Fatalf("the identity's own turn: %q, picture=%v", text, picture != nil)
	}
	routed := withTurnImages(t.Context(), app.currentProvider().Name, "text-worker")
	if text, picture := app.readPicture(routed, result); picture != nil || !strings.Contains(text, "it is not shown to you: nobody has said this model takes images") {
		t.Fatalf("a worker on a model nobody has spoken for: %q, picture=%v", text, picture != nil)
	}
	setModelRow(t, app, "text-worker", modelCapability{Effort: []string{}, Images: boolp(false)})
	if text, picture := app.readPicture(routed, result); picture != nil || !strings.Contains(text, "it is not shown to you: this model does not take images") {
		t.Fatalf("a worker whose row says no: %q, picture=%v", text, picture != nil)
	}
	// And the reverse: the identity's model takes none, the worker's does.
	setModelRow(t, app, "fake", modelCapability{Effort: []string{}, Images: boolp(false)})
	setModelRow(t, app, "text-worker", modelCapability{Effort: []string{}, Images: boolp(true)})
	if _, picture := app.readPicture(routed, result); picture == nil {
		t.Fatal("a worker whose model takes images was answered by the identity's model, which does not")
	}
}

// THE PICTURES THE PAGE DRAWS AND THE PICTURES READ ANSWERS ARE ONE LIST,
// kept in two packages that do not import each other: every type a file
// is found to be is one the page draws under some name, and the page
// draws no type a file is never found to be.
func TestTheImageTypesAreOneList(t *testing.T) {
	drawn := map[string]bool{}
	for _, mediaType := range dashboard.ImageTypes {
		drawn[mediaType] = true
	}
	for _, mediaType := range tools.PictureMediaTypes {
		if !drawn[mediaType] {
			t.Errorf("read answers %s as a picture and the page draws no file as it", mediaType)
		}
		delete(drawn, mediaType)
	}
	for mediaType := range drawn {
		t.Errorf("the page draws %s and read never finds a file to be it", mediaType)
	}
}

// AN IDENTITY CAN LOOK AT AN IMAGE IT MADE. Its read of an image file is
// answered in words, and a model that takes images is shown the picture
// at the round's boundary, in one user turn after the results. A model
// that does not is told so in the answer.
func TestAnIdentityCanLookAtAnImageItMade(t *testing.T) {
	for _, c := range []struct {
		name  string
		row   *modelCapability
		shown bool
		says  string
	}{
		{"a model that takes images", &modelCapability{Effort: []string{}, Images: boolp(true)}, true, "the picture is sent to you once, after the results of the round that read it; to see it in a later turn, read the file again"},
		{"a model that does not", &modelCapability{Effort: []string{}, Images: boolp(false)}, false, "it is not shown to you: this model does not take images"},
		{"nobody has said", nil, false, "it is not shown to you: nobody has said this model takes images"},
		{"over the model's bound", &modelCapability{Effort: []string{}, Images: boolp(true), ImageMaxBytes: 8}, false, "bytes are past this model's bound of 8 for one picture"},
		{"past the model's longer side", &modelCapability{Effort: []string{}, Images: boolp(true), ImageMaxSide: 3}, false, "it is not shown to you: 4 by 3 pixels is past this model's bound of 3 a side"},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, _, dataURL := imageApp(t, c.row)
			obs := &contScript{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				if obs.add(string(body)) == 1 {
					_, _ = io.WriteString(w, callReply("", toolCall("c1", "read", `{"file_path":"uploads/proof.png"}`)))
					return
				}
				_, _ = io.WriteString(w, textReply("it is a page proof"))
			}))
			t.Cleanup(srv.Close)
			app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1, NoStream: true}))
			operatorTurn(t, app, "look at the proof you made")
			reqs := obs.snapshot()
			if len(reqs) != 2 {
				t.Fatalf("a read and an answer made %d requests", len(reqs))
			}
			second := reqs[1]
			if !strings.Contains(second, "is an image: image/png") || !strings.Contains(second, c.says) {
				t.Fatalf("the read's answer does not say what it is and whether it is shown (%q)", c.says)
			}
			if got := strings.Count(second, dataURL) == 1; got != c.shown {
				t.Fatalf("the picture in the next request: %v, want %v", got, c.shown)
			}
			if c.shown && !strings.Contains(second, "sent here: the 1 picture(s) this round's results describe") {
				t.Fatal("the picture came with no line saying what it is")
			}
			var content string
			if err := app.store.DB().QueryRow(`SELECT content FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')='read'`).Scan(&content); err != nil {
				t.Fatalf("the read's record: %v", err)
			}
			if strings.Contains(content, "base64") || !strings.Contains(content, "is an image: image/png") {
				t.Fatalf("the record holds %q", content)
			}
		})
	}
}

// A PICTURE OF A TYPE THE DIALECT WRITES NONE OF IS SENT NONE, and the
// identity is told why, once. Gemini's dialect writes pictures, and no
// GIF: a model there that takes images is shown a PNG and not a GIF, on a
// read and attached alike.
func TestADialectThatWritesNoPictureIsSentNone(t *testing.T) {
	app, _, _ := imageApp(t, &modelCapability{Effort: []string{}, Images: boolp(true)})
	var gif bytes.Buffer
	if err := stdgif.Encode(&gif, image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.configSnapshot().Tools.CWD, "uploads", "moving.gif"), gif.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(app.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	reg.Providers = append(reg.Providers, providerEntry{Name: "google-native", APIType: "gemini", URL: "https://example.invalid"})
	if reg.ModelCapabilities == nil {
		reg.ModelCapabilities = map[string]modelCapability{}
	}
	reg.ModelCapabilities["gem-vision"] = modelCapability{Effort: []string{}, Input: map[string]bool{"image": true}}
	if _, err := saveProvidersFile(app.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	if p := app.imagePolicyFor("google-native", "gem-vision"); p.takes != capYes || p.withheld("image", "image/png") != "" {
		t.Fatalf("a model on a dialect that writes pictures: %+v", p)
	}
	turn := withTurnImages(t.Context(), "google-native", "gem-vision")
	result := tools.Result{Output: "uploads/moving.gif is an image.", Image: &tools.ResultImage{Path: "uploads/moving.gif", MediaType: "image/gif"}}
	if text, picture := app.readPicture(turn, result); picture != nil || !strings.Contains(text, "it is not shown to you: this host does not send image/gif in this provider's dialect") {
		t.Fatalf("a read on that dialect: %q, picture=%v", text, picture != nil)
	}
	ref, err := app.recordConversationRef(t.Context(), "operator", "look at this on the other model")
	if err != nil {
		t.Fatal(err)
	}
	row := store.ConversationTurn{ID: ref.ID, Attached: []string{"uploads/moving.gif", "uploads/proof.png"}}
	images, err := app.picturesWith(turn, &row)
	if err != nil || len(images) != 1 || images[0].MediaType != "image/png" ||
		!slices.Equal(row.NotShown, []string{"uploads/moving.gif, this host does not send image/gif in this provider's dialect"}) {
		t.Fatalf("attached on that dialect: %d sent, noted %q, %v", len(images), row.NotShown, err)
	}
	if state := turnImagesOf(turn); len(state.sent) != 1 || state.sent[0].path != "uploads/proof.png" {
		t.Fatalf("waiting to be recorded as shown: %+v", state.sent)
	}
}

// A PDF, A SOUND AND A VIDEO ARE SHOWN TO A MODEL THAT TAKES THEM, in the
// parts its dialect writes, and recorded as shown when the model answered
// the request that carried them; one the model does not take, or whose
// type the dialect writes none of, stays in the folder, and the identity
// is told why after the file's line. A document no model is shown is told
// by its line alone. What a file is, is read from what it holds.
func TestAnAttachedFileIsShownToAModelThatTakesItsKind(t *testing.T) {
	files := map[string][]byte{
		"brief.pdf":  []byte("%PDF-1.7\n1 0 obj << /Type /Catalog >> endobj\n%%EOF\n"),
		"note.wav":   append([]byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00"), make([]byte, 32)...),
		"note.ogg":   append([]byte("OggS\x00\x02"), make([]byte, 32)...),
		"clip.mp4":   append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), make([]byte, 32)...),
		"notes.docx": []byte("PK\x03\x04 not read as anything a model is shown"),
	}
	for _, c := range []struct {
		name  string
		input map[string]bool
		sent  []string // the parts the request carries
		notes []string // the lines after the files'
	}{
		{"the model takes them", map[string]bool{"pdf": true, "audio": true, "video": true},
			[]string{`"type":"file"`, `"filename":"brief.pdf"`, `"type":"input_audio"`, `"format":"wav"`},
			[]string{"uploads/note.ogg, this host does not send audio/ogg in this provider's dialect", "uploads/clip.mp4, this host does not send video/mp4 in this provider's dialect"}},
		{"the model takes none", map[string]bool{"pdf": false, "audio": false},
			nil,
			[]string{"uploads/brief.pdf, this model does not take PDFs", "uploads/note.wav, this model does not take audio", "uploads/note.ogg, this host does not send audio/ogg in this provider's dialect", "uploads/clip.mp4, this host does not send video/mp4 in this provider's dialect"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, obs, _ := imageApp(t, &modelCapability{Effort: []string{}, Input: c.input, FileTokens: 1000})
			var paths []string
			for _, name := range []string{"brief.pdf", "note.wav", "note.ogg", "clip.mp4", "notes.docx"} {
				if err := os.WriteFile(filepath.Join(app.configSnapshot().Tools.CWD, "uploads", name), files[name], 0o600); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, "uploads/"+name)
			}
			attachedTurn(t, app, "what are these?", paths)
			req := obs.snapshot()[0]
			for _, part := range c.sent {
				if !strings.Contains(req, part) {
					t.Fatalf("the request does not carry %s:\n%s", part, req)
				}
			}
			if c.sent == nil && (strings.Contains(req, `"type":"file"`) || strings.Contains(req, "input_audio")) {
				t.Fatal("a file was sent to a model that takes none of its kind")
			}
			for _, note := range c.notes {
				if !strings.Contains(req, "[not shown to you: "+note+"]") {
					t.Fatalf("the identity is not told %q:\n%s", note, req)
				}
			}
			if strings.Contains(req, "notes.docx,") {
				t.Fatal("a document no model is shown was given a line of its own")
			}
			shown := pictureNotices(t, app, store.ReasonShown, "what are these?")
			if want := map[bool]int{true: 2, false: 0}[c.sent != nil]; len(shown) != want {
				t.Fatalf("recorded as shown: %v, want %d", shown, want)
			}
		})
	}
}

// THE CARD SAYS WHERE EACH ANSWER CAME FROM, and marks a kind the model
// takes that this host does not send it: every kind but pictures, and
// pictures in a dialect whose encoder writes none.
func TestTheCardSaysWhereEachAnswerCameFrom(t *testing.T) {
	cat := "https://openrouter.ai/api/v1/models"
	entry := providerEntry{Name: "Fixture", URL: "https://fixture.example/v1", APIType: "openai", CatalogueAuthor: "anthropic", CapabilityCatalogue: &cat}
	reg := &providerRegistry{Providers: []providerEntry{entry}, ModelCapabilities: map[string]modelCapability{"claude-opus-5": {Input: map[string]bool{"audio": false}}}}
	a := probeApp(t)
	a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {key: probeKey(entry), state: probeOK, meta: map[string]modelMeta{"claude-opus-5": {Input: map[string]bool{"image": true}}}}}
	a.modalities.put(modelModalitiesURL(entry, "claude-opus-5", cat), modelModalities{in: []string{"text", "image", "video"}})
	at := time.Date(2026, 10, 20, 16, 10, 0, 0, time.UTC)
	a.setSubstrateCapability(substrateCapability{provider: entry.Name, model: "claude-opus-5", toolCalls: capYes, checkedAt: at})
	view := a.capabilityView(reg, entry.Name, "claude-opus-5")
	shipped := embeddedRegistry().ModelCapabilities["claude-opus-5"]
	want := map[string]dashboard.CapabilityLine{
		"image": {Kind: "image", State: "yes", From: "Fixture's model list", Types: []string{"image/png", "image/jpeg", "image/gif", "image/webp"}},
		"audio": {Kind: "audio", State: "no", From: "your providers.json", Types: []string{"audio/wav", "audio/mpeg"}},
		"video": {Kind: "video", State: "yes", NotSent: true},
		"pdf":   {Kind: "pdf", State: "no", Types: []string{"application/pdf"}},
	}
	for _, l := range view.Takes {
		w := want[l.Kind]
		if l.Kind == "video" || l.Kind == "pdf" {
			if !strings.HasPrefix(l.From, "the public catalogue at openrouter.ai, ") {
				t.Fatalf("%s: from %q", l.Kind, l.From)
			}
			w.From = l.From
		}
		if !reflect.DeepEqual(l, w) {
			t.Fatalf("takes %s = %+v, want %+v", l.Kind, l, w)
		}
	}
	if view.Tools.From != "measured when this model was chosen, 2026-10-20 16:10 UTC" || view.Tools.State != "yes" {
		t.Fatalf("tools = %+v", view.Tools)
	}
	if text := view.Gives[0]; text.State != "yes" || text.From != "the shipped list ("+shipped.Source+", "+shipped.AsOf+")" {
		t.Fatalf("gives text = %+v", text)
	}
	if view.Window.Tokens != shipped.ContextLength || view.Window.From != "the shipped list ("+shipped.Source+", "+shipped.AsOf+")" {
		t.Fatalf("window = %+v", view.Window)
	}
	gemini := providerEntry{Name: "Gem", URL: "https://gem.example/v1beta", APIType: "gemini"}
	reg = &providerRegistry{Providers: []providerEntry{gemini}}
	takes := a.capabilityView(reg, "Gem", "gemini-3.8-flash").Takes
	if l := takes[3]; l.Kind != "video" || l.State != "yes" || l.NotSent || !slices.Contains(l.Types, "video/mp4") {
		t.Fatalf("a video this host sends in Gemini's dialect: %+v", l)
	}
	if l := takes[0]; slices.Contains(l.Types, "image/gif") || !slices.Contains(l.Types, "image/png") {
		t.Fatalf("the picture types sent in Gemini's dialect: %+v", l)
	}
}

// A FILE'S PRICE DOES NOT END A TURN ITS WORDS WOULD FIT. A request whose
// estimate passes the model's window only with its files is refused by the
// client before it is sent; it is sent without them, the identity is told
// in the request, and the record says what became of the file and of the
// turn's request, where the operator reads it.
func TestAFileTooLargeForTheWindowIsLeftOutNotTheTurn(t *testing.T) {
	app, obs, _ := imageApp(t, &modelCapability{Effort: []string{}, Input: map[string]bool{"pdf": true}, FileTokens: 1_000_000})
	if err := os.WriteFile(filepath.Join(app.configSnapshot().Tools.CWD, "uploads", "brief.pdf"), []byte("%PDF-1.7\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachedTurn(t, app, "read the brief", []string{"uploads/brief.pdf"})
	reqs := obs.snapshot()
	if len(reqs) != 1 || strings.Contains(reqs[0], `"type":"file"`) || !strings.Contains(reqs[0], "did not fit this model's window with the rest of the request") {
		t.Fatalf("the request was not sent without its file, saying so: %d requests\n%s", len(reqs), strings.Join(reqs, "\n---\n"))
	}
	if got := pictureNotices(t, app, store.ReasonNotShown, "read the brief"); len(got) != 1 || got[0] != "uploads/brief.pdf, the request that carried it did not fit the model's window; read the file to see it" {
		t.Fatalf("the file's notice: %q", got)
	}
	rows, err := app.store.DB().Query(`SELECT content FROM conversations WHERE role='system' AND json_extract(details,'$.reason_code')=?`, reasonPicturesRefused)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var notes []string
	for rows.Next() {
		var note string
		if err := rows.Scan(&note); err != nil {
			t.Fatal(err)
		}
		notes = append(notes, note)
	}
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "this turn's picture(s) or file(s) did not fit the model's window") {
		t.Fatalf("the turn's notice: %q", notes)
	}
}

// THE IDENTITY'S READ OF A PDF, A SOUND OR A VIDEO IS SENT TO A MODEL THAT
// TAKES ITS KIND, as a picture's read is: at the round's boundary, priced
// and bounded as an attached file of its kind, and the answer says so; a
// model that does not take the kind, or a type its dialect writes none of,
// is told why in the answer and sent nothing.
func TestAReadOfAPDFASoundOrAVideoIsSentToAModelThatTakesIt(t *testing.T) {
	files := map[string][]byte{
		"brief.pdf": []byte("%PDF-1.7\n%%EOF\n"),
		"note.wav":  append([]byte("RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00"), make([]byte, 32)...),
		"note.ogg":  append([]byte("OggS\x00\x02"), make([]byte, 32)...),
		"clip.mp4":  append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), make([]byte, 32)...),
	}
	types := map[string]string{"brief.pdf": "application/pdf", "note.wav": "audio/wav", "note.ogg": "audio/ogg", "clip.mp4": "video/mp4"}
	for _, c := range []struct {
		name  string
		input map[string]bool
		want  map[string]string // file -> what the answer ends with; "" is sent
	}{
		{"the model takes them", map[string]bool{"pdf": true, "audio": true, "video": true}, map[string]string{
			"brief.pdf": "", "note.wav": "",
			"note.ogg": "[it is not shown to you: this host does not send audio/ogg in this provider's dialect]",
			"clip.mp4": "[it is not shown to you: this host does not send video/mp4 in this provider's dialect]"}},
		{"the model does not", map[string]bool{"pdf": false}, map[string]string{
			"brief.pdf": "[it is not shown to you: this model does not take PDFs]",
			"note.wav":  "[it is not shown to you: nobody has said this model takes audio]"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, _, _ := imageApp(t, &modelCapability{Effort: []string{}, Input: c.input, FileTokens: 1234})
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(app.configSnapshot().Tools.CWD, "uploads", name), body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for name, want := range c.want {
				result := tools.Result{Output: "uploads/" + name + " is a file.", Image: &tools.ResultImage{Path: "uploads/" + name, MediaType: types[name]}}
				text, file := app.readPicture(withTurnImages(t.Context(), "", ""), result)
				if want == "" {
					if file == nil || file.MediaType != types[name] || file.Name != name || file.Tokens != 1234 || !strings.HasSuffix(text, fileSent) {
						t.Fatalf("%s was not sent: %q %+v", name, text, file)
					}
					continue
				}
				if file != nil || !strings.HasSuffix(text, want) {
					t.Fatalf("%s: %q, sent=%v, want %q", name, text, file != nil, want)
				}
			}
		})
	}
}
