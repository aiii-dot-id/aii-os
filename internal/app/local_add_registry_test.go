package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// AN ADD DECIDES ON THE REGISTRY IT WRITES (0.2.1 #2; a review 2026-11-06
// 14:50Z, finding 2). The add's probe is held while a save from the card
// lands; once released, the add's duplicate check and its name are decided on
// the registry as it stands at the write, not on a copy loaded before the
// probe.

// heldLocalServer answers the model list only once release is closed, and
// says on probing when a probe has arrived.
func heldLocalServer(t *testing.T) (port int, probing <-chan struct{}, release chan<- struct{}) {
	t.Helper()
	p := make(chan struct{}, 8)
	r := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		p <- struct{}{}
		<-r
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen"}]}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-r:
		default:
			close(r)
		}
	})
	u, _ := url.Parse(srv.URL)
	port, _ = strconv.Atoi(u.Port())
	ports := localServerPorts
	t.Cleanup(func() { localServerPorts = ports })
	localServerPorts = []localPort{{port, "Test"}}
	return port, p, r
}

func addWhileHeld(t *testing.T, a *App, port int, probing <-chan struct{}, release chan<- struct{}, during func()) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- a.addLocalServer(port, "qwen") }()
	select {
	case <-probing:
	case <-time.After(5 * time.Second):
		t.Fatal("setup: the add never probed")
	}
	during()
	close(release)
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the add never finished once released")
		return nil
	}
}

// A card save takes the name the add would choose, for another address,
// while the add is held: the add must not replace it.
func TestALocalAddDoesNotReplaceAnEntryNamedWhileItProbed(t *testing.T) {
	port, probing, release := heldLocalServer(t)
	a := localApp(t, t.TempDir())
	name := "Local server, port " + strconv.Itoa(port)
	other := "http://192.168.1.50:" + strconv.Itoa(port) + "/v1"
	err := addWhileHeld(t, a, port, probing, release, func() {
		if err := a.setProviderFromCard(providerEntry{Name: name, APIType: "openai", URL: other, DefaultModel: "m"}, false, false); err != nil {
			t.Fatalf("setup: the card save failed: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("the add was refused: %v", err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(reg, name); e == nil || e.URL != other {
		t.Fatalf("the add replaced the entry the card saved while it probed: %q is now %+v", name, e)
	}
	if n := entryAtLocalPort(reg, port); n == "" || n == name {
		t.Fatalf("the add's own entry is missing or took the card's name: %q", n)
	}
}

// A card save adds this very server under another name while the add is
// held: the add must be refused, not add it twice.
func TestALocalAddDoesNotDuplicateAnEndpointSavedWhileItProbed(t *testing.T) {
	port, probing, release := heldLocalServer(t)
	a := localApp(t, t.TempDir())
	err := addWhileHeld(t, a, port, probing, release, func() {
		if err := a.setProviderFromCard(providerEntry{Name: "My server", APIType: "openai", URL: localServerAddress(port), DefaultModel: "qwen", Local: true}, false, false); err != nil {
			t.Fatalf("setup: the card save failed: %v", err)
		}
	})
	reg, rerr := a.loadProviders()
	if rerr != nil {
		t.Fatal(rerr)
	}
	at := 0
	for _, e := range reg.Providers {
		if e.URL == localServerAddress(port) {
			at++
		}
	}
	if err == nil || !strings.Contains(err.Error(), "already points") || at != 1 {
		t.Fatalf("an endpoint saved while the add probed: add said %v, %d entries at the address; want refused as already there, 1", err, at)
	}
}
