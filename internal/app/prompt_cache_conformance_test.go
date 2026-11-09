package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

var updatePromptCache = flag.Bool("update-prompt-cache", false, "rewrite testdata/prompt_cache/reasoning_resets.txt from the scripted sessions")

const m3ResetNote = "[Earlier provider reasoning was reset because its context changed."

// m3Reasoning names the reasoning the fake returns with its answer to the
// session's nth request: a thinking block's signature, a reasoning item's
// encrypted content, a thought signature, or reasoning_content.
func m3Reasoning(n int) string { return fmt.Sprintf("m3-reasoning-%03d", n) }

// m3Call is one tool call the fake asks for; m3Reply is its answer to one
// request: calls, or the turn's answer when there are none.
type m3Call struct{ name, args string }

type m3Reply struct {
	calls []m3Call
	text  string
}

// m3Turn is one resident turn of the script: the operator's words and the
// answer to each request the turn makes, its tool rounds first.
type m3Turn struct {
	words  string
	rounds []m3Reply
}

// m3Script is an ordinary working session: twelve operator turns, eight of
// them with tool rounds (eleven rounds, one of them two calls), touching a
// file, the record, recall and a work session, so that what moves between
// requests is what moves in real use.
func m3Script(dir string) []m3Turn {
	plan := filepath.Join(dir, "plan.md")
	draft := filepath.Join(dir, "draft.md")
	q := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	answer := func(n int) m3Reply { return m3Reply{text: fmt.Sprintf("M3 answer %02d.", n)} }
	return []m3Turn{
		{"M3 turn 01: good morning, what is on the plan today", []m3Reply{answer(1)}},
		{"M3 turn 02: read the plan and tell me its first step", []m3Reply{
			{calls: []m3Call{{"read", q(map[string]any{"file_path": plan})}}}, answer(2)}},
		{"M3 turn 03: note that the lighthouse inspection is due on Friday", []m3Reply{
			{calls: []m3Call{{"note", q(map[string]any{"content": "M3 the lighthouse inspection is due on Friday"})}}}, answer(3)}},
		{"M3 turn 04: compare the plan with what is in the folder", []m3Reply{
			{calls: []m3Call{{"read", q(map[string]any{"file_path": plan})}, {"ls", q(map[string]any{"path": dir})}}},
			{calls: []m3Call{{"grep", q(map[string]any{"pattern": "lantern", "path": dir})}}},
			answer(4)}},
		{"M3 turn 05: thank you, that is clear", []m3Reply{answer(5)}},
		{"M3 turn 06: start the inspection work and record where it stands", []m3Reply{
			{calls: []m3Call{{"work", q(map[string]any{"action": "start", "description": "M3 lighthouse inspection"})}}},
			{calls: []m3Call{{"work", q(map[string]any{"action": "update", "state": "M3 the lantern is checked; the gallery is next"})}}},
			answer(6)}},
		{"M3 turn 07: what do you remember about the lighthouse", []m3Reply{
			{calls: []m3Call{{"recall", q(map[string]any{"query": "lighthouse"})}}}, answer(7)}},
		{"M3 turn 08: draft the inspection note and read it back to me", []m3Reply{
			{calls: []m3Call{{"write", q(map[string]any{"file_path": draft, "content": "M3 draft: the lantern is sound.\n"})}}},
			{calls: []m3Call{{"read", q(map[string]any{"file_path": draft})}}},
			answer(8)}},
		{"M3 turn 09: good, keep it short", []m3Reply{answer(9)}},
		{"M3 turn 10: how does the inspection work stand", []m3Reply{
			{calls: []m3Call{{"work", q(map[string]any{"action": "status"})}}}, answer(10)}},
		{"M3 turn 11: read the plan once more for the last step", []m3Reply{
			{calls: []m3Call{{"read", q(map[string]any{"file_path": plan})}}}, answer(11)}},
		{"M3 turn 12: that is all for today", []m3Reply{answer(12)}},
	}
}

// m3Part is one part of a request as the provider reads it, and its bytes.
type m3Part struct {
	label string
	bytes []byte
}

// m3Request is one request of the session: where it stands, its parts in
// the order the provider reads them, the cache markers it carries (by
// path, with the marker as sent), and its top-level members.
type m3Request struct {
	turn, round int
	reply       int // the session's ordinal of this request, which names its answer's reasoning
	body        []byte
	parts       []m3Part
	marks       map[string]string
	members     map[string]json.RawMessage
}

