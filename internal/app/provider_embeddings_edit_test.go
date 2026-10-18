package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// A PROVIDER SAVE KEEPS, SETS OR CLEARS THE EMBEDDINGS MODEL BY WHAT IT
// SAYS OF IT. The model is what memory's meaning layer is built from, and
// until now only the file could name it: the refusal a resident reads has
// always pointed at Settings → Providers, where no field was. Now the card
// carries it, and the three cases are decided by the server from the frame
// as it arrives over a real socket:
//
//   - a save that says nothing of it keeps the stored one. That is an
//     older page or an API caller; erasing the model would drop every
//     vector at the next backfill for an edit that never mentioned it;
//   - a save that names a model stores it;
//   - a save that sends it empty clears it. That is the operator emptying
//     the field.
//
// The listing a save answers with carries the stored model, so the page
// shows what the file holds and not what it last typed.
func TestAProviderSaveKeepsSetsOrClearsTheEmbeddingsModelByWhatItSays(t *testing.T) {
	a, conn := providerPage(t)
	const entry = `"name":"Keyed","api_type":"openai","endpoint":"http://127.0.0.1:1/v1","default_model":"m","default":true,"chat":true,"has_key":true`
	stored := func() string {
		t.Helper()
		reg, err := a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range reg.Providers {
			if e.Name == "Keyed" {
				return e.EmbeddingsModel
			}
		}
		t.Fatal("the saved entry is gone")
		return ""
	}
	for _, c := range []struct {
		name, said string
		want       string
	}{
		{"a save that says nothing of it keeps it", ``, "bge-m3"},
		{"a save that names a model stores it", `,"embeddings_model":"text-embedding-3-small"`, "text-embedding-3-small"},
		{"a model is stored without the spaces typed around it", `,"embeddings_model":"  nomic-embed-text "`, "nomic-embed-text"},
		{"a save that sends it empty clears it", `,"embeddings_model":""`, ""},
		{"a save that sends only spaces clears it", `,"embeddings_model":"   "`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			with := keyedProviderEntry()
			with.EmbeddingsModel = "bge-m3"
			writeTestProviders(t, ".", with)
			frame := pageFrame(t, conn, "save", `{"type":"provider_set","request_id":"save","entry":{`+entry+c.said+`}}`)
			if got := stored(); got != c.want {
				t.Fatalf("the stored embeddings model after the save is %q, want %q", got, c.want)
			}
			var listing struct {
				Providers []struct {
					Name            string `json:"name"`
					EmbeddingsModel string `json:"embeddings_model"`
				} `json:"providers"`
			}
			if err := json.Unmarshal(frame, &listing); err != nil {
				t.Fatal(err)
			}
			shown, found := "", false
			for _, p := range listing.Providers {
				if p.Name == "Keyed" {
					shown, found = p.EmbeddingsModel, true
				}
			}
			if !found || shown != c.want {
				t.Fatalf("the listing the save answers with shows %q (entry found: %v), want %q:\n%s", shown, found, c.want, frame)
			}
			// The key the entry holds is untouched by any of it.
			if got := storedKey(t, a); got != keptSecret {
				t.Fatalf("a save about the embeddings model changed the stored key to %q", got)
			}
		})
	}
}

// A new entry made from the card holds the model the save named for it.
func TestANewProviderFromTheCardHoldsTheEmbeddingsModelItNamed(t *testing.T) {
	a, conn := providerPage(t)
	writeTestProviders(t, ".", keyedProviderEntry())
	pageFrame(t, conn, "save", `{"type":"provider_set","request_id":"save","entry":{"name":"Second","api_type":"openai","endpoint":"http://127.0.0.1:2/v1","default_model":"m","chat":true,"embeddings_model":"bge-m3"}}`)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range reg.Providers {
		names = append(names, e.Name+"="+e.EmbeddingsModel)
	}
	if got := strings.Join(names, " "); got != "Keyed= Second=bge-m3" {
		t.Fatalf("after adding an entry with a model: %s", got)
	}
}
