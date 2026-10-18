package app

import (
	"context"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type contextProbe struct {
	name   string
	result string
	delay  bool
	mu     sync.Mutex
	calls  []pluginhost.ActingSession
	args   []map[string]any
}

func (p *contextProbe) Name() string               { return p.name }
func (p *contextProbe) Description() string        { return "Read local task context" }
func (p *contextProbe) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (p *contextProbe) ContextProvider() bool      { return true }
func (p *contextProbe) DispatchOwner() any         { return p }
func (p *contextProbe) Execute(ctx context.Context, args map[string]any) (tools.Result, error) {
	p.mu.Lock()
	p.calls = append(p.calls, pluginhost.ActingSessionFrom(ctx))
	p.args = append(p.args, args)
	p.mu.Unlock()
	if p.delay {
		<-ctx.Done()
		return tools.Result{}, ctx.Err()
	}
	return tools.Result{Output: p.result}, nil
}
func (p *contextProbe) snapshot() ([]pluginhost.ActingSession, []map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]pluginhost.ActingSession(nil), p.calls...), append([]map[string]any(nil), p.args...)
}

type contextSearch struct{ contextProbe }

func (*contextSearch) ContextProvider() bool { return false }
func (*contextSearch) Discovery() tools.Discovery {
	return tools.Discovery{Plugin: "org.example.context", Operation: "search", Effects: "read.internal", MaxResultBytes: 4096}
}
func addContextProbe(t *testing.T, a *App, result string) *contextProbe {
	t.Helper()
	search := &contextSearch{contextProbe: contextProbe{name: "pl_example_search"}}
	if e := a.toolReg.RegisterDynamic(search, "org.example.context"); e != nil {
		t.Fatal(e)
	}
	if _, e := a.toolReg.Offer(search.Name()); e != nil {
		t.Fatal(e)
	}
	p := &contextProbe{name: "pl_example_context", result: result}
	if e := a.toolReg.RegisterHostOp(p, "org.example.context"); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestPluginContextReachesFirstParentProviderRequest(t *testing.T) {
	a, obs := promptCacheApp(t)
	p := addContextProbe(t, a, `{"text":"synthetic context for compiler fixture","detail_route":"search id=exact-parent"}`)
	operatorTurn(t, a, "inspect compiler fixture")
	reqs := obs.snapshot()
	if len(reqs) != 1 || !strings.Contains(reqs[0], "synthetic context for compiler fixture") || !strings.Contains(reqs[0], "search id=exact-parent") {
		t.Fatal(reqs)
	}
	var body struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if e := json.Unmarshal([]byte(reqs[0]), &body); e != nil {
		t.Fatal(e)
	}
	for _, tool := range body.Tools {
		if tool.Function.Name == p.Name() {
			t.Fatal("host context in provider tools")
		}
	}
	calls, args := p.snapshot()
	if len(calls) != 1 || calls[0].CallID != "" || args[0]["max_bytes"] != pluginhost.ContextMaxBytes {
		t.Fatal(calls, args)
	}
	var n int
	if e := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE source_kind='tool' AND json_extract(details,'$.tool')=?`, p.Name()).Scan(&n); e != nil || n != 0 {
		t.Fatal("automatic read counted as model tool", n)
	}
}
func TestPluginContextReachesFirstFoldedChildRequestAndUsesChildSession(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("completed"))
	}))
	defer srv.Close()
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 4, SubagentMaxToolRounds: 4, SubagentMaxToolCalls: 32, SubagentMaxLegs: 1, SubagentWallSeconds: 5})
	p := addContextProbe(t, a, `{"text":"synthetic child lesson survives folding","detail_route":"search id=exact-child"}`)
	id := spawnChild(t, a, st, "inspect child fixture")
	waitDelivered(t, st, id)
	body := <-got
	if !strings.Contains(body, "synthetic child lesson survives folding") || !strings.Contains(body, "search id=exact-child") {
		t.Fatal(body)
	}
	calls, args := p.snapshot()
	if len(calls) != 1 || calls[0].ID != id || calls[0].CallID != "" || args[0]["query"] != "inspect child fixture" {
		t.Fatal(calls, args)
	}
	var callsN, emitted int
	if e := st.DB().QueryRow(`SELECT calls,emitted_calls FROM subagent_metrics WHERE session_id=?`, id).Scan(&callsN, &emitted); e != nil || callsN != 0 || emitted != 0 {
		t.Fatal("automatic read counted as model call", callsN, emitted, e)
	}
}
func TestPluginContextQuietEmptyUnavailableMalformedTimeoutAndDisabled(t *testing.T) {
	for _, tc := range []struct {
		name, result    string
		delay, disabled bool
		want            int
	}{
		{"empty", `{"text":"","detail_route":""}`, false, false, 0},
		{"malformed", `{"text":"x"}`, false, false, 1},
		{"timeout", ``, true, false, 1},
		{"disabled", `{"text":"x","detail_route":"search"}`, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := New(&Config{})
			a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
			p := addContextProbe(t, a, tc.result)
			p.delay = tc.delay
			if tc.disabled {
				a.toolReg.SetToolEnabled("pl_example_search", false)
			}
			start := time.Now()
			s := a.pluginContext(t.Context(), strings.Repeat("界", 1000))
			if len(s) != tc.want {
				t.Fatal(s)
			}
			if tc.delay && time.Since(start) > time.Second {
				t.Fatal("timeout failed to bound automatic read")
			}
			calls, args := p.snapshot()
			if tc.disabled {
				if len(calls) != 0 {
					t.Fatal("disabled plugin called")
				}
				return
			}
			if len(calls) != 1 || calls[0].ID != "" || calls[0].Reason != "no_session" || calls[0].CallID != "" || len(args[0]["query"].(string)) > pluginhost.ContextQueryBytes {
				t.Fatal(calls, args)
			}
			if len(s) > 0 && !strings.Contains(s[0].Content, "unavailable") {
				t.Fatal("failure presented as no match", s)
			}
		})
	}
}
func TestPluginContextChildSessionIgnoresMutableFocus(t *testing.T) {
	a := New(&Config{})
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	p := addContextProbe(t, a, `{"text":"","detail_route":""}`)
	a.pluginContext(context.WithValue(t.Context(), identity.SubagentWorkSession{}, "bound-child"), "task")
	calls, _ := p.snapshot()
	if len(calls) != 1 || calls[0].ID != "bound-child" {
		t.Fatal(calls)
	}
}

func TestPluginContextSafeAndFailedSessionLookupNeverCall(t *testing.T) {
	a := New(&Config{})
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	p := addContextProbe(t, a, `{"text":"","detail_route":""}`)
	a.mode.mode = ModeSafe
	if got := a.pluginContext(t.Context(), "task"); len(got) != 0 {
		t.Fatal(got)
	}
	calls, _ := p.snapshot()
	if len(calls) != 0 {
		t.Fatal("SAFE called plugin")
	}
	a.mode.mode = ModeNormal
	st, e := store.NewForTest(filepath.Join(t.TempDir(), "closed.db"))
	if e != nil {
		t.Fatal(e)
	}
	a.store = st
	_ = st.Close()
	if got := a.pluginContext(t.Context(), "task"); len(got) != 1 || !strings.Contains(got[0].Content, "unavailable") {
		t.Fatal(got)
	}
	calls, _ = p.snapshot()
	if len(calls) != 0 {
		t.Fatal("failed lookup called plugin")
	}
}
