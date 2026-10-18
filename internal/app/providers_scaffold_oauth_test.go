package app

import (
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

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
	// LENT BY NAME: the fresh file's effective contract is the shipped one,
	// so whatever ships next is what the install sees next.
	if got := install.oauthContracts()[name]; got.TokenURL == "" || got.TokenURL != shipped.TokenURL {
		t.Fatalf("the shipped contract is not lent to a fresh file: %+v", got)
	}
	// A BLOCK IN THE FILE STANDS — which is exactly why the old scaffold
	// froze what it copied: an operator's own block is meant to win; a copy
	// nobody wrote was not. The shipped registry itself is not touched
	// here; it is shared by everything in the process.
	mine := shipped
	mine.TokenURL = "https://example.invalid/the-operators-own-token-endpoint"
	frozen := install
	frozen.OAuth = map[string]oauth.Provider{name: mine}
	if got := frozen.oauthContracts()[name]; got.TokenURL != mine.TokenURL {
		t.Fatalf("an operator's own block does not stand: %q", got.TokenURL)
	}
}

// THE SCAFFOLD IS CUT FROM THE ONE PARSE OF THE EMBED, which every load
// shares for the life of the process: leaving the speech mappings and the
// sign-in contracts out of the operator's copy must leave the shared one
// whole, or every later load lends nothing.
func TestScaffoldingLeavesTheSharedRegistryWhole(t *testing.T) {
	speaking := func() (n int) {
		for _, e := range embeddedRegistry().Providers {
			if e.Speech != nil {
				n++
			}
		}
		return n
	}
	before, contracts := speaking(), len(embeddedRegistry().OAuth)
	if before == 0 || contracts == 0 {
		t.Fatal("the shipped registry lends no speech mapping or no sign-in contract; the test proves nothing")
	}
	var install providerRegistry
	if err := json.Unmarshal(scaffoldProviders(), &install); err != nil {
		t.Fatal(err)
	}
	for _, e := range install.Providers {
		if e.Speech != nil {
			t.Fatalf("the scaffold wrote %s's speech mapping into the operator's file", e.Name)
		}
	}
	if after := speaking(); after != before || len(embeddedRegistry().OAuth) != contracts {
		t.Fatalf("scaffolding cut the shared registry: speech mappings %d -> %d, contracts %d -> %d",
			before, after, contracts, len(embeddedRegistry().OAuth))
	}
}
