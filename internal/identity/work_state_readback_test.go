package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func workReadbackEngine(t *testing.T, ports Ports) (*Engine, *store.Store) {
	t.Helper()
	base, st, _, _, _ := setupEngine(t)
	return NewEngine(st, base.ledger, base.rings, base.toolDisc, ports), st
}

func TestWorkStatusReadsAuthoredStateBeforeTheWorkerRendezvous(t *testing.T) {
	type contextMarker struct{}
	ctx := context.WithValue(t.Context(), contextMarker{}, "calling-turn")
	ctx = context.WithValue(ctx, SubagentWorkSession{}, "bound-child")
	const state = "AUTHORED_HEAD\nExpected evidence: whole retained criterion 界\nAUTHORED_TAIL"
	called := 0
	ports := Ports{WorkState: func(got context.Context) (string, error) {
		called++
		if got != ctx || got.Value(contextMarker{}) != "calling-turn" || got.Value(SubagentWorkSession{}) != "bound-child" {
			t.Fatal("work-state readback lost the caller's authenticated context")
		}
		return state, nil
	}}
	e, st := workReadbackEngine(t, ports)
	// The port is copied at construction, not looked up from a mutable setup.
	ports.WorkState = func(context.Context) (string, error) {
		t.Fatal("the engine reread its caller's changed Ports value")
		return "", nil
	}
	subAgentSession(t, st, "ws_running", "ongoing investigation", "")
	subAgentSession(t, st, "ws_delivered", "the retained outcome", "served: RESULT_WHOLE")
	st.DB().SetMaxOpenConns(1)
	var before int64
	if err := st.DB().QueryRow("SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	out, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "status"})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || !strings.HasPrefix(out, state+"\n\nSub-agents: 1 running, 1 delivered awaiting your next compose.\n") {
		t.Fatalf("status does not lead with the whole host-authored state: calls=%d, %q", called, out)
	}
	for _, want := range []string{"- running ws_running: ongoing investigation", "- delivered ws_delivered: the retained outcome", "work action=status id="} {
		if !strings.Contains(out, want) {
			t.Errorf("status lost existing rendezvous content %q: %q", want, out)
		}
	}
	var after int64
	if err := st.DB().QueryRow("SELECT total_changes()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("a work-status read wrote to the store: total_changes %d to %d", before, after)
	}
	if delivered, err := st.UnharvestedDeliveries(10); err != nil || len(delivered) != 1 || delivered[0].ID != "ws_delivered" {
		t.Fatalf("status changed pending delivery state: %+v, %v", delivered, err)
	}
	result, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "status", "id": "ws_delivered part:1"})
	if err != nil || !strings.Contains(result, "RESULT_WHOLE") || called != 1 {
		t.Fatalf("id status changed delivered-result behavior or called the work-state port: calls=%d, %q, %v", called, result, err)
	}
}

func TestWorkStatusDistinguishesUnavailableAndEmptyAuthoredState(t *testing.T) {
	for _, tc := range []struct {
		name string
		read func(context.Context) (string, error)
		want string
	}{
		{"unwired", nil, "Authored work-state readback unavailable:"},
		{"empty", func(context.Context) (string, error) { return "", nil }, "No authored work state is recorded."},
		{"whitespace", func(context.Context) (string, error) { return " \t\n", nil }, "No authored work state is recorded."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, st := workReadbackEngine(t, Ports{WorkState: tc.read})
			out, err := e.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
			if err != nil || !strings.HasPrefix(out, tc.want) || !strings.Contains(out, "Sub-agents: 0 running, 0 delivered awaiting your next compose.") {
				t.Fatalf("missing/empty state lost its honest diagnosis or worker rendezvous: %q, %v", out, err)
			}
			if tc.read != nil && (!strings.Contains(out, "work action=update standing=") || !strings.Contains(out, "work action=start description=")) {
				t.Fatalf("empty authored state names no existing way to author it: %q", out)
			}
			tiny := NewEngine(st, e.ledger, e.rings, e.toolDisc, Ports{WorkState: tc.read, ToolResultChars: 1})
			out, err = tiny.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
			if out != "" || err == nil || !strings.HasPrefix(err.Error(), "rejected_before_effect (status.size):") {
				t.Fatalf("missing/empty state diagnostics bypassed the whole-response bound: %q, %v", out, err)
			}
		})
	}
}

