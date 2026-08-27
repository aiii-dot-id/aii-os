package oauth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The codex store expresses EITHER an OAuth token or a plain API key,
// depending on how the operator signed in — and the two want different
// routes. An OAuth subscription forces the ChatGPT backend and the
// chatgpt dialect; a plain key forces neither, because it is valid on
// the provider's ordinary base.
//
// Credential() re-reads the file on every request, by design: the owning
// CLI refreshes it out from under us. But the CALLER reads Dialect() and
// BaseURL() once, when it builds the client. So a sign-out and sign-in
// of the other kind left the new credential travelling the old route —
// wrong endpoint, wrong wire dialect, silently.

func codexSource(t *testing.T, path string) *Source {
	t.Helper()
	return mustSource(t, KindCodex, map[string]string{"file": path})
}

func mustSource(t *testing.T, kind string, options map[string]string) *Source {
	t.Helper()
	s, err := New(kind, options)
	if err != nil {
		t.Fatalf("building the source: %v", err)
	}
	return s
}

const codexOAuth = `{"tokens":{"access_token":"%s","account_id":"acct-1"}}`
const codexAPIKey = `{"OPENAI_API_KEY":"sk-plain"}`

func TestACredentialStoreCannotChangeSpeciesMidFlight(t *testing.T) {
	fixtureHome(t)
	path := filepath.Join(t.TempDir(), "auth.json")
	// An unexpired JWT is awkward to fake; the parser accepts a token
	// with no readable expiry as "unknown", which is what we want.
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"oauth-token","account_id":"acct-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := codexSource(t, path)

	if _, err := s.Credential(context.Background()); err != nil {
		t.Fatalf("the OAuth credential was refused: %v", err)
	}
	// The route the caller baked into its client, once.
	if s.Dialect() != "chatgpt" || s.BaseURL() == "" {
		t.Fatalf("the subscription did not force a route: %q %q", s.Dialect(), s.BaseURL())
	}

	// The operator signs out and back in with a plain API key.
	if err := os.WriteFile(path, []byte(codexAPIKey), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Credential(context.Background())
	if err == nil {
		t.Fatal("the store changed from an OAuth credential to an API key and kept serving — " +
			"the new key would be sent to the subscription endpoint, in the subscription dialect")
	}
	if !strings.Contains(err.Error(), "changed from") {
		t.Fatalf("the refusal does not say what changed: %v", err)
	}
	if !strings.Contains(err.Error(), "restart or re-select") {
		t.Fatalf("the refusal does not say how to recover: %v", err)
	}
}

// A refresh of the SAME kind is the normal case — the owning CLI does it
// constantly — and must stay silent.
func TestARefreshOfTheSameKindIsSilent(t *testing.T) {
	fixtureHome(t)
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"first","account_id":"a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := codexSource(t, path)
	if _, err := s.Credential(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"second","account_id":"a"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cr, err := s.Credential(context.Background())
	if err != nil {
		t.Fatalf("an ordinary token refresh was refused: %v", err)
	}
	if cr.Token != "second" {
		t.Fatalf("the refreshed token did not arrive: %q", cr.Token)
	}
}