// m3Dialect is one wire dialect, on the shipped entry that speaks it.
type m3Dialect struct {
	name  string // its row in the recorded file
	entry string // the shipped providers.json entry its client is resolved from
	model string // the model asked for; "" is the entry's default_model
	// replays: a round's reasoning rides the later calls of its turn on
	// this entry: the provider's own blocks on the native dialects
	// (anthropic.go, chatgpt.go and gemini.go replay NativeContent), raw
	// reasoning on the chat dialect only where the entry sets
	// replay_reasoning (client.go, withReplay).
	replays bool
	setup   func(t *testing.T, e *providerEntry)
	answer  func(w http.ResponseWriter, n int, model string, r m3Reply)
	read    func(t *testing.T, body []byte) ([]m3Part, map[string]string)
	marks   func(t *testing.T, d m3Dialect, s *m3Session)
}

func m3Dialects() []m3Dialect {
	return []m3Dialect{
		{name: "anthropic", entry: "Anthropic", replays: true, setup: m3Key,
			answer: m3AnswerMessages, read: m3ReadMessages, marks: m3MessagesMarks},
		{name: "anthropic-subscription", entry: "Claude (Max/Pro)", replays: true, setup: m3ClaudeCredential,
			answer: m3AnswerMessages, read: m3ReadMessages, marks: m3MessagesMarks},
		{name: "openai-chat", entry: "Local server", model: "m3-local-model", replays: true, setup: m3LocalServer,
			answer: m3AnswerChat, read: m3ReadChat, marks: m3PlainChatMarks},
		{name: "openai-chat-explicit-cache", entry: "OpenAI", replays: false, setup: m3Key,
			answer: m3AnswerChat, read: m3ReadChat, marks: m3ExplicitChatMarks},
		{name: "responses", entry: "ChatGPT (Plus/Pro)", replays: true, setup: m3CodexCredential,
			answer: m3AnswerResponses, read: m3ReadResponses, marks: m3ResponsesMarks},
		{name: "responses-api-key", entry: "OpenAI", replays: true, setup: m3ResponsesKey,
			answer: m3AnswerResponses, read: m3ReadResponses, marks: m3ResponsesMarks},
		{name: "gemini", entry: "Google Gemini", replays: true, setup: m3Key,
			answer: m3AnswerGemini, read: m3ReadGemini, marks: m3GeminiMarks},
	}
}

func TestEveryDialectKeepsTheCachedPrefixAcrossAScriptedSession(t *testing.T) {
	var rows []string
	for _, d := range m3Dialects() {
		t.Run(d.name, func(t *testing.T) {
			s := m3RunSession(t, d)
			m3HoldPrefix(t, d, s)
			d.marks(t, d, s)
			rows = append(rows, m3ReasoningRow(t, d, s))
		})
	}
	if t.Failed() {
		return
	}
	got := "# What a scripted session of resident turns sends on each wire dialect the host speaks\n" +
		"# (prompt_cache_conformance_test.go): its requests; those that carried a provider's reasoning\n" +
		"# from an earlier round of their turn; and the times the host dropped that reasoning (each\n" +
		"# declared to the identity). Regenerate with -update-prompt-cache and review the diff.\n" +
		fmt.Sprintf("%-28s %8s %8s %7s\n", "dialect", "requests", "carried", "resets") +
		strings.Join(rows, "")
	file := filepath.Join("testdata", "prompt_cache", "reasoning_resets.txt")
	if *updatePromptCache {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (generate it with -update-prompt-cache)", err)
	}
	if got != string(want) {
		t.Fatalf("the reasoning the scripted session carried and dropped moved from %s.\n%s", file, goldenDiff(string(want), got))
	}
}

// m3Session is what one dialect's session sent: its resident requests in
// order, the words of each turn, and the requests no resident turn made.
type m3Session struct {
	app      *App
	requests []m3Request
	words    []string
	// current is, by turn, the index of the part that holds the turn's
	// words: the last part of the turn's first request.
	current []int
	others  int
}

