package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoveryNeedsAKeyOnlyWhenNoneWasShown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{Name: "Keyed", APIType: "openai", URL: srv.URL + "/v1"})
	a := firstbootApp(t, dir)

	for _, h := range []struct {
		name     string
		discover func(string, string, string) ([]string, bool, error)
	}{
		{"before birth", a.buildFirstbootHandler().DiscoverModels},
		{"live", a.buildLiveHandler().DiscoverModels},
	} {
		models, needsKey, err := h.discover("Keyed", "", "")
		if err == nil || !needsKey || len(models) != 0 {
			t.Errorf("%s, no key: models %v, needsKey %v, err %v — want a refusal that needs a key", h.name, models, needsKey, err)
		}
		_, needsKey, err = h.discover("Keyed", "", "typed-key")
		if err == nil || needsKey {
			t.Errorf("%s, a refused key: needsKey %v, err %v — want a refusal that does not ask for a key", h.name, needsKey, err)
		}
	}
}
