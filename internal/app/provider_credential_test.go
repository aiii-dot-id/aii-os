package app

import (
	"os"
	"testing"
)

// A credential belongs to the endpoint it was issued for. Sending one
// provider's key to another provider's URL discloses it to a third
// party, and the mistake that triggered it was as ordinary as an
// unexported variable.

func TestAProviderNeverInheritsAnothersCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai-the-operators-real-key")
	os.Unsetenv("ANTHROPIC_API_KEY")

	entry := providerEntry{Name: "Claude (Max/Pro)", APIKeyEnv: "ANTHROPIC_API_KEY"}
	got := providerAPIKey(entry, "", "OPENAI_API_KEY")
	if got != "" {
		t.Fatalf("an Anthropic provider resolved to %q — that key would be sent to api.anthropic.com", got)
	}
}

// Its own variable, when set, is of course the answer.
func TestAProviderUsesTheVariableItNames(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")

	entry := providerEntry{Name: "Claude (Max/Pro)", APIKeyEnv: "ANTHROPIC_API_KEY"}
	if got := providerAPIKey(entry, "", "OPENAI_API_KEY"); got != "sk-ant" {
		t.Fatalf("the provider's own variable was not used: %q", got)
	}
}

// The global fallback is what llm.api_key_env MEANS: the operator's
// answer for a provider that gave none. An entry naming no source still
// gets it — removing that would break configs that rely on it.
func TestAnEntryThatNamesNoSourceStillUsesTheGlobal(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai")

	entry := providerEntry{Name: "Some OpenAI-compatible host"}
	if got := providerAPIKey(entry, "", "OPENAI_API_KEY"); got != "sk-openai" {
		t.Fatalf("the operator's global default stopped working: %q", got)
	}
}

// An inline key in providers.json outranks any environment, and a
// supplied key (adopted credential) outranks everything.
func TestInlineAndSuppliedKeysOutrankTheEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	t.Setenv("LILAC_API_KEY", "sk-env")

	entry := providerEntry{Name: "Lilac", APIKey: "sk-inline", APIKeyEnv: "LILAC_API_KEY"}
	if got := providerAPIKey(entry, "", "OPENAI_API_KEY"); got != "sk-inline" {
		t.Fatalf("the inline key lost to the environment: %q", got)
	}
	if got := providerAPIKey(entry, "sk-supplied", "OPENAI_API_KEY"); got != "sk-supplied" {
		t.Fatalf("a supplied credential was overridden: %q", got)
	}
}
