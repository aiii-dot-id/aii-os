package broker

import (
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"strconv"
	"strings"
	"testing"
)

func TestMemoryRecentDoesNotReinforceAndScopeDescribesStoredRow(t *testing.T) {
	h, b := memHost(t, Config{})
	memoryTask(t, b, "task-inspect", "project-inspect")
	note := remember(t, b, `{"text":"bounded inspection note","applicability":"task"}`)
	ref := store.MemoryRef{Store: "plugin_memories", ID: note.ID}
	before, _, err := h.cfg.Store.MemoryAccessOf(ref)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		res := recall(t, b, `{"recent":true,"selection":"task"}`)
		if len(res.Hits) != 1 {
			t.Fatal(res)
		}
	}
	after, _, err := h.cfg.Store.MemoryAccessOf(ref)
	if err != nil || after.Count != before.Count {
		t.Fatal("inspection reinforced", before, after, err)
	}
	recall(t, b, fmt.Sprintf(`{"id":%q,"selection":"task"}`, note.ID))
	after, _, err = h.cfg.Store.MemoryAccessOf(ref)
	if err != nil || after.Count != before.Count+1 {
		t.Fatal("detail did not reinforce", before, after, err)
	}
	temporary := h.Bind("p", packagefmt.TierT0, []string{"ring4.memory"})
	setMemoryTask(temporary, "task-inspect")
	reused := remember(t, temporary, `{"text":"bounded inspection note","applicability":"task"}`)
	if reused.ID != note.ID || reused.Outcome != "reinforced" || reused.Scope != "persistent" {
		t.Fatal("scope describes caller rather than retained row", reused)
	}
}

func memoryTask(t *testing.T, b *Binding, session, project string) {
	t.Helper()
	st := b.host.cfg.Store
	if err := st.SetActiveProject(project); err != nil {
		t.Fatal(err)
	}
	if err := st.StartWorkSession(t.Context(), session, "synthetic work"); err != nil {
		t.Fatal(err)
	}
	setMemoryTask(b, session)
}
func setMemoryTask(b *Binding, session string) {
	b.BeginOperation(OperationScope{Operation: "store", WorkSession: session, Declared: true, Capabilities: []string{"ring4.memory"}})
}

func TestMemoryScopedRetrievalAndExactCorrections(t *testing.T) {
	h, b := memHost(t, Config{})
	legacy := remember(t, b, `{"text":"compiler test recipe"}`)
	memoryTask(t, b, "task-a", "project-a")
	private := remember(t, b, `{"text":"compiler test recipe","applicability":"task"}`)
	shared := remember(t, b, `{"text":"compiler test recipe shared","applicability":"project"}`)
	reusable := remember(t, b, `{"text":"compiler test recipe portable","applicability":"reusable"}`)
	if err := h.cfg.Store.SetActiveProject("unrelated-ui-project"); err != nil {
		t.Fatal(err)
	}
	r := remember(t, b, `{"text":"run compiler test checks","applicability":"task"}`)
	m, found, err := h.cfg.Store.PluginMemoryGet(b.storageScope(), r.ID)
	if err != nil || !found || m.Project != "project-a" || m.WorkSession != "task-a" {
		t.Fatalf("UI project stole executing context: %+v %v", m, err)
	}
	again := remember(t, b, `{"text":"run compiler test checks","applicability":"task"}`)
	if again.ID != r.ID || again.Outcome != "reinforced" {
		t.Fatal("exact equivalent not reinforced", again)
	}
	opposite := remember(t, b, `{"text":"do not run compiler test checks","applicability":"task"}`)
	if opposite.ID == r.ID || opposite.Outcome != "created" {
		t.Fatal("similar opposite replaced original", opposite)
	}
	memoryTask(t, b, "task-b", "project-a")
	res := recall(t, b, `{"query":"compiler test recipe","selection":"relevant","exact":true}`)
	seen := map[string]bool{}
	for _, hit := range res.Hits {
		seen[hit.ID] = true
	}
	if seen[private.ID] || seen[legacy.ID] || !seen[shared.ID] || !seen[reusable.ID] {
		t.Fatal("selection leaked another task or lost explicit shared notes", seen)
	}
	wantResult(t, dispatch(t, b, memParams(opMemoryRecall, fmt.Sprintf(`{"id":%q,"selection":"relevant"}`, private.ID))), statusFailed, reasonMemoryNotFound)
	wantResult(t, dispatch(t, b, memParams(opMemoryRemember, fmt.Sprintf(`{"text":"replace other task","applicability":"task","supersedes":%q}`, private.ID))), statusFailed, reasonMemoryNotFound)
	memoryTask(t, b, "task-c", "project-c")
	res = recall(t, b, `{"recent":true,"selection":"relevant"}`)
	if len(res.Hits) != 1 || res.Hits[0].ID != reusable.ID {
		t.Fatal("recent crossed project/task boundary", res)
	}
	res = recall(t, b, `{"recent":true,"selection":"legacy"}`)
	if len(res.Hits) != 1 || res.Hits[0].ID != legacy.ID {
		t.Fatal("legacy did not remain explicit", res)
	}
	setMemoryTask(b, "task-a")
	corrected := remember(t, b, fmt.Sprintf(`{"text":"correct recipe","applicability":"task","supersedes":%q}`, private.ID))
	wantResult(t, dispatch(t, b, memParams(opMemoryRemember, fmt.Sprintf(`{"text":"stale recipe","applicability":"task","supersedes":%q}`, private.ID))), statusFailed, reasonMemoryNotFound)
	raw := dispatch(t, b, memParams(opMemoryRecall, fmt.Sprintf(`{"id":%q,"selection":"task"}`, private.ID)))
	if !strings.Contains(string(raw["operation_result"]), corrected.ID) {
		t.Fatal("historical detail hid successor", raw)
	}
}