func m3RunSession(t *testing.T, d m3Dialect) *m3Session {
	t.Helper()
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	// The scripted session answers each turn once: the work's own
	// continuation (2026-11-06) is switched off here, as the chain tests
	// do, so the prefix is measured across the turns the script makes.
	off := false
	a.cfgMu.Lock()
	a.cfg.Agency.TurnContinuation = &off
	a.cfgMu.Unlock()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("M3 plan\n1. check the lantern\n2. check the gallery\n3. write the inspection note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := m3Script(dir)
	var queue []m3Reply
	for _, turn := range script {
		queue = append(queue, turn.rounds...)
	}

	var mu sync.Mutex
	var bodies [][]byte
	others := 0
	model := m3Model(t, d)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		// A resident turn's request carries the turn block; anything else
		// (a facility pass) is answered and kept out of the session.
		if !bytes.Contains(body, []byte(conversation.TurnOpen)) {
			others++
			mu.Unlock()
			d.answer(w, 0, model, m3Reply{text: "NOTHING SURFACED"})
			return
		}
		bodies = append(bodies, body)
		n := len(bodies)
		reply := m3Reply{text: "M3 the script has no answer for this request."}
		if n <= len(queue) {
			reply = queue[n-1]
		}
		mu.Unlock()
		d.answer(w, n, model, reply)
	}))
	t.Cleanup(srv.Close)

	a.llmSwap.Swap(m3Client(t, d, model, srv.URL))

	s := &m3Session{app: a}
	for i, turn := range script {
		mu.Lock()
		before := len(bodies)
		mu.Unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		ctx = interaction.WithSaid(ctx, interaction.SaidTyped) // as the chat door marks what the operator types
		if err := a.acquireTurn(ctx); err != nil {
			cancel()
			t.Fatalf("%s: turn %d: %v", d.name, i+1, err)
		}
		reply, err := a.observeChat(ctx, turn.words, func(kind, name, args string) {})
		cancel()
		if err != nil {
			t.Fatalf("%s: turn %d failed: %v", d.name, i+1, err)
		}
		if want := turn.rounds[len(turn.rounds)-1].text; reply != want {
			t.Fatalf("%s: turn %d answered %q, want the script's %q", d.name, i+1, reply, want)
		}
		waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
		mu.Lock()
		after := bodies[before:]
		mu.Unlock()
		if len(after) != len(turn.rounds) {
			t.Fatalf("%s: turn %d made %d requests; the script answers %d", d.name, i+1, len(after), len(turn.rounds))
		}
		s.words = append(s.words, turn.words)
		for round, body := range after {
			parts, marks := d.read(t, body)
			s.requests = append(s.requests, m3Request{turn: i + 1, round: round + 1, reply: before + round + 1, body: body,
				parts: parts, marks: marks, members: m3Members(t, body)})
			if round == 0 {
				s.current = append(s.current, len(parts)-1)
			}
		}
	}
	mu.Lock()
	s.others = others
	mu.Unlock()
	if s.others != 0 {
		t.Fatalf("%s: %d requests during the session were no resident turn's: the session is not the script alone", d.name, s.others)
	}
	return s
}

func m3Client(t *testing.T, d m3Dialect, model, endpoint string) *llm.Client {
	t.Helper()
	dir := t.TempDir()
	entry := liveProvider(t, d.entry, model)
	d.setup(t, &entry)
	writeTestProviders(t, dir, entry)
	r := New(&Config{LLM: LLMConfig{Provider: entry.Name, Model: entry.DefaultModel, TimeoutSeconds: 10, Retries: -1}, SourcePath: filepath.Join(dir, "config.json")})
	t.Cleanup(r.bgCancel)
	cc, _, err := r.resolveLLM()
	if err != nil {
		t.Fatalf("%s: resolve %q: %v", d.name, d.entry, err)
	}
	if got := llm.DialectFor(cc.Provider); got != m3DialectOf(d) {
		t.Fatalf("%s: the entry %q resolved to the %q dialect, want %q", d.name, d.entry, got, m3DialectOf(d))
	}
	cc.Endpoint = endpoint
	return llm.New(&cc)
}

// m3DialectOf is the dialect a row is for.
func m3DialectOf(d m3Dialect) llm.Dialect {
	switch {
	case strings.HasPrefix(d.name, "anthropic"):
		return llm.DialectAnthropic
	case strings.HasPrefix(d.name, "openai"):
		return llm.DialectOpenAI
	case strings.HasPrefix(d.name, "responses"):
		return llm.DialectResponses
	}
	return llm.DialectGemini
}

// m3Model is the model a row asks for: its own, or its entry's
// default_model as shipped.
func m3Model(t *testing.T, d m3Dialect) string {
	t.Helper()
	if d.model != "" {
		return d.model
	}
	for _, e := range embeddedRegistry().Providers {
		if e.Name == d.entry {
			return e.DefaultModel
		}
	}
	t.Fatalf("embedded provider %q not found", d.entry)
	return ""
}

// m3Key gives an entry that takes a key a fixture one, so nothing reads
// the environment for it.
func m3Key(t *testing.T, e *providerEntry) { e.APIKey = "m3-fixture-key" }

// m3ResponsesKey is the OpenAI entry on a key, with the Responses shape
// selected on it, as an operator selects it on the provider's card.
func m3ResponsesKey(t *testing.T, e *providerEntry) {
	e.APIKey = "m3-fixture-key"
	e.APIType = "responses"
}

// m3LocalServer is the operator's own server, as a local thinking model is
// set up: its reasoning goes back within the turn (replay_reasoning), the
// one way the chat dialect carries it.
func m3LocalServer(t *testing.T, e *providerEntry) {
	e.APIKey = "m3-fixture-key"
	e.ReplayReasoning = true
}

