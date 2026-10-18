package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// facilityDoor is the door the boot hands the facilities (wireCognitive),
// over a real client whose provider the test scripts, on a runtime whose
// provider entry is named.
func facilityDoor(t *testing.T, st *store.Store, answer http.HandlerFunc) facilityLLM {
	t.Helper()
	srv := httptest.NewServer(answer)
	t.Cleanup(srv.Close)
	a := &App{store: st}
	a.adoptProvider(providerEntry{Name: "fixture-provider"})
	c := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fixture", NoStream: true, MaxInputTokens: 100000, Retries: 1, RetryBackoffMS: 1})
	return facilityLLM{newSwappableLLM(c), a}
}

// answering replies with one choice, ended as reason says, and usage.
func answering(t *testing.T, message map[string]any, reason string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"id": "r", "model": "fixture",
			"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": reason}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		}); err != nil {
			t.Error(err)
		}
	}
}

func facilityRowCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM turn_metrics WHERE source LIKE 'facility:%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// wantFacilityRow holds the last row to one facility call: its source,
// the entry and model it ran on, no tool calls, one provider call.
func wantFacilityRow(t *testing.T, st *store.Store, source string) turnRow {
	t.Helper()
	got := lastTurnRow(t, st)
	var calls int
	if err := st.DB().QueryRow(`SELECT calls FROM turn_metrics ORDER BY ts_ms DESC LIMIT 1`).Scan(&calls); err != nil {
		t.Fatal(err)
	}
	if got.source.String != source || got.provider.String != "fixture-provider" || got.model.String != "fixture" {
		t.Fatalf("row says source=%v provider=%v model=%v, want %s on fixture-provider/fixture", got.source, got.provider, got.model, source)
	}
	if !got.known || got.usage.Calls != 1 || calls != 0 {
		t.Fatalf("a facility call's row: known=%v rounds=%d tool calls=%d, want known, 1, 0", got.known, got.usage.Calls, calls)
	}
	return got
}

// A DREAM PASS SPENDS, AND NOW THE RECORD SAYS SO. Its one call reaches the
// model through the facility door and leaves exactly one turn_metrics row:
// source facility:dream, the entry and model it ran on, and what the call
// reported — a whole total, because the call reported.
func TestADreamPassKeepsItsCallAsAFacilityRow(t *testing.T) {
	b := newTalkBench(t, nil, nil)
	b.mintExperiences(t, "exp_a")
	b.say(t, "operator", "are you there?")
	door := facilityDoor(t, b.st, answering(t, map[string]any{"role": "assistant", "content": "You notice the operator checks in before asking."}, "stop"))
	b.fac = cognitive.NewDream(b.st, door, b.door, b.rings, dreamConfig(*defaultConfig()))
	b.fac.SetConversation(b.st)
	if err := b.fac.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.notes(t)) != 1 {
		t.Fatalf("fixture: the pass minted %d notes, want 1", len(b.notes(t)))
	}
	if n := turnRowCount(t, b.st); n != 1 {
		t.Fatalf("one DREAM call left %d turn_metrics rows, want exactly 1", n)
	}
	got := wantFacilityRow(t, b.st, "facility:dream")
	if u := got.usage; u.PromptTokens != 10 || u.CompletionTokens != 5 || u.TotalTokens != 15 || !u.Complete() {
		t.Fatalf("the call's usage did not arrive whole: %+v", u)
	}
}

