package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedLedger(t *testing.T, s *store.Store, n int, ts time.Time) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if _, err := s.DB().Exec(`INSERT OR IGNORE INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (?, '', ?, 'test.event', 3, '{}', '', '')`,
			i, ts.Add(time.Duration(i)*time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
}

func addExperience(t *testing.T, s *store.Store, id, content string, seq int, at time.Time, private bool) {
	t.Helper()
	p := 0
	if private {
		p = 1
	}
	if _, err := s.DB().Exec(`INSERT INTO experiences (id, content, category, raw, private, provenance, created_seq, created_at) VALUES (?, ?, 'observation', 1, ?, 'self', ?, ?)`,
		id, content, p, seq, at.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func addBelief(t *testing.T, s *store.Store, id, statement string, ring, seq int) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES (?, ?, ?, 0.8, 0, ?, ?)`,
		id, statement, ring, seq, seq); err != nil {
		t.Fatal(err)
	}
}

func addPluginMemory(t *testing.T, s *store.Store, id, plugin, text string, supersededBy string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var sup any
	if supersededBy != "" {
		sup = supersededBy
	}
	if _, err := s.DB().Exec(`INSERT INTO plugin_memories (id, plugin_id, text, superseded_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, plugin, text, sup, now, now); err != nil {
		t.Fatal(err)
	}
}

func sourceOf(res Result, name string) Source {
	for _, s := range res.Sources {
		if s.Store == name {
			return s
		}
	}
	return Source{}
}

func hitIDs(hits []Hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Store+"/"+h.ID)
	}
	return out
}

