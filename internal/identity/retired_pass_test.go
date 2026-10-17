package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// recordingConsolidation keeps what it is shown, on either channel, and
// answers nothing a pass can use: a pass that runs it mints nothing and
// consumes nothing, so the same pass can be run again over the same record.
type recordingConsolidation struct{ shown *[]string }

func (r recordingConsolidation) ChatSimple(_ context.Context, _, user string) (string, string, error) {
	*r.shown = append(*r.shown, "simple: "+user)
	return "no envelope", "fixture-model", nil
}

func (r recordingConsolidation) ChatStructured(_ context.Context, _, user string, _ wire.ToolDefinition) (string, string, bool, error) {
	*r.shown = append(*r.shown, "structured: "+user)
	return "no envelope", "fixture-model", false, nil
}

// passInput is what one metabolism pass over st's record is shown.
func passInput(t *testing.T, st *store.Store, w *testEventWriter) string {
	t.Helper()
	var shown []string
	c := cognitive.NewConsolidate(st, recordingConsolidation{&shown}, w, nil, cognitive.ConsolidateConfig{Threshold: 3})
	c.SetTensions(st)
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(shown) != 1 || !strings.HasPrefix(shown[0], "structured: ") {
		t.Fatalf("the metabolism pass made %d call(s), want one through the structured channel: %q", len(shown), shown)
	}
	return strings.TrimPrefix(shown[0], "structured: ")
}

// second is a time of the record as the prompt states it — spelled out
// here, not taken from the code under test.
func second(ts string) string {
	tm, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		panic(err)
	}
	return tm.UTC().Format("2006-01-02T15:04:05Z")
}

// recordedAt reads the ledger FILE: the time of each experience, and of each
// retirement (keyed "retire:<belief id>").
func recordedAt(t *testing.T, w *testEventWriter) map[string]string {
	t.Helper()
	events, err := ledger.ReadAll(w.ledger.Path())
	if err != nil {
		t.Fatal(err)
	}
	at := map[string]string{}
	for _, evt := range events {
		var p struct {
			ID    string `json:"id"`
			OldID string `json:"old_id"`
		}
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			continue
		}
		switch evt.Type {
		case ledger.EventExperienceCreate:
			at[p.ID] = evt.Timestamp
		case ledger.EventBeliefSupersede:
			at["retire:"+p.OldID] = evt.Timestamp
		case ledger.EventBeliefArchive:
			at["retire:"+p.ID] = evt.Timestamp
		}
	}
	return at
}

const retiredHeader = "Beliefs retired since the oldest experience above (an experience recorded before a belief was retired is not evidence for it now; one recorded after may be):"

// idsIn is the belief ids in the bracket at the start of each line.
func idsIn(lines []string) []string {
	var ids []string
	for _, ln := range lines {
		if id, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(ln), "["), "]"); ok {
			id, _, _ = strings.Cut(id, ",")
			ids = append(ids, id)
		}
	}
	return ids
}

// blockOf is the lines of the block a header opens, up to the next line that
// does not begin with two spaces.
func blockOf(input, header string) []string {
	var out []string
	in := false
	for _, ln := range strings.Split(input, "\n") {
		switch {
		case strings.HasPrefix(ln, header):
			in = true
		case in && strings.HasPrefix(ln, "  ["):
			out = append(out, ln)
		case in:
			return out
		}
	}
	return out
}

