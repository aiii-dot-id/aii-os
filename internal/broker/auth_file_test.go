package broker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// A bearer profile whose secret is a FILE — the shape a key pasted on a
// plugin's card takes — rides the file's trimmed content as the bearer
// token, read at the call: a replaced file rides the new key with no
// restart, a removed one is refused as a missing secret, and the value
// never reaches the receipt.
func TestABearerProfileRidesItsSecretFileReadAtTheCall(t *testing.T) {
	var seen string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	secret := filepath.Join(t.TempDir(), "p.api_key.key")
	if err := os.WriteFile(secret, []byte("sk-pasted-111\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHost(t, newStore(t), Config{
		Grants:       map[string]Grant{"p": {Hosts: []string{hostPort}, CredentialHandles: []string{"p.api_key"}}},
		AuthProfiles: map[string]AuthProfile{"p.api_key": {Scheme: "bearer", SecretFile: secret, Host: host, Port: port}},
		Guard:        guardFor(ts), Transport: ts.Client().Transport,
	})
	b := h.Bind("p", packagefmt.TierT3, []string{"net.outbound:" + hostPort})

	m := dispatch(t, b, netParams(ts.URL, `{"auth_profile":"p.api_key"}`))
	wantResult(t, m, statusSucceeded, "")
	if seen != "Bearer sk-pasted-111" {
		t.Fatalf("the file's key rides as the bearer token: %q", seen)
	}
	raw, _ := m["external_receipt"].MarshalJSON()
	if strings.Contains(string(raw), "sk-pasted") {
		t.Fatal("the key reached a receipt")
	}

	if err := os.WriteFile(secret, []byte("sk-pasted-222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantResult(t, dispatch(t, b, netParams(ts.URL, `{"auth_profile":"p.api_key"}`)), statusSucceeded, "")
	if seen != "Bearer sk-pasted-222" {
		t.Fatalf("a replaced key rides at the next call: %q", seen)
	}

	if err := os.Remove(secret); err != nil {
		t.Fatal(err)
	}
	wantResult(t, dispatch(t, b, netParams(ts.URL, `{"auth_profile":"p.api_key"}`)), statusDenied, reasonAuthSecretMissing)
}