func m3ClaudeCredential(t *testing.T, e *providerEntry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-fixture.json")
	body, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
		"accessToken": "m3-fixture-access", "refreshToken": "m3-fixture-refresh",
		"expiresAt": time.Now().Add(time.Hour).UnixMilli(),
		"scopes":    []string{"user:profile", "user:inference"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	e.CredentialOptions["file"] = path
	e.CredentialOptions["keychain_service"] = "aii-os-test-absent-" + path
}

// m3CodexCredential gives the ChatGPT subscription entry a fixture original
// of its credential: a token that names its account as the vendor's does.
func m3CodexCredential(t *testing.T, e *providerEntry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.json")
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	claims, err := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "m3-account", "chatgpt_plan_type": "pro"}})
	if err != nil {
		t.Fatal(err)
	}
	token := header + "." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"`+token+`","account_id":"m3-account"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e.CredentialOptions["file"] = path
}

// The fake's answers, one dialect each. Every answer carries reasoning
// named by its request (m3Reasoning), as a thinking model's does.

func m3AnswerMessages(w http.ResponseWriter, n int, model string, r m3Reply) {
	content := []map[string]any{{"type": "thinking", "thinking": "", "signature": m3Reasoning(n)}}
	stop := "end_turn"
	if len(r.calls) == 0 {
		content = append(content, map[string]any{"type": "text", "text": r.text})
	}
	for i, c := range r.calls {
		content = append(content, map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_m3_%03d_%d", n, i), "name": c.name, "input": json.RawMessage(c.args)})
		stop = "tool_use"
	}
	m3WriteJSON(w, map[string]any{"id": fmt.Sprintf("msg_m3_%03d", n), "type": "message", "role": "assistant", "model": model,
		"content": content, "stop_reason": stop, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5}})
}

