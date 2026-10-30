package workrecord

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// recordRig is a store with two sessions, one in a project with a contract,
// graded, with its turn's usage, and the projects beside it.
func recordRig(t *testing.T) (*store.Store, *project.Manager, string) {
	t.Helper()
	s, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	projects := project.NewManager(filepath.Join(t.TempDir(), "projects"))
	p, err := projects.Create("Parser", "a parser", "identity", nil, &project.Contract{Outcome: "it parses", Acceptance: []string{"tests pass"}, Constraints: []string{"no cgo"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveProject(p.ID); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := s.StartWorkSession(ctx, "ws_a", "build the parser"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkSessionFamily(ctx, "ws_a", "parser-build"); err != nil {
		t.Fatal(err)
	}
	two, dur := 2, int64(1400)
	if err := s.RecordToolStart("turn-1", 1, "main", "model-x", "call-1", "shell", `{"command":"go test"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDoneMeasured("turn-1", 1, "shell", `{"command":"go test"}`, "FAIL parser\n[exit status 2]", interaction.Failed, false,
		store.ToolMeasurement{ExitStatus: &two, DurationMS: &dur, ReasonCode: "exit_status"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolStart("turn-1", 2, "main", "model-x", "call-2", "file_read", `{"path":"parser.go"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDoneMeasured("turn-1", 2, "file_read", `{"path":"parser.go"}`, "package parser", interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTurnMetric(store.TurnMetric{TsMs: 1_700_000_000_000, Calls: 2, Rounds: 3, Source: "chat", Provider: "p", Model: "model-x", TurnID: "turn-1",
		Usage: &store.TurnUsage{PromptTokens: 900, CompletionTokens: 100, TotalTokens: 1000, CachedPromptTokens: 500}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverWorkSession(ctx, "ws_a", "partial: half the tests pass", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordConversation(ctx, string(interaction.Operator), "[grade ws_a] partial — the lexer is fine", "", interaction.Details{Grade: &interaction.Grade{Session: "ws_a", Grade: "partial"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveProject(""); err != nil {
		t.Fatal(err)
	}
	if err := s.StartWorkSession(ctx, "ws_b", "write the docs"); err != nil {
		t.Fatal(err)
	}
	return s, projects, p.ID
}

func lines(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatalf("a line is not JSON: %v\n%s", err, l)
		}
		out = append(out, m)
	}
	return out
}

func TestTheExportIsALinePerSessionWithEverythingTheRecordHolds(t *testing.T) {
	s, projects, pid := recordRig(t)
	var buf bytes.Buffer
	n, err := Write(t.Context(), &buf, s, projects, Filter{}, time.Date(2026, 10, 24, 21, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("wrote %d sessions, want 2", n)
	}
	if got, err := Check(bytes.NewReader(buf.Bytes())); err != nil || got != 2 {
		t.Fatalf("the export does not hold to its own shape: %d %v", got, err)
	}
	ls := lines(t, buf.Bytes())
	if len(ls) != 4 || ls[0]["kind"] != "header" || ls[3]["kind"] != "end" {
		t.Fatalf("header, two sessions, end: %d lines", len(ls))
	}
	if ls[0]["schema"] != Schema || !strings.Contains(ls[0]["warning"].(string), "raw") || !strings.Contains(ls[0]["warning"].(string), "secrets") {
		t.Errorf("the header does not say the output is raw and may hold secrets: %v", ls[0])
	}
	a := ls[1]
	sess := a["session"].(map[string]any)
	for k, want := range map[string]any{"id": "ws_a", "description": "build the parser", "status": "delivered", "family": "parser-build", "project_id": pid, "result": "partial: half the tests pass"} {
		if sess[k] != want {
			t.Errorf("session.%s = %v, want %v", k, sess[k], want)
		}
	}
	proj, _ := a["project"].(map[string]any)
	if proj == nil || proj["outcome"] != "it parses" || len(proj["acceptance"].([]any)) != 1 || len(proj["constraints"].([]any)) != 1 {
		t.Errorf("the project and its contract: %v (%v)", a["project"], a["project_unavailable"])
	}
	grades := a["grades"].([]any)
	if len(grades) != 1 || grades[0].(map[string]any)["grade"] != "partial" || !strings.Contains(grades[0].(map[string]any)["text"].(string), "the lexer is fine") {
		t.Errorf("the grade: %v", grades)
	}
	turns := a["turns"].([]any)
	if len(turns) != 1 || turns[0].(map[string]any)["total_tokens"] != float64(1000) || turns[0].(map[string]any)["cached_prompt_tokens"] != float64(500) {
		t.Errorf("the turn's usage: %v", turns)
	}
	calls := a["calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("two calls, got %d", len(calls))
	}
	first := calls[0].(map[string]any)
	for k, want := range map[string]any{"tool": "shell", "actor": "main", "model": "model-x", "provider_call_id": "call-1", "work_session_id": "ws_a", "outcome": "failed",
		"reason_code": "exit_status", "exit_status": float64(2), "duration_ms": float64(1400), "args_excerpt": `{"command":"go test"}`, "result": "FAIL parser\n[exit status 2]",
		"result_cut": false, "ended": true, "turn_id": "turn-1", "ordinal": float64(1)} {
		if first[k] != want {
			t.Errorf("call.%s = %#v, want %#v", k, first[k], want)
		}
	}
	for _, k := range []string{"tex", "args_record", "result_record", "called_at", "ended_at"} {
		if s, _ := first[k].(string); s == "" {
			t.Errorf("call.%s is empty", k)
		}
	}
	if b := ls[2]["session"].(map[string]any); b["id"] != "ws_b" || len(ls[2]["calls"].([]any)) != 0 || ls[2]["project"] != nil {
		t.Errorf("the second session: %v", ls[2])
	}
}

// EVERY CALL OF A SESSION IN THE EXPORT IS A ROW THE DASHBOARD SHOWS, BY tex_
// ID AND sha256 (the design's falsifier C). The dashboard's read of the
// session's result rows (QueryInteractions, the interaction reader) and the
// export name the same executions with the same records, row for row.
func TestTheExportMatchesTheTranscriptRowForRow(t *testing.T) {
	s, projects, _ := recordRig(t)
	var buf bytes.Buffer
	if _, err := Write(t.Context(), &buf, s, projects, Filter{Session: "ws_a"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	page, err := s.QueryInteractions(t.Context(), interaction.Query{Version: interaction.Version, Source: "recorded", Limit: 50,
		Filter: interaction.Filter{WorkSession: "ws_a", Kinds: []interaction.Kind{interaction.ToolResult}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, r := range page.Rows {
		want[r.Source.ID] = r.Details.ResultRecord
	}
	got := map[string]string{}
	for _, c := range lines(t, buf.Bytes())[1]["calls"].([]any) {
		m := c.(map[string]any)
		got[m["tex"].(string)] = m["result_record"].(string)
	}
	if len(want) != 2 || len(got) != len(want) {
		t.Fatalf("the transcript holds %d results of ws_a, the export %d", len(want), len(got))
	}
	for tex, rec := range want {
		if got[tex] != rec {
			t.Errorf("%s: the transcript's record %q, the export's %q", tex, rec, got[tex])
		}
	}
}

// THE FILTERS TAKE WHAT THEY NAME, AND A SESSION THE RECORD DOES NOT HOLD IS
// REFUSED BEFORE ANYTHING IS WRITTEN; ONE THE OTHER FILTERS LEAVE OUT IS
// REFUSED SAYING SO. Removing a filter writes both sessions
// where one is asked for; removing the refusal writes an empty export.
func TestTheExportsFiltersTakeWhatTheyName(t *testing.T) {
	s, projects, pid := recordRig(t)
	count := func(f Filter) int {
		t.Helper()
		var buf bytes.Buffer
		n, err := Write(t.Context(), &buf, s, projects, f, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(Filter{Session: "ws_b"}); n != 1 {
		t.Errorf("-session ws_b: %d sessions", n)
	}
	if n := count(Filter{Project: pid}); n != 1 {
		t.Errorf("-project %s: %d sessions", pid, n)
	}
	if n := count(Filter{Since: time.Now().Add(time.Hour)}); n != 0 {
		t.Errorf("-since an hour ahead: %d sessions", n)
	}
	if n := count(Filter{Since: time.Now().Add(-time.Hour)}); n != 2 {
		t.Errorf("-since an hour ago: %d sessions", n)
	}
	var buf bytes.Buffer
	_, err := Write(t.Context(), &buf, s, projects, Filter{Session: "ws_nope"}, time.Now())
	var unknown *UnknownSessionError
	if !errors.As(err, &unknown) || unknown.ID != "ws_nope" || buf.Len() != 0 {
		t.Errorf("a session the record does not hold: %v, %d bytes written", err, buf.Len())
	}
	_, err = Write(t.Context(), &buf, s, projects, Filter{Session: "ws_a", Since: time.Now().Add(time.Hour)}, time.Now())
	var outside *OutsideFiltersError
	if !errors.As(err, &outside) || outside.ID != "ws_a" || buf.Len() != 0 {
		t.Errorf("a session the record holds, outside -since: %v, %d bytes written", err, buf.Len())
	}
}

// AN EXPORT THAT DID NOT ARRIVE WHOLE IS NOT TAKEN FOR ONE. Without its end
// line, with an end line that counts other than what came, with a line of
// another schema, or without its header first, the checker refuses it.
func TestAnExportThatDidNotArriveWholeIsRefused(t *testing.T) {
	s, projects, _ := recordRig(t)
	var buf bytes.Buffer
	if _, err := Write(t.Context(), &buf, s, projects, Filter{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	all := strings.SplitAfter(buf.String(), "\n")
	for name, raw := range map[string]string{
		"no end line":      strings.Join(all[:len(all)-2], ""),
		"a miscount":       strings.Join(all[:2], "") + strings.Join(all[3:], ""),
		"another schema":   strings.Replace(buf.String(), Schema, "aii.work-record/2", 1),
		"no header first":  strings.Join(all[1:], ""),
		"a line after end": buf.String() + all[1],
	} {
		if _, err := Check(strings.NewReader(raw)); !errors.Is(err, ErrIncomplete) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
