//go:build live

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestALiveSessionKeepsEachVendorsCacheAndReasoning(t *testing.T) {
	for _, d := range m3Dialects() {
		t.Run(d.name, func(t *testing.T) {
			s, usage := m3RunLive(t, d)
			m3HoldPrefix(t, d, s)
			d.marks(t, d, s)
			m3LiveReasoning(t, d, s, usage)
			m3LiveShare(t, d, s, usage)
		})
	}
}

// m3LiveWords are the session's operator turns: the hermetic script's
// work, asked in words, so the model makes its own tool calls.
var m3LiveWords = []string{
	"M3 live turn 01: Good morning. In one sentence, what can you help me with today?",
	"M3 live turn 02: Read the file plan.md in your working folder and tell me its first step.",
	"M3 live turn 03: Please note in your record that the lighthouse inspection is due on Friday.",
	"M3 live turn 04: Compare plan.md with what is in your working folder: list the folder, and search it for the word lantern.",
	"M3 live turn 05: Thank you, that is clear.",
	"M3 live turn 06: Start a work session for the lighthouse inspection, and record in it that the lantern is checked and the gallery is next.",
	"M3 live turn 07: What do you remember about the lighthouse? Look it up with recall.",
	"M3 live turn 08: Write a short inspection note to draft.md in your working folder saying the lantern is sound, then read it back to me.",
	"M3 live turn 09: Good. Keep your answers short from now on.",
	"M3 live turn 10: How does the inspection work stand? Check your work status.",
	"M3 live turn 11: Read plan.md once more and tell me its last step.",
	"M3 live turn 12: That is all for today.",
}

// m3LiveUsage is what the vendor said of a request: its input, the part of
// it read from the cache, the part written to it, a Messages response's
// cache diagnosis, and whether the answer carried reasoning of its own.
type m3LiveUsage struct {
	input, cached, written int
	diagnosis              string
	reasoned               bool
}

