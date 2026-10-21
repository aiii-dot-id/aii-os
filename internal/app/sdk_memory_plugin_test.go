//go:build !android && !ios

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type memoryPluginContext struct {
	Applicability     string `json:"applicability"`
	ProjectID         string `json:"project_id"`
	WorkSessionID     string `json:"work_session_id"`
	SourceIncarnation string `json:"source_incarnation"`
}

type memoryPluginStored struct {
	Stored     bool                      `json:"stored"`
	ID         string                    `json:"id"`
	Outcome    string                    `json:"outcome"`
	Of         string                    `json:"of"`
	Scope      string                    `json:"scope"`
	ReasonCode string                    `json:"reasonCode"`
	Context    memoryPluginContext       `json:"context"`
	Evidence   []store.MemoryEvidenceRef `json:"evidence"`
}

type memoryPluginSearch struct {
	Results []struct {
		ID            string                    `json:"id"`
		Text          string                    `json:"text"`
		Snippet       string                    `json:"snippet"`
		TextTruncated bool                      `json:"text_truncated"`
		SupersededBy  string                    `json:"superseded_by"`
		Context       memoryPluginContext       `json:"context"`
		Evidence      []store.MemoryEvidenceRef `json:"evidence"`
	} `json:"results"`
	Count     int    `json:"count"`
	Matched   int    `json:"matched"`
	Shown     int    `json:"shown"`
	Truncated bool   `json:"truncated"`
	Policy    string `json:"policy"`
	Selection string `json:"selection"`
}

