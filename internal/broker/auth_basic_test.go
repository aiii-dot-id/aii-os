package broker

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
// .
// .
func TestABasicAuthProfileRidesAsBasic(t *testing.T) {
	var seen string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	t.Setenv("AII_TEST_BASIC", "AC123:tok456")
	t.Setenv("AII_TEST_NOCOLON", "tok456")
	h := newHost(t, newStore(t), Config{
		Grants: map[string]Grant{"p": {Hosts: []string{hostPort}, CredentialHandles: []string{"tw", "bad", "odd"}}},
		AuthProfiles: map[string]AuthProfile{
			"tw":  {SecretEnv: "AII_TEST_BASIC", Host: host, Port: port, Scheme: "basic"},
			"bad": {SecretEnv: "AII_TEST_NOCOLON", Host: host, Port: port, Scheme: "basic"},
			"odd": {SecretEnv: "AII_TEST_BASIC", Host: host, Port: port, Scheme: "digest"},
		},
		Guard: guardFor(ts), Transport: ts.Client().Transport,
	})
	b := h.Bind("p", packagefmt.TierT2, []string{"net.outbound:" + hostPort})
	m := dispatch(t, b, netParams(ts.URL, `{"auth_profile":"tw"}`))
	wantResult(t, m, statusSucceeded, "")
	if seen != "Basic "+base64.StdEncoding.EncodeToString([]byte("AC123:tok456")) {
		t.Fatalf("basic rides as basic: %q", seen)
	}
	raw, _ := m["external_receipt"].MarshalJSON()
	if strings.Contains(string(raw), "tok456") {
		t.Fatal("the secret reached a receipt")
	}
	wantResult(t, dispatch(t, b, netParams(ts.URL, `{"auth_profile":"bad"}`)), statusDenied, reasonAuthInvalid)
	wantResult(t, dispatch(t, b, netParams(ts.URL, `{"auth_profile":"odd"}`)), statusDenied, reasonAuthInvalid)
}
