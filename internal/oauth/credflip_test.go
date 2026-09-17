package oauth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

func codexSource(t *testing.T, path string) *Source {
	t.Helper()
	return mustSource(t, KindCodex, map[string]string{"file": path})
}

func mustSource(t *testing.T, kind string, options map[string]string) *Source {
	t.Helper()
	s, err := testSource(kind, options)
	if err != nil {
		t.Fatalf("building the source: %v", err)
	}
	return s
}

const codexAPIKey = `{"OPENAI_API_KEY":"sk-plain"}`

func TestACredentialStoreCannotChangeSpeciesMidFlight(t *testing.T) {
	fixtureHome(t)
	path := filepath.Join(t.TempDir(), "auth.json")
	// .
	// .
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"oauth-token","account_id":"acct-1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := codexSource(t, path)

	if _, err := s.Credential(context.Background()); err != nil {
		t.Fatalf("the OAuth credential was refused: %v", err)
	}
	// .
	if s.Dialect() != "chatgpt" || s.BaseURL() == "" {
		t.Fatalf("the subscription did not force a route: %q %q", s.Dialect(), s.BaseURL())
	}

	// .
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

// .
// .
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