// m3RunLive drives the session against the vendor of d's entry and reads
// back what was sent and what the vendor said of each request.
func m3RunLive(t *testing.T, d m3Dialect) (*m3Session, []m3LiveUsage) {
	t.Helper()
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("M3 plan\n1. check the lantern\n2. check the gallery\n3. write the inspection note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cc := m3LiveClient(t, d)
	upstream := strings.TrimSuffix(cc.Endpoint, "/")

	// An exchange is kept when its request arrives, so a turn's requests are
	// counted in the order they were sent, and done when its response has
	// been passed on whole.
	type exchange struct {
		request  []byte
		response bytes.Buffer
		done     chan struct{}
	}
	var mu sync.Mutex
	var resident []*exchange
	others := 0
	relay := &http.Client{Timeout: 10 * time.Minute}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		x := &exchange{request: body, done: make(chan struct{})}
		defer close(x.done)
		mu.Lock()
		if bytes.Contains(body, []byte(conversation.TurnOpen)) {
			resident = append(resident, x)
		} else {
			others++
		}
		mu.Unlock()
		out, err := http.NewRequestWithContext(r.Context(), r.Method, upstream+r.URL.RequestURI(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		for k, v := range r.Header {
			if !strings.EqualFold(k, "Accept-Encoding") && !strings.EqualFold(k, "Content-Length") {
				out.Header[k] = v
			}
		}
		resp, err := relay.Do(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			if !strings.EqualFold(k, "Content-Length") && !strings.EqualFold(k, "Content-Encoding") {
				w.Header()[k] = v
			}
		}
		w.WriteHeader(resp.StatusCode)
		// Passed on as it arrives, so the client's idle watch reads the
		// vendor's own pace, and kept whole for its usage.
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 32<<10)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				x.response.Write(buf[:n])
				_, _ = w.Write(buf[:n])
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rerr != nil {
				break
			}
		}
	}))
	t.Cleanup(srv.Close)
	cc.Endpoint = srv.URL
	a.llmSwap.Swap(llm.New(&cc))

	s := &m3Session{app: a}
	var usage []m3LiveUsage
	for i, words := range m3LiveWords {
		mu.Lock()
		before := len(resident)
		mu.Unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		ctx = interaction.WithSaid(ctx, interaction.SaidTyped)
		if err := a.acquireTurn(ctx); err != nil {
			cancel()
			t.Fatalf("live %s: turn %d: %v", d.name, i+1, err)
		}
		reply, err := a.observeChat(ctx, words, func(kind, name, args string) {})
		cancel()
		if err != nil {
			t.Fatalf("live %s: turn %d failed: %v", d.name, i+1, err)
		}
		waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
		mu.Lock()
		after := append([]*exchange(nil), resident[before:]...)
		mu.Unlock()
		if len(after) == 0 {
			t.Fatalf("live %s: turn %d sent nothing the relay saw", d.name, i+1)
		}
		for _, x := range after {
			select {
			case <-x.done:
			case <-time.After(time.Minute):
				t.Fatalf("live %s: turn %d ended before the relay passed on a response whole", d.name, i+1)
			}
		}
		t.Logf("live %s turn %02d answered: %.200q", d.name, i+1, reply)
		s.words = append(s.words, words)
		for round, x := range after {
			parts, marks := d.read(t, x.request)
			s.requests = append(s.requests, m3Request{turn: i + 1, round: round + 1, reply: before + round + 1, body: x.request,
				parts: parts, marks: marks, members: m3Members(t, x.request)})
			if round == 0 {
				s.current = append(s.current, len(parts)-1)
			}
			u := m3ReadUsage(t, d, x.response.Bytes())
			usage = append(usage, u)
			line := fmt.Sprintf("live %s turn %02d round %d: %d bytes; input %d tokens, cached %d (%.1f%%), written %d; reasoning carried in %d block(s)",
				d.name, i+1, round+1, len(x.request), u.input, u.cached, m3Share(u.cached, u.input), u.written, m3CarriedReasoning(t, d, x.request))
			if u.diagnosis != "" {
				line += "; cache diagnosis " + u.diagnosis
			}
			t.Log(line)
		}
	}
	mu.Lock()
	s.others = others
	mu.Unlock()
	if s.others != 0 {
		t.Logf("live %s: %d requests during the session were no resident turn's (a facility's pass); they are not counted", d.name, s.others)
	}
	return s, usage
}

func m3LiveClient(t *testing.T, d m3Dialect) llm.ClientConfig {
	t.Helper()
	dir := t.TempDir()
	model := m3Model(t, d)
	if m := os.Getenv("AII_PROMPT_LIVE_MODEL"); m != "" {
		model = m
	}
	entry := liveProvider(t, d.entry, model)
	switch d.name {
	case "openai-chat":
		url := os.Getenv("AII_PROMPT_LIVE_LOCAL_URL")
		local := os.Getenv("AII_PROMPT_LIVE_LOCAL_MODEL")
		if url == "" || local == "" {
			t.Fatal("the local row needs AII_PROMPT_LIVE_LOCAL_URL and AII_PROMPT_LIVE_LOCAL_MODEL: the operator's own server and the model it serves")
		}
		entry = liveProvider(t, d.entry, local)
		entry.URL = url
		entry.APIKey = os.Getenv("AII_PROMPT_LIVE_LOCAL_KEY")
		entry.ReplayReasoning = true // as the hermetic row: the one way this dialect carries reasoning
	case "anthropic", "openai-chat-explicit-cache", "gemini", "responses-api-key":
		if os.Getenv(entry.VendorKeyEnv) == "" {
			t.Fatalf("the %s row needs %s", d.name, entry.VendorKeyEnv)
		}
		entry.APIKeyEnv = entry.VendorKeyEnv
		if d.name == "responses-api-key" {
			entry.APIType = "responses" // as the hermetic row: the shape selected on the card
		}
	default:
		m3KeepOriginal(t, entry)
	}
	writeTestProviders(t, dir, entry)
	r := New(&Config{LLM: LLMConfig{Provider: entry.Name, Model: entry.DefaultModel, TimeoutSeconds: 300, Retries: -1}, SourcePath: filepath.Join(dir, "config.json")})
	t.Cleanup(r.bgCancel)
	cc, _, err := r.resolveLLM()
	if err != nil {
		t.Fatalf("live %s: resolve %q: %v", d.name, d.entry, err)
	}
	if got := llm.DialectFor(cc.Provider); got != m3DialectOf(d) {
		t.Fatalf("live %s: the entry %q resolved to the %q dialect, want %q", d.name, d.entry, got, m3DialectOf(d))
	}
	t.Logf("live %s: %s at %s, model %s", d.name, d.entry, cc.Endpoint, cc.Model)
	return cc
}

