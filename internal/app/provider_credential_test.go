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

func TestAnEntryThatNamesNoSourceIsNotSentAnotherVendorsVariable(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai")

	own := providerEntry{Name: "Some OpenAI-compatible host"}
	if got := providerAPIKey(own, "", "OPENAI_API_KEY"); got != "" {
		t.Fatalf("OpenAI's own variable was sent to another endpoint: %q", got)
	}
	own.APIKeyEnv = "OPENAI_API_KEY"
	if got := providerAPIKey(own, "", "OPENAI_API_KEY"); got != "sk-openai" {
		t.Fatalf("an endpoint that names the variable on its entry did not get it: %q", got)
	}
}

func TestInlineAndSuppliedKeysOutrankTheEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	t.Setenv("ACME_API_KEY", "sk-env")

	entry := providerEntry{Name: "Acme", APIKey: "sk-inline", APIKeyEnv: "ACME_API_KEY"}
	if got := providerAPIKey(entry, "", "OPENAI_API_KEY"); got != "sk-inline" {
		t.Fatalf("the inline key lost to the environment: %q", got)
	}
	if got := providerAPIKey(entry, "sk-supplied", "OPENAI_API_KEY"); got != "sk-supplied" {
		t.Fatalf("a supplied credential was overridden: %q", got)
	}
}
