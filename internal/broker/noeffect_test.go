package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// answerReason is the reason code of either reply shape: a result's
// reasonCode, or a capability denial's data.reasonCode.
func answerReason(t *testing.T, m map[string]json.RawMessage) string {
	t.Helper()
	var r struct {
		ReasonCode string `json:"reasonCode"`
		Data       struct {
			ReasonCode string `json:"reasonCode"`
		} `json:"data"`
	}
	raw, _ := json.Marshal(m)
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.ReasonCode != "" {
		return r.ReasonCode
	}
	return r.Data.ReasonCode
}

func TestNoEffectNamesNoCodeAWrittenRequestAnswers(t *testing.T) {
	var reached atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		fmt.Fprint(w, `{}`)
	}))
	defer elsewhere.Close()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/created-then-moved": // POST-redirect-GET, to a host outside the grant
			http.Redirect(w, r, elsewhere.URL+"/result", http.StatusSeeOther)
		case "/down":
			http.Error(w, "down", http.StatusServiceUnavailable)
		default:
			fmt.Fprint(w, `{"ok":true}`)
		}
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	granted := fmt.Sprintf("%s:%d", host, port)
	eh, ep := tsHostPort(t, elsewhere)
	ungranted := fmt.Sprintf("%s:%d", eh, ep)
	guard := func(_ context.Context, rawURL string) error {
		if strings.HasPrefix(rawURL, ts.URL) || strings.HasPrefix(rawURL, elsewhere.URL) {
			return nil // the grant, not the substrate, tells them apart
		}
		return firewall.FetchGuard(context.Background(), rawURL)
	}
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{granted}}, "q": {Hosts: []string{granted, ungranted}}}, Guard: guard, Transport: ts.Client().Transport})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + granted, "net.outbound:" + ungranted})
	outside := h.Bind("q", packagefmt.TierT1, []string{"net.outbound:" + granted})
	body := `{"body":"x","content_type":"text/plain"}`
	for _, c := range []struct {
		name    string
		b       *Binding
		params  string
		reached bool
		reason  string
		effect  string
	}{
		{"a POST answered with a redirect off the grant", b, verbParams("http.post", ts.URL+"/created-then-moved", `{"body":"x","content_type":"text/plain","follow_redirects":true}`), true, reasonNetRemoteFailed, EffectPerformed},
		{"a POST answered 503", b, verbParams("http.post", ts.URL+"/down", body), true, reasonNetRemoteFailed, EffectPerformed},
		{"a host the operator never granted", b, verbParams("http.post", elsewhere.URL+"/", body), false, reasonPolicyDeny, ""},
		{"a host outside the signed envelope", outside, verbParams("http.post", elsewhere.URL+"/", body), false, reasonNotInEnvelope, ""},
		{"a malformed URL", b, verbParams("http.post", "https://", body), false, reasonTargetInvalid, ""},
		{"an argument the broker does not read", b, verbParams("http.post", ts.URL+"/", `{"body":"x","content_type":"text/plain","retry":true}`), false, reasonNetUnknownArgument, ""},
		{"a timeout over the ceiling", b, verbParams("http.post", ts.URL+"/", `{"body":"x","content_type":"text/plain","timeout_ms":600000}`), false, reasonNetTimeoutLimit, ""},
		{"a body over the ceiling", b, verbParams("http.post", ts.URL+"/", `{"body":"`+strings.Repeat("x", DefaultMaxRequestBytes+1)+`","content_type":"text/plain"}`), false, reasonNetRequestTooBig, ""},
		{"a body with no type", b, verbParams("http.post", ts.URL+"/", `{"body":"x"}`), false, reasonArgumentInvalid, ""},
		{"a header the transport owns", b, verbParams("http.post", ts.URL+"/", `{"body":"x","content_type":"text/plain","headers":{"Host":"x"}}`), false, reasonNetHeaderInvalid, ""},
		{"an auth profile below a review-proven tier", b, verbParams("http.post", ts.URL+"/", `{"body":"x","content_type":"text/plain","auth_profile":"bot"}`), false, reasonAuthNotAdmitted, ""},
	} {
		before := reached.Load()
		m := dispatch(t, c.b, c.params)
		reason := answerReason(t, m)
		wrote := reached.Load() != before
		if reason != c.reason || wrote != c.reached {
			t.Fatalf("%s: answered %q (reached a server: %v), want %q (%v) — the fixture no longer shows what it claims", c.name, reason, wrote, c.reason, c.reached)
		}
		if c.effect != "" {
			if rec := wantResult(t, m, statusFailed, c.reason); receiptField(t, rec, "effect") != c.effect {
				t.Errorf("%s: the receipt says %q, want %q", c.name, receiptField(t, rec, "effect"), c.effect)
			}
		}
		if wrote && NoEffect(reason) {
			t.Errorf("%s: a performed request answered %s, which NoEffect names — a channel walk would send the message again", c.name, reason)
		}
		if !wrote && !NoEffect(reason) {
			t.Errorf("%s: %s is minted here before any write, yet NoEffect does not name it", c.name, reason)
		}
	}
}