func TestWorkStatusPreservesReadErrorsAndLeavesResultReadsIndependent(t *testing.T) {
	cause := errors.New("authored work cannot be read")
	called := 0
	e, st := workReadbackEngine(t, Ports{WorkState: func(context.Context) (string, error) {
		called++
		return "INCOMPLETE_STATE", fmt.Errorf("host renderer: %w", cause)
	}})
	subAgentSession(t, st, "ws_result", "independent delivery", "served: INDEPENDENT_RESULT")
	for _, safe := range []bool{false, true} {
		if safe {
			e.SetSafeMode("read-only diagnostic")
		}
		out, err := e.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
		if !errors.Is(err, cause) || out != "" {
			t.Fatalf("safe=%v: authored read failure became absence or partial success: %q, %v", safe, out, err)
		}
	}
	result, err := e.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status", "id": " ws_result part:1 "})
	if err != nil || !strings.Contains(result, "INDEPENDENT_RESULT") || called != 2 {
		t.Fatalf("an authored-state failure blocked the existing id read: calls=%d, %q, %v", called, result, err)
	}
}

func TestWorkStatusFitsItsWholeFormattedResponseOrRefuses(t *testing.T) {
	const rendezvous = "\n\nSub-agents: 0 running, 0 delivered awaiting your next compose."
	const short = "HEAD é界🧭 TAIL"
	shortSize := utf8.RuneCountInString(short + rendezvous)
	defaultState := strings.Repeat("界", conversation.DefaultMaxToolResultChars-utf8.RuneCountInString(rendezvous))
	for _, tc := range []struct {
		name  string
		state string
		limit int
		ok    bool
	}{
		{"configured exact fit includes all framing", short, shortSize, true},
		{"configured one over", short, shortSize - 1, false},
		{"default exact fit counts Unicode characters", defaultState, 0, true},
		{"default one over", defaultState + "界", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := workReadbackEngine(t, Ports{ToolResultChars: tc.limit, WorkState: func(context.Context) (string, error) { return tc.state, nil }})
			want := tc.state + rendezvous
			out, err := e.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
			if tc.ok {
				if err != nil || out != want || utf8.RuneCountInString(out) != e.ToolResultLimit() {
					t.Fatalf("an exact-fit whole Unicode response was refused or changed: got %d characters, limit %d, err %v", utf8.RuneCountInString(out), e.ToolResultLimit(), err)
				}
				return
			}
			if out != "" || err == nil || !strings.HasPrefix(err.Error(), "rejected_before_effect (status.size):") {
				t.Fatalf("oversized state was returned/clipped instead of a classifiable refusal: %q, %v", out, err)
			}
			var sizeError *WorkStatusSizeError
			if !errors.As(err, &sizeError) || sizeError.Required != utf8.RuneCountInString(want) || sizeError.Limit != e.ToolResultLimit() {
				t.Fatalf("the size refusal lost its typed required/allowed bounds: %#v, %v", sizeError, err)
			}
			for _, required := range []string{"prompt.max_tool_result_chars", "restart", fmt.Sprintf("needs %d characters", utf8.RuneCountInString(want)), fmt.Sprintf("allows %d", e.ToolResultLimit())} {
				if !strings.Contains(err.Error(), required) {
					t.Errorf("size refusal lost %q: %v", required, err)
				}
			}
		})
	}
}

func TestWorkStatusRefusesInvalidUTF8BeforeSizingWithoutWriting(t *testing.T) {
	for _, limit := range []int{0, 1} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			e, st := workReadbackEngine(t, Ports{ToolResultChars: limit, WorkState: func(context.Context) (string, error) {
				return "RETAINED_HEAD\xffRETAINED_TAIL", nil
			}})
			st.DB().SetMaxOpenConns(1)
			var before int64
			if err := st.DB().QueryRow("SELECT total_changes()").Scan(&before); err != nil {
				t.Fatal(err)
			}
			out, err := e.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
			if out != "" || err == nil || !strings.HasPrefix(err.Error(), "rejected_before_effect (status.encoding):") || !strings.Contains(err.Error(), "not valid UTF-8") {
				t.Fatalf("malformed retained bytes were normalized, returned or mistaken for size overflow: %q, %v", out, err)
			}
			var sizeError *WorkStatusSizeError
			if errors.As(err, &sizeError) {
				t.Fatalf("encoding was checked after sizing: %#v", sizeError)
			}
			var after int64
			if err := st.DB().QueryRow("SELECT total_changes()").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("refusing malformed retained bytes wrote to the store: total_changes %d to %d", before, after)
			}
		})
	}
}

