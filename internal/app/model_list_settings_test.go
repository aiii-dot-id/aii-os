package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// THE PROVIDER PROBE'S TIMEOUT AND CACHE ARE THE OPERATOR'S
// (llm.model_list_timeout_seconds, llm.model_list_cache_seconds). They were
// constants, three seconds and a minute. Unset they are those still; set,
// the next probe is held to them: a provider slower than the timeout is
// read as not answering, and an answer older than the cache is asked again.
// Neither builds a new client.
func TestTheProviderProbesTimeoutAndCacheAreTheOperatorsSettings(t *testing.T) {
	if timeout, kept := (&App{}).modelListBounds(); timeout != 3*time.Second || kept != time.Minute {
		t.Fatalf("with nothing configured the probe has %s and its answer stands %s; want the three seconds and the minute it always had", timeout, kept)
	}

	var hits atomic.Int32
	var delay atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-time.After(time.Duration(delay.Load())):
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
	}))
	t.Cleanup(srv.Close)
	a := &App{cfg: defaultConfig()}
	set := func(timeout, cache int) {
		a.cfgMu.Lock()
		a.cfg.LLM.ModelListTimeoutSeconds, a.cfg.LLM.ModelListCacheSeconds = timeout, cache
		a.cfgMu.Unlock()
		a.providers.runtime.mu.Lock()
		a.providers.runtime.provStatus = nil // what the last probe found is not this one's
		a.providers.runtime.mu.Unlock()
	}
	reg := &providerRegistry{Providers: []providerEntry{{Name: "Metal", URL: srv.URL + "/v1"}}}

	// A provider that answers in two seconds: inside the default three, and
	// past a timeout of one.
	delay.Store(int64(2 * time.Second))
	set(0, 0)
	if got := a.probeProviders(reg)["Metal"].state; got != probeOK {
		t.Fatalf("unset, a provider answering in two seconds reads %q; want %q", got, probeOK)
	}
	set(1, 0)
	began := time.Now()
	if got := a.probeProviders(reg)["Metal"].state; got == probeOK {
		t.Fatalf("llm.model_list_timeout_seconds 1: a provider answering in two seconds still read %q", got)
	}
	if took := time.Since(began); took > 1900*time.Millisecond {
		t.Fatalf("llm.model_list_timeout_seconds 1: the probe waited %s", took)
	}

	// An answer stands for the cache and is asked again after it.
	delay.Store(0)
	set(0, 1)
	a.probeProviders(reg)
	first := hits.Load()
	a.probeProviders(reg)
	if hits.Load() != first {
		t.Fatalf("llm.model_list_cache_seconds 1: an answer was asked again inside its second")
	}
	time.Sleep(1100 * time.Millisecond)
	a.probeProviders(reg)
	if hits.Load() != first+1 {
		t.Fatalf("llm.model_list_cache_seconds 1: an answer older than its second was not asked again (%d asks, want %d)", hits.Load(), first+1)
	}
	set(0, 0)
	a.probeProviders(reg)
	before := hits.Load()
	time.Sleep(1100 * time.Millisecond)
	a.probeProviders(reg)
	if hits.Load() != before {
		t.Fatalf("unset, an answer a second old was asked again; the default is a minute")
	}

	// Read at each probe: a change of them alone builds no client.
	was, now := a.cfg.LLM, a.cfg.LLM
	now.ModelListTimeoutSeconds, now.ModelListCacheSeconds = 9, 99
	if llmRuntimeChanged(now, was) {
		t.Fatalf("a change of the probe's numbers alone was read as a change of the client")
	}
}