// THE CORRECTION, THROUGH THE REAL RECORD: three notes that still say the
// ferry leaves at dawn, a noon belief that replaced the dawn one, and the
// pass's whole input — the retired belief with the second the record says
// it was retired, what replaced it, and every note with the second the
// record says it was recorded. The same after a replay of the record, a
// rebuild from it and a restart: nothing here is held outside the ledger.
func TestAPassIsShownTheBeliefTheIdentityRetiredThroughTheRealRecord(t *testing.T) {
	e, st, w, origin := newCurrencyFixture(t)
	var said, words []string
	for _, c := range []string{"the ferry left the harbor at dawn again", "caught the dawn ferry from the harbor", "harbor at dawn: the ferry was on time"} {
		said = append(said, noteID(t, e, st, map[string]any{"content": c}))
		words = append(words, c)
	}
	upsertBelief(t, e, "b_ferry_dawn", ferryDawn, origin)
	upsertBelief(t, e, "b_ferry_noon", ferryNoon, origin)
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.supersede", "old_id": "b_ferry_dawn", "new_id": "b_ferry_noon", "reason": "the timetable changed"})
	at := recordedAt(t, w)

	lines := []string{"Experiences to consolidate (cite their ids as evidence):"}
	for i, id := range said {
		lines = append(lines, fmt.Sprintf("- %s [%s] a note of yours: %s", second(at[id]), id, words[i]))
	}
	lines = append(lines,
		"Current beliefs (for merge/supersede decisions):",
		"  [b_ferry_noon, new] "+ferryNoon,
		retiredHeader,
		fmt.Sprintf(`  [b_ferry_dawn] %q — retired %s, replaced by [b_ferry_noon]`, ferryDawn, second(at["retire:b_ferry_dawn"])))
	want := strings.Join(lines, "\n")

	check := func(label string, st *store.Store) {
		t.Helper()
		if got := passInput(t, st, w); got != want {
			t.Fatalf("%s: the pass was shown:\n%s\n\nwant:\n%s", label, got, want)
		}
	}
	check("live", st)
	if err := st.ReplayFromFile(w.ledger.Path()); err != nil {
		t.Fatal(err)
	}
	check("replayed", st)
	reopened(t, e, w, func(label string, _ *Engine, st *store.Store) { check(label, st) })
}

// A CHAIN, AN ARCHIVE, AND THE PARTITION: a correction of a correction shows
// each retired belief replaced by the END of the chain; a belief let go says
// nothing replaced it; newest retirement first; and no belief is in both
// the block of what stands and the block of what was retired.
func TestAPassIsShownAChainAndAnArchiveThroughTheRealRecord(t *testing.T) {
	e, st, w, origin := newCurrencyFixture(t)
	for _, c := range []string{"the galley kettle whistled early", "oil in the aft locker again", "the kettle is electric now"} {
		noteID(t, e, st, map[string]any{"content": c})
	}
	upsertBelief(t, e, "b_kettle_six", "The kettle in the galley whistles at six", origin)
	upsertBelief(t, e, "b_oil_aft", "The lantern oil is kept in the aft locker", origin)
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.upsert", "id": "b_kettle_seven", "statement": "The kettle in the galley whistles at seven",
		"evidence_refs": []any{origin}, "distinct_from": []any{"b_kettle_six"}, "distinction": "seven, not six"})
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.supersede", "old_id": "b_kettle_six", "new_id": "b_kettle_seven", "reason": "it was seven"})
	upsertBelief(t, e, "b_kettle_electric", "The galley kettle is electric now and never whistles", origin)
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.supersede", "old_id": "b_kettle_seven", "new_id": "b_kettle_electric", "reason": "replaced"})
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.archive", "id": "b_oil_aft", "reason": "the oil is no longer carried"})
	at := recordedAt(t, w)

	got := passInput(t, st, w)
	retired := blockOf(got, retiredHeader)
	wantRetired := []string{
		fmt.Sprintf(`  [b_oil_aft] "The lantern oil is kept in the aft locker" — retired %s, nothing replaced it`, second(at["retire:b_oil_aft"])),
		fmt.Sprintf(`  [b_kettle_seven] "The kettle in the galley whistles at seven" — retired %s, replaced by [b_kettle_electric]`, second(at["retire:b_kettle_seven"])),
		fmt.Sprintf(`  [b_kettle_six] "The kettle in the galley whistles at six" — retired %s, replaced by [b_kettle_electric]`, second(at["retire:b_kettle_six"])),
	}
	if strings.Join(retired, "\n") != strings.Join(wantRetired, "\n") {
		t.Fatalf("the retired beliefs, newest retirement first:\n%s\n\nwant:\n%s\n\nwhole input:\n%s", strings.Join(retired, "\n"), strings.Join(wantRetired, "\n"), got)
	}
	standing := idsIn(blockOf(got, "Current beliefs (for merge/supersede decisions):"))
	if len(standing) != 1 || standing[0] != "b_kettle_electric" {
		t.Fatalf("the beliefs that stand: %v", standing)
	}
	for _, id := range idsIn(retired) {
		for _, s := range standing {
			if id == s {
				t.Fatalf("%s is in the block of what stands and the block of what was retired", id)
			}
		}
	}
}

