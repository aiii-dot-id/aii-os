package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The exact-ID inspection read uses source=standing, id and query="".
// Every read here validates the registered schema before calling its handler.
// Unknown IDs report absence; read failures never certify a standing.
func TestRecallBeliefInspectRendersTheEvaluatorsReport(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "commit",
		map[string]any{
			"variant": "belief.upsert", "id": "b_probe",
			"statement": "the inspection read exists", "confidence": 0.5,
			"evidence_refs": "exp_test_origin",
		}); err != nil {
		t.Fatalf("belief.upsert setup: %v", err)
	}
	if got := mustStanding(t, st, "b_probe"); got != "new" {
		t.Fatalf("standing = %q, want new (1 source is below the gate)", got)
	}

	out, err := callAdvertisedRecall(t, engine, ctx,
		map[string]any{"source": "standing", "id": "b_probe", "query": ""})
	if err != nil {
		t.Fatalf("recall belief inspect: %v", err)
	}
	for _, want := range []string{
		"Belief b_probe [ring 3, new, eligible false]",
		"Read at ledger head",
		"the inspection read exists",
		"Qualifying evidence: 1 edge(s) from 1 distinct source(s)",
		"exp_test_origin",
		"Contradiction: none.",
		"Missing for Ring 2: 2 more distinct source(s) (has 1; the gate asks 3)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report must say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "eligibility unavailable") {
		t.Errorf("a successful read must not render unavailable:\n%s", out)
	}
	// Whose words a source holds is shown beside it and asked of nobody:
	// the read names no class of author and no voice to go and find.
	for _, gone := range []string{"Authorship", "authorship", "independent voice"} {
		if strings.Contains(out, gone) {
			t.Errorf("the report must not speak of %q:\n%s", gone, out)
		}
	}
}

// Three distinct sources meet the gate; promotion is still the
// resident's conscious act, not this read's.
func TestRecallBeliefInspectNamesTheMetGate(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if err := engine.RecordConversationTurn("operator", "it held up"); err != nil {
		t.Fatal(err)
	}
	opTurn, err := st.GetLatestOperatorTurn()
	if err != nil || opTurn == nil {
		t.Fatalf("operator turn: %v %v", opTurn, err)
	}
	if _, err := engine.ExecuteAction(ctx, "verb", "commit",
		map[string]any{
			"variant": "belief.upsert", "id": "b_met",
			"statement": "the gate is met", "confidence": 0.7,
			"evidence_refs": "exp_test_origin",
		}); err != nil {
		t.Fatalf("belief.upsert setup: %v", err)
	}
	for i, note := range []map[string]any{
		{"content": "I watched it hold", "supports": "b_met"},
		{"content": "the operator confirmed it", "source_turn": opTurn.TurnSeq, "supports": "b_met"},
		{"content": "second observation of it holding", "supports": "b_met"},
	} {
		if _, err := engine.ExecuteAction(ctx, "verb", "note", note); err != nil {
			t.Fatalf("note %d: %v", i, err)
		}
	}
	if got := mustStanding(t, st, "b_met"); got != "confirmed" {
		t.Fatalf("standing = %q, want confirmed", got)
	}

	out, err := callAdvertisedRecall(t, engine, ctx,
		map[string]any{"source": "standing", "id": "b_met", "query": ""})
	if err != nil {
		t.Fatalf("recall belief inspect: %v", err)
	}
	for _, want := range []string{
		"Belief b_met [ring 3, confirmed, eligible true]",
		"Ring 2 requirements met; promotion is the resident's conscious act, not this read's.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report must say %q:\n%s", want, out)
		}
	}
}

func TestRecallBeliefUnknownIDReportsAbsence(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	out, err := callAdvertisedRecall(t, engine, context.Background(),
		map[string]any{"source": "standing", "id": "b_nosuch", "query": ""})
	if err != nil {
		t.Fatalf("unknown ID inspection: %v", err)
	}
	if out != `No belief has id "b_nosuch".` {
		t.Fatalf("unknown ID must report absence, not a listing or verdict: %s", out)
	}
}

