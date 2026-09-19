package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
type foldProbeLLM struct {
	mu     sync.Mutex
	marker string
	tool   string
	args   string
	second []byte
	got    chan struct{}
}

func newFoldProbeLLM(t *testing.T, marker, tool, args string) (*foldProbeLLM, *httptest.Server) {
	t.Helper()
	p := &foldProbeLLM{marker: marker, tool: tool, args: args, got: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		text := string(body)
		switch {
		case !strings.Contains(text, p.marker):
			_, _ = io.WriteString(w, `{"id":"o","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"noted"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
		case !strings.Contains(text, `"role":"tool"`):
			p.mu.Lock()
			args := p.args
			p.mu.Unlock()
			call, _ := json.Marshal(args)
			_, _ = io.WriteString(w, `{"id":"c","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_probe","type":"function","function":{"name":"`+p.tool+`","arguments":`+string(call)+`}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`)
		default:
			p.mu.Lock()
			if p.second == nil {
				p.second = body
				close(p.got)
			}
			p.mu.Unlock()
			_, _ = io.WriteString(w, `{"id":"c","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"VERDICT: done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return p, srv
}

// .
// .
func (p *foldProbeLLM) notice(t *testing.T, wait time.Duration) string {
	t.Helper()
	select {
	case <-p.got:
	case <-time.After(wait):
		t.Fatal("the run never brought its tool result back to the model")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(p.second, &req); err != nil {
		t.Fatalf("the captured request is not a chat request: %v", err)
	}
	for _, m := range req.Messages {
		if m.Role != "tool" {
			continue
		}
		text, _ := m.Content.(string)
		if !strings.Contains(text, "tool result folded") {
			t.Fatalf("the fixture did not reach a fold — the result came back whole (%d chars); raise the pressure", len(text))
		}
		return text
	}
	t.Fatal("no tool message in the captured request")
	return ""
}

// .
// .
func bigFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "diagnostics.txt")
	line := strings.Repeat("the quick brown fox jumps over the lazy dog ", 2) + "\n"
	if err := os.WriteFile(path, []byte(strings.Repeat(line, 320)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestASubagentsFoldedReadMayBeRepeatedAndItsFoldedActMayNot(t *testing.T) {
	cases := []struct {
		name, tool string
		args       func(root string) string
		replayable bool
	}{
		{"read", "read", func(root string) string {
			return `{"file_path":` + jsonString(bigFile(t, root)) + `}`
		}, true},
		{"shell", "shell", func(string) string {
			return `{"command":"i=0; while [ $i -lt 400 ]; do echo 'a line the act printed, which no re-run is licensed to recover'; i=$((i+1)); done"}`
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.tool == "shell" && runtime.GOOS == "windows" {
				t.Skip("the act is a POSIX shell loop")
			}
			const goal = "FOLDPROBE inspect the diagnostics"
			probe, srv := newFoldProbeLLM(t, goal, tc.tool, "")
			a, st, _ := legFixtureBudget(t, srv.URL, AgencyConfig{
				MaxToolRounds: 4, SubagentMaxToolRounds: 4, SubagentMaxToolCalls: 16, SubagentMaxLegs: 1, SubagentWallSeconds: 20,
			}, 12000)
			root, _ := a.toolReg.Roots()
			probe.mu.Lock()
			probe.args = tc.args(root)
			probe.mu.Unlock()

			id := spawnChild(t, a, st, goal)
			n := probe.notice(t, 20*time.Second)
			waitDelivered(t, st, id)
			if got := strings.Contains(n, "may be repeated exactly"); got != tc.replayable {
				t.Fatalf("a sub-agent's folded %s: licensed for replay = %v, want %v\n%s", tc.tool, got, tc.replayable, n)
			}
			if !tc.replayable && !strings.Contains(n, "do not repeat the tool") {
				t.Fatalf("a folded act must say it is not to be re-run: %s", n)
			}
		})
	}
}

func TestSafeBootsFoldedReadMayBeRepeated(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeFold")
	buildPriorProjection(t, ledgerPath, dbPath)
	tamperChain(t, keyPath, ledgerPath)

	const ask = "FOLDPROBE what do the diagnostics say?"
	probe, srv := newFoldProbeLLM(t, ask, "read", `{"file_path":`+jsonString(bigFile(t, dir))+`}`)
	cfg := safebootConfig(t, dir, "SafeFold", keyPath, ledgerPath, dbPath)
	cfg.LLM = withTestProvider(t, dir, "test", srv.URL, "m", "sk-x")
	cfg.Prompt.MaxTokens = 12000

	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("SAFE boot must come up: %v", err)
	}
	defer app.Stop()
	if _, safe := app.SafeMode(); !safe {
		t.Fatal("fixture: this boot was meant to be SAFE")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := app.handleMessage(ctx, ask); err != nil {
		t.Fatalf("the SAFE conversation's turn: %v", err)
	}
	n := probe.notice(t, time.Second)
	if !strings.Contains(n, "may be repeated exactly") {
		t.Fatalf("SAFE is the read-only surface, and its folded read was not told it can be read again: %s", n)
	}
	if strings.Contains(n, "transcript excerpt") {
		t.Fatalf("SAFE retains no excerpt, so the notice must not send the identity to ask for one: %s", n)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestBootSafeRunsItsReadOnlyToolsWithoutWritingTheProjection(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeTools")
	buildPriorProjection(t, ledgerPath, dbPath)
	tamperChain(t, keyPath, ledgerPath)
	before, _ := fileDigest(t, dbPath)

	const ask = "FOLDPROBE list what is here"
	_, srv := newFoldProbeLLM(t, ask, "ls", `{"path":"."}`)
	cfg := safebootConfig(t, dir, "SafeTools", keyPath, ledgerPath, dbPath)
	cfg.LLM = withTestProvider(t, dir, "test", srv.URL, "m", "sk-x")

	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("SAFE boot must come up: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reply, err := app.handleMessage(ctx, ask)
	if err != nil {
		app.Stop()
		t.Fatalf("a read-only tool call failed the SAFE conversation's turn: %v", err)
	}
	if !strings.Contains(reply, "VERDICT: done") {
		app.Stop()
		t.Fatalf("the turn did not finish after its tool call: %q", reply)
	}
	events := app.safeTools.Events()
	if len(events) != 1 || events[0].Tool != "ls" || !events[0].Done || events[0].Failed {
		app.Stop()
		t.Fatalf("the transient record must hold the one call, started and done: %+v", events)
	}
	app.Stop()
	if after, _ := fileDigest(t, dbPath); after != before {
		t.Fatal("the SAFE conversation's tool call wrote the projection under suspicion")
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