func m3KeepOriginal(t *testing.T, entry providerEntry) {
	t.Helper()
	path := entry.CredentialOptions["default_file"]
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(home, path[2:])
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Logf("live: %s is not readable here (%v); the entry reads its credential elsewhere", path, err)
		return
	}
	digest := sha256.Sum256(before)
	t.Cleanup(func() {
		after, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(after) != digest {
			t.Errorf("the credential original %s changed during the row (%v)", path, err)
		}
	})
}

// m3Objects is a response as it came, one JSON object or a stream of them.
func m3Objects(raw []byte) []map[string]any {
	var objects []map[string]any
	add := func(b []byte) {
		var v map[string]any
		if json.Unmarshal(b, &v) == nil {
			objects = append(objects, v)
		}
	}
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		add(raw)
		return objects
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if p, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:")); ok {
			add(bytes.TrimSpace(p))
		}
	}
	return objects
}

// m3ReadUsage reads the vendor's report of a request from its response.
func m3ReadUsage(t *testing.T, d m3Dialect, raw []byte) m3LiveUsage {
	t.Helper()
	objects := m3Objects(raw)
	num := func(m map[string]any, keys ...string) int {
		var v any = m
		for _, k := range keys {
			mm, ok := v.(map[string]any)
			if !ok {
				return 0
			}
			v = mm[k]
		}
		f, _ := v.(float64)
		return int(f)
	}
	// Every usage object the response carries, deepest first found; the
	// largest count of each kind is the request's (a stream repeats or
	// completes its counts as it goes).
	var u m3LiveUsage
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			// The answer's own reasoning, in the dialect's shape: a thinking
			// block, a reasoning item, a signed part, reasoning_content.
			switch typ, _ := x["type"].(string); {
			case typ == "thinking" || typ == "redacted_thinking" || typ == "reasoning":
				u.reasoned = true
			case x["thoughtSignature"] != nil && x["thoughtSignature"] != "":
				u.reasoned = true
			case x["reasoning_content"] != nil && x["reasoning_content"] != "":
				u.reasoned = true
			}
			if diag, ok := x["diagnostics"]; ok && diag != nil && u.diagnosis == "" {
				b, _ := json.Marshal(diag)
				u.diagnosis = string(b)
			}
			for key, inner := range x {
				m, ok := inner.(map[string]any)
				if !ok {
					walk(inner)
					continue
				}
				switch {
				case key == "usage" && m3DialectOf(d) == llm.DialectAnthropic:
					read, wrote := num(m, "cache_read_input_tokens"), num(m, "cache_creation_input_tokens")
					u.input = max(u.input, num(m, "input_tokens")+read+wrote)
					u.cached, u.written = max(u.cached, read), max(u.written, wrote)
				case key == "usage" && m3DialectOf(d) == llm.DialectResponses:
					u.input = max(u.input, num(m, "input_tokens"))
					u.cached = max(u.cached, num(m, "input_tokens_details", "cached_tokens"))
					u.written = max(u.written, num(m, "input_tokens_details", "cache_write_tokens"))
				case key == "usage":
					u.input = max(u.input, num(m, "prompt_tokens"))
					u.cached = max(u.cached, num(m, "prompt_tokens_details", "cached_tokens"))
					u.written = max(u.written, num(m, "prompt_tokens_details", "cache_write_tokens"))
				case key == "usageMetadata":
					u.input = max(u.input, num(m, "promptTokenCount"))
					u.cached = max(u.cached, num(m, "cachedContentTokenCount"))
				default:
					walk(m)
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	for _, o := range objects {
		walk(o)
	}
	return u
}

func m3Share(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

func m3CarriedReasoning(t *testing.T, d m3Dialect, body []byte) int {
	t.Helper()
	m := m3Members(t, body)
	n := 0
	switch m3DialectOf(d) {
	case llm.DialectAnthropic:
		var msgs []struct{ Content []struct{ Type string } }
		_ = json.Unmarshal(m["messages"], &msgs)
		for _, msg := range msgs {
			for _, b := range msg.Content {
				if b.Type == "thinking" || b.Type == "redacted_thinking" {
					n++
				}
			}
		}
	case llm.DialectResponses:
		var items []struct{ Type string }
		_ = json.Unmarshal(m["input"], &items)
		for _, it := range items {
			if it.Type == "reasoning" {
				n++
			}
		}
	case llm.DialectGemini:
		var contents []struct {
			Parts []struct {
				ThoughtSignature string `json:"thoughtSignature"`
			}
		}
		_ = json.Unmarshal(m["contents"], &contents)
		for _, c := range contents {
			for _, p := range c.Parts {
				if p.ThoughtSignature != "" && p.ThoughtSignature != "skip_thought_signature_validator" {
					n++
				}
			}
		}
	default:
		var msgs []struct {
			ReasoningContent string `json:"reasoning_content"`
		}
		_ = json.Unmarshal(m["messages"], &msgs)
		for _, msg := range msgs {
			if msg.ReasoningContent != "" {
				n++
			}
		}
	}
	return n
}

// m3LiveReasoning prints the row in the recorded file's form and holds the
// ordinary session to no reset: on a row that replays, a request of a turn
// carries what the request before it carried, and more when the answer to
// that request reasoned; less is reasoning dropped.
func m3LiveReasoning(t *testing.T, d m3Dialect, s *m3Session, usage []m3LiveUsage) {
	t.Helper()
	carried, resets := 0, 0
	for i, r := range s.requests {
		n := m3CarriedReasoning(t, d, r.body)
		if n > 0 {
			carried++
		}
		if i == 0 || s.requests[i-1].turn != r.turn {
			continue
		}
		prev := s.requests[i-1]
		was := m3CarriedReasoning(t, d, prev.body)
		if n < was || d.replays && usage[i-1].reasoned && n == was {
			resets++
			declared := bytes.Count(r.body, []byte(m3ResetNote)) > bytes.Count(prev.body, []byte(m3ResetNote))
			t.Errorf("live %s: turn %d request %d dropped reasoning its turn was carrying (declared to the identity: %t)", d.name, r.turn, r.round, declared)
		}
	}
	t.Logf("live row: %-28s %8d %8d %7d", d.name, len(s.requests), carried, resets)
	if strings.HasPrefix(d.name, "anthropic") && carried == 0 {
		t.Errorf("live %s: no request carried thinking into a later call of its turn, so whether the vendor keeps it was not asked", d.name)
	}
}

// m3LiveShare prints the row's cached share after its first turn and holds
// the dialects that declare a cache to some reuse.
func m3LiveShare(t *testing.T, d m3Dialect, s *m3Session, usage []m3LiveUsage) {
	t.Helper()
	input, cached := 0, 0
	for i, r := range s.requests {
		if r.turn > 1 {
			input += usage[i].input
			cached += usage[i].cached
		}
	}
	t.Logf("live %s: after the first turn, %d of %d input tokens were read from the cache (%.1f%%)", d.name, cached, input, m3Share(cached, input))
	if (strings.HasPrefix(d.name, "anthropic") || d.name == "openai-chat-explicit-cache") && cached == 0 {
		t.Errorf("live %s: nothing was read from the cache after the first turn", d.name)
	}
}