// The promotion refusal names counts from the same complete assessment.
func TestPromoteRefusalNamesTheCounts(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "commit",
		map[string]any{
			"variant": "belief.upsert", "id": "b_unearned",
			"statement": "unsupported", "confidence": 0.5,
			"evidence_refs": "exp_test_origin",
		}); err != nil {
		t.Fatalf("belief.upsert setup: %v", err)
	}
	_, err := engine.ExecuteAction(ctx, "verb", "commit",
		map[string]any{"variant": "belief.promote", "id": "b_unearned", "ring": 2})
	if err == nil {
		t.Fatal("promote without confirmed standing must be refused")
	}
	for _, want := range []string{
		"refused",
		`"b_unearned" has standing "new"`,
		"1 distinct qualifying source(s)",
		"at least 3 distinct",
		"no live contradiction",
		"Ring 2 requires confirmed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %q:\n%v", want, err)
		}
	}
}

func TestRecallBeliefInspectFailsClosed(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := context.Background()
	if _, err := engine.ExecuteAction(ctx, "verb", "commit",
		map[string]any{
			"variant": "belief.upsert", "id": "b_dark",
			"statement": "the graph will fail to read", "confidence": 0.5,
			"evidence_refs": "exp_test_origin",
		}); err != nil {
		t.Fatalf("belief.upsert setup: %v", err)
	}
	st, ok := engine.store.(*store.Store)
	if !ok {
		t.Fatalf("engine store is %T, want *store.Store for the fail-closed probe", engine.store)
	}
	if _, err := st.DB().Exec(`ALTER TABLE experiences RENAME TO experiences_gone`); err != nil {
		t.Fatalf("rename experiences: %v", err)
	}
	defer st.DB().Exec(`ALTER TABLE experiences_gone RENAME TO experiences`)

	out, err := callAdvertisedRecall(t, engine, ctx,
		map[string]any{"source": "standing", "id": "b_dark", "query": ""})
	if err != nil {
		t.Fatalf("a read failure renders unavailable, it does not error: %v", err)
	}
	for _, want := range []string{
		"Belief b_dark — eligibility unavailable",
		"This is a read failure, not a standing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the fail-closed render must say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "standing new") || strings.Contains(out, "eligible false") {
		t.Errorf("a read failure must not render a standing or eligibility:\n%s", out)
	}
	// A query must not bypass required metadata reads or change their failure.
	for _, query := range []string{"foobar", "anything at all", "  unrelated query  "} {
		got, err := callAdvertisedRecall(t, engine, ctx,
			map[string]any{"source": "standing", "id": "b_dark", "query": query})
		if err != nil || got != out {
			t.Fatalf("query %q changed the required-read failure: err=%v\nwant: %s\ngot: %s", query, err, out, got)
		}
	}
}

// Use the actual registered wire schema and handler, not ExecuteAction's
// internal bypass. Round-trip through JSON as a provider does.
func callAdvertisedRecall(t *testing.T, e *Engine, ctx context.Context, args map[string]any) (string, error) {
	t.Helper()
	for _, v := range verbRegistry {
		if v.Name != "recall" {
			continue
		}
		data, err := json.Marshal(v.Params)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		schema, err := jsonschema.Compile(raw)
		if err != nil {
			t.Fatal(err)
		}
		data, err = json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(wire); err != nil {
			return "", err
		}
		return v.Handler(e, ctx, wire)
	}
	t.Fatal("recall is not registered")
	return "", nil
}

