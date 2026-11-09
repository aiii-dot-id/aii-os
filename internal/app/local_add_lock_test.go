package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TWO ADDS AT ONCE ARE ONE AT A TIME (0.2.1 #2 item 7). Off the socket's
// read loop, two adds of one server (two tabs) could both pass the "already
// points" check before either saved. The server here holds each probe until
// a second one arrives or a second passes, so without the add lock both
// probes meet and both adds save; with it, the second add finds the first's
// entry and is refused.
func TestTwoAddsOfOneServerAtOnceAddItOnce(t *testing.T) {
	var mu sync.Mutex
	arrived := 0
	met := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrived++
		if arrived == 2 {
			close(met)
		}
		mu.Unlock()
		select {
		case <-met:
		case <-time.After(time.Second):
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen"}]}`))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	ports := localServerPorts
	t.Cleanup(func() { localServerPorts = ports })
	localServerPorts = []localPort{{port, "Test"}}

	a := localApp(t, t.TempDir())
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = a.addLocalServer(port, "qwen") }()
	}
	wg.Wait()

	added, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			added++
		case strings.Contains(err.Error(), "already points"):
			refused++
		default:
			t.Errorf("an add failed for another reason: %v", err)
		}
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	entries := 0
	for _, e := range reg.Providers {
		if e.Local {
			entries++
		}
	}
	if added != 1 || refused != 1 || entries != 1 {
		t.Fatalf("two adds at once: %d added, %d refused as already there, %d local entries; want 1, 1, 1", added, refused, entries)
	}
}