// THE 401 PREMISE, AND WHERE IT ENDS. A request the API refused with 401
// is refreshed once; a refused refresh answers the profile disconnected,
// and the call had no effect — the server saw one request and refused it.
// A write declared idempotent whose first attempt was written and cut
// off, and whose retry was refused with 401, may have had an effect: it
// answers NET_EFFECT_UNKNOWN, which NoEffect does not name. (A POST, so
// the transport does not replay it on its own and the broker's retry is
// the one observed.)
func TestA401WhoseRefreshIsRefusedHadNoEffect(t *testing.T) {
	var apiCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`) // the authority revoked the grant
	})
	mux.HandleFunc("/api/refused", func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/api/cut-then-refused", func(w http.ResponseWriter, r *http.Request) {
		if apiCalls.Add(1) == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("no hijacker")
				return
			}
			conn, _, _ := hj.Hijack()
			conn.Close() // written, and answered nothing
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "profiles", "svc.json")
	secretFile := filepath.Join(dir, "svc.client")
	if err := os.WriteFile(secretFile, []byte("csecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTokens(t, tokenFile, "at-0", "rt-0", time.Now().Add(2*time.Hour))
	h := newHost(t, newStore(t), Config{
		Grants: map[string]Grant{"p": {Hosts: []string{hostPort}, CredentialHandles: []string{"svc"}}},
		AuthProfiles: map[string]AuthProfile{"svc": {Scheme: SchemeOAuth2, Provider: "custom", ClientID: "cid", ClientSecretFile: secretFile,
			TokenURL: ts.URL + "/token", Hosts: []string{hostPort}, TokenFile: tokenFile}},
		ProfilesDir: filepath.Join(dir, "profiles"),
		Guard:       guardFor(ts), Transport: ts.Client().Transport,
	})
	b := h.Bind("p", packagefmt.TierT2, []string{"net.outbound:" + hostPort})

	m := dispatch(t, b, verbParams("http.post", ts.URL+"/api/refused", `{"body":"x","content_type":"text/plain","auth_profile":"svc"}`))
	wantResult(t, m, statusDenied, reasonAuthDisconnected)
	if apiCalls.Load() != 1 || !NoEffect(reasonAuthDisconnected) {
		t.Fatalf("a request refused at the door (%d seen) answers a code NoEffect names: %v", apiCalls.Load(), NoEffect(reasonAuthDisconnected))
	}

	apiCalls.Store(0)
	m = dispatch(t, b, verbParams("http.post", ts.URL+"/api/cut-then-refused", `{"body":"x","content_type":"text/plain","idempotent":true,"auth_profile":"svc"}`))
	rec := wantResult(t, m, statusFailed, reasonNetEffectUnknown)
	if apiCalls.Load() != 2 || receiptField(t, rec, "effect") != EffectUnknown || NoEffect(reasonNetEffectUnknown) {
		t.Fatalf("a retry refused at the door after an attempt that answered nothing is unknown: %d requests, effect %q", apiCalls.Load(), receiptField(t, rec, "effect"))
	}
}