func TestMemoryMissingContextAndSelectorValidation(t *testing.T) {
	_, b := memHost(t, Config{})
	for _, args := range []string{`{"text":"x","applicability":"task"}`, `{"text":"x","applicability":"project"}`, `{"text":"x","applicability":"reusable"}`, `{"text":"x","applicability":"relevant"}`, `{"text":"x","applicability":"task","work_session_id":"forged"}`} {
		wantResult(t, dispatch(t, b, memParams(opMemoryRemember, args)), statusDenied, reasonArgumentInvalid)
	}
	for _, args := range []string{`{"query":"x","recent":true}`, `{"query":"x","id":"p"}`, `{"recent":true,"selection":"task"}`, `{"recent":true,"selection":"alien"}`, `{"recent":true,"exact":true}`, `{"recent":true,"limit":51}`, `{"id":"x","since":"2026-01-01T00:00:00Z"}`} {
		wantResult(t, dispatch(t, b, memParams(opMemoryRecall, args)), statusDenied, reasonArgumentInvalid)
	}
	setMemoryTask(b, "missing")
	wantResult(t, dispatch(t, b, memParams(opMemoryRemember, `{"text":"x","applicability":"task"}`)), statusDenied, reasonArgumentInvalid)
}

func TestMemoryExplicitLegacyRetainsLegacyConsolidation(t *testing.T) {
	_, b := memHost(t, Config{})
	first := remember(t, b, `{"text":"The operator prefers concise replies with clear examples","applicability":"legacy"}`)
	again := remember(t, b, `{"text":"The operator prefers concise replies with clear examples.","applicability":"legacy"}`)
	if again.ID != first.ID || again.Outcome != "reinforced" {
		t.Fatal("explicit legacy lost legacy behavior", first, again)
	}
}

