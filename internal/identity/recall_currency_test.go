package identity

import (
	"context"
	"hash/fnv"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// currencyEmbedder gives the meaning leg words that share no letters:
// "boat port" means "ferry harbor" and "lamp cupboard" means "lantern
// locker", while every other word takes a small weight on a dimension of
// its own.
type currencyEmbedder struct{}

var currencyConcepts = map[string]int{
	"ferry": 0, "boat": 0,
	"harbor": 1, "port": 1, "⚓": 1,
	"lantern": 2, "lamp": 2,
	"locker": 3, "cupboard": 3,
}

func (currencyEmbedder) Basis() (string, error) { return "currency/v1", nil }
func (currencyEmbedder) Floor() float64         { return memory.ProviderFloor }

func (currencyEmbedder) Embed(_ context.Context, _ memory.Kind, inputs []string) (string, [][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		v := make([]float32, 64)
		for _, w := range strings.Fields(strings.ToLower(in)) {
			w = strings.Trim(w, ".,;:!?")
			if d, ok := currencyConcepts[w]; ok {
				v[d]++
				continue
			}
			h := fnv.New32a()
			h.Write([]byte(w))
			v[4+h.Sum32()%60] += 0.2
		}
		out[i] = v
	}
	return "currency/v1", out, nil
}

// currencyQuery is one ranked recall and the leg that must answer it
// alone ("" when the words and the windows both carry it).
type currencyQuery struct{ text, only string }

// newCurrencyFixture is an identity whose meaning leg is wired, the
// writer a reopened engine appends through, and an unmetabolized note every
// belief here may cite as its origin.
func newCurrencyFixture(t *testing.T) (*Engine, *store.Store, *testEventWriter, string) {
	t.Helper()
	e, st, lg, kp := citingEngine(t)
	e.instruments.SetEmbedder(currencyEmbedder{})
	w := &testEventWriter{ledger: lg, store: st, key: kp}
	origin := noteID(t, e, st, map[string]any{"content": "a prior observed fact", "unmetabolized": true})
	return e, st, w, origin
}

func mustVerb(t *testing.T, e *Engine, name string, args map[string]any) string {
	t.Helper()
	out, err := e.ExecuteAction(context.Background(), "verb", name, args)
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return out
}

// noteID notes and returns the new experience's id.
func noteID(t *testing.T, e *Engine, st *store.Store, args map[string]any) string {
	t.Helper()
	mustVerb(t, e, "note", args)
	exps, err := st.ListExperiences(1)
	if err != nil || len(exps) != 1 {
		t.Fatalf("the note is not in the record: %v %v", exps, err)
	}
	return exps[0].ID
}

func upsertBelief(t *testing.T, e *Engine, id, statement string, evidence ...string) {
	t.Helper()
	refs := make([]any, len(evidence))
	for i, ev := range evidence {
		refs[i] = ev
	}
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.upsert", "id": id, "statement": statement, "evidence_refs": refs})
}

func TestTheMeaningFooterCarriesTheFloorAndClaimsNothingOfTheRecord(t *testing.T) {
	e, st, _, _ := newCurrencyFixture(t)
	noteID(t, e, st, map[string]any{"content": "the ferry left the harbor at dawn"})
	if _, err := e.Instruments().Backfill(context.Background(), 0); err != nil {
		t.Fatalf("meaning backfill: %v", err)
	}
	found := mustVerb(t, e, "recall", map[string]any{"query": "boat port"})
	if want := "Meaning layer: consulted (basis currency/v1, floor 0.600); a hit marked meaning or both carries its similarity, on this model's own scale."; !strings.Contains(found, want) {
		t.Fatalf("a recall the layer answered must end with:\n%s\ngot:\n%s", want, found)
	}
	if !strings.Contains(found, ", meaning, ") || !strings.Contains(found, "similarity ") {
		t.Fatalf("the hit itself keeps how it matched and how close (N8):\n%s", found)
	}
	nothing := mustVerb(t, e, "recall", map[string]any{"query": "zeppelin"})
	if want := "Meaning layer: consulted (basis currency/v1, floor 0.600); no memory scored at or above the floor. That is about the scores, not about what the record holds."; !strings.Contains(nothing, want) {
		t.Fatalf("a recall the layer found nothing for must end with:\n%s\ngot:\n%s", want, nothing)
	}
	if strings.Contains(nothing, "nothing above the floor") {
		t.Fatalf("the old line offered the floor as a finding about the record:\n%s", nothing)
	}
}

func TestASymbolCueIsReadByMeaningAndTheFooterIsTrue(t *testing.T) {
	e, st, _, _ := newCurrencyFixture(t)
	noteID(t, e, st, map[string]any{"content": "the ferry left the harbor at dawn"})
	if _, err := e.Instruments().Backfill(context.Background(), 0); err != nil {
		t.Fatalf("meaning backfill: %v", err)
	}
	out := mustVerb(t, e, "recall", map[string]any{"query": "⚓"})
	if !strings.Contains(out, "the ferry left the harbor at dawn") || !strings.Contains(out, ", meaning, ") {
		t.Fatalf("an anchor means the harbor; the memory must be shown, marked meaning:\n%s", out)
	}
	if want := "Meaning layer: consulted (basis currency/v1, floor 0.600); a hit marked meaning or both carries its similarity"; !strings.Contains(out, want) || strings.Contains(out, "no memory scored at or above the floor") {
		t.Fatalf("the footer must say the layer answered, and nothing else:\n%s", out)
	}
	exact := mustVerb(t, e, "recall", map[string]any{"query": "⚓", "exact": true})
	if !strings.HasPrefix(exact, `No exact-word or fuzzy match for "⚓"`) || strings.Contains(exact, "Meaning layer") || strings.Contains(exact, "the ferry left") {
		t.Fatalf("forced exact consults no meaning and has no words to search:\n%s", exact)
	}
}

// A recall whose meaning read failed in every store scored no memory: its
// answer says the layer could not be read, and why, and never that nothing
// met the floor. The words still answer.
func TestAFailedMeaningReadSaysItFailedNotThatNothingMetTheFloor(t *testing.T) {
	e, st, _, _ := newCurrencyFixture(t)
	noteID(t, e, st, map[string]any{"content": "the ferry left the harbor at dawn"})
	if _, err := e.Instruments().Backfill(context.Background(), 0); err != nil {
		t.Fatalf("meaning backfill: %v", err)
	}
	if _, err := st.DB().Exec(`DROP TABLE memory_vectors`); err != nil {
		t.Fatal(err)
	}
	out := mustVerb(t, e, "recall", map[string]any{"query": "ferry"})
	if !strings.Contains(out, "the ferry left the harbor at dawn") {
		t.Fatalf("the words must still answer:\n%s", out)
	}
	if !strings.Contains(out, "meaning layer unavailable (see the last line)") || !strings.Contains(out, "Meaning layer: unavailable — the vectors could not be read") ||
		strings.Contains(out, "Meaning layer: consulted") || strings.Contains(out, "no memory scored at or above the floor") {
		t.Fatalf("a meaning read that failed must say so, and never that nothing met the floor:\n%s", out)
	}
}

// rankedBeliefs runs the recall verb and returns its Beliefs group, one
// line per belief id.
func rankedBeliefs(t *testing.T, e *Engine, query string) map[string]string {
	t.Helper()
	if _, err := e.Instruments().Backfill(context.Background(), 0); err != nil {
		t.Fatalf("meaning backfill: %v", err)
	}
	out := mustVerb(t, e, "recall", map[string]any{"query": query})
	if !strings.Contains(out, "Meaning layer: consulted") {
		t.Fatalf("recall %q did not consult the meaning layer, so it proves nothing about it:\n%s", query, out)
	}
	lines := map[string]string{}
	for _, ln := range strings.Split(sectionOf(out, "Beliefs:"), "\n") {
		if id, _, ok := strings.Cut(strings.TrimPrefix(ln, "  ["), ","); ok {
			lines[id] = ln
		}
	}
	return lines
}

// reopened runs check against the identity as a boot finds it twice
// over: a database rebuilt from the record alone, then that database
// closed and opened again. Both paths replay the record.
func reopened(t *testing.T, e *Engine, w *testEventWriter, check func(label string, e *Engine, st *store.Store)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restarted.db")
	for _, label := range []string{"rebuilt from the record", "restarted"} {
		st, err := store.OpenForStartup(context.Background(), path, store.MirrorHead{Seq: w.ledger.LastSeq(), Hash: w.ledger.LastHash()},
			func(yield func(*ledger.Event) error) error { return ledger.Stream(w.ledger.Path(), yield) })
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		again := NewEngine(st, &testEventWriter{ledger: w.ledger, store: st, key: w.key}, ring.NewManager(), e.toolDisc, Ports{Embedder: currencyEmbedder{}})
		check(label, again, st)
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// attentionOn composes attention five years on, when every current belief
// has faded: the decay alerts name exactly the current ring-3 beliefs.
func attentionOn(t *testing.T, e *Engine) map[string][]string {
	t.Helper()
	byKind := map[string][]string{}
	var items []memory.AttentionItem
	for _, cost := range []string{attention.CostMedium, attention.CostLow} {
		got, err := e.Instruments().Attention(context.Background(), time.Now().Add(5*365*24*time.Hour), cost)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, got...)
	}
	for _, it := range items {
		byKind[it.Kind] = append(byKind[it.Kind], it.ID+" "+it.Text)
	}
	return byKind
}

// inHistory reports whether the paged read of the beliefs store — the
// history the descriptor's filter allows, never ranked — still holds id
// with its words.
func inHistory(t *testing.T, e *Engine, query, id, text string) bool {
	t.Helper()
	hits, _, err := e.Instruments().Enumerate(context.Background(), "beliefs", query, false, 0, 10)
	if err != nil {
		t.Fatalf("enumerate beliefs: %v", err)
	}
	for _, h := range hits {
		if h.ID == id && h.Text == text {
			return true
		}
	}
	return false
}

func mentions(items []string, s string) bool {
	for _, it := range items {
		if strings.Contains(it, s) {
			return true
		}
	}
	return false
}

const (
	ferryDawn = "The ferry to the island leaves the harbor at dawn"
	ferryNoon = "The ferry to the island leaves the harbor at noon since the timetable changed"
	ferryCrew = "The ferry crew keeps a harbor logbook"
)

var ferryQueries = []currencyQuery{{"ferry harbor", ""}, {"ferri harbr", "fuzzy"}, {"boat port", "meaning"}}

// Job 10 end to end: the operator's correction is noted against the
// belief, the successor cites that note, the identity supersedes.
func TestACorrectedBeliefLeavesRankedRecallAndItsSuccessorAnswers(t *testing.T) {
	e, st, w, origin := newCurrencyFixture(t)
	upsertBelief(t, e, "b_ferry_dawn", ferryDawn, origin)
	upsertBelief(t, e, "b_ferry_crew", ferryCrew, origin)
	for _, q := range ferryQueries {
		if _, ok := rankedBeliefs(t, e, q.text)["b_ferry_dawn"]; !ok {
			t.Fatalf("before the correction %q must find the belief, or its absence afterwards proves nothing", q.text)
		}
	}
	correction := noteID(t, e, st, map[string]any{"content": "the operator says the ferry has left at noon since the timetable changed", "contradicts": "b_ferry_dawn"})
	if !mentions(attentionOn(t, e)[attention.AttentionContradiction], ferryDawn) {
		t.Fatal("before the correction the contradiction is an open tension")
	}
	upsertBelief(t, e, "b_ferry_noon", ferryNoon, correction)
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.supersede", "old_id": "b_ferry_dawn", "new_id": "b_ferry_noon", "reason": "the timetable changed"})

	check := func(label string, e *Engine, st *store.Store) {
		t.Helper()
		standing, err := st.StandingFor("b_ferry_noon")
		if err != nil || standing != "new" {
			t.Fatalf("%s: the successor's standing is %q (%v); the contradiction stays with the retired belief", label, standing, err)
		}
		for _, q := range ferryQueries {
			got := rankedBeliefs(t, e, q.text)
			if line, ok := got["b_ferry_dawn"]; ok {
				t.Fatalf("%s: recall %q still answers with the superseded belief: %s", label, q.text, line)
			}
			line, ok := got["b_ferry_noon"]
			if !ok || !strings.HasPrefix(line, "  [b_ferry_noon, "+standing+", ring 3 | ") {
				t.Fatalf("%s: recall %q must answer with the successor and its derived standing %q: %v", label, q.text, standing, got)
			}
			if q.only != "" && !strings.Contains(line, ", "+q.only+", ") {
				t.Fatalf("%s: recall %q must reach the successor by the %s leg alone: %s", label, q.text, q.only, line)
			}
			if _, ok := got["b_ferry_crew"]; !ok {
				t.Fatalf("%s: recall %q lost a current belief that only shares the words: %v", label, q.text, got)
			}
		}
		edges, err := st.ListEdgesForBelief("b_ferry_noon")
		derived := false
		for _, ed := range edges {
			derived = derived || (ed.FromID == correction && ed.ToID == "b_ferry_noon" && ed.EdgeType == "DERIVED_FROM")
		}
		if err != nil || !derived {
			t.Fatalf("%s: the successor lost its provenance, the correction it came from: %+v %v", label, edges, err)
		}
		// The attention the turn is shown agrees with recall: the retired
		// belief neither fades nor stands in an open tension, because the
		// supersession resolved it.
		items := attentionOn(t, e)
		if mentions(items[attention.AttentionDecayAlert], "b_ferry_dawn") || !mentions(items[attention.AttentionDecayAlert], "b_ferry_noon") {
			t.Fatalf("%s: attention's current beliefs disagree with recall: %v", label, items[attention.AttentionDecayAlert])
		}
		if mentions(items[attention.AttentionContradiction], ferryDawn) {
			t.Fatalf("%s: the superseded belief is still offered as an open tension: %v", label, items[attention.AttentionContradiction])
		}
		// The listing of what the identity holds leaves it out; the history
		// keeps it.
		if listing := sectionOf(mustVerb(t, e, "recall", map[string]any{}), "Beliefs:"); strings.Contains(listing, "b_ferry_dawn") || !strings.Contains(listing, "b_ferry_noon") {
			t.Fatalf("%s: the listing of current beliefs: %s", label, listing)
		}
		if !inHistory(t, e, "ferry", "b_ferry_dawn", ferryDawn) {
			t.Fatalf("%s: the belief's history was lost with its currency", label)
		}
		if led := mustVerb(t, e, "recall", map[string]any{"source": "ledger", "query": "b_ferry_dawn"}); !strings.Contains(led, "belief.supersede") || !strings.Contains(led, `belief.upsert, ring 3`) {
			t.Fatalf("%s: the record no longer shows the belief and its supersession:\n%s", label, led)
		}
	}
	check("live", e, st)
	if err := st.ReplayFromFile(w.ledger.Path()); err != nil {
		t.Fatal(err)
	}
	check("replayed", e, st)
	reopened(t, e, w, check)
}

// Archival without a successor: the belief is let go, the similar belief
// beside it stands, and the history keeps what was let go.
func TestAnArchivedBeliefLeavesRankedRecallAndKeepsItsHistory(t *testing.T) {
	const oilAft = "The lantern oil is kept in the aft locker"
	e, st, w, origin := newCurrencyFixture(t)
	upsertBelief(t, e, "b_oil_aft", oilAft, origin)
	upsertBelief(t, e, "b_spare_lantern", "The spare lantern waits in the forward locker", origin)
	queries := []currencyQuery{{"lantern locker", ""}, {"lantrn lockr", "fuzzy"}, {"lamp cupboard", "meaning"}}
	for _, q := range queries {
		if _, ok := rankedBeliefs(t, e, q.text)["b_oil_aft"]; !ok {
			t.Fatalf("before the archive %q must find the belief", q.text)
		}
	}
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.archive", "id": "b_oil_aft", "reason": "the oil is no longer carried"})

	check := func(label string, e *Engine, _ *store.Store) {
		t.Helper()
		for _, q := range queries {
			got := rankedBeliefs(t, e, q.text)
			if line, ok := got["b_oil_aft"]; ok {
				t.Fatalf("%s: recall %q still answers with the archived belief: %s", label, q.text, line)
			}
			line, ok := got["b_spare_lantern"]
			if !ok || (q.only != "" && !strings.Contains(line, ", "+q.only+", ")) {
				t.Fatalf("%s: recall %q lost the current belief beside it: %v", label, q.text, got)
			}
		}
		items := attentionOn(t, e)[attention.AttentionDecayAlert]
		if mentions(items, "b_oil_aft") || !mentions(items, "b_spare_lantern") {
			t.Fatalf("%s: attention's current beliefs disagree with recall: %v", label, items)
		}
		if !inHistory(t, e, "lantern", "b_oil_aft", oilAft) {
			t.Fatalf("%s: the archived belief's history was lost", label)
		}
		if led := mustVerb(t, e, "recall", map[string]any{"source": "ledger", "query": "b_oil_aft"}); !strings.Contains(led, "belief.archive") {
			t.Fatalf("%s: the record no longer shows the archive:\n%s", label, led)
		}
	}
	check("live", e, st)
	if err := st.ReplayFromFile(w.ledger.Path()); err != nil {
		t.Fatal(err)
	}
	check("replayed", e, st)
	reopened(t, e, w, check)
}

// A correction of a correction: only the last of the chain is current,
// and a replay does not bring back the middle.
func TestOnlyTheLastOfASupersessionChainAnswers(t *testing.T) {
	e, st, w, origin := newCurrencyFixture(t)
	upsertBelief(t, e, "b_kettle_six", "The kettle in the galley whistles at six", origin)

	mustVerb(t, e, "commit", map[string]any{"variant": "belief.upsert", "id": "b_kettle_seven", "statement": "The kettle in the galley whistles at seven",
		"evidence_refs": []any{origin}, "distinct_from": []any{"b_kettle_six"}, "distinction": "seven, not six"})
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.supersede", "old_id": "b_kettle_six", "new_id": "b_kettle_seven", "reason": "it was seven"})
	upsertBelief(t, e, "b_kettle_electric", "The galley kettle is electric now and never whistles", origin)
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.supersede", "old_id": "b_kettle_seven", "new_id": "b_kettle_electric", "reason": "replaced"})

	check := func(label string) {
		t.Helper()
		got := rankedBeliefs(t, e, "kettle galley")
		if _, ok := got["b_kettle_electric"]; !ok || len(got) != 1 {
			t.Fatalf("%s: only the last of the chain is current: %v", label, got)
		}
	}
	check("live")
	if err := st.ReplayFromFile(w.ledger.Path()); err != nil {
		t.Fatal(err)
	}
	check("replayed")
}