func TestRecallStandingAdvertisedContractRefusesInvalidCalls(t *testing.T) {
	e, _, _, _, _ := setupEngine(t)
	for _, args := range []map[string]any{
		{"source": "beliefs", "query": "b_probe", "after_seq": 3}, // read ranked and whole: no sequence to continue from
		{"source": "intentions", "query": ""},                     // a ranked source needs a cue
		{"source": "standing", "id": "b_probe"},                   // query is explicitly required
		{"source": "standing", "query": ""},
		{"source": "standing", "id": "b_probe", "query": "", "after_seq": 3},
		{"source": "standing", "id": "b_probe", "query": "", "after_edge": "edge"},
		{"source": "standing", "id": "b_probe", "query": "", "after_seq": -1, "after_edge": "edge"},
		{"source": "standing", "id": "b_probe", "query": "", "after_seq": 1.5, "after_edge": "edge"},
	} {
		if out, err := callAdvertisedRecall(t, e, t.Context(), args); err == nil {
			t.Fatalf("invalid call accepted: %v -> %s", args, out)
		}
	}
}

func TestRecallStandingPrintedCursorWalksTiedOriginsWithoutWrites(t *testing.T) {
	e, st, lg, kp, _ := setupEngine(t)
	refs := make([]string, 15)
	for i := range refs {
		refs[i] = fmt.Sprintf("inspect_origin_%02d", i)
		payload := map[string]any{"id": refs[i], "content": refs[i], "provenance": "self"}
		if i == 0 {
			payload["provenance"] = "external"
			payload["source_url"] = "https://example.org/inspection-test"
		}
		evt, err := lg.Append(ledger.EventExperienceCreate, 3, payload, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.ExecuteAction(t.Context(), "verb", "commit", map[string]any{
		"variant": "belief.upsert", "id": "b_walk", "statement": "inspection preserves the entire assessment",
		"evidence_refs": strings.Join(refs, ","),
	}); err != nil {
		t.Fatal(err)
	}
	base, err := st.StandingReportFor("b_walk")
	if err != nil || base.DistinctSources != 15 || !base.Eligible {
		t.Fatalf("fixture assessment: %+v %v", base, err)
	}
	edges, err := st.ListEdgesForBelief("b_walk")
	if err != nil || len(edges) != 15 {
		t.Fatalf("fixture edges: %v %v", edges, err)
	}
	want := map[string]bool{}
	for _, edge := range edges {
		if edge.CreatedSeq != edges[0].CreatedSeq {
			t.Fatal("fixture origins must share a sequence")
		}
		want[edge.ID] = true
	}
	head, hash := lg.LastSeq(), lg.LastHash()
	seen := map[string]bool{}
	lineRE := regexp.MustCompile(`(?m)^    \[([^,]+), seq (\d+), experience, from ([^\]]+)\] provenance (\w+)$`)
	cursorRE := regexp.MustCompile(`continue with source=standing, id=("[^"]+"), query="", after_seq=(\d+), after_edge=("[^"]+")`)
	args := map[string]any{"source": "standing", "id": "b_walk", "query": ""}
	pages := 0
	for {
		pages++
		if pages > 3 {
			t.Fatal("printed cursor did not terminate")
		}
		out, err := callAdvertisedRecall(t, e, t.Context(), args)
		if err != nil {
			t.Fatal(err)
		}
		// Shared-schema query is ignored: compare the whole response, including
		// assessment, counts, metadata disclosure, detail and printed cursor,
		// at every page. The final head/hash assertions cover all these reads.
		for _, query := range []string{"foobar", "anything at all", "  unrelated query  "} {
			varied := make(map[string]any, len(args))
			for key, value := range args {
				varied[key] = value
			}
			varied["query"] = query
			got, err := callAdvertisedRecall(t, e, t.Context(), varied)
			if err != nil || got != out {
				t.Fatalf("page %d query %q changed inspection: err=%v\nwant: %s\ngot: %s", pages, query, err, out, got)
			}
		}
		for _, text := range []string{
			"Belief b_walk [ring 3, confirmed, eligible true]",
			fmt.Sprintf("Read at ledger head %d,", base.ReadHeadSeq),
			"Qualifying evidence: 15 edge(s) from 15 distinct source(s)",
			"Contradiction: none.",
			"Ring 2 requirements met;",
		} {
			if !strings.Contains(out, text) {
				t.Fatalf("page %d assessment missing %q:\n%s", pages, text, out)
			}
		}
		lines := lineRE.FindAllStringSubmatch(out, -1)
		if len(lines) == 0 || len(lines) > store.StandingDetailMax {
			t.Fatalf("page %d has %d detail lines:\n%s", pages, len(lines), out)
		}
		for _, line := range lines {
			if !want[line[1]] || seen[line[1]] {
				t.Fatalf("unexpected/duplicate edge %q", line[1])
			}
			seen[line[1]] = true
		}
		match := cursorRE.FindStringSubmatch(out)
		if match == nil {
			if strings.Contains(out, "More evidence is withheld") {
				t.Fatalf("unparseable printed continuation: %s", out)
			}
			break
		}
		var id, edgeID string
		if err := json.Unmarshal([]byte(match[1]), &id); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(match[3]), &edgeID); err != nil {
			t.Fatal(err)
		}
		seq, err := strconv.ParseUint(match[2], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		args = map[string]any{"source": "standing", "id": id, "query": "", "after_seq": seq, "after_edge": edgeID}
	}
	if len(seen) != len(want) || pages != 2 {
		t.Fatalf("printed cursor reached %d/%d edges in %d pages", len(seen), len(want), pages)
	}
	if lg.LastSeq() != head || lg.LastHash() != hash {
		t.Fatal("inspection appended ledger events")
	}
	if got, err := st.MaxLedgerSeq(); err != nil || got != base.ReadHeadSeq {
		t.Fatalf("inspection changed projection head: %d %v", got, err)
	}
}

// THE STANDING REPORT SAYS HOW AN ID BOTH KINDS HOLD WAS READ (row 9.1). A
// record an earlier release wrote can hold an experience and a belief under
// one id; the source is read as the one recorded first, and the line that
// lists it says so.
func TestTheStandingReportSaysHowAnIDBothKindsHoldWasRead(t *testing.T) {
	e, st, lg, kp, _ := setupEngine(t)
	appended := func(typ ledger.EventType, payload map[string]any) uint64 {
		t.Helper()
		evt, err := lg.Append(typ, 3, payload, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Materialize(evt); err != nil {
			t.Fatal(err)
		}
		return evt.Seq
	}
	first := appended(ledger.EventExperienceCreate, map[string]any{"id": "pair_note", "content": "observed", "provenance": "self"})
	later := appended(ledger.EventBeliefUpsert, map[string]any{"id": "pair_note", "statement": "a belief under a note's id", "ring": 3, "confidence": 0.5})
	// The belief rests on the pair through an edge the record already
	// holds: a new belief may not cite such an id as its origin (the
	// materializer's rule, unchanged).
	appended(ledger.EventExperienceCreate, map[string]any{"id": "other_note", "content": "observed again", "provenance": "self"})
	appended(ledger.EventBeliefUpsert, map[string]any{"id": "b_on_pair", "statement": "a claim resting on the pair", "ring": 3, "confidence": 0.5,
		"origin_refs": []map[string]string{{"kind": "experience", "id": "other_note"}}})
	appended(ledger.EventEdgeCreate, map[string]any{"id": "edge_from_pair", "from_id": "pair_note", "to_id": "b_on_pair", "edge_type": "SUPPORTS"})
	out, err := callAdvertisedRecall(t, e, t.Context(), map[string]any{"source": "standing", "id": "b_on_pair", "query": ""})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("experience, from pair_note] provenance self — this id also names a belief recorded later (record %d); it is read as the experience recorded first (record %d)", later, first)
	if !strings.Contains(out, want) || strings.Contains(out, "eligibility unavailable") {
		t.Fatalf("THE STANDING REPORT DOES NOT SAY HOW AN ID BOTH KINDS HOLD WAS READ (want %q):\n%s", want, out)
	}
}