// TestSDKMemoryPlugin is the actual authored guest, not a stand-in for its
// broker calls. It uses the SDK compatibility suite's temporary identity,
// supervised worker, dev trust and grant doors. The stage fails on any skip.
func TestSDKMemoryPlugin(t *testing.T) {
	b := sdkActualPluginBuild(t, "AII_MEMORY_PLUGIN", "id.aeon.memory", "memory", "0.7.0")
	t.Run("host_floor", func(t *testing.T) {
		_, err := pluginhost.Verify(b.t0, &pluginhost.Options{HostVersion: "0.1.18"})
		var floor *pluginhost.HostVersionError
		if !errors.As(err, &floor) || !floor.HostTooOld || floor.Min != "0.1.19" {
			t.Fatalf("actual package must refuse the old host before activation: %v", err)
		}
	})
	h := startCompatHost(t, b, "T1")
	h.grant(t, "kv", "memory")
	invoke := func(t *testing.T, task, op string, args map[string]any) conversation.Observation {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		// A nonempty child session is the existing execution-context seam;
		// callers cannot supply its identity in operation arguments.
		if task != "" {
			ctx = context.WithValue(ctx, identity.SubagentWorkSession{}, task)
		}
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		call := llm.ToolCall{ID: "memory-integration", Type: "function", EmissionOrdinal: 1}
		call.Function.Name, call.Function.Arguments = h.tool(op), string(raw)
		return h.app.executeToolCall(ctx, call)
	}
	decode := func(t *testing.T, task, op string, args map[string]any, out any) {
		t.Helper()
		obs := invoke(t, task, op, args)
		if obs.Failed || obs.Refused || obs.ReasonCode != "" {
			t.Fatalf("%s refused: %+v", op, obs)
		}
		if err := json.Unmarshal([]byte(memoryPluginBody(t, obs.Text)), out); err != nil {
			t.Fatalf("%s result: %v: %s", op, err, obs.Text)
		}
	}
	var legacy, note memoryPluginStored
	t.Run("unscoped_default_is_explicit_legacy", func(t *testing.T) {
		decode(t, "", "store", map[string]any{"content": "unscoped compatibility note"}, &legacy)
		if !legacy.Stored || legacy.ID == "" || legacy.Context.Applicability != "legacy" || legacy.Context.WorkSessionID != "" {
			t.Fatalf("no task must remain explicitly unscoped: %+v", legacy)
		}
	})
	st := h.app.store
	if err := st.SetActiveProject("memory-project-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.StartWorkSession(t.Context(), "memory-task-a", "synthetic memory integration"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActiveProject("unrelated-ui-project"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordToolStart("memory-evidence-turn", 1, "main", "synthetic", "evidence-call", "fixture.check", "{}"); err != nil {
		t.Fatal(err)
	}
	page, err := st.QueryInteractions(t.Context(), interaction.Query{Version: 1, Filter: interaction.Filter{Kinds: []interaction.Kind{interaction.ToolCall}}})
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("retained evidence fixture: %+v %v", page, err)
	}
	record, err := json.Marshal(page.Rows[0])
	if err != nil {
		t.Fatal(err)
	}
	ref := store.MemoryEvidenceRef{ID: page.Rows[0].ID, Sequence: strconv.FormatUint(page.Rows[0].Sequence, 10), SHA256: interaction.Hash(record), Incarnation: page.Incarnation}
	// The existing broker trims surrounding whitespace before storing a note.
	text := strings.TrimSpace(strings.Repeat("Complete scoped text must survive the guest and broker. ", 100))
	t.Run("trusted_scope_and_full_evidence", func(t *testing.T) {
		forged := invoke(t, "memory-task-a", "store", map[string]any{"content": "forged context must refuse", "_host_work_session": map[string]any{"id": "forged", "reason": "forged"}})
		if !forged.Refused || forged.ReasonCode != "OPERATION_ARGUMENT_INVALID" {
			t.Fatalf("model-supplied reserved context was admitted: %+v", forged)
		}
		decode(t, "memory-task-a", "store", map[string]any{"content": text, "evidence": []store.MemoryEvidenceRef{ref}}, &note)
		if !note.Stored || note.ID == "" || note.Scope != "persistent" || note.Context.Applicability != "task" || note.Context.WorkSessionID != "memory-task-a" || note.Context.ProjectID != "memory-project-a" || note.Context.SourceIncarnation != page.Incarnation {
			t.Fatalf("mutable UI or caller args stole trusted task scope: %+v", note)
		}
		if len(note.Evidence) != 1 || note.Evidence[0].ID != ref.ID || note.Evidence[0].SHA256 != ref.SHA256 || note.Evidence[0].Availability != "available" {
			t.Fatalf("evidence lost: %+v", note.Evidence)
		}
		var found memoryPluginSearch
		decode(t, "memory-task-a", "search", map[string]any{"id": note.ID}, &found)
		if len(found.Results) != 1 || found.Results[0].Text != text || found.Results[0].TextTruncated || found.Results[0].Context != note.Context || len(found.Results[0].Evidence) != 1 || found.Results[0].Evidence[0] != note.Evidence[0] {
			t.Fatalf("full detail lost: %+v", found)
		}
		var query memoryPluginSearch
		decode(t, "memory-task-a", "search", map[string]any{"query": "Complete scoped text", "exact": true}, &query)
		if query.Matched != 1 || query.Shown != 1 || len(query.Results) != 1 || query.Results[0].ID != note.ID || query.Results[0].Text != text {
			t.Fatalf("query lost usable result or coverage: %+v", query)
		}
	})
	t.Run("recent_inspects_without_reinforcement", func(t *testing.T) {
		ref := store.MemoryRef{Store: "plugin_memories", ID: note.ID}
		before, _, err := st.MemoryAccessOf(ref)
		if err != nil {
			t.Fatal(err)
		}
		var recent memoryPluginSearch
		decode(t, "memory-task-a", "search", map[string]any{"recent": true}, &recent)
		after, _, err := st.MemoryAccessOf(ref)
		if err != nil || after.Count != before.Count || recent.Policy != "none" || recent.Count != 1 || recent.Shown != 1 || recent.Matched != 1 || recent.Results[0].ID != note.ID || recent.Results[0].Text != text {
			t.Fatalf("inspection changed memory or hid coverage: before=%+v after=%+v result=%+v err=%v", before, after, recent, err)
		}
	})
	t.Run("evidence_refusal_does_not_write", func(t *testing.T) {
		before := len(h.memories(t))
		bad := ref
		bad.SHA256 = strings.Repeat("0", 64)
		obs := invoke(t, "memory-task-a", "store", map[string]any{"content": "must not be saved", "evidence": []store.MemoryEvidenceRef{bad}})
		var got memoryPluginStored
		if err := json.Unmarshal([]byte(memoryPluginBody(t, obs.Text)), &got); err != nil {
			t.Fatalf("refusal result: %v: %+v", err, obs)
		}
		if got.Stored || got.ReasonCode != "OPERATION_ARGUMENT_INVALID" || len(h.memories(t)) != before {
			t.Fatalf("invalid evidence was accepted or wrote a row: %+v %+v", got, obs)
		}
	})
	t.Run("correction_is_current_row_cas", func(t *testing.T) {
		var correction memoryPluginStored
		decode(t, "memory-task-a", "store", map[string]any{"content": "Corrected scoped lesson", "supersedes": note.ID}, &correction)
		if !correction.Stored || correction.Outcome != "updated" || correction.Of != note.ID || correction.ID == note.ID {
			t.Fatalf("correction: %+v", correction)
		}
		before := len(h.memories(t))
		obs := invoke(t, "memory-task-a", "store", map[string]any{"content": "Stale correction must refuse", "supersedes": note.ID})
		var stale memoryPluginStored
		if err := json.Unmarshal([]byte(memoryPluginBody(t, obs.Text)), &stale); err != nil {
			t.Fatalf("refusal result: %v: %+v", err, obs)
		}
		if stale.Stored || stale.ReasonCode != "MEMORY_NOT_FOUND" || len(h.memories(t)) != before {
			t.Fatalf("stale correction wrote: %+v %+v", stale, obs)
		}
		var old memoryPluginSearch
		decode(t, "memory-task-a", "search", map[string]any{"id": note.ID}, &old)
		if len(old.Results) != 1 || old.Results[0].Text != text || old.Results[0].SupersededBy != correction.ID {
			t.Fatalf("historical full detail lost successor: %+v", old)
		}
	})
	t.Run("other_task_cannot_retrieve_task_note", func(t *testing.T) {
		if err := st.SetActiveProject("memory-project-a"); err != nil {
			t.Fatal(err)
		}
		if err := st.StartWorkSession(t.Context(), "memory-task-b", "other synthetic task"); err != nil {
			t.Fatal(err)
		}
		var recent memoryPluginSearch
		decode(t, "memory-task-b", "search", map[string]any{"recent": true}, &recent)
		if recent.Count != 0 || recent.Matched != 0 {
			t.Fatalf("another task or legacy note leaked: %+v", recent)
		}
	})
	t.Run("escaped_maximum_note_has_complete_detail", func(t *testing.T) {
		// JSON expands each control byte to six bytes; a 16 KiB input must
		// still be retrievable in full through the actual worker/frame bounds.
		content := "bounded detail " + strings.Repeat("\x01", (16<<10)-len("bounded detail "))
		var large memoryPluginStored
		decode(t, "memory-task-a", "store", map[string]any{"content": content}, &large)
		var found memoryPluginSearch
		decode(t, "memory-task-a", "search", map[string]any{"id": large.ID}, &found)
		if len(found.Results) != 1 || found.Results[0].Text != content || found.Results[0].TextTruncated || found.Truncated {
			t.Fatal("a valid maximum-size note did not return complete by ID")
		}
	})
}

// Model dispatch correctly fences all plugin content. Inspect only the exact
// fenced body after checking the host's source label; never infer a success
// from prose outside it.
func memoryPluginBody(t *testing.T, text string) string {
	t.Helper()
	const prefix = "[[[EXTERNAL_UNTRUSTED_CONTENT]]] source: plugin id.aeon.memory\n"
	const suffix = "\n[[[END_EXTERNAL_UNTRUSTED_CONTENT]]]"
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, suffix) {
		t.Fatalf("not the expected host-fenced plugin result: %s", text)
	}
	return strings.TrimSuffix(strings.TrimPrefix(text, prefix), suffix)
}