func TestWorkStatusCountsTheWorkerListInItsBound(t *testing.T) {
	base, st, _, _, _ := setupEngine(t)
	state := func(context.Context) (string, error) { return "AUTHORED", nil }
	makeEngine := func(limit int) *Engine {
		return NewEngine(st, base.ledger, base.rings, base.toolDisc, Ports{WorkState: state, ToolResultChars: limit})
	}
	subAgentSession(t, st, "ws_unicode", "investigate 界🧭 é", "")
	out, err := makeEngine(0).ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
	if err != nil {
		t.Fatal(err)
	}
	size := utf8.RuneCountInString(out)
	got, err := makeEngine(size).ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
	if err != nil || got != out || !strings.Contains(got, "Never sleep or poll") {
		t.Fatalf("the exact-fit worker rendezvous changed: %q, %v", got, err)
	}
	got, err = makeEngine(size-1).ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
	if got != "" || err == nil || !strings.HasPrefix(err.Error(), "rejected_before_effect (status.size):") {
		t.Fatalf("worker lines or waiting guidance bypassed the result bound: %q, %v", got, err)
	}
}

func TestWorkStatusKeepsReadableStateWhenSAFEWorkerQueriesFail(t *testing.T) {
	for _, tc := range []struct{ name, damage string }{
		{"live query fails", "DROP TABLE work_sessions"},
		{"delivery query fails", "ALTER TABLE work_sessions RENAME COLUMN harvested_ms TO retained_harvested_ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, st, _, _, _ := setupEngine(t)
			const state = "RETAINED_STANDING_HEAD\nExpected evidence: retained whole\nRETAINED_STANDING_TAIL"
			if err := st.SetStandingState(state); err != nil {
				t.Fatal(err)
			}
			e := NewEngine(st, base.ledger, base.rings, base.toolDisc, Ports{WorkState: func(context.Context) (string, error) { return st.StandingState() }})
			if _, err := st.DB().Exec(tc.damage); err != nil {
				t.Fatal(err)
			}
			st.DB().SetMaxOpenConns(1)
			var before int64
			if err := st.DB().QueryRow("SELECT total_changes()").Scan(&before); err != nil {
				t.Fatal(err)
			}
			out, err := e.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
			if err == nil || errors.Unwrap(err) == nil || out != "" {
				t.Fatalf("normal mode lost a real worker-query failure: %q, %v", out, err)
			}
			cause := errors.Unwrap(err).Error()
			e.SetSafeMode("historical projection is read-only")
			st.SetFrozen(true, "historical projection is read-only")
			out, err = e.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
			if err != nil || !strings.HasPrefix(out, state+"\n\nSub-agent rendezvous unavailable in SAFE:") || !strings.Contains(out, cause) {
				t.Fatalf("SAFE discarded readable state or concealed the worker failure: %q, %v", out, err)
			}
			if strings.Contains(out, "Sub-agents:") || !strings.Contains(out, "do not infer zero running or delivered workers") {
				t.Fatalf("SAFE turned unavailable worker state into an empty rendezvous: %q", out)
			}
			tight := NewEngine(st, base.ledger, base.rings, base.toolDisc, Ports{WorkState: func(context.Context) (string, error) { return st.StandingState() }, ToolResultChars: utf8.RuneCountInString(out) - 1})
			tight.SetSafeMode("historical projection is read-only")
			out, err = tight.ExecuteAction(t.Context(), "verb", "work", map[string]any{"action": "status"})
			if out != "" || err == nil || !strings.HasPrefix(err.Error(), "rejected_before_effect (status.size):") {
				t.Fatalf("SAFE worker diagnostics bypassed the whole-response bound: %q, %v", out, err)
			}
			var sizeError *WorkStatusSizeError
			if !errors.As(err, &sizeError) || sizeError.Required != tight.ToolResultLimit()+1 || sizeError.Limit != tight.ToolResultLimit() {
				t.Fatalf("SAFE's size refusal lost its typed whole-response bounds: %#v, %v", sizeError, err)
			}
			var after int64
			if err := st.DB().QueryRow("SELECT total_changes()").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("normal/SAFE status wrote to the diagnostic projection: total_changes %d to %d", before, after)
			}
		})
	}
}

func TestWorkStatusAdvertisesTheBoundedReadback(t *testing.T) {
	props := lookupVerb("work").Params["properties"].(map[string]any)
	action := props["action"].(map[string]any)["description"].(string)
	for _, want := range []string{"without id", "authored working state whole", "rendezvous", "oversized reply refuses", "prompt.max_tool_result_chars", "restart", "id reads one delivered result"} {
		if !strings.Contains(action, want) {
			t.Errorf("the advertised work status lost its bounded readback requirement %q: %s", want, action)
		}
	}
}

func TestEngineToolResultLimitUsesItsImmutableBootValue(t *testing.T) {
	for _, value := range []int{0, -1, 6000} {
		ports := Ports{ToolResultChars: value}
		e, _ := workReadbackEngine(t, ports)
		ports.ToolResultChars = 1
		want := value
		if want <= 0 {
			want = conversation.DefaultMaxToolResultChars
		}
		if got := e.ToolResultLimit(); got != want {
			t.Errorf("boot value %d resolved to %d, want %d", value, got, want)
		}
	}
}