func TestRecallFusesExactAndFuzzyAcrossStoresWithTheContract(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 3, now.Add(-48*time.Hour))
	addExperience(t, s, "e1", "the lighthouse keeper kept a ledger of every ship", 1, now.Add(-24*time.Hour), false)
	addExperience(t, s, "e2", "a quiet day of routine observations", 2, now.Add(-2*time.Hour), false)
	addBelief(t, s, "b1", "Lighthouses guide ships home", 3, 3)
	if err := s.AddConversationTurn("operator", "did you see the lihgthouse this morning?"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddConversationTurn("system", "tool output: lighthouse lighthouse lighthouse"); err != nil {
		t.Fatal(err)
	}

	f := New(s)
	res, err := f.Recall(context.Background(), Query{Text: "lighthouse", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 3 {
		t.Fatalf("hits = %v, want the experience, the belief and the operator's turn", hitIDs(res.Hits))
	}
	byKey := map[string]Hit{}
	for _, h := range res.Hits {
		byKey[h.Store+"/"+h.ID] = h
	}
	exp := byKey["experiences/e1"]
	if exp.Match != MatchBoth {
		t.Errorf("the experience carries the word and its substring: match %q, want both", exp.Match)
	}
	if exp.Attribution != "self" || exp.Ring != 3 || exp.Time.IsZero() || exp.Seq != 1 {
		t.Errorf("the experience lacks its contract fields: %+v", exp)
	}
	bel := byKey["beliefs/b1"]
	if bel.Match != MatchFuzzy {
		t.Errorf("\"Lighthouses\" is not the word \"lighthouse\"; the fuzzy layer must carry it: match %q", bel.Match)
	}
	if bel.Fuzz < 0.99 || bel.Class != "core" {
		t.Errorf("belief hit = %+v", bel)
	}
	var turn Hit
	for k, h := range byKey {
		if strings.HasPrefix(k, "conversations/") {
			turn = h
		}
	}
	if turn.Match != MatchFuzzy || turn.Attribution != "operator" || turn.Ring != RingWorking {
		t.Errorf("the operator's misspelt turn must be a fuzzy hit attributed to the operator in the working ring: %+v", turn)
	}
	if res.Hits[0].Store != "experiences" {
		t.Errorf("the hit two layers agree on must lead: %v", hitIDs(res.Hits))
	}
	for _, h := range res.Hits {
		if h.Snippet == "" || h.Strength <= 0 || h.Strength > 1 || h.Score <= 0 {
			t.Errorf("hit without a snippet or a sane score: %+v", h)
		}
	}
	for name, want := range map[string]string{"experiences": StatusFound, "beliefs": StatusFound, "conversations": StatusFound, "intentions": StatusFoundNothing, "commitments": StatusFoundNothing, "inbound": StatusFoundNothing} {
		if got := sourceOf(res, name).Status; got != want {
			t.Errorf("%s: status %q, want %q", name, got, want)
		}
	}
	if sourceOf(res, "plugin_memories").Store != "" {
		t.Error("a plugin's record answered the identity's recall")
	}
	if res.Policy != DecayDefault || res.Truncated {
		t.Errorf("result = policy %q truncated %v", res.Policy, res.Truncated)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}
}

func TestForcedExactNeverDegrades(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	seedLedger(t, s, 2, now)
	addExperience(t, s, "e1", "the lighthouse keeper kept a ledger", 1, now, false)
	addExperience(t, s, "e2", "a keeper of the lighthouse", 2, now, false)
	f := New(s)

	res, err := f.Recall(context.Background(), Query{Text: "lighthouse keeper", Exact: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(res.Hits); len(got) != 1 || got[0] != "experiences/e1" {
		t.Fatalf("the phrase in order is in one row: %v", got)
	}
	if res.Hits[0].Match != MatchExactWords {
		t.Errorf("a forced exact hit is an exact_words hit: %q", res.Hits[0].Match)
	}
	res, err = f.Recall(context.Background(), Query{Text: "keeper lightouse", Exact: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("forced exact must not fall back to fuzzy: %v", hitIDs(res.Hits))
	}
	if sourceOf(res, "experiences").Status != StatusFoundNothing {
		t.Errorf("experiences: %+v", sourceOf(res, "experiences"))
	}
	// .
	res, err = f.Recall(context.Background(), Query{Text: "keeper lighthouse"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("words in any order: %v", hitIDs(res.Hits))
	}
}

func TestNoPaddingAndPartialDisclosure(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	seedLedger(t, s, 10, now)
	for i := 1; i <= 10; i++ {
		addExperience(t, s, fmt.Sprintf("e%d", i), fmt.Sprintf("alpha item %d", i), i, now, false)
	}
	f := New(s)
	// .
	// .
	res, err := f.Recall(context.Background(), Query{Text: "alpha", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 2 || !res.Truncated {
		t.Fatalf("limit 2 of 10: %d hits, truncated %v", len(res.Hits), res.Truncated)
	}
	src := sourceOf(res, "experiences")
	if src.Status != StatusPartial || src.Matched != 10 || src.Shown != 2 {
		t.Errorf("experiences must disclose the cut with the row count: %+v", src)
	}
	res, err = f.Recall(context.Background(), Query{Text: "zzz-nothing-matches-this"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("a miss must not be padded: %v", hitIDs(res.Hits))
	}
	for _, src := range res.Sources {
		if src.Status != StatusFoundNothing {
			t.Errorf("%s: %q after a miss", src.Store, src.Status)
		}
	}
	res, err = f.Recall(context.Background(), Query{Text: "!!! ??"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 || !strings.Contains(sourceOf(res, "experiences").Detail, "no searchable words") {
		t.Errorf("a query without words: %+v", res.Sources)
	}
	if _, err := f.Recall(context.Background(), Query{Text: "   "}); err == nil {
		t.Error("an empty query must be refused")
	}
	if _, err := f.Recall(context.Background(), Query{Text: "alpha", Decay: "sigmoid"}); err == nil {
		t.Error("an unknown decay policy must be refused by name")
	}
	if _, err := f.Recall(context.Background(), Query{Text: "alpha", Stores: []string{"dreams"}}); err == nil {
		t.Error("an unknown store must be refused")
	}
}

func TestReinforcementIsConsciousAndDecayIsNamed(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 2, now)
	addExperience(t, s, "old", "the harbour bell rang at midnight", 1, now.Add(-60*24*time.Hour), false)
	addExperience(t, s, "new", "the harbour bell rang again this morning", 2, now.Add(-1*time.Hour), false)
	f := New(s)
	ctx := context.Background()

	// .
	if _, err := f.Recall(ctx, Query{Text: "harbour bell", Now: now}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.MemoryAccessOf(store.MemoryRef{Store: "experiences", ID: "old"}); ok {
		t.Fatal("an unconscious recall wrote an access record")
	}
	// .
	res, err := f.Recall(ctx, Query{Text: "harbour bell", Now: now, Reinforce: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Hits {
		a, ok, err := s.MemoryAccessOf(store.MemoryRef{Store: h.Store, ID: h.ID})
		if err != nil || !ok || a.Count != 1 || !a.LastAt.Equal(now) {
			t.Errorf("%s/%s: access %+v ok=%v err=%v", h.Store, h.ID, a, ok, err)
		}
	}
	// .
	// .
	if res.Hits[0].ID != "new" || res.Hits[1].ID != "old" {
		t.Fatalf("carrd must rank the fresh memory first: %v", hitIDs(res.Hits))
	}
	if res.Hits[1].Strength >= res.Hits[0].Strength || res.Hits[1].Strength > 0.2 {
		t.Errorf("strengths new=%v old=%v", res.Hits[0].Strength, res.Hits[1].Strength)
	}
	// .
	// .
	before := res.Hits[1].Strength
	for i := 0; i < 20; i++ {
		if _, err := f.Recall(ctx, Query{Text: "midnight", Now: now, Reinforce: true}); err != nil {
			t.Fatal(err)
		}
	}
	res, err = f.Recall(ctx, Query{Text: "harbour bell", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var old Hit
	for _, h := range res.Hits {
		if h.ID == "old" {
			old = h
		}
	}
	if old.Accesses != 21 || old.Strength <= before {
		t.Errorf("after 21 conscious recalls: accesses %d strength %v (was %v)", old.Accesses, old.Strength, before)
	}
	// .
	// .
	res, err = f.Recall(ctx, Query{Text: "harbour bell", Now: now, Decay: DecayNone})
	if err != nil {
		t.Fatal(err)
	}
	if res.Policy != DecayNone {
		t.Errorf("policy = %q", res.Policy)
	}
	for _, h := range res.Hits {
		if h.Strength != 1 || h.Score != h.Fused {
			t.Errorf("pure retrieval must carry no strength term: %+v", h)
		}
	}
}

func TestAMissingSourceIsUnavailableNotSilent(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	seedLedger(t, s, 1, now)
	addExperience(t, s, "e1", "the signal fire", 1, now, false)
	if _, err := s.DB().Exec(`DROP TABLE conversations`); err != nil {
		t.Fatal(err)
	}
	f := New(s)
	res, err := f.Recall(context.Background(), Query{Text: "signal"})
	if err != nil {
		t.Fatal(err)
	}
	conv := sourceOf(res, "conversations")
	if conv.Status != StatusSourceUnavailable || !strings.Contains(conv.Detail, "no such table") {
		t.Errorf("conversations: %+v", conv)
	}
	if len(res.Hits) != 1 || sourceOf(res, "experiences").Status != StatusFound {
		t.Errorf("the other sources must still answer: %v %+v", hitIDs(res.Hits), sourceOf(res, "experiences"))
	}
	// .
	// .
	s.Close()
	res, err = f.Recall(context.Background(), Query{Text: "signal"})
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range res.Sources {
		if src.Status != StatusSourceUnavailable {
			t.Errorf("%s: %q on a closed store", src.Store, src.Status)
		}
	}
}

func TestAPluginsRecordIsSearchedOnlyByNameAndOnlyItsOwn(t *testing.T) {
	s := newStore(t)
	addPluginMemory(t, s, "m1", "org.example.memory", "the operator prefers terse summaries", "")
	addPluginMemory(t, s, "m2", "id.other.plugin", "the operator prefers long summaries", "")
	addPluginMemory(t, s, "m0", "org.example.memory", "the operator prefers summaries, an older note", "m1")
	f := New(s)
	ctx := context.Background()

	res, err := f.Recall(ctx, Query{Text: "summaries"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("the identity's recall reached a plugin's record: %v", hitIDs(res.Hits))
	}
	if _, err := f.Recall(ctx, Query{Text: "summaries", Stores: []string{"plugin_memories"}}); err == nil {
		t.Fatal("plugin_memories without a plugin id was searched")
	}
	res, err = f.Recall(ctx, Query{Text: "summaries", Stores: []string{"plugin_memories"}, PluginID: "org.example.memory"})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(res.Hits); len(got) != 1 || got[0] != "plugin_memories/m1" {
		t.Fatalf("one plugin's current memories only: %v", got)
	}
	if res.Hits[0].Attribution != "plugin" || res.Hits[0].Ring != RingWorking {
		t.Errorf("plugin hit = %+v", res.Hits[0])
	}
}

func TestSinceAndBeforeBoundTheHits(t *testing.T) {
	s := newStore(t)
	seedLedger(t, s, 2, time.Now())
	early := time.Date(2026, 9, 1, 9, 0, 0, 123456789, time.UTC)
	late := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	addExperience(t, s, "early", "the tide chart", 1, early, false)
	addExperience(t, s, "late", "the tide chart, revised", 2, late, false)
	f := New(s)
	ctx := context.Background()
	res, err := f.Recall(ctx, Query{Text: "tide chart", Since: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(res.Hits); len(got) != 1 || got[0] != "experiences/late" {
		t.Errorf("Since: %v", got)
	}
	res, err = f.Recall(ctx, Query{Text: "tide chart", Before: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitIDs(res.Hits); len(got) != 1 || got[0] != "experiences/early" {
		t.Errorf("Before: %v", got)
	}
}

func TestEnumeratePagesTheRecordNewestFirstWithoutRanking(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	seedLedger(t, s, 6, now)
	for i := 1; i <= 5; i++ {
		addExperience(t, s, fmt.Sprintf("e%d", i), fmt.Sprintf("alpha entry %d", i), i, now, false)
	}
	addExperience(t, s, "p6", "alpha, but private", 6, now, true)
	f := New(s)
	ctx := context.Background()

	page, total, err := f.Enumerate(ctx, "experiences", "alpha", false, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(page) != 2 || page[0].Seq != 5 || page[1].Seq != 4 {
		t.Fatalf("page 1: total %d, %v", total, hitIDs(page))
	}
	if page[0].Match != MatchExactWords || page[0].Strength != 1 {
		t.Errorf("an enumerated hit is a word match without decay: %+v", page[0])
	}
	page, _, err = f.Enumerate(ctx, "experiences", "alpha", false, page[1].Seq, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Seq != 3 || page[1].Seq != 2 {
		t.Fatalf("page 2: %v", hitIDs(page))
	}
	page, _, err = f.Enumerate(ctx, "experiences", "alpha", false, page[1].Seq, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Seq != 1 {
		t.Fatalf("page 3: %v", hitIDs(page))
	}
	for _, h := range page {
		if h.ID == "p6" {
			t.Fatal("a private experience surfaced through enumeration")
		}
	}
	// .
	if _, ok, _ := s.MemoryAccessOf(store.MemoryRef{Store: "experiences", ID: "e5"}); ok {
		t.Fatal("enumeration wrote an access record")
	}
	// .
	all, total, err := f.Enumerate(ctx, "experiences", "", false, 0, 10)
	if err != nil || total != 5 || len(all) != 5 || all[0].Seq != 5 {
		t.Fatalf("listing: total %d, %v, err %v", total, hitIDs(all), err)
	}
	for _, bad := range []string{"dreams", "inbound", "plugin_memories"} {
		if _, _, err := f.Enumerate(ctx, bad, "", false, 0, 5); err == nil {
			t.Errorf("Enumerate(%s) must be refused", bad)
		}
	}
}

func TestExcerptIsBoundedAndCentredOnTheMatch(t *testing.T) {
	var words []string
	for i := 0; i < 200; i++ {
		words = append(words, fmt.Sprintf("w%d", i))
	}
	words[150] = "needle"
	text := strings.Join(words, " ")
	got := Excerpt(text, []string{"needle"}, 64)
	if n := len(strings.Fields(got)); n > 64 {
		t.Fatalf("excerpt holds %d words", n)
	}
	if !strings.Contains(got, "needle") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("excerpt = %q", got)
	}
	if short := Excerpt("a short text", []string{"text"}, 64); short != "a short text" {
		t.Errorf("a short text is returned whole: %q", short)
	}
}
