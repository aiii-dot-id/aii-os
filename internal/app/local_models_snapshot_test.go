package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ONE PROBE SNAPSHOT FOR THE WHOLE PROVIDERS MESSAGE. The cards and the
// local-model line are read from the same probes, so they cannot disagree.
// Read separately, they can: a probe is stamped when it starts and a batch
// waits for its slowest entry, so with a short cache life a slow provider
// lets the local entry's fresh probe expire before the local line reads it.
// The local line then probes again, and here the second probe fails: the
// card says answering while the line says nothing answers.
func TestTheCardsAndTheLocalLineReadOneProbeSnapshot(t *testing.T) {
	// asked counts probes (GET /v1/models); a probe also asks a few model-detail
	// paths (/props, /api/v0/models, /api/ps), which are not counted.
	var asked atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if asked.Add(1) > 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"metal-model"}]}`))
	}))
	t.Cleanup(local.Close)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2500 * time.Millisecond)
		_, _ = w.Write([]byte(`{"data":[{"id":"s"}]}`))
	}))
	t.Cleanup(slow.Close)

	a := localApp(t, t.TempDir(),
		providerEntry{Name: "Slow", APIType: "openai", URL: slow.URL + "/v1", DefaultModel: "s"},
		providerEntry{Name: "Metal", APIType: "openai", URL: local.URL + "/v1", Local: true, DefaultModel: "metal-model"})
	a.cfg.LLM.ModelListCacheSeconds = 1

	dir := a.providerDirectory()
	card := ""
	for _, p := range dir.Providers {
		if p.Name == "Metal" {
			card = p.Status
		}
	}
	if dir.Local == nil {
		t.Fatal("no local-model state in the providers message")
	}
	line := dir.Local.Provider
	if (card == probeOK) != (line == "Metal") {
		t.Fatalf("the card and the local line disagree: card status %q, local line names %q (the local server was probed %d times)", card, line, asked.Load())
	}
	if card != probeOK || line != "Metal" || dir.Local.Model != "metal-model" {
		t.Fatalf("card %q, line %+v: want both answering from the one probe", card, *dir.Local)
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("the local server was probed %d times for one providers message, want 1", n)
	}
}