// A FAILED CALL WAS STILL A CALL. A brief whose provider faulted twice
// leaves a row that says one call told nothing and one earlier attempt's
// outcome is unknown — a floor, and never a zero read as exact. A
// consolidation reply cut off at the output limit is refused as a product
// and still spent what it reported, so its row is whole.
func TestAFailedFacilityCallKeepsWhatItObserved(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var faulting atomic.Bool
	faulting.Store(true)
	door := facilityDoor(t, st, func(w http.ResponseWriter, r *http.Request) {
		if faulting.Load() {
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		answering(t, map[string]any{"role": "assistant", "content": `{"operations":[{"op":"upsert","id":"n1","stat`}, "length")(w, r)
	})

	if _, _, err := door.ChatSimple(wire.WithTapSource(t.Context(), "brief"), "s", "u"); err == nil {
		t.Fatal("fixture: the faulting provider answered")
	}
	got := wantFacilityRow(t, st, "facility:brief")
	if u := got.usage; u.Silent != 1 || u.UnknownAttempts != 1 || u.TotalTokens != 0 || u.Complete() {
		t.Fatalf("a failed call's row: %+v, want one silent call beside one unknown attempt, incomplete", u)
	}

	faulting.Store(false)
	tool := llm.ToolDefinition{Type: "function"}
	tool.Function.Name = "commit"
	_, _, _, err = door.ChatStructured(wire.WithTapSource(t.Context(), "consolidate"), "s", "u", tool)
	var incomplete *llm.IncompleteResponseError
	if !errors.As(err, &incomplete) {
		t.Fatalf("fixture: a reply cut off came back as %v", err)
	}
	got = wantFacilityRow(t, st, "facility:consolidate")
	if u := got.usage; u.TotalTokens != 15 || !u.Complete() {
		t.Fatalf("a refused reply's usage was lost: %+v", u)
	}
	if n := facilityRowCount(t, st); n != 2 {
		t.Fatalf("%d facility rows, want one per call", n)
	}
}

// SELF_MODEL calls Chat, not ChatSimple, because it needs its commit tool;
// the door keeps its call the same way, under the name it gives the call.
func TestSelfModelKeepsItsCallAsAFacilityRow(t *testing.T) {
	b, engine := selfModelEvidence(t)
	scripted, _ := selfModelToolLLM{}.Chat(t.Context(), nil, llm.ChatOptions{})
	commit := scripted.Choices[0].Message.ToolCalls[0]
	door := facilityDoor(t, b.st, answering(t, map[string]any{"role": "assistant", "content": "",
		"tool_calls": []any{map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "commit", "arguments": commit.Function.Arguments}}}}, "tool_calls"))
	if err := cognitive.NewSelfModel(b.st, door, selfModelCommitter{engine: engine}).Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if current, err := b.st.CurrentSelfModel(); err != nil || current == nil || current.ID != "syn_app" {
		t.Fatalf("fixture: the pass did not commit: %+v, %v", current, err)
	}
	if n := turnRowCount(t, b.st); n != 1 {
		t.Fatalf("one SELF_MODEL call left %d rows, want 1", n)
	}
	if u := wantFacilityRow(t, b.st, "facility:selfmodel").usage; u.TotalTokens != 15 || !u.Complete() {
		t.Fatalf("SELF_MODEL's usage: %+v", u)
	}
}

// rhythmBlock is the rhythm paragraph of the identity's working state.
func rhythmBlock(t *testing.T, a *App) string {
	t.Helper()
	ws, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(ws, "### Rhythm")
	if i < 0 {
		t.Fatalf("no rhythm line:\n%s", ws)
	}
	block, _, _ := strings.Cut(ws[i:], "\n\n")
	return block
}

// A FACILITY'S CALL IS NOT A TURN. The rhythm line the identity reads about
// itself — turns, calls, read-only share, the batch factor and the
// calibration clause — says exactly what it said before the facilities
// spent anything, however many facility rows now sit beside the turns.
func TestFacilityRowsLeaveTheRhythmLineAsItWas(t *testing.T) {
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC().UnixMilli()
	for _, m := range []store.TurnMetric{
		{TsMs: now - 60000, Calls: 12, ReadOnly: 6, Rounds: 4, Predicted: 10, DeclaredOrdinal: 2, Source: turnSourceOperator,
			Usage: &store.TurnUsage{PromptTokens: 90, CompletionTokens: 10, TotalTokens: 100}, Emissions: &store.ToolCallEmissions{Count: 12, Complete: true}},
		{TsMs: now - 50000, Calls: 3, ReadOnly: 3}, // a row from before sources were kept
	} {
		if err := st.InsertTurnMetric(m); err != nil {
			t.Fatal(err)
		}
	}
	reader := &App{store: st}
	before := rhythmBlock(t, reader)
	if !strings.Contains(before, "2 turns, 15 calls, 60% read-only") || !strings.Contains(before, "3.00 calls per model round") || !strings.Contains(before, "first estimates totaled 10 remaining tool-call requests; 10 were emitted after those declarations") {
		t.Fatalf("fixture: the rhythm line is not exercising its clauses:\n%s", before)
	}

	var faulting atomic.Bool
	door := facilityDoor(t, st, func(w http.ResponseWriter, r *http.Request) {
		if faulting.Load() {
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		answering(t, map[string]any{"role": "assistant", "content": "a noticing"}, "stop")(w, r)
	})
	if _, _, err := door.ChatSimple(wire.WithTapSource(t.Context(), "dream"), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := door.Chat(t.Context(), []llm.Message{{Role: "user", Content: "u"}}, llm.ChatOptions{Source: "selfmodel"}); err != nil {
		t.Fatal(err)
	}
	faulting.Store(true)
	door.ChatSimple(wire.WithTapSource(t.Context(), "brief"), "s", "u")
	if n := facilityRowCount(t, st); n != 3 {
		t.Fatalf("fixture: %d facility rows, want 3", n)
	}
	if after := rhythmBlock(t, reader); after != before {
		t.Fatalf("facility rows changed the identity's rhythm line:\nbefore %s\n after %s", before, after)
	}
}
