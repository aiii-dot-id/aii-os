package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// RING 0 ON EVERY HOP. An operation that declared net.outbound for one
// host is held to it by the first URL's ring 0 (scopeDeniesNet); a
// redirect is the same call reaching somewhere else, so it is held to
// the same declaration. Here the plugin's envelope and grant cover two
// hosts and the operation declared only the first: the first answers
// with a redirect to the second, which must never be contacted. A
// redirect inside the declaration still follows, and with no operation
// in flight the grant alone governs, as it always did.
func TestARedirectCannotLeaveTheOperationsDeclaredHosts(t *testing.T) {
	var secondHits atomic.Int32
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		fmt.Fprint(w, `{"second":true}`)
	}))
	defer second.Close()
	first := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/leave":
			http.Redirect(w, r, second.URL+"/x", http.StatusFound)
		case "/stay":
			http.Redirect(w, r, "/final", http.StatusFound)
		default:
			fmt.Fprint(w, `{"final":true}`)
		}
	}))
	defer first.Close()
	firstHost, firstPort := tsHostPort(t, first)
	secondHost, secondPort := tsHostPort(t, second)
	firstHP := fmt.Sprintf("%s:%d", firstHost, firstPort)
	secondHP := fmt.Sprintf("%s:%d", secondHost, secondPort)
	guard := func(_ context.Context, rawURL string) error {
		if strings.HasPrefix(rawURL, first.URL) || strings.HasPrefix(rawURL, second.URL) {
			return nil
		}
		return firewall.FetchGuard(context.Background(), rawURL)
	}
	h := newHost(t, newStore(t), Config{
		Grants:    map[string]Grant{"p": {Hosts: []string{firstHP, secondHP}}},
		Guard:     guard,
		Transport: first.Client().Transport,
	})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + firstHP, "net.outbound:" + secondHP})
	b.BeginOperation(OperationScope{Operation: "fetch", Effects: EffectsReadExternal,
		Capabilities: []string{"net.outbound:" + firstHP}, Declared: true})

	m := dispatch(t, b, netParams(first.URL+"/leave", ""))
	if secondHits.Load() != 0 {
		t.Fatalf("a redirect left the operation's declared hosts: the second host was contacted %d time(s)", secondHits.Load())
	}
	rec := wantResult(t, m, statusFailed, reasonNetRemoteFailed) // the first hop answered; the hop off it was not taken (C2)
	var detail string
	_ = json.Unmarshal(rec["detail"], &detail)
	if !strings.Contains(detail, "outside the operation's declared capabilities") {
		t.Fatalf("the refusal must name the operation's declaration, got %q", detail)
	}

	// A redirect inside the declaration follows.
	wantResult(t, dispatch(t, b, netParams(first.URL+"/stay", "")), statusSucceeded, "")

	// No operation in flight: the envelope and the grant govern alone.
	b.EndOperation()
	wantResult(t, dispatch(t, b, netParams(first.URL+"/leave", "")), statusSucceeded, "")
	if secondHits.Load() != 1 {
		t.Fatalf("with no declaration the granted redirect must follow once, followed %d", secondHits.Load())
	}
}

// The local half of the same rule. A first URL on the operator's own
// network is held to the operation's declaration of net.local
// (scopeDeniesLocal); a redirect from the internet onto a granted local
// device is held to it too. The transport is a function: no sockets.
func TestARedirectOntoTheLocalNetworkNeedsTheOperationsNetLocal(t *testing.T) {
	const api, device = "api.example.test", "192.168.1.20"
	for _, declared := range [][]string{
		{"net.outbound:" + api},
		{"net.outbound:" + api, capNetLocal},
	} {
		admitted := len(declared) == 2
		t.Run(fmt.Sprintf("net.local declared=%v", admitted), func(t *testing.T) {
			var deviceHits atomic.Int32
			transport := authRedirectTransport(func(r *http.Request) (*http.Response, error) {
				header, status := make(http.Header), http.StatusOK
				if r.URL.Hostname() == api {
					header.Set("Location", "http://"+device+":8080/state")
					status = http.StatusFound
				} else {
					deviceHits.Add(1)
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			})
			h := newHost(t, newStore(t), Config{
				Grants: map[string]Grant{"p": {Hosts: []string{api}, Local: []string{device + ":8080"}}},
				Guard: func(_ context.Context, raw string) error {
					if _, err := url.Parse(raw); err != nil {
						return firewall.ErrEgressBlocked
					}
					return nil
				},
				Transport: transport,
			})
			b := h.Bind("p", packagefmt.TierT2, []string{"net.outbound:" + api, capNetLocal})
			b.BeginOperation(OperationScope{Operation: "fetch", Effects: EffectsReadExternal, Capabilities: declared, Declared: true})
			m := dispatch(t, b, netParams("https://"+api+"/start", ""))
			if admitted {
				wantResult(t, m, statusSucceeded, "")
				if deviceHits.Load() != 1 {
					t.Fatalf("a declared local hop must be reached once, reached %d", deviceHits.Load())
				}
				return
			}
			if deviceHits.Load() != 0 {
				t.Fatal("a redirect reached the local network the operation never declared")
			}
			wantResult(t, m, statusDenied, reasonPolicyDeny)
		})
	}
}
