package llm

import "testing"

// The client resolves its dialect once, from the provider string, and the
// door a call leaves through, the cache and effort plans and the log's
// label all read that one value. Every spelling a provider entry or a
// sign-in hands the client resolves here.
func TestTheClientResolvesItsDialectOnce(t *testing.T) {
	for _, tc := range []struct {
		provider string
		dialect  Dialect
		label    string
	}{
		{"", DialectOpenAI, "openai-compatible"},
		{"openai", DialectOpenAI, "openai-compatible"},
		{"chatgpt", DialectResponses, "chatgpt"},
		{"responses", DialectResponses, "responses"},
		{"anthropic", DialectAnthropic, "anthropic"},
		{"gemini", DialectGemini, "gemini"},
	} {
		c := New(&ClientConfig{Provider: tc.provider})
		if c.dialect != tc.dialect || c.providerLabel() != tc.label {
			t.Errorf("provider %q -> dialect %q labelled %q, want %q labelled %q", tc.provider, c.dialect, c.providerLabel(), tc.dialect, tc.label)
		}
	}
}
