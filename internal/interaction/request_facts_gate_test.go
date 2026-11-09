package interaction

import (
	"encoding/json"
	"testing"
)

// A request's facts are held to how they are made (F2): the store refuses
// to keep facts that are not, and the payload says a count that was not
// reported as null, never 0.
func TestRequestFactsAreHeldToHowTheyAreMade(t *testing.T) {
	n, zero, minus := 110, 0, -1
	facts := func(change func(*RequestFacts)) RequestFacts {
		f := RequestFacts{Ordinal: 2, Model: "m", PromptTokens: &n, CompletionTokens: &n, TotalTokens: &n, CachedPromptTokens: &zero, UnknownAttempts: 1}
		if change != nil {
			change(&f)
		}
		return f
	}
	if !facts(nil).Valid() {
		t.Fatal("a request's reported facts are refused")
	}
	if !(RequestFacts{Ordinal: 1, Model: "m"}).Valid() {
		t.Fatal("a request that reported no usage is refused")
	}
	for name, f := range map[string]RequestFacts{
		"no ordinal":                     facts(func(f *RequestFacts) { f.Ordinal = 0 }),
		"a negative count":               facts(func(f *RequestFacts) { f.PromptTokens = &minus }),
		"negative unknown attempts":      facts(func(f *RequestFacts) { f.UnknownAttempts = -1 }),
		"a total without input":          facts(func(f *RequestFacts) { f.PromptTokens = nil }),
		"a cache read with no usage":     facts(func(f *RequestFacts) { f.PromptTokens, f.CompletionTokens, f.TotalTokens = nil, nil, nil }),
		"a negative cache write":         facts(func(f *RequestFacts) { f.CacheWriteTokens = &minus }),
		"output without input and total": facts(func(f *RequestFacts) { f.PromptTokens, f.TotalTokens = nil, nil }),
	} {
		if f.Valid() {
			t.Errorf("%s was admitted", name)
		}
	}
	raw, _ := json.Marshal(facts(nil))
	if string(raw) != `{"ordinal":2,"model":"m","prompt_tokens":110,"completion_tokens":110,"total_tokens":110,"cached_prompt_tokens":0,"cache_write_tokens":null,"unknown_attempts":1}` {
		t.Fatalf("the payload: %s", raw)
	}
}