func TestMemoryLargeRecallFitsBrokerFrameWithHonestCoverage(t *testing.T) {
	h, b := memHost(t, Config{})
	for i := 0; i < 50; i++ {
		if err := h.cfg.Store.PluginMemoryAdd(b.storageScope(), store.PluginMemory{ID: fmt.Sprintf("large-%02d", i), Text: strings.Repeat("x", 16<<10)}, 100, ""); err != nil {
			t.Fatal(err)
		}
	}
	out := dispatch(t, b, memParams(opMemoryRecall, `{"recent":true,"limit":50,"selection":"legacy"}`))
	wantResult(t, out, statusSucceeded, "")
	raw := out["operation_result"]
	if len(raw) > memoryRecallResultBytes {
		t.Fatal("result crossed bounded transport", len(raw))
	}
	var result recallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Matched != 50 || result.Shown != len(result.Hits) || result.Shown == 0 || result.Shown >= 50 || !result.Truncated || result.Status != "partial" {
		t.Fatal("coverage concealed response bound", result)
	}
	for _, hit := range result.Hits {
		if len(hit.Text) != 16<<10 {
			t.Fatal("partial hit was called complete")
		}
	}
	// A ranked query has the same frame limit; only the transmitted prefix
	// is reinforced, never all fifty candidates selected before serialization.
	args, _ := json.Marshal(map[string]any{"query": strings.Repeat("x", 16<<10), "exact": true, "limit": 50, "selection": "legacy"})
	out = dispatch(t, b, memParams(opMemoryRecall, string(args)))
	wantResult(t, out, statusSucceeded, "")
	if err := json.Unmarshal(out["operation_result"], &result); err != nil {
		t.Fatal(err)
	}
	if result.Shown == 0 || result.Shown >= 50 || !result.Truncated {
		t.Fatal("query did not exercise the result bound")
	}
	shown := map[string]bool{}
	for _, hit := range result.Hits {
		shown[hit.ID] = true
	}
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("large-%02d", i)
		access, _, err := h.cfg.Store.MemoryAccessOf(store.MemoryRef{Store: "plugin_memories", ID: id})
		if err != nil {
			t.Fatal(err)
		}
		want := int64(0)
		if shown[id] {
			want = 1
		}
		if access.Count != want {
			t.Fatalf("access for %s = %d, shown=%v", id, access.Count, shown[id])
		}
	}
}

func TestMemoryEvidenceBindsExactRetainedRecord(t *testing.T) {
	h, b := memHost(t, Config{})
	memoryTask(t, b, "task-evidence", "project-evidence")
	st := h.cfg.Store
	err := st.RecordToolStart("fixture-turn", 1, "main", "synthetic", "call", "fixture.check", "{}")
	if err != nil {
		t.Fatal(err)
	}
	page, err := st.QueryInteractions(t.Context(), interaction.Query{Version: 1, Filter: interaction.Filter{Kinds: []interaction.Kind{interaction.ToolCall}}})
	if err != nil || len(page.Rows) != 1 {
		t.Fatalf("fixture: %+v %v", page, err)
	}
	raw, _ := json.Marshal(page.Rows[0])
	ref := store.MemoryEvidenceRef{ID: page.Rows[0].ID, Sequence: strconv.FormatUint(page.Rows[0].Sequence, 10), SHA256: interaction.Hash(raw), Incarnation: page.Incarnation}
	args := func(r store.MemoryEvidenceRef) string {
		raw, _ := json.Marshal(map[string]any{"text": "validated method", "applicability": "task", "evidence": []store.MemoryEvidenceRef{r}})
		return string(raw)
	}
	valid := remember(t, b, args(ref))
	for _, alter := range []func(*store.MemoryEvidenceRef){func(r *store.MemoryEvidenceRef) { r.Incarnation = "foreign" }, func(r *store.MemoryEvidenceRef) { r.Sequence = "01" }, func(r *store.MemoryEvidenceRef) { r.SHA256 = strings.Repeat("0", 64) }, func(r *store.MemoryEvidenceRef) { r.ID = "unavailable" }, func(r *store.MemoryEvidenceRef) { r.Availability = "available" }} {
		bad := ref
		alter(&bad)
		wantResult(t, dispatch(t, b, memParams(opMemoryRemember, args(bad))), statusDenied, reasonArgumentInvalid)
	}
	out := dispatch(t, b, memParams(opMemoryRecall, fmt.Sprintf(`{"id":%q,"selection":"task"}`, valid.ID)))
	if !strings.Contains(string(out["operation_result"]), `"availability":"available"`) {
		t.Fatal("evidence status absent", out)
	}
	count, err := st.PluginMemoryCount(b.storageScope())
	if err != nil || count != 1 {
		t.Fatal("rejected references wrote memory", count, err)
	}
}
