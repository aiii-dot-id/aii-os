package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestChatGPTSendsACacheKeyThatFollowsTheStablePartOfThePrompt(t *testing.T) {
	head, middle, tail := strings.Repeat("What every resident is told. ", 30), "Who THIS resident is. ", strings.Repeat("How the tools are used. ", 30)
	stable := head + middle + tail
	system := func(stable, volatile string) Message {
		return Message{Role: "system", Content: stable + volatile, StableLen: len(stable)}
	}
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunction{Name: "read", Description: "Read a file.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}},
		{Type: "function", Function: ToolFunction{Name: "shell", Description: "Run a command.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}}}},
	}
	// One provider and, for each entry, ONE client for every request: what a
	// client carried from one request to the next would show here.
	var mu sync.Mutex
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		mu.Lock()
		last = got
		mu.Unlock()
		_, _ = io.WriteString(w, streamOneItem+"\n"+streamTerminal)
	}))
	t.Cleanup(srv.Close)
	client := func(cfg ClientConfig) *Client {
		cfg.Endpoint, cfg.APIKey, cfg.Provider = srv.URL, "fixture", "chatgpt"
		if cfg.Model == "" {
			cfg.Model = "fixture"
		}
		return New(&cfg)
	}
	send := func(c *Client, opts ChatOptions, messages ...Message) map[string]any {
		t.Helper()
		if _, err := c.Chat(t.Context(), messages, opts); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return last
	}
	keyOf := func(r map[string]any) string { k, _ := r["prompt_cache_key"].(string); return k }
	bytesOf := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	with := ChatOptions{Tools: tools}
	user := Message{Role: "user", Content: "one"}

	plain := client(ClientConfig{})
	one := send(plain, with, system(stable, "It is 09:00."), user)
	key := keyOf(one)
	if key == "" || len(key) > 64 {
		t.Fatalf("AN ENTRY THAT STATES NO KEY SENDS THE KEY %q; the backend routes by it, and takes at most 64 bytes", key)
	}
	// The next turn of the same life: what follows the seam and the
	// conversation have moved, the stable part has not.
	two := send(plain, with, system(stable, "It is 09:05, and a dream surfaced."), user, Message{Role: "assistant", Content: "the reply to one"}, Message{Role: "user", Content: "two"})
	if keyOf(two) != key {
		t.Fatalf("the same stable part, and another key: %q, then %q", key, keyOf(two))
	}
	if a, b := bytesOf(one["tools"]), bytesOf(two["tools"]); a != b {
		t.Fatalf("the tools are not the same bytes in the next request:\n%s\n%s", a, b)
	}
	for _, r := range []map[string]any{one, two} {
		if in, _ := r["instructions"].(string); !strings.HasPrefix(in, stable) {
			t.Fatalf("the instructions do not begin with the stable part of the system prompt: %.80q", in)
		}
	}
	if a, b := bytesOf(one["input"].([]any)[0]), bytesOf(two["input"].([]any)[0]); a != b {
		t.Fatalf("the same first message is not the same bytes in the next request:\n%s\n%s", a, b)
	}

	// THE KEY IS THE STABLE PART'S, ALL OF IT: the same client, a prompt whose
	// stable part has the same head and the same tail and differs between
	// them, has another key; and the first prompt again has the first key.
	other := head + "Who ANOTHER resident is. " + tail
	if k := keyOf(send(plain, with, system(other, "It is 09:00."), user)); k == key || k == "" {
		t.Fatalf("a stable part that differs only in its middle carries the key %q; the first carried %q", k, key)
	}
	if k := keyOf(send(plain, with, system(stable, "It is 09:10."), user)); k != key {
		t.Fatalf("the first prompt again, on the same client, carries %q; it carried %q", k, key)
	}
	// AND NOTHING ELSE'S: not the tools offered, not a call with none, not
	// the model, not whether the entry says "auto" or says nothing.
	for what, r := range map[string]map[string]any{
		"one tool fewer":            send(plain, ChatOptions{Tools: tools[:1]}, system(stable, "x"), user),
		"a call with no tools":      send(plain, ChatOptions{DisableTools: true}, system(stable, "x"), user),
		"another model":             send(client(ClientConfig{Model: "another"}), with, system(stable, "x"), user),
		"the entry's cache on auto": send(client(ClientConfig{Cache: &CachePolicy{Mode: CacheAuto}}), with, system(stable, "x"), user),
		"an empty cache object":     send(client(ClientConfig{Cache: &CachePolicy{}}), with, system(stable, "x"), user),
	} {
		if k := keyOf(r); k != key {
			t.Errorf("%s: the key is %q; the stable part is the same and its key is %q", what, k, key)
		}
	}
	// The seam is where the composer put it, near or far: a short stable part
	// keeps its key whatever follows it.
	short := "A short stable part. "
	if a, b := keyOf(send(plain, with, system(short, strings.Repeat("One morning. ", 200)), user)), keyOf(send(plain, with, system(short, strings.Repeat("Another. ", 200)), user)); a == "" || a != b || a == key {
		t.Fatalf("a short stable part, followed by one thing and then another, carries %q and then %q (the long one's key is %q)", a, b, key)
	}
	// A prompt that is stable to its last byte has a key like any other.
	whole := send(plain, with, Message{Role: "system", Content: stable, StableLen: len(stable)}, user)
	if keyOf(whole) != key {
		t.Fatalf("a system prompt that is stable to its end carries the key %q, want %q", keyOf(whole), key)
	}
	// The operator's own key is sent as written, with a mode stated or not,
	// and for every prompt.
	for what, cfg := range map[string]ClientConfig{
		"with the mode": {Cache: &CachePolicy{Mode: CacheAuto, Key: "the-operators-key"}},
		"with no mode":  {Cache: &CachePolicy{Key: "the-operators-key"}},
	} {
		c := client(cfg)
		for _, s := range []string{stable, other} {
			if k := keyOf(send(c, with, system(s, "x"), user)); k != "the-operators-key" {
				t.Fatalf("the entry states the key the-operators-key (%s) and the request carries %q", what, k)
			}
		}
	}
	// A prompt with no stable part marked has nothing to key: none is sent.
	if k, sent := send(plain, with, Message{Role: "system", Content: stable}, user)["prompt_cache_key"]; sent {
		t.Fatalf("a system prompt with no stable part marked sent the key %q", k)
	}
}
