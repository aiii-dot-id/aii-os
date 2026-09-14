package broker

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
// .
// .

func localDevice(t *testing.T) (*httptest.Server, string, int) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/leave":
			http.Redirect(w, r, "https://example.com/", http.StatusFound)
		default:
			fmt.Fprintf(w, `{"auth":%q,"path":%q}`, r.Header.Get("Authorization"), r.URL.Path)
		}
	}))
	t.Cleanup(ts.Close)
	host, port := tsHostPort(t, ts)
	return ts, host, port
}

func TestLocalGrantAdmitsTheOperatorsDeviceAtAnyTier(t *testing.T) {
	ts, host, port := localDevice(t)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	for _, grant := range []string{hostPort, "127.0.0.0/8:*", fmt.Sprintf("127.0.0.1:%d", port)} {
		st := newStore(t)
		h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Local: []string{grant}}}})
		b := h.Bind("p", packagefmt.TierT0, []string{"net.local"})
		m := dispatch(t, b, netParams(ts.URL+"/state", ""))
		rec := wantResult(t, m, statusSucceeded, "")
		if receiptField(t, rec, "effect") != EffectPerformed {
			t.Fatalf("grant %q: effect %s", grant, rec["effect"])
		}
	}
}

func TestALocalTargetNeedsTheClassInTheEnvelope(t *testing.T) {
	ts, host, port := localDevice(t)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	// .
	// .
	// .
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Local: []string{hostPort}}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:api.example.test:443"})
	m := dispatch(t, b, netParams(ts.URL+"/state", ""))
	wantErrorReason(t, m, reasonNotInEnvelope)
	assertNoReceipts(t, st, "p")
	// .
	// .
	// .
	st2 := newStore(t)
	h2 := newHost(t, st2, Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}, Local: []string{hostPort}}}})
	b2 := h2.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})
	m = dispatch(t, b2, netParams(ts.URL+"/state", ""))
	rec := wantResult(t, m, statusDenied, reasonPolicyDeny)
	var detail string
	_ = json.Unmarshal(rec["detail"], &detail)
	if !strings.Contains(detail, "egress guard") || !strings.Contains(detail, "net.local") {
		t.Fatalf("the denial names the missing class: %q", detail)
	}
	recs, _ := st2.PluginReceipts("p")
	for _, r := range recs {
		if r.Success {
			t.Fatalf("a guard denial never mints a success receipt: %+v", r)
		}
	}
}

func TestALocalTargetNoGrantNamesIsRefusedWithTheKey(t *testing.T) {
	ts, host, port := localDevice(t)
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Local: []string{fmt.Sprintf("%s:%d", host, port+1)}}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.local"})
	m := dispatch(t, b, netParams(ts.URL+"/state", ""))
	rec := wantResult(t, m, statusDenied, reasonPolicyDeny)
	var detail string
	_ = json.Unmarshal(rec["detail"], &detail)
	if !strings.Contains(detail, "no local grant names it") || !strings.Contains(detail, "plugins.grants.p.local") {
		t.Fatalf("the denial names the key the operator sets: %q", detail)
	}
	// .
	h2 := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{fmt.Sprintf("%s:%d", host, port)}}}})
	b2 := h2.Bind("p", packagefmt.TierT1, []string{"net.local", "net.outbound:" + fmt.Sprintf("%s:%d", host, port)})
	m = dispatch(t, b2, netParams(ts.URL+"/state", ""))
	wantResult(t, m, statusDenied, reasonPolicyDeny)
}

func TestALocalGrantNeverReachesThisHostsOwnListener(t *testing.T) {
	ts, host, port := localDevice(t)
	st := newStore(t)
	h := newHost(t, st, Config{
		Grants:      map[string]Grant{"p": {Local: []string{"127.0.0.0/8:*"}}},
		OwnListener: func(ip net.IP, p int) bool { return ip.IsLoopback() && p == port },
	})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.local"})
	m := dispatch(t, b, netParams(ts.URL+"/ws", ""))
	rec := wantResult(t, m, statusDenied, reasonPolicyDeny)
	var detail string
	_ = json.Unmarshal(rec["detail"], &detail)
	if !strings.Contains(detail, "own listener") {
		t.Fatalf("the denial says why: %q", detail)
	}
	_ = host
}

func TestACredentialOverPlainHTTPOnTheLocalNetworkNeedsConsent(t *testing.T) {
	ts, host, port := localDevice(t)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	t.Setenv("AII_TEST_LOCAL_TOKEN", "ha-long-lived-token")
	profile := AuthProfile{Scheme: "bearer", Host: host, Port: port, SecretEnv: "AII_TEST_LOCAL_TOKEN"}
	withoutConsent := newHost(t, newStore(t), Config{
		Grants:       map[string]Grant{"p": {Local: []string{hostPort}, CredentialHandles: []string{"ha"}}},
		AuthProfiles: map[string]AuthProfile{"ha": profile},
	})
	b := withoutConsent.Bind("p", packagefmt.TierT1, []string{"net.local"})
	m := dispatch(t, b, netParams(ts.URL+"/api", `{"auth_profile":"ha"}`))
	rec := wantResult(t, m, statusDenied, reasonAuthRequiresHTTPS)
	var detail string
	_ = json.Unmarshal(rec["detail"], &detail)
	if !strings.Contains(detail, "plaintext_credentials") {
		t.Fatalf("the denial names the consent to give: %q", detail)
	}
	withConsent := newHost(t, newStore(t), Config{
		Grants:       map[string]Grant{"p": {Local: []string{hostPort}, CredentialHandles: []string{"ha"}, PlaintextCredentials: true}},
		AuthProfiles: map[string]AuthProfile{"ha": profile},
	})
	// .
	// .
	b = withConsent.Bind("p", packagefmt.TierT1, []string{"net.local"})
	m = dispatch(t, b, netParams(ts.URL+"/api", `{"auth_profile":"ha"}`))
	wantResult(t, m, statusSucceeded, "")
	var or map[string]interface{}
	_ = json.Unmarshal(m["operation_result"], &or)
	body, _ := or["body"].(string)
	if !strings.Contains(body, "Bearer ha-long-lived-token") && !strings.Contains(fmt.Sprint(or), "ha-long-lived-token") {
		t.Fatalf("the device received the credential: %v", or)
	}
	_ = os.Getenv
}

func TestARedirectFromTheDeviceToTheInternetIsRefusedUnlessGranted(t *testing.T) {
	ts, host, port := localDevice(t)
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Local: []string{fmt.Sprintf("%s:%d", host, port)}}}})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.local"})
	m := dispatch(t, b, netParams(ts.URL+"/leave", ""))
	rec := wantResult(t, m, statusDenied, reasonPolicyDeny)
	var detail string
	_ = json.Unmarshal(rec["detail"], &detail)
	if !strings.Contains(strings.ToLower(detail), "redirect") && !strings.Contains(strings.ToLower(detail), "egress") && !strings.Contains(strings.ToLower(detail), "granted hosts") {
		t.Fatalf("a hop off the local network is the guard's to refuse: %q", detail)
	}
}