// NOTHING RETIRED SINCE ITS OLDEST NOTE, NOTHING SAID: the identity retired
// the belief, and only THEN wrote the notes, so no note on the table can
// predate the retirement. There is no section, no header and no time on any
// note — the input is what it was before the section existed.
func TestAPassIsShownNothingWhenEveryNoteFollowsTheRetirement(t *testing.T) {
	e, st, w, origin := newCurrencyFixture(t)
	upsertBelief(t, e, "b_ferry_dawn", ferryDawn, origin)
	upsertBelief(t, e, "b_ferry_noon", ferryNoon, origin)
	mustVerb(t, e, "commit", map[string]any{"variant": "belief.supersede", "old_id": "b_ferry_dawn", "new_id": "b_ferry_noon", "reason": "the timetable changed"})
	var said, words []string
	for _, c := range []string{"the noon ferry was full", "the noon ferry left on time", "noon: the ferry was empty"} {
		said = append(said, noteID(t, e, st, map[string]any{"content": c}))
		words = append(words, c)
	}
	lines := []string{"Experiences to consolidate (cite their ids as evidence):"}
	for i, id := range said {
		lines = append(lines, fmt.Sprintf("- [%s] a note of yours: %s", id, words[i]))
	}
	lines = append(lines, "Current beliefs (for merge/supersede decisions):", "  [b_ferry_noon, new] "+ferryNoon)
	if got, want := passInput(t, st, w), strings.Join(lines, "\n"); got != want {
		t.Fatalf("the pass was shown:\n%s\n\nwant:\n%s", got, want)
	}
}

// OVER THE BOUND, THE REST IS COUNTED: ten beliefs let go after the notes,
// and the pass is shown the eight newest and told two more exist.
func TestAPassIsToldHowManyRetirementsItWasNotShown(t *testing.T) {
	e, st, w, origin := newCurrencyFixture(t)
	for _, c := range []string{"one note", "another note", "a third note"} {
		noteID(t, e, st, map[string]any{"content": c})
	}
	claims := []string{
		"The harbor bell rings at nine", "The tide table hangs by the door", "Kestrels nest on the north cliff",
		"The lighthouse keeper sleeps until noon", "Salt cod is the Friday supper", "The pier lamps are lit by hand",
		"Gulls avoid the eastern mole", "The chart room key is brass", "Rope is stored under the stairs", "The fog signal sounds twice",
	}
	for i, claim := range claims {
		id := fmt.Sprintf("b_claim_%02d", i)
		upsertBelief(t, e, id, claim, origin)
		mustVerb(t, e, "commit", map[string]any{"variant": "belief.archive", "id": id, "reason": "let go"})
	}
	got := passInput(t, st, w)
	shown := blockOf(got, retiredHeader)
	if len(shown) != 8 || !strings.Contains(shown[0], "b_claim_09") || !strings.Contains(shown[7], "b_claim_02") {
		t.Fatalf("the eight newest retirements, newest first: %v", idsIn(shown))
	}
	if !strings.HasSuffix(got, "\n  (and 2 earlier retirement(s) not shown here)") {
		t.Fatalf("the two retirements left out are not declared:\n%s", got)
	}
}
