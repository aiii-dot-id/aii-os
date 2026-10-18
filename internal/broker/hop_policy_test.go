package broker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// THE HOP POLICY, AS A TABLE. Every hop of a brokered HTTP call — the
// first, and each redirect the client is asked to follow — passes one
// decision: the substrate guard, then the auth profile's pin (an oauth2
// profile's host list over HTTPS; any other profile's exact first
// host:port over HTTPS, or plain http to the same local device at the
// operator's word), then either the operator's local scopes (a plugin
// whose envelope declares net.local) or the signed envelope AND the
// operator grant — and, for either, the in-flight operation's own
// declaration (net.local, or its net.outbound scopes). Each row starts
// at /start, is redirected to hop, and is admitted when the hop is
// reached and refused, with nothing sent to it, when it is not. The
// transport is a function: no DNS, no sockets.
func TestTheHopPolicyAdmitsAndRefusesEachHop(t *testing.T) {
	const (
		api     = "api.example.test"
		alt     = "alt.example.test"
		other   = "other.example.test"
		blocked = "blocked.example.test"
		device  = "192.168.1.20"
		device2 = "192.168.1.21"
	)
	outbound := func(hosts ...string) []string {
		var out []string
		for _, h := range hosts {
			out = append(out, "net.outbound:"+h)
		}
		return out
	}
	type row struct {
		name      string
		first     string
		hop       string
		envelope  []string
		grant     []string
		local     []string
		auth      string // "", "bearer", "oauth2"
		profile   AuthProfile
		plaintext bool
		tier      packagefmt.Tier
		declares  []string // the operation in flight's declared capabilities; nil: none in flight
		admitted  bool
	}
	bearerAPI := AuthProfile{Scheme: "bearer", SecretEnv: "AII_TEST_HOP_SECRET", Host: api, Port: 443}
	bearerDevice := AuthProfile{Scheme: "bearer", SecretEnv: "AII_TEST_HOP_SECRET", Host: device, Port: 8080}
	rows := []row{
		// No credential: the envelope and the grant, on every hop.
		{name: "same host, relative", first: "https://" + api + "/start", hop: "/final",
			envelope: outbound(api), grant: []string{api}, admitted: true},
		{name: "another host in envelope and grant", first: "https://" + api + "/start", hop: "https://" + other + "/final",
			envelope: outbound(api, other), grant: []string{api, other}, admitted: true},
		{name: "another host granted, not in the envelope", first: "https://" + api + "/start", hop: "https://" + other + "/final",
			envelope: outbound(api), grant: []string{api, other}},
		{name: "another host in the envelope, not granted", first: "https://" + api + "/start", hop: "https://" + other + "/final",
			envelope: outbound(api, other), grant: []string{api}},
		{name: "a plain-http hop is the grant's question, not the scheme's", first: "https://" + api + "/start", hop: "http://" + other + "/final",
			envelope: outbound(api, other), grant: []string{api, other}, admitted: true},
		{name: "port-scoped: the scoped port", first: "https://" + api + "/start", hop: "https://" + api + ":443/final",
			envelope: outbound(api + ":443"), grant: []string{api + ":443"}, admitted: true},
		{name: "port-scoped: another port", first: "https://" + api + "/start", hop: "https://" + api + ":8443/final",
			envelope: outbound(api + ":443"), grant: []string{api + ":443"}},
		{name: "port-scoped: the default port of plain http", first: "https://" + api + "/start", hop: "http://" + api + "/final",
			envelope: outbound(api + ":443"), grant: []string{api + ":443"}},
		{name: "the guard outranks envelope and grant", first: "https://" + api + "/start", hop: "https://" + blocked + "/final",
			envelope: outbound(api, blocked), grant: []string{api, blocked}},
		{name: "host matching ignores case", first: "https://" + api + "/start", hop: "https://OTHER.example.test/final",
			envelope: outbound(api, other), grant: []string{api, other}, admitted: true},

		{name: "local to a granted device", first: "http://" + device + ":8080/start", hop: "http://" + device2 + ":8080/final",
			envelope: []string{capNetLocal}, local: []string{device + ":8080", device2 + ":8080"}, tier: packagefmt.TierT1, admitted: true},
		{name: "local to a device no scope names", first: "http://" + device + ":8080/start", hop: "http://" + device2 + ":8080/final",
			envelope: []string{capNetLocal}, local: []string{device + ":8080"}, tier: packagefmt.TierT1},
		{name: "local to the internet, not granted", first: "http://" + device + ":8080/start", hop: "https://" + other + "/final",
			envelope: []string{capNetLocal}, local: []string{device + ":8080"}, tier: packagefmt.TierT1},
		{name: "public to a granted device, net.local declared", first: "https://" + api + "/start", hop: "http://" + device + ":8080/final",
			envelope: append(outbound(api), capNetLocal), grant: []string{api}, local: []string{device + ":8080"}, admitted: true},
		{name: "public to a device, net.local not declared", first: "https://" + api + "/start", hop: "http://" + device + ":8080/final",
			envelope: outbound(api), grant: []string{api}, local: []string{device + ":8080"}},

		// The operation in flight's own declaration (ring 0) bounds every
		// hop as it bounds the first URL.
		{name: "operation: a hop it declared", first: "https://" + api + "/start", hop: "https://" + other + "/final",
			envelope: outbound(api, other), grant: []string{api, other}, declares: outbound(api, other), admitted: true},
		{name: "operation: a granted hop it did not declare", first: "https://" + api + "/start", hop: "https://" + other + "/final",
			envelope: outbound(api, other), grant: []string{api, other}, declares: outbound(api)},
		{name: "operation: a local hop with net.local declared", first: "https://" + api + "/start", hop: "http://" + device + ":8080/final",
			envelope: append(outbound(api), capNetLocal), grant: []string{api}, local: []string{device + ":8080"},
			declares: append(outbound(api), capNetLocal), admitted: true},
		{name: "operation: a local hop without net.local declared", first: "https://" + api + "/start", hop: "http://" + device + ":8080/final",
			envelope: append(outbound(api), capNetLocal), grant: []string{api}, local: []string{device + ":8080"},
			declares: outbound(api)},

		// A bearer profile pins its first host:port and HTTPS.
		{name: "bearer: same destination", first: "https://" + api + "/start", hop: "/final",
			envelope: outbound(api, other), grant: []string{api, other}, auth: "bearer", profile: bearerAPI, admitted: true},
		{name: "bearer: explicit default port", first: "https://" + api + "/start", hop: "https://" + api + ":443/final",
			envelope: outbound(api), grant: []string{api}, auth: "bearer", profile: bearerAPI, admitted: true},
		{name: "bearer: another granted host", first: "https://" + api + "/start", hop: "https://" + other + "/final",
			envelope: outbound(api, other), grant: []string{api, other}, auth: "bearer", profile: bearerAPI},
		{name: "bearer: another port", first: "https://" + api + "/start", hop: "https://" + api + ":444/final",
			envelope: outbound(api), grant: []string{api}, auth: "bearer", profile: bearerAPI},
		{name: "bearer: downgrade on the same port", first: "https://" + api + "/start", hop: "http://" + api + ":443/final",
			envelope: outbound(api), grant: []string{api}, auth: "bearer", profile: bearerAPI},
		{name: "bearer: plaintext consent is for the local device only", first: "https://" + api + "/start", hop: "http://" + api + ":443/final",
			envelope: outbound(api), grant: []string{api}, auth: "bearer", profile: bearerAPI, plaintext: true},
		{name: "bearer: the consented local device over http", first: "http://" + device + ":8080/start", hop: "/final",
			envelope: []string{capNetLocal}, local: []string{device + ":8080"}, auth: "bearer", profile: bearerDevice, plaintext: true, tier: packagefmt.TierT1, admitted: true},
		{name: "bearer: the consented device's neighbour", first: "http://" + device + ":8080/start", hop: "http://" + device2 + ":8080/final",
			envelope: []string{capNetLocal}, local: []string{device + ":8080", device2 + ":8080"}, auth: "bearer", profile: bearerDevice, plaintext: true, tier: packagefmt.TierT1},

		// An oauth2 profile pins its authority's host list, over HTTPS.
		{name: "oauth2: another of its hosts", first: "https://" + api + "/start", hop: "https://" + alt + "/final",
			envelope: outbound(api, alt), grant: []string{api, alt}, auth: "oauth2", admitted: true},
		{name: "oauth2: one of its hosts over http", first: "https://" + api + "/start", hop: "http://" + alt + ":443/final",
			envelope: outbound(api, alt), grant: []string{api, alt}, auth: "oauth2"},
		{name: "oauth2: a granted host off its list", first: "https://" + api + "/start", hop: "https://" + other + "/final",
			envelope: outbound(api, other), grant: []string{api, other}, auth: "oauth2"},
		{name: "oauth2: one of its hosts the grant does not cover", first: "https://" + api + "/start", hop: "https://" + alt + "/final",
			envelope: outbound(api, alt), grant: []string{api}, auth: "oauth2"},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AII_TEST_HOP_SECRET", "fixture-hop-token")
			var finalHits, startHits atomic.Int32
			transport := authRedirectTransport(func(r *http.Request) (*http.Response, error) {
				header, status := make(http.Header), http.StatusOK
				if r.URL.Path == "/start" {
					startHits.Add(1)
					header.Set("Location", tc.hop)
					status = http.StatusFound
				} else {
					finalHits.Add(1)
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
			})
			guard := func(_ context.Context, raw string) error {
				u, err := url.Parse(raw)
				if err != nil || strings.EqualFold(u.Hostname(), blocked) {
					return firewall.ErrEgressBlocked
				}
				return nil
			}
			profiles := map[string]AuthProfile{}
			args, profilesDir := "", ""
			switch tc.auth {
			case "bearer":
				profiles["api"] = tc.profile
				args = `{"auth_profile":"api"}`
			case "oauth2":
				dir := t.TempDir()
				profilesDir = dir
				tokenFile := filepath.Join(dir, "api.json")
				writeTokens(t, tokenFile, "fixture-access", "fixture-refresh", time.Now().Add(2*time.Hour))
				secretFile := filepath.Join(dir, "api.client")
				if err := os.WriteFile(secretFile, []byte("csecret\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				profiles["api"] = AuthProfile{Scheme: SchemeOAuth2, Provider: "custom", ClientID: "cid", ClientSecretFile: secretFile,
					TokenURL: "https://" + api + "/token", Hosts: []string{api + ":443", alt + ":443"}, TokenFile: tokenFile}
				args = `{"auth_profile":"api"}`
			}
			var handles []string
			if tc.auth != "" {
				handles = []string{"api"}
			}
			h := newHost(t, newStore(t), Config{
				Grants: map[string]Grant{"p": {Hosts: tc.grant, Local: tc.local, CredentialHandles: handles,
					PlaintextCredentials: tc.plaintext}},
				AuthProfiles: profiles, ProfilesDir: profilesDir,
				Guard: guard, Transport: transport,
			})
			tier := tc.tier
			if tier == packagefmt.TierT0 {
				tier = packagefmt.TierT2
			}
			b := h.Bind("p", tier, tc.envelope)
			if tc.declares != nil {
				b.BeginOperation(OperationScope{Operation: "fetch", Effects: EffectsReadExternal, Capabilities: tc.declares, Declared: true})
			}
			m := dispatch(t, b, netParams(tc.first, args))
			if startHits.Load() != 1 {
				t.Fatalf("the first hop must be admitted and sent once (sent %d): %s", startHits.Load(), m["detail"])
			}
			if tc.admitted {
				wantResult(t, m, statusSucceeded, "")
				if finalHits.Load() != 1 {
					t.Fatalf("an admitted hop must be reached once, reached %d", finalHits.Load())
				}
				return
			}
			if finalHits.Load() != 0 {
				t.Fatal("a refused hop reached the transport")
			}
			// The function transport writes nothing a trace sees, so a
			// refused hop reads as the egress policy's denial.
			wantResult(t, m, statusDenied, reasonPolicyDeny)
		})
	}
}

// The one branch no redirect can reach: the client parses a Location
// before it asks, so only a caller of admit itself can hand it a URL
// that does not parse. It is refused as egress, after the guard.
func TestAHopThatDoesNotParseIsRefused(t *testing.T) {
	h := hopPolicy{guard: func(context.Context, string) error { return nil }}
	if err := h.admit(context.Background(), "http://[::1"); !errors.Is(err, firewall.ErrEgressBlocked) {
		t.Fatalf("an unparseable hop must be refused as egress: %v", err)
	}
}
