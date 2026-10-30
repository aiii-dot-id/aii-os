package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// script is what a scripted model says in one trial: the founding words
// at the birth, and for each exchange — a turn the operator's words
// open, a sub-agent's run, a wake — the responses it gives, one per
// request of that exchange, in order.
type script struct {
	Greeting  string     `json:"greeting"`
	Exchanges []exchange `json:"exchanges"`
}

// exchange is opened by the newest user message that holds On; the
// number of the model's own messages after that one says which step a
// request is at.
type exchange struct {
	On    string `json:"on"`
	Steps []step `json:"steps"`
}

// step is one response: tool calls, or the final words.
type step struct {
	Calls []scriptCall `json:"calls,omitempty"`
	Text  string       `json:"text,omitempty"`
}

type scriptCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type fakeModel struct {
	srv    *httptest.Server
	model  string
	script script

	key string

	mu     sync.Mutex
	served map[int]int // exchange -> steps served
	first  map[string]string
	misses []string
	calls  int
}

// fakeWindow is the context the fake model publishes: large enough that
// no request of a trial is folded, as a live model's is.
const fakeWindow = 200000

func newFakeModel(t *testing.T, sc script) *fakeModel {
	t.Helper()
	return newKeyedFakeModel(t, sc, "")
}

// newKeyedFakeModel is a scripted model that answers only requests
// carrying key as their bearer.
func newKeyedFakeModel(t *testing.T, sc script, key string) *fakeModel {
	t.Helper()
	f := &fakeModel{model: "scripted-model", script: sc, key: key, served: map[int]int{}, first: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if !f.authorized(r) {
			http.Error(w, "a key is required", http.StatusUnauthorized)
			return
		}
		writeJSONResponse(w, map[string]any{"object": "list", "data": []map[string]any{{"id": f.model, "object": "model", "context_length": fakeWindow, "max_tokens": 8192}}})
	})
	mux.HandleFunc("/v1/chat/completions", f.complete)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeModel) url() string { return f.srv.URL + "/v1" }

type chatRequest struct {
	Messages []struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		ToolCalls []any           `json:"tool_calls"`
	} `json:"messages"`
	Tools []any `json:"tools"`
}

// text is a message's content as text, whether the dialect sent a string
// or a list of parts.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// authorized reports whether a request carries the bearer the model
// wants, when it wants one.
func (f *fakeModel) authorized(r *http.Request) bool {
	return f.key == "" || r.Header.Get("Authorization") == "Bearer "+f.key
}

func (f *fakeModel) complete(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		http.Error(w, "a key is required", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if len(req.Messages) == 1 && len(req.Tools) == 0 {
		// The founding request: the bootstrap packet alone.
		f.answer(w, step{Text: f.script.Greeting})
		return
	}
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" && strings.Contains(text(req.Messages[0].Content), "# Facility prompt") {
		// A facility's pass (DREAM, CONSOLIDATE, SELF_MODEL, the brief):
		// the trial grades none of them, and each takes this as having
		// nothing to add.
		words := "NOTHING SURFACED"
		if strings.Contains(text(req.Messages[0].Content), "NO_CHANGE") {
			words = "NO_CHANGE"
		}
		f.answer(w, step{Text: words})
		return
	}
	at, ex := -1, -1
	for i := len(req.Messages) - 1; i >= 0 && ex < 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		said := text(req.Messages[i].Content)
		for j, e := range f.script.Exchanges {
			if strings.Contains(said, e.On) {
				at, ex = i, j
				break
			}
		}
	}
	if ex < 0 {
		f.miss("no exchange opens this request; its newest user message: " + newestUser(req))
		f.answer(w, step{Text: "(the script has nothing for this request)"})
		return
	}
	n := 0
	for _, m := range req.Messages[at+1:] {
		if m.Role == "assistant" {
			n++
		}
	}
	e := f.script.Exchanges[ex]
	if n >= len(e.Steps) {
		f.miss(fmt.Sprintf("exchange %q has %d step(s) and was asked for step %d", e.On, len(e.Steps), n+1))
		f.answer(w, step{Text: "(the script has ended)"})
		return
	}
	f.mu.Lock()
	if n+1 > f.served[ex] {
		f.served[ex] = n + 1
	}
	if _, seen := f.first[e.On]; !seen && n == 0 {
		var all strings.Builder
		for _, m := range req.Messages {
			all.WriteString(text(m.Content))
			all.WriteString("\n")
		}
		f.first[e.On] = all.String()
	}
	f.mu.Unlock()
	f.answer(w, e.Steps[n])
}

// firstRequest is every message's text of the first request the exchange
// opened by on was asked, or "" when none was.
func (f *fakeModel) firstRequest(on string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.first[on]
}

func newestUser(req chatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			s := text(req.Messages[i].Content)
			if len(s) > 300 {
				s = s[:300] + "…"
			}
			return s
		}
	}
	return "(none)"
}

func (f *fakeModel) miss(why string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.misses = append(f.misses, why)
}

// unserved names the exchanges whose steps were not all served: a script
// the trial did not follow to its end proves nothing about its grader.
func (f *fakeModel) unserved() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for i, e := range f.script.Exchanges {
		if f.served[i] < len(e.Steps) {
			out = append(out, fmt.Sprintf("%q served %d of %d step(s)", e.On, f.served[i], len(e.Steps)))
		}
	}
	return append(out, f.misses...)
}

func (f *fakeModel) answer(w http.ResponseWriter, s step) {
	msg := map[string]any{"role": "assistant", "content": s.Text}
	finish := "stop"
	if len(s.Calls) > 0 {
		var calls []map[string]any
		for i, c := range s.Calls {
			args, _ := json.Marshal(c.Args)
			if c.Args == nil {
				args = []byte("{}")
			}
			f.mu.Lock()
			id := fmt.Sprintf("call_%d_%d", f.calls, i)
			f.mu.Unlock()
			calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(args)}})
		}
		msg["tool_calls"] = calls
		if s.Text == "" {
			msg["content"] = nil
		}
		finish = "tool_calls"
	}
	writeJSONResponse(w, map[string]any{
		"id": "chatcmpl-scripted", "object": "chat.completion", "model": f.model,
		"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110},
	})
}

func writeJSONResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