func m3AnswerChat(w http.ResponseWriter, n int, model string, r m3Reply) {
	message := map[string]any{"role": "assistant", "content": r.text, "reasoning_content": m3Reasoning(n)}
	finish := "stop"
	var calls []map[string]any
	for i, c := range r.calls {
		calls = append(calls, map[string]any{"id": fmt.Sprintf("call_m3_%03d_%d", n, i), "type": "function",
			"function": map[string]any{"name": c.name, "arguments": c.args}})
		finish = "tool_calls"
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	m3WriteJSON(w, map[string]any{"id": fmt.Sprintf("chatcmpl_m3_%03d", n), "model": model,
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
}

func m3AnswerResponses(w http.ResponseWriter, n int, model string, r m3Reply) {
	items := []map[string]any{{"type": "reasoning", "id": fmt.Sprintf("rs_m3_%03d", n), "summary": []any{}, "encrypted_content": m3Reasoning(n)}}
	for i, c := range r.calls {
		items = append(items, map[string]any{"type": "function_call", "id": fmt.Sprintf("fc_m3_%03d_%d", n, i),
			"call_id": fmt.Sprintf("call_m3_%03d_%d", n, i), "name": c.name, "arguments": c.args, "status": "completed"})
	}
	if len(r.calls) == 0 {
		items = append(items, map[string]any{"type": "message", "id": fmt.Sprintf("msg_m3_%03d", n), "role": "assistant", "status": "completed",
			"content": []map[string]any{{"type": "output_text", "text": r.text}}})
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, item := range items {
		m3WriteEvent(w, map[string]any{"type": "response.output_item.done", "item": item})
	}
	m3WriteEvent(w, map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_m3_%03d", n),
		"status": "completed", "model": model, "output": []any{},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}})
}

func m3AnswerGemini(w http.ResponseWriter, n int, model string, r m3Reply) {
	parts := []map[string]any{{"text": "M3 thinking", "thought": true}}
	for i, c := range r.calls {
		part := map[string]any{"functionCall": map[string]any{"name": c.name, "args": json.RawMessage(c.args)}}
		if i == 0 {
			part["thoughtSignature"] = m3Reasoning(n) // the signature rides a step's first call
		}
		parts = append(parts, part)
	}
	if len(r.calls) == 0 {
		parts = append(parts, map[string]any{"text": r.text, "thoughtSignature": m3Reasoning(n)})
	}
	m3WriteJSON(w, map[string]any{"candidates": []map[string]any{{"index": 0, "content": map[string]any{"role": "model", "parts": parts}, "finishReason": "STOP"}},
		"usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
		"modelVersion":  model, "responseId": fmt.Sprintf("m3_%03d", n)})
}

func m3WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func m3WriteEvent(w http.ResponseWriter, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

// Reading a request: its parts in the order the provider reads them, and
// its cache markers.

// m3Members is a request body's top-level members, as sent.
func m3Members(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("request body: %v", err)
	}
	return m
}

// m3Elements is a JSON array's elements, each as sent.
func m3Elements(t *testing.T, raw json.RawMessage) []json.RawMessage {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var out []json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("not an array: %v: %.200s", err, raw)
	}
	return out
}

// m3Settings is a request's other top-level members — its model, its
// output and thinking settings, its cache key — as one part, read first: a
// provider keys its cache on them too, and none of them is appended to.
func m3Settings(t *testing.T, m map[string]json.RawMessage, parts ...string) m3Part {
	t.Helper()
	rest := map[string]json.RawMessage{}
	for k, v := range m {
		if !slices.Contains(parts, k) {
			rest[k] = v
		}
	}
	return m3Part{"settings", m3Encode(t, rest)}
}

// m3Label names a message by its index and what it is.
func m3Label(prefix string, i int, raw json.RawMessage) string {
	var m struct{ Role, Type string }
	_ = json.Unmarshal(raw, &m)
	what := m.Role
	if what == "" {
		what = m.Type
	}
	return fmt.Sprintf("%s[%d] (%s)", prefix, i, what)
}

// m3ReadMessages reads the Messages dialect in the provider's own order:
// tools, then the system blocks, then the messages. Its cache markers are
// read out and the parts compared without them, because the dialect moves
// the tail marker on every request (anthropic.go, "Auto caches the growing
// conversation tail") and a replayed block re-encodes its members around a
// marker (replay.go, anthContent.MarshalJSON): a block is compared as its
// members, in one order, without the marker. The subscription's billing
// block is compared as a placeholder: its checksum covers the whole body
// and its prompt id is new on every request (claude_wire.go, claudeBilling
// and signClaudeBody), so it is new bytes on every request by design.
func m3ReadMessages(t *testing.T, body []byte) ([]m3Part, map[string]string) {
	t.Helper()
	m := m3Members(t, body)
	marks := map[string]string{}
	parts := []m3Part{m3Settings(t, m, "tools", "system", "messages"), {"tools", m3Canonical(t, m["tools"], "tools", marks)}}
	system := m3Canonical(t, m["system"], "system", marks)
	var blocks []map[string]any
	if err := json.Unmarshal(system, &blocks); err != nil {
		t.Fatalf("system: %v", err)
	}
	if len(blocks) > 0 && strings.HasPrefix(fmt.Sprint(blocks[0]["text"]), "x-anthropic-billing-header:") {
		blocks[0]["text"] = "<the billing block: new bytes on every request>"
		system = m3Encode(t, blocks)
	}
	parts = append(parts, m3Part{"system", system})
	for i, msg := range m3Elements(t, m["messages"]) {
		parts = append(parts, m3Part{m3Label("messages", i, msg), m3Canonical(t, msg, fmt.Sprintf("messages[%d]", i), marks)})
	}
	return parts, marks
}

// m3Canonical is raw re-encoded with its members in one order and every
// cache_control member taken out, each recorded in marks by its path.
func m3Canonical(t *testing.T, raw json.RawMessage, path string, marks map[string]string) []byte {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var strip func(v any, path string) any
	strip = func(v any, path string) any {
		switch x := v.(type) {
		case map[string]any:
			if mark, ok := x["cache_control"]; ok {
				marks[path] = string(m3Encode(t, mark))
				delete(x, "cache_control")
			}
			for k, e := range x {
				x[k] = strip(e, path+"."+k)
			}
		case []any:
			for i, e := range x {
				x[i] = strip(e, fmt.Sprintf("%s[%d]", path, i))
			}
		}
		return v
	}
	return m3Encode(t, strip(v, path))
}

func m3Encode(t *testing.T, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// m3ReadChat reads the chat dialect as sent: the system message, the
// tools, then the other messages. Its cache markers are the breakpoints in
// a message's parts (cache.go, applyOpenAICache).
func m3ReadChat(t *testing.T, body []byte) ([]m3Part, map[string]string) {
	t.Helper()
	m := m3Members(t, body)
	msgs := m3Elements(t, m["messages"])
	if len(msgs) == 0 {
		t.Fatal("a chat request with no messages")
	}
	marks := map[string]string{}
	parts := []m3Part{m3Settings(t, m, "messages", "tools"), {"system", msgs[0]}, {"tools", m["tools"]}}
	for i, msg := range msgs {
		var shape struct {
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(msg, &shape)
		var pieces []map[string]json.RawMessage
		if json.Unmarshal(shape.Content, &pieces) == nil {
			for j, p := range pieces {
				if b, ok := p["prompt_cache_breakpoint"]; ok {
					marks[fmt.Sprintf("messages[%d].content[%d]", i, j)] = string(b)
				}
			}
		}
		if i > 0 {
			parts = append(parts, m3Part{m3Label("messages", i, msg), msg})
		}
	}
	return parts, marks
}

// m3ReadResponses reads the Responses dialect as sent: the instructions,
// the tools, then the input items. It carries no marker; its cache is
// routed by prompt_cache_key, a top-level member.
func m3ReadResponses(t *testing.T, body []byte) ([]m3Part, map[string]string) {
	t.Helper()
	m := m3Members(t, body)
	parts := []m3Part{m3Settings(t, m, "instructions", "tools", "input"), {"instructions", m["instructions"]}, {"tools", m["tools"]}}
	for i, item := range m3Elements(t, m["input"]) {
		parts = append(parts, m3Part{m3Label("input", i, item), item})
	}
	return parts, map[string]string{}
}

// m3ReadGemini reads generateContent as sent: the system instruction, the
// tools, then the contents. It carries no cache setting (cache.go: "the
// Gemini API caches a repeated prefix by itself").
func m3ReadGemini(t *testing.T, body []byte) ([]m3Part, map[string]string) {
	t.Helper()
	m := m3Members(t, body)
	parts := []m3Part{m3Settings(t, m, "systemInstruction", "tools", "contents"), {"systemInstruction", m["systemInstruction"]}, {"tools", m["tools"]}}
	for i, c := range m3Elements(t, m["contents"]) {
		parts = append(parts, m3Part{m3Label("contents", i, c), c})
	}
	return parts, map[string]string{}
}

// m3HoldPrefix holds every request of the session to the one before it:
// within a turn, the whole of it; across turns, everything up to the end of
// the previous turn's words.
func m3HoldPrefix(t *testing.T, d m3Dialect, s *m3Session) {
	t.Helper()
	for i := 1; i < len(s.requests); i++ {
		prev, next := s.requests[i-1], s.requests[i]
		need, why := m3Length(prev.parts), "the whole of the request before it"
		if next.turn != prev.turn {
			c := s.current[prev.turn-1]
			words := []byte(s.words[prev.turn-1])
			at := bytes.Index(prev.parts[c].bytes, words)
			if at < 0 {
				t.Fatalf("%s: turn %d's words are not in %s of its requests", d.name, prev.turn, prev.parts[c].label)
			}
			need, why = m3Offset(prev.parts, c)+at+len(words), "everything up to the end of the previous turn's words"
		}
		same, label, inner := m3Common(prev.parts, next.parts)
		if same < need {
			t.Errorf("%s: turn %d request %d does not keep %s (turn %d request %d): the first byte that differs is byte %d of the cached prefix, %d bytes into %s, and %d bytes were to be kept.\nbefore: %s\nafter:  %s",
				d.name, next.turn, next.round, why, prev.turn, prev.round, same, inner, label, need,
				m3Around(prev.parts, same), m3Around(next.parts, same))
		}
	}
}

// m3Length is the length of a request's parts read as one stream, a byte
// between parts.
func m3Length(parts []m3Part) int { return m3Offset(parts, len(parts)) }

// m3Offset is where part i starts in that stream.
func m3Offset(parts []m3Part, i int) int {
	n := 0
	for _, p := range parts[:i] {
		n += len(p.bytes) + 1
	}
	return n
}

// m3Common is how many bytes two requests share from the start, and the
// part (and the offset in it) where they first differ.
func m3Common(a, b []m3Part) (int, string, int) {
	n := 0
	for i := range a {
		if i >= len(b) {
			return n, "(the later request has no " + a[i].label + ")", 0
		}
		pa, pb := a[i].bytes, b[i].bytes
		j := 0
		for j < len(pa) && j < len(pb) && pa[j] == pb[j] {
			j++
		}
		if j < len(pa) || a[i].label != b[i].label {
			return n + j, a[i].label, j
		}
		n += len(pa) + 1
	}
	return n, "(nothing: the earlier request is a prefix of the later)", 0
}

// m3Around is the stream's bytes either side of offset, for a failure.
func m3Around(parts []m3Part, offset int) string {
	var stream []byte
	for _, p := range parts {
		stream = append(stream, p.bytes...)
		stream = append(stream, '\n')
	}
	from, to := max(0, offset-60), min(len(stream), offset+60)
	if from > len(stream) {
		return "(past the end)"
	}
	return strconv.Quote(string(stream[from:offset])) + " | " + strconv.Quote(string(stream[offset:to]))
}

// m3ComposedStable is the stable part of the system text the session's
// identity is composed with (prompt.Prompt.StableLen): what the dialects
// mark, key or split on.
func m3ComposedStable(t *testing.T, s *m3Session) string {
	t.Helper()
	ws, err := s.app.buildWorkState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.app.composeTurn(s.app.readSafe(), ws, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.StableLen <= 0 || p.StableLen > len(p.Text) {
		t.Fatalf("the composed system text has no stable part (StableLen %d of %d)", p.StableLen, len(p.Text))
	}
	return p.Text[:p.StableLen]
}

// m3MessagesMarks holds the Messages dialect's cache markers to the places
// anthropic.go names: the last tool (it "extends the cached span over the
// (frozen) tool schemas"), the system's stable block ("the system prompt
// splits at the composer's cache seam ... into a stable block marked
// cache_control:ephemeral and a volatile block after it"), in auto mode the
// last block before the current message (THE HISTORY BOUNDARY: "It takes
// the fourth slot: tools, the system's stable block, this, and the tail"),
// and the tail, the last block that can carry one. The tail takes the
// entry's tail_ttl, the others its ttl; nothing else carries a marker, and
// never more than four.
func m3MessagesMarks(t *testing.T, d m3Dialect, s *m3Session) {
	t.Helper()
	stable := m3ComposedStable(t, s)
	var policy struct{ TTL, Tail string }
	for _, e := range embeddedRegistry().Providers {
		if e.Name == d.entry && e.Cache != nil {
			policy.TTL, policy.Tail = e.Cache.TTL, e.Cache.TailTTL
		}
	}
	if policy.Tail == "" {
		policy.Tail = policy.TTL
	}
	marker := func(ttl string) string {
		if ttl == "" {
			return `{"type":"ephemeral"}`
		}
		return `{"ttl":"` + ttl + `","type":"ephemeral"}`
	}
	for _, r := range s.requests {
		m := m3Members(t, r.body)
		tools := m3Elements(t, m["tools"])
		var system []struct{ Text string }
		if err := json.Unmarshal(m["system"], &system); err != nil {
			t.Fatal(err)
		}
		stableAt := 0
		if len(system) > 0 && strings.HasPrefix(system[0].Text, "x-anthropic-billing-header:") {
			stableAt = 1
		}
		if stableAt >= len(system) || system[stableAt].Text != stable {
			t.Errorf("%s: turn %d request %d: the system's block %d is not the composed stable part, so its marker is not on the cache seam", d.name, r.turn, r.round, stableAt)
		}
		var msgs []struct {
			Content []struct {
				Type, Text string
			}
		}
		if err := json.Unmarshal(m["messages"], &msgs); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			fmt.Sprintf("tools[%d]", len(tools)-1): marker(policy.TTL),
			fmt.Sprintf("system[%d]", stableAt):    marker(policy.TTL),
		}
		// The current message is the turn's words; its part index is past
		// the tools and the system.
		current := s.current[r.turn-1] - 3
		last := func(upTo int) string {
			for i := upTo - 1; i >= 0; i-- {
				for j := len(msgs[i].Content) - 1; j >= 0; j-- {
					b := msgs[i].Content[j]
					if b.Type == "text" && b.Text == "" {
						continue
					}
					if b.Type == "text" || b.Type == "tool_result" || b.Type == "tool_use" {
						return fmt.Sprintf("messages[%d].content[%d]", i, j)
					}
				}
			}
			return ""
		}
		if boundary := last(current); boundary != "" {
			want[boundary] = marker(policy.TTL)
		}
		if tail := last(len(msgs)); tail != "" {
			want[tail] = marker(policy.Tail)
		}
		if len(r.marks) > 4 {
			t.Errorf("%s: turn %d request %d carries %d cache markers; the dialect allows four", d.name, r.turn, r.round, len(r.marks))
		}
		if got, wantS := m3Marks(r.marks), m3Marks(want); got != wantS {
			t.Errorf("%s: turn %d request %d puts its cache markers at\n%s\nwhere the dialect's comments put them at\n%s", d.name, r.turn, r.round, got, wantS)
		}
	}
}

func m3Marks(m map[string]string) string {
	var lines []string
	for k, v := range m {
		lines = append(lines, "  "+k+" "+v)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// m3PlainChatMarks: a chat entry with no cache policy sends what it always
// sent, the system text as a string and no cache member (cache.go,
// applyOpenAICache: "Generic compatible servers keep their original
// string-message shape unless their model contract explicitly declares
// support for cache breakpoints").
func m3PlainChatMarks(t *testing.T, d m3Dialect, s *m3Session) {
	t.Helper()
	for _, r := range s.requests {
		for _, k := range []string{"prompt_cache_key", "prompt_cache_options", "prompt_cache_retention"} {
			if v, ok := r.members[k]; ok {
				t.Errorf("%s: turn %d request %d sends %s=%s, and the entry states no cache policy", d.name, r.turn, r.round, k, v)
			}
		}
		if len(r.marks) != 0 {
			t.Errorf("%s: turn %d request %d carries cache breakpoints %s", d.name, r.turn, r.round, m3Marks(r.marks))
		}
		var sys struct{ Content json.RawMessage }
		_ = json.Unmarshal(r.parts[1].bytes, &sys)
		if !bytes.HasPrefix(sys.Content, []byte(`"`)) {
			t.Errorf("%s: turn %d request %d sends its system text in parts, not as the string a compatible server takes", d.name, r.turn, r.round)
		}
	}
}

// m3ExplicitChatMarks: on a model that declares cache breakpoints the
// system message is split at the stable seam and the stable part carries
// the breakpoint (cache.go, applyOpenAICache, "Up to three stable
// boundaries plus the vendor's implicit tail"); the request is routed by a
// key that is the digest of that stable part (stablePrefixKey), with the
// vendor's implicit mode and its 30-minute retention. All of it holds still
// for the session.
func m3ExplicitChatMarks(t *testing.T, d m3Dialect, s *m3Session) {
	t.Helper()
	stable := m3ComposedStable(t, s)
	key := fmt.Sprintf("%q", fmt.Sprintf("%x", sha256.Sum256([]byte(stable))))
	for _, r := range s.requests {
		if got := m3Marks(r.marks); got != `  messages[0].content[0] {"mode":"explicit"}` {
			t.Errorf("%s: turn %d request %d carries its breakpoints at\n%s\nnot on the system text's stable part alone", d.name, r.turn, r.round, got)
		}
		var sys struct {
			Content []struct{ Text string }
		}
		if err := json.Unmarshal(r.parts[1].bytes, &sys); err != nil || len(sys.Content) == 0 || sys.Content[0].Text != stable {
			t.Errorf("%s: turn %d request %d: the system message's first part is not the composed stable part (%v)", d.name, r.turn, r.round, err)
		}
		if got := string(r.members["prompt_cache_key"]); got != key {
			t.Errorf("%s: turn %d request %d routes by prompt_cache_key %s, not the digest of the stable part %s", d.name, r.turn, r.round, got, key)
		}
		if got := string(r.members["prompt_cache_options"]); got != `{"mode":"implicit","ttl":"30m"}` {
			t.Errorf("%s: turn %d request %d sends prompt_cache_options %s", d.name, r.turn, r.round, got)
		}
	}
}

// m3ResponsesMarks: the Responses dialect routes its cache by a key that
// follows the stable part of the system prompt where the entry states none
// (chatgpt.go: "the key follows the stable part of the system prompt").
// The output cap goes to the public API on a key and never to the ChatGPT
// backend, which refuses it.
func m3ResponsesMarks(t *testing.T, d m3Dialect, s *m3Session) {
	t.Helper()
	stable := m3ComposedStable(t, s)
	key := fmt.Sprintf("%q", fmt.Sprintf("%x", sha256.Sum256([]byte(stable))))
	onKey := d.entry == "OpenAI"
	for _, r := range s.requests {
		cap, sent := r.members["max_output_tokens"]
		if n, err := strconv.Atoi(string(cap)); sent != onKey || (onKey && (err != nil || n <= 0)) {
			t.Errorf("%s: turn %d request %d sends max_output_tokens=%s; on an API key the cap is sent and on the subscription it is not", d.name, r.turn, r.round, cap)
		}
		var instructions string
		if err := json.Unmarshal(r.parts[1].bytes, &instructions); err != nil || !strings.HasPrefix(instructions, stable) {
			t.Errorf("%s: turn %d request %d: the instructions do not open with the composed stable part (%v)", d.name, r.turn, r.round, err)
		}
		if got := string(r.members["prompt_cache_key"]); got != key {
			t.Errorf("%s: turn %d request %d routes by prompt_cache_key %s, not the digest of the stable part %s", d.name, r.turn, r.round, got, key)
		}
	}
}

// m3GeminiMarks: a Gemini request carries no cache setting at all.
func m3GeminiMarks(t *testing.T, d m3Dialect, s *m3Session) {
	t.Helper()
	for _, r := range s.requests {
		if v, ok := r.members["cachedContent"]; ok {
			t.Errorf("%s: turn %d request %d names cachedContent %s; the dialect sends no cache setting", d.name, r.turn, r.round, v)
		}
	}
}

func m3ReasoningRow(t *testing.T, d m3Dialect, s *m3Session) string {
	t.Helper()
	carried, resets := 0, 0
	var expect []int // the replies whose reasoning this request should carry
	for i, r := range s.requests {
		if bytes.Contains(r.body, []byte("m3-reasoning-")) {
			carried++
		}
		if i == 0 || s.requests[i-1].turn != r.turn {
			expect = nil
			continue
		}
		prev := s.requests[i-1]
		if d.replays {
			expect = append(expect, prev.reply)
		}
		var missing []string
		for _, n := range expect {
			if !bytes.Contains(r.body, []byte(m3Reasoning(n))) {
				missing = append(missing, m3Reasoning(n))
			}
		}
		if len(missing) == 0 {
			continue
		}
		resets++
		expect = nil
		if bytes.Count(r.body, []byte(m3ResetNote)) <= bytes.Count(prev.body, []byte(m3ResetNote)) {
			t.Errorf("%s: turn %d request %d dropped reasoning its turn was carrying (%s), and the identity was not told", d.name, r.turn, r.round, strings.Join(missing, ", "))
		}
	}
	return fmt.Sprintf("%-28s %8d %8d %7d\n", d.name, len(s.requests), carried, resets)
}
