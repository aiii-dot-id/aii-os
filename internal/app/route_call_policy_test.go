package app

import "testing"

// A ROLE ROUTED TO ANOTHER MODEL KEEPS THE IDENTITY'S CALL POLICY. The
// llm block's timeout, retries, backoff and streaming are how this
// identity calls a model, and a route changes which model answers, not
// that. A route was resolved from the pointer and the key alone: a routed
// sub-agent gave up at the client's 120 seconds on a local model the
// operator had given fifteen minutes, retried where the operator had said
// never, and streamed from a server that cannot stream.
func TestARoleRoutedToAnotherModelKeepsTheIdentitysCallPolicy(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "alpha", APIType: "openai", URL: "https://alpha.example/v1", APIKey: "ka", DefaultModel: "am", Default: true, ContextLength: 64000, MaxOutputTokens: 4096},
		providerEntry{Name: "beta", APIType: "openai", URL: "http://127.0.0.1:9/v1", DefaultModel: "bm", Local: true, ContextLength: 64000, MaxOutputTokens: 4096},
	)
	whole := false
	block := LLMConfig{Provider: "alpha", TimeoutSeconds: 900, Retries: -1, RetryBackoffMS: 5000, Stream: &whole}
	a := resolverApp(dir, block)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	own, _, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	if own.TimeoutSeconds != 900 || own.Retries != -1 || own.RetryBackoffMS != 5000 || !own.NoStream {
		t.Fatalf("rig: the identity's own client carries the block's policy: %+v", own)
	}
	for _, route := range []struct{ provider, model, wantModel string }{
		{"beta", "", "bm"},         // the checkbox: a local entry, its default model
		{"beta", "other", "other"}, // an explicit route, its model
		{"alpha", "am", "am"},      // a route that names the selected entry
	} {
		cc, entry, err := a.resolveRouteLLM(a.configSnapshot().LLM, reg, route.provider, route.model)
		if err != nil {
			t.Fatalf("route %s/%s: %v", route.provider, route.model, err)
		}
		if entry.Name != route.provider || cc.Model != route.wantModel {
			t.Fatalf("rig: route %s/%s resolved %s/%s", route.provider, route.model, entry.Name, cc.Model)
		}
		if cc.TimeoutSeconds != 900 || cc.Retries != -1 || cc.RetryBackoffMS != 5000 || !cc.NoStream {
			t.Errorf("A ROLE ROUTED TO %s/%s LOST THE IDENTITY'S CALL POLICY: timeout %d s (want 900), retries %d (want -1, none), backoff %d ms (want 5000), read whole %v (want true)",
				route.provider, cc.Model, cc.TimeoutSeconds, cc.Retries, cc.RetryBackoffMS, cc.NoStream)
		}
	}

	if cc, _, err := a.resolveRouteLLM(LLMConfig{Provider: "alpha", APIKeyEnv: "ROUTE_POLICY_TEST_KEY", TimeoutSeconds: 900}, reg, "beta", ""); err != nil || cc.APIKey != "" || cc.TimeoutSeconds != 900 {
		t.Errorf("a route to another entry carries the policy and never the block's key variable: key %q, timeout %d, %v", cc.APIKey, cc.TimeoutSeconds, err)
	}
}
