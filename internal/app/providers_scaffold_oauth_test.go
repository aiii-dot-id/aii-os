package app

import (
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// .
// .
// .
// .
// .
// .
func TestAScaffoldedFileInheritsTheShippedSignInContracts(t *testing.T) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(scaffoldProviders(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, copied := raw["oauth"]; copied {
		t.Fatal("the scaffold copied the shipped sign-in contracts into the operator's file")
	}
	var install providerRegistry
	if err := json.Unmarshal(scaffoldProviders(), &install); err != nil {
		t.Fatal(err)
	}
	const name = "anthropic-claude"
	shipped, ok := embeddedRegistry().OAuth[name]
	if !ok {
		t.Fatalf("the shipped registry names no %s contract", name)
	}
	// .
	// .
	if got := install.oauthContracts()[name]; got.TokenURL == "" || got.TokenURL != shipped.TokenURL {
		t.Fatalf("the shipped contract is not lent to a fresh file: %+v", got)
	}
	// .
	// .
	// .
	// .
	mine := shipped
	mine.TokenURL = "https://example.invalid/the-operators-own-token-endpoint"
	frozen := install
	frozen.OAuth = map[string]oauth.Provider{name: mine}
	if got := frozen.oauthContracts()[name]; got.TokenURL != mine.TokenURL {
		t.Fatalf("an operator's own block does not stand: %q", got.TokenURL)
	}
}
