package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// turnRow is one turn_metrics row as a reader finds it: the usage is
// rebuilt from the stored columns, and known is false when they are NULL.
type turnRow struct {
	source, provider, model sql.NullString
	usage                   conversation.TurnUsage
	known                   bool
}

func lastTurnRow(t *testing.T, st *store.Store) turnRow {
	t.Helper()
	var r turnRow
	var prompt, completion, total, cached, write, write5m, write1h, readReports, writeReports, unknown, silent sql.NullInt64
	if err := st.DB().QueryRow(`SELECT source, provider, model, rounds,
		prompt_tokens, completion_tokens, total_tokens, cached_prompt_tokens,
		cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens,
		cache_read_reports, cache_write_reports, unknown_attempts, silent_calls
		FROM turn_metrics ORDER BY ts_ms DESC LIMIT 1`).Scan(&r.source, &r.provider, &r.model, &r.usage.Calls,
		&prompt, &completion, &total, &cached, &write, &write5m, &write1h, &readReports, &writeReports, &unknown, &silent); err != nil {
		t.Fatalf("no turn row: %v", err)
	}
	cols := []sql.NullInt64{prompt, completion, total, cached, write, write5m, write1h, readReports, writeReports, unknown, silent}
	for _, c := range cols {
		if c.Valid != silent.Valid {
			t.Fatalf("usage is part NULL, part known: %+v — a row's usage is whole or unknown", cols)
		}
	}
	r.known = silent.Valid
	u := &r.usage
	u.PromptTokens, u.CompletionTokens, u.TotalTokens = int(prompt.Int64), int(completion.Int64), int(total.Int64)
	u.CachedPromptTokens, u.CacheWriteTokens = int(cached.Int64), int(write.Int64)
	u.CacheWrite5mTokens, u.CacheWrite1hTokens = int(write5m.Int64), int(write1h.Int64)
	u.CacheReadReports, u.CacheWriteReports = int(readReports.Int64), int(writeReports.Int64)
	u.UnknownAttempts, u.Silent = int(unknown.Int64), int(silent.Int64)
	return r
}

func turnRowCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM turn_metrics`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// THE WHOLE TURN, NOT ITS HEADLINE. Every field the loop sums reaches the
// row and comes back as itself, and completeness is read from what was
// stored — never a flag written beside it. Each field carries its own
// value, so a column dropped or crossed with its neighbour cannot
// round-trip. A silent call, or an earlier attempt of unknown usage,
// reads back as a floor, because that is what the loop said it was.
func TestTurnUsageRoundTripsWholeThroughTheSummaryRow(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{store: st}
	a.adoptProvider(providerEntry{Name: "fixture-provider"})

	base := conversation.TurnUsage{
		PromptTokens: 1001, CompletionTokens: 1002, TotalTokens: 1003, CachedPromptTokens: 1004,
		CacheWriteTokens: 1005, CacheWrite5mTokens: 1006, CacheWrite1hTokens: 1007,
		CacheReadReports: 8, CacheWriteReports: 9, Calls: 3,
	}
	silent, unknown := base, base
	silent.Silent = 1
	unknown.UnknownAttempts = 2
	for name, tc := range map[string]struct {
		usage    conversation.TurnUsage
		complete bool
	}{
		"every call reported":            {base, true},
		"a silent call":                  {silent, false},
		"an attempt of unknown usage":    {unknown, false},
		"a silent call and an attempt":   {func() conversation.TurnUsage { u := silent; u.UnknownAttempts = 1; return u }(), false},
		"one call that reported nothing": {conversation.TurnUsage{Calls: 1, Silent: 1}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.usage.Complete() != tc.complete {
				t.Fatalf("fixture: Complete() = %v, want %v", tc.usage.Complete(), tc.complete)
			}
			a.recordTurnCost(withTurnSource(t.Context(), turnSourceTimer), conversation.Result{Usage: tc.usage, ModelID: "fixture-model"})
			got := lastTurnRow(t, st)
			if !got.known {
				t.Fatal("usage stored as unknown for a turn whose loop reported it")
			}
			if got.usage != tc.usage {
				t.Fatalf("usage did not round-trip:\n got %+v\nwant %+v", got.usage, tc.usage)
			}
			if got.usage.Complete() != tc.complete {
				t.Fatalf("read back as complete=%v, want %v", got.usage.Complete(), tc.complete)
			}
			if got.source.String != turnSourceTimer || got.provider.String != "fixture-provider" || got.model.String != "fixture-model" {
				t.Fatalf("row says source=%v provider=%v model=%v", got.source, got.provider, got.model)
			}
		})
	}
}

// Unknown is NULL, never zero and never a guess: a summary no loop
// stands behind (the meter's own tests reach it that way), and a turn
// whose path and provider entry were never named.
func TestTurnRowWithoutItsFactsReadsUnknown(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := &App{store: st}

	a.logTurnSummary(turnLoop{})
	got := lastTurnRow(t, st)
	if got.known || got.source.Valid || got.provider.Valid || got.model.Valid {
		t.Fatalf("a summary with no loop behind it claimed facts: %+v", got)
	}

	a.recordTurnCost(t.Context(), conversation.Result{Usage: conversation.TurnUsage{Calls: 1, PromptTokens: 5, TotalTokens: 5}})
	got = lastTurnRow(t, st)
	if !got.known || got.usage.PromptTokens != 5 {
		t.Fatalf("reported usage lost: %+v", got)
	}
	if got.source.Valid || got.provider.Valid || got.model.Valid {
		t.Fatalf("unnamed path, entry and model were stored as %v/%v/%v, want NULL", got.source, got.provider, got.model)
	}
}

// meteredApp is a runtime with a mind and a memory store, on a provider
// the test scripts: enough to run a real turn through its own path.
func meteredApp(t *testing.T, answer http.HandlerFunc) *App {
	t.Helper()
	srv := httptest.NewServer(answer)
	t.Cleanup(srv.Close)
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	a := New(&Config{Prompt: PromptConfig{MaxTokens: 32000}, Agency: AgencyConfig{MaxToolRounds: 3}})
	t.Cleanup(a.bgCancel)
	a.store = st
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	a.composer = prompt.New(rm, 32000)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, 32000)
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{a.toolReg}, identity.Ports{})
	c := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Provider: "anthropic", Model: "fixture", Retries: -1, TimeoutSeconds: 2})
	a.conv = conversation.New(c, pass4Executor{}, pass4Defs{}, nil, nil, conversation.Config{MaxIterations: 3, ContextBudgetTokens: 32000})
	a.adoptProvider(providerEntry{Name: "fixture-provider"})
	return a
}

// A TURN THAT FAILS STILL SPENT WHAT IT SPENT. The operator's turn makes
// one call that reports and one that fails; the row is written once, by
// the interrupted path, and reads as a floor: two calls, one silent.
func TestFailedTurnIsRecordedOnceAsAFloor(t *testing.T) {
	var requests atomic.Int32
	a := meteredApp(t, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 1 {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":{"type":"api_error","message":"fixture failure"}}`))
			return
		}
		response := map[string]any{"content": []any{map[string]any{"type": "text", "text": "Looking."}, map[string]any{"type": "tool_use", "id": "c1", "name": "read", "input": map[string]any{}}}, "stop_reason": "tool_use", "usage": map[string]any{"input_tokens": 100, "cache_read_input_tokens": 900, "output_tokens": 10}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	})
	if _, err := a.handleMessage(t.Context(), "what is on the timer?"); err == nil {
		t.Fatal("fixture expected the turn to fail")
	}
	if requests.Load() != 2 {
		t.Fatalf("fixture expected two requests; got %d", requests.Load())
	}
	if n := turnRowCount(t, a.store); n != 1 {
		t.Fatalf("the failed turn left %d rows, want exactly 1", n)
	}
	got := lastTurnRow(t, a.store)
	if !got.known || got.usage.Calls != 2 || got.usage.Silent != 1 || got.usage.CachedPromptTokens != 900 || got.usage.CompletionTokens != 10 {
		t.Fatalf("the failed turn's usage is not what its calls reported: %+v", got.usage)
	}
	if got.usage.Complete() {
		t.Fatal("a turn with a call that told nothing read back as an exact total")
	}
	if got.source.String != turnSourceOperator || got.provider.String != "fixture-provider" || got.model.String != "fixture" {
		t.Fatalf("row says source=%v provider=%v model=%v", got.source, got.provider, got.model)
	}
}

// A wake names its own path: the identity's alarm, through the entry the
// timer really takes, is a timer turn on its row.
func TestTimerWakeRecordsItsSource(t *testing.T) {
	a := meteredApp(t, func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{"content": []any{map[string]any{"type": "text", "text": "The kettle."}}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 40, "cache_read_input_tokens": 0, "output_tokens": 3}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	})
	a.wakeTimerAlarm(t.Context(), "alarm_fixture", "the kettle's alarm fired")
	if n := turnRowCount(t, a.store); n != 1 {
		t.Fatalf("the timer turn left %d rows, want 1", n)
	}
	got := lastTurnRow(t, a.store)
	if got.source.String != turnSourceTimer {
		t.Fatalf("timer turn recorded source %v", got.source)
	}
	if !got.known || got.usage.Calls != 1 || got.usage.PromptTokens != 40 || !got.usage.Complete() {
		t.Fatalf("timer turn usage: %+v", got.usage)
	}
}

// The join an outside harness makes: a real turn's usage row carries the id
// its recorded operator message carries.
func TestATurnsUsageRowNamesTheTurnItsMessageNames(t *testing.T) {
	a, _ := promptCacheApp(t)
	operatorTurn(t, a, "what did that cost")
	var said string
	if err := a.store.DB().QueryRow(`SELECT turn_id FROM conversations WHERE role = 'operator' AND content = ? ORDER BY turn_seq DESC LIMIT 1`, "what did that cost").Scan(&said); err != nil {
		t.Fatalf("no recorded operator message: %v", err)
	}
	if said == "" {
		t.Fatal("rig: the operator message names no turn")
	}
	var rows, calls int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(rounds), 0) FROM turn_metrics WHERE turn_id = ?`, said).Scan(&rows, &calls); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || calls != 1 {
		t.Fatalf("the turn %s has %d usage row(s) of %d call(s); want one row of one call", said, rows, calls)
	}
}
