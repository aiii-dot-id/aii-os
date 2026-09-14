package broker

import (
	"encoding/json"
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
// .
// .
// .
// .
// .
func TestAPathAuthProfileStandsInTheURLAndIsScrubbedFromWhatReturns(t *testing.T) {
	const secret = "123456:ABC-def_ghi"
	var seenPath string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		// .
		// .
		w.Header().Set("Location", "https://echo.example"+r.URL.Path)
		fmt.Fprintf(w, `{"ok":true,"path":%q}`, r.URL.Path)
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	t.Setenv("AII_TEST_PATH_SECRET", secret)
	h := newHost(t, newStore(t), Config{
		Grants: map[string]Grant{"p": {Hosts: []string{hostPort}, CredentialHandles: []string{"tg", "bearer"}}},
		AuthProfiles: map[string]AuthProfile{
			"tg":     {SecretEnv: "AII_TEST_PATH_SECRET", Host: host, Port: port, Scheme: "path"},
			"bearer": {SecretEnv: "AII_TEST_PATH_SECRET", Host: host, Port: port},
		},
		Guard: guardFor(ts), Transport: ts.Client().Transport,
	})
	b := h.Bind("p", packagefmt.TierT2, []string{"net.outbound:" + hostPort})
	withPlaceholder := ts.URL + "/bot" + CredentialPlaceholder + "/getUpdates"

	m := dispatch(t, b, netParams(withPlaceholder, `{"auth_profile":"tg"}`))
	wantResult(t, m, statusSucceeded, "")
	if seenPath != "/bot"+secret+"/getUpdates" {
		t.Fatalf("the dialed URL carries the secret in the path: %q", seenPath)
	}
	whole, _ := json.Marshal(m)
	if strings.Contains(string(whole), secret) || strings.Contains(string(whole), "ABC-def") {
		t.Fatalf("the secret reached the reply:\n%s", whole)
	}
	var or struct {
		Body     json.RawMessage `json:"body"`
		Location string          `json:"location"`
	}
	if err := json.Unmarshal(m["operation_result"], &or); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(or.Body), CredentialPlaceholder) || !strings.Contains(or.Location, CredentialPlaceholder) {
		t.Fatalf("what came back keeps the placeholder where the secret was: body %s location %s", or.Body, or.Location)
	}
	if !strings.Contains(string(m["external_receipt"]), CredentialPlaceholder) {
		t.Fatalf("the receipt names the placeholder form: %s", m["external_receipt"])
	}

	// .
	wantResult(t, dispatch(t, b, netParams(withPlaceholder, `{}`)), statusDenied, reasonArgumentInvalid)
	wantResult(t, dispatch(t, b, netParams(withPlaceholder, `{"auth_profile":"bearer"}`)), statusDenied, reasonAuthInvalid)
	wantResult(t, dispatch(t, b, netParams(ts.URL+"/bot/getUpdates", `{"auth_profile":"tg"}`)), statusDenied, reasonAuthInvalid)
	wantResult(t, dispatch(t, b, netParams(withPlaceholder, `{"auth_profile":"tg","stream":true}`)), statusDenied, reasonAuthInvalid)
	if seenPath != "/bot"+secret+"/getUpdates" {
		t.Fatal("a refusal dials nothing")
	}

	// .
	// .
	ts.Close()
	m = dispatch(t, b, netParams(withPlaceholder, `{"auth_profile":"tg"}`))
	wantResult(t, m, statusFailed, reasonNetRemoteFailed)
	whole, _ = json.Marshal(m)
	if strings.Contains(string(whole), secret) {
		t.Fatalf("the transport's error leaked the secret:\n%s", whole)
	}
	if !strings.Contains(string(whole), "transport:") {
		t.Fatalf("the transport failure is named: %s", whole)
	}
}
