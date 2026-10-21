package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// plugin_memory_retention_test.go — a plugin's replaced memories count
// against its ceiling and are kept 90 days.

func pluginMemoryIDs(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT id FROM plugin_memories ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(ids, " ")
}

// remembered writes one persistent memory of plugin at a stated time,
// replacing old when it is named: the replaced row's updated_at is that
// time, which is what the window counts from.
func remembered(t *testing.T, s *Store, plugin, id, old string, at time.Time) {
	t.Helper()
	if err := s.PluginMemoryAdd(PluginScope{PluginID: plugin}, PluginMemory{ID: id, Text: "memory " + id, CreatedAt: at}, 0, old); err != nil {
		t.Fatalf("remember %s: %v", id, err)
	}
}

// A PLUGIN THAT ONLY EVER REPLACES MEETS ITS CEILING. The ceiling counted
// current memories and took one off for a replacement, so this loop held
// one current memory, grew by a row at every turn and was never refused.
// The refusal says what is held and changes nothing (when room comes back
// is the operator's window, which the broker says); the window's prune is
// what gives the room back.
func TestAPluginThatOnlyReplacesMeetsItsCeiling(t *testing.T) {
	s := testStore(t)
	scope := PluginScope{PluginID: "id.loop"}
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	replace := func(id, old string) error {
		return s.PluginMemoryAdd(scope, PluginMemory{ID: id, Text: "status " + id, CreatedAt: at}, 3, old)
	}
	if err := replace("s1", ""); err != nil {
		t.Fatal(err)
	}
	if err := replace("s2", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := replace("s3", "s2"); err != nil {
		t.Fatal(err)
	}
	err := replace("s4", "s3")
	if !errors.Is(err, ErrPluginMemoryQuota) {
		t.Fatalf("a fourth row under a three-memory ceiling: %v", err)
	}
	if want := "3 memories held at the 3-memory ceiling, 1 current and 2 replaced"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the refusal does not say what is held: %q, want %q in it", err, want)
	}
	if m, found, _ := s.PluginMemoryGet(scope, "s3"); !found || m.SupersededBy != "" {
		t.Fatalf("the refused replacement changed the memory it named: %+v found=%v", m, found)
	}
	if n, _ := s.PluginMemoryCount(scope); n != 1 {
		t.Fatalf("current memories = %d, want 1", n)
	}

	// The window ends for the two replaced rows, and there is room again.
	conn, err := s.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	n, err := prunePluginMemoriesReplaced(t.Context(), conn, at.Add(DefaultPluginMemoryReplacedRetention+time.Hour), DefaultPluginMemoryReplacedRetention, nil)
	conn.Close()
	if err != nil || n != 2 {
		t.Fatalf("pruned %d (%v), want the two replaced rows", n, err)
	}
	if err := replace("s4", "s3"); err != nil {
		t.Fatalf("a replacement after the window gave the room back: %v", err)
	}
	if got := pluginMemoryIDs(t, s); got != "s3 s4" {
		t.Fatalf("rows held = %q, want the replaced s3 and the current s4", got)
	}
}

// THE DAILY PASS REMOVES THE MEMORIES REPLACED MORE THAN 90 DAYS AGO AND
// KEEPS THE REST. Nothing removed a replaced row before. The window is
// counted from the replacement, never from when the memory was written;
// the memory that replaced a row stays; the access record goes with its
// row; the full-text sidecars follow; a second pass has nothing to do.
func TestHousekeepPrunesPluginMemoriesReplacedPastNinetyDays(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "replaced.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	day := 24 * time.Hour
	// One chain: a1 (replaced 150 days ago) -> a2 (replaced 120 days ago)
	// -> a3 (replaced 89 days ago) -> a4 (current, written 89 days ago).
	remembered(t, s, "id.a", "a1", "", now.Add(-400*day))
	remembered(t, s, "id.a", "a2", "a1", now.Add(-150*day))
	remembered(t, s, "id.a", "a3", "a2", now.Add(-120*day))
	remembered(t, s, "id.a", "a4", "a3", now.Add(-89*day))
	// A current memory far older than the window is not a replaced one.
	remembered(t, s, "id.a", "old-and-current", "", now.Add(-400*day))
	// Another plugin's replaced row, inside the window.
	remembered(t, s, "id.b", "b1", "", now.Add(-30*day))
	remembered(t, s, "id.b", "b2", "b1", now.Add(-10*day))
	for _, id := range []string{"a1", "a3"} {
		if err := s.RecordMemoryAccess(now, MemoryRef{Store: "plugin_memories", ID: id}); err != nil {
			t.Fatal(err)
		}
		// Both were embedded: a pruned memory's vector goes with it, in
		// the same transaction, whether or not a source of vectors is
		// configured for the meaning layer's own prune to run.
		if err := s.PutMemoryVectors([]MemoryVector{{Store: "plugin_memories", ID: id, Basis: "p/m", ContentSHA: "sha", Dims: 2, Scale: 0.01, Q: []byte{1, 2}}}); err != nil {
			t.Fatal(err)
		}
	}

	note, err := s.Housekeep(t.Context())
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if !strings.Contains(note, "replaced plugin memories pruned 2") {
		t.Fatalf("the report does not say what the window took: %q", note)
	}
	if got := pluginMemoryIDs(t, s); got != "a3 a4 b1 b2 old-and-current" {
		t.Fatalf("memories after the pass = %q, want a1 and a2 gone and the rest kept", got)
	}
	if m, found, _ := s.PluginMemoryGet(PluginScope{PluginID: "id.a"}, "a3"); !found || m.SupersededBy != "a4" {
		t.Fatalf("the replaced memory inside the window lost its place: %+v found=%v", m, found)
	}
	if _, ok, _ := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: "a1"}); ok {
		t.Fatal("the access record of a removed memory stayed")
	}
	if _, ok, _ := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: "a3"}); !ok {
		t.Fatal("the access record of a kept memory went")
	}
	if gone, kept := countRows(t, s, `SELECT COUNT(*) FROM memory_vectors WHERE store = 'plugin_memories' AND id = 'a1'`), countRows(t, s, `SELECT COUNT(*) FROM memory_vectors WHERE store = 'plugin_memories' AND id = 'a3'`); gone != 0 || kept != 1 {
		t.Fatalf("vectors after the pass: %d of the removed memory (want 0), %d of the kept one (want 1)", gone, kept)
	}
	if n := countRows(t, s, pmFTS, "a1"); n != 0 {
		t.Fatalf("the sidecar still holds a removed memory: %d", n)
	}
	if failed := checkSidecarsOn(s.DB()); len(failed) != 0 {
		t.Fatalf("sidecars disagree after the pass: %v", failed)
	}
	note, err = s.Housekeep(t.Context())
	if err != nil || strings.Contains(note, "replaced plugin memories") {
		t.Fatalf("a second pass found more to prune: %q %v", note, err)
	}
}

// THE PASS TAKES THE WINDOW IT IS HANDED. The 90 days were a number in the
// store; the operator's window (plugins.runtime.replaced_memory_days)
// reaches the pass as an argument at each run, and the store keeps none.
// A memory replaced 40 days ago stays under the default, which is what a
// pass handed nothing uses, and goes under a window of 30 days; one
// replaced 20 days ago stays under both.
func TestTheDailyPassTakesTheWindowItIsHanded(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "window.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	day := 24 * time.Hour
	remembered(t, s, "id.a", "forty", "", now.Add(-200*day))
	remembered(t, s, "id.a", "forty-successor", "forty", now.Add(-40*day))
	remembered(t, s, "id.a", "twenty", "", now.Add(-200*day))
	remembered(t, s, "id.a", "twenty-successor", "twenty", now.Add(-20*day))
	const all = "forty forty-successor twenty twenty-successor"
	if DefaultPluginMemoryReplacedRetention != 90*day {
		t.Fatalf("the default window is %s; it was 90 days in the code and a file that names none must keep that", DefaultPluginMemoryReplacedRetention)
	}

	for _, handed := range []time.Duration{0, DefaultPluginMemoryReplacedRetention} {
		note, err := s.HousekeepOptimized(t.Context(), CompressionOptimization{}, handed)
		if err != nil || strings.Contains(note, "replaced plugin memories") {
			t.Fatalf("handed %s the pass took something inside the default window: %q %v", handed, note, err)
		}
		if got := pluginMemoryIDs(t, s); got != all {
			t.Fatalf("handed %s: memories = %q, want all four", handed, got)
		}
	}
	note, err := s.HousekeepOptimized(t.Context(), CompressionOptimization{}, 30*day)
	if err != nil || !strings.Contains(note, "replaced plugin memories pruned 1") {
		t.Fatalf("handed 30 days the pass says %q (%v), want one memory taken", note, err)
	}
	if got := pluginMemoryIDs(t, s); got != "forty-successor twenty twenty-successor" {
		t.Fatalf("handed 30 days: memories = %q, want the one replaced 40 days ago gone", got)
	}
}

// THE WINDOW IS 90 DAYS TO THE MILLISECOND, COMPARED AS TIME, as the
// receipts' is: a memory replaced exactly 90 days before now is kept and
// one replaced a millisecond earlier goes.
func TestTheReplacedMemoryWindowIsNinetyDaysToTheMillisecond(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "boundary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-DefaultPluginMemoryReplacedRetention)
	written := cutoff.Add(-24 * time.Hour)
	for _, c := range []struct {
		id string
		at time.Time
	}{
		{"a-before-by-1ms", cutoff.Add(-time.Millisecond)},
		{"b-before-by-half-a-second", cutoff.Add(-500 * time.Millisecond)},
		{"c-at-the-cutoff", cutoff},
		{"d-after-by-half-a-second", cutoff.Add(500 * time.Millisecond)},
	} {
		remembered(t, s, "id.w", c.id, "", written)
		remembered(t, s, "id.w", c.id+"-successor", c.id, c.at)
	}
	conn, err := s.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	n, err := prunePluginMemoriesReplaced(t.Context(), conn, now, DefaultPluginMemoryReplacedRetention, nil)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d (%v), want the two replaced before the cutoff", n, err)
	}
	for id, kept := range map[string]bool{
		"a-before-by-1ms": false, "b-before-by-half-a-second": false,
		"c-at-the-cutoff": true, "d-after-by-half-a-second": true,
	} {
		if _, found, _ := s.PluginMemoryGet(PluginScope{PluginID: "id.w"}, id); found != kept {
			t.Fatalf("%s: found=%v, want %v", id, found, kept)
		}
	}
}

// THE WINDOW KEEPS ONLY WHAT A RUNNING ACTIVATION'S CORRECTION STANDS ON,
// AND SAYS SO. A running uncertified activation's local correction points
// at the persistent memory it overlays; a row something points at cannot
// go, and neither can what that row points at in turn. The pass used to
// leave such a plugin whole, silently, on every pass for as long as it
// ran below the proven tier: its other replaced rows never left and
// counted against its ceiling. Now the overlaid memory and its chain of
// successors stay, everything else of the plugin past the window goes,
// the report names the plugin and how many rows stay and why, and they go
// at the pass after the activation's rows have ended.
func TestTheWindowKeepsOnlyWhatARunningActivationsCorrectionStandsOn(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "held.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	day := 24 * time.Hour
	// The chain a correction stands on: root, overlaid by a running
	// activation's temporary row, then replaced for good by a, and a by
	// b, and b by the current c; all three replacements long ago.
	remembered(t, s, "id.t", "root", "", now.Add(-400*day))
	live := PluginScope{PluginID: "id.t", Activation: "live"}
	if err := s.PluginMemoryAdd(live, PluginMemory{ID: "local", Text: "a local correction", Temp: true, CreatedAt: now.Add(-350 * day)}, 0, "root"); err != nil {
		t.Fatal(err)
	}
	remembered(t, s, "id.t", "a", "root", now.Add(-300*day))
	remembered(t, s, "id.t", "b", "a", now.Add(-250*day))
	remembered(t, s, "id.t", "c", "b", now.Add(-200*day))
	// And a replaced memory of the same plugin that nothing stands on.
	remembered(t, s, "id.t", "x1", "", now.Add(-400*day))
	remembered(t, s, "id.t", "x2", "x1", now.Add(-300*day))

	note, err := s.Housekeep(t.Context())
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if !strings.Contains(note, "replaced plugin memories pruned 1") {
		t.Fatalf("the plugin's free replaced row did not go while its activation ran: %q", note)
	}
	if want := "replaced plugin memories kept past their window, a running activation's corrections stand on them: id.t 3"; !strings.Contains(note, want) {
		t.Fatalf("the rows that stay are not named: %q, want %q in it", note, want)
	}
	if got := pluginMemoryIDs(t, s); got != "a b c local root x2" {
		t.Fatalf("rows held = %q, want the overlaid chain whole and x1 gone", got)
	}
	if m, found, _ := s.PluginMemoryGet(live, "root"); !found || m.SupersededBy != "a" {
		t.Fatalf("the activation still reads the memory it corrected, and what replaced it for good: %+v found=%v", m, found)
	}

	if err := s.PluginMemoryClearTemp(live); err != nil {
		t.Fatal(err)
	}
	note, err = s.Housekeep(t.Context())
	if err != nil || !strings.Contains(note, "replaced plugin memories pruned 3") || strings.Contains(note, "kept past their window") {
		t.Fatalf("after the activation's rows ended the pass says %q (%v), want the three taken and none kept", note, err)
	}
	if got := pluginMemoryIDs(t, s); got != "c x2" {
		t.Fatalf("rows held = %q, want the two current memories alone", got)
	}
}

// A RUNNING ACTIVATION'S TEMPORARY ROW THAT A PERSISTENT MEMORY REPLACED
// POINTS AT THAT MEMORY, and the window keeps it and its successors while
// the temporary row lives, though nothing overlays it. Here a temporary
// memory t was replaced for good by m, and m long ago by the current n:
// m is past the window and stays, said in the report, until t has ended.
func TestTheWindowKeepsTheMemoryARunningActivationsRowWasReplacedBy(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "successor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	day := 24 * time.Hour
	live := PluginScope{PluginID: "id.t", Activation: "live"}
	if err := s.PluginMemoryAdd(live, PluginMemory{ID: "t", Text: "a temporary memory", Temp: true, CreatedAt: now.Add(-400 * day)}, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginMemoryAdd(live, PluginMemory{ID: "m", Text: "kept for good", CreatedAt: now.Add(-300 * day)}, 0, "t"); err != nil {
		t.Fatal(err)
	}
	remembered(t, s, "id.t", "n", "m", now.Add(-200*day))

	note, err := s.Housekeep(t.Context())
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if want := "replaced plugin memories kept past their window, a running activation's corrections stand on them: id.t 1"; !strings.Contains(note, want) || strings.Contains(note, "FAILED") {
		t.Fatalf("the pass says %q, want %q in it and no failure", note, want)
	}
	if got := pluginMemoryIDs(t, s); got != "m n t" {
		t.Fatalf("rows held = %q, want the temporary row's successor kept", got)
	}
	if err := s.PluginMemoryClearTemp(live); err != nil {
		t.Fatal(err)
	}
	if note, err := s.Housekeep(t.Context()); err != nil || !strings.Contains(note, "replaced plugin memories pruned 1") {
		t.Fatalf("after the activation's rows ended the pass says %q (%v), want m taken", note, err)
	}
	if got := pluginMemoryIDs(t, s); got != "n" {
		t.Fatalf("rows held = %q, want the current memory alone", got)
	}
}

// THE PASS STARTS NO NEW TRANSACTION OVER A PLUGIN'S MEMORIES AFTER SAFE.
// SAFE is sampled once by the pass and can arrive after (it waits for
// nothing); the prune is a new transaction on the pass's own connection,
// past the write handle that refuses under the freeze. The store asks the
// freeze itself before it, leaves the record as it is, and the report
// says so; with the freeze let go the same pass prunes.
func TestTheDailyPassPrunesNoPluginMemoryUnderSAFE(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "frozen.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	day := 24 * time.Hour
	remembered(t, s, "id.a", "old", "", now.Add(-400*day))
	remembered(t, s, "id.a", "new", "old", now.Add(-200*day))

	s.SetFrozen(true, "the record did not verify")
	note, err := s.Housekeep(t.Context())
	if err != nil {
		t.Fatalf("housekeep under SAFE: %v", err)
	}
	if !strings.Contains(note, "replaced plugin memories not pruned: the projection is frozen: the record did not verify") {
		t.Fatalf("the report does not say the prune was refused under SAFE: %q", note)
	}
	if got := pluginMemoryIDs(t, s); got != "new old" {
		t.Fatalf("a frozen record lost a plugin's memory: %q", got)
	}
	s.SetFrozen(false, "")
	if note, err := s.Housekeep(t.Context()); err != nil || !strings.Contains(note, "replaced plugin memories pruned 1") {
		t.Fatalf("with the freeze let go the pass says %q (%v)", note, err)
	}
}

// THE OPERATOR'S REMOVAL TAKES EVERYTHING ONE PLUGIN STORED, SAYS WHAT IT
// TOOK, AND TOUCHES NOTHING ELSE. Before, no statement deleted a
// persistent memory of a plugin, and nothing removed a plugin's keys once
// it was uninstalled. The listing counts what each plugin holds; the
// removal answers the counts of the rows that went, takes the replaced
// and the temporary rows and the access records with the current ones,
// and leaves another plugin's record and the host's receipts as they were.
func TestTheOperatorsRemovalTakesEverythingAPluginStoredAndSaysWhat(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	remembered(t, s, "id.gone", "g1", "", at)
	remembered(t, s, "id.gone", "g2", "g1", at.Add(time.Hour))
	remembered(t, s, "id.gone", "g3", "", at)
	live := PluginScope{PluginID: "id.gone", Activation: "live"}
	if err := s.PluginMemoryAdd(live, PluginMemory{ID: "local", Text: "a local correction", Temp: true, CreatedAt: at}, 0, "g3"); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(PluginScope{PluginID: "id.gone"}, "cursor", "42", false, 16, 1<<16); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(live, "scratch", "x", true, 16, 1<<16); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMemoryAccess(at, MemoryRef{Store: "plugin_memories", ID: "g2"}); err != nil {
		t.Fatal(err)
	}
	remembered(t, s, "id.kept", "k1", "", at)
	if err := s.PluginKVPut(PluginScope{PluginID: "id.kept"}, "cursor", "7", false, 16, 1<<16); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendPluginReceipt("r1", "id.gone", "memory.remember", "g1", true, []byte(`{"audit_persisted":true}`)); err != nil {
		t.Fatal(err)
	}
	// Two of the memories were embedded: the vector of a removed memory
	// goes with it, in the same transaction, and another plugin's stays.
	// Left to the meaning layer's prune it stayed for as long as no source
	// of vectors was configured.
	for _, id := range []string{"g1", "k1"} {
		if err := s.PutMemoryVectors([]MemoryVector{{Store: "plugin_memories", ID: id, Basis: "p/m", ContentSHA: "sha", Dims: 2, Scale: 0.01, Q: []byte{1, 2}}}); err != nil {
			t.Fatal(err)
		}
	}
	vectors := func(id string) int {
		t.Helper()
		return countRows(t, s, `SELECT COUNT(*) FROM memory_vectors WHERE store = 'plugin_memories' AND id = ?`, id)
	}

	stored, err := s.PluginsStored()
	if err != nil {
		t.Fatal(err)
	}
	want := []PluginStored{
		{PluginID: "id.gone", Memories: 4, Replaced: 1, Keys: 2},
		{PluginID: "id.kept", Memories: 1, Keys: 1},
	}
	if len(stored) != 2 || stored[0] != want[0] || stored[1] != want[1] {
		t.Fatalf("what the plugins hold = %+v, want %+v", stored, want)
	}

	removed, err := s.RemovePluginStored("id.gone")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed != want[0] {
		t.Fatalf("the removal says it took %+v, want %+v", removed, want[0])
	}
	if got := pluginMemoryIDs(t, s); got != "k1" {
		t.Fatalf("memories left = %q, want the other plugin's alone", got)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM plugin_kv WHERE plugin_id = ?`, "id.gone"); n != 0 {
		t.Fatalf("%d keys of the removed plugin stayed", n)
	}
	if v, found, _ := s.PluginKVGet(PluginScope{PluginID: "id.kept"}, "cursor"); !found || v != "7" {
		t.Fatalf("another plugin's key was touched: %q found=%v", v, found)
	}
	if _, ok, _ := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: "g2"}); ok {
		t.Fatal("the access record of a removed memory stayed")
	}
	if vectors("g1") != 0 || vectors("k1") != 1 {
		t.Fatalf("vectors after the removal: %d of the removed memory (want 0), %d of the other plugin's (want 1)", vectors("g1"), vectors("k1"))
	}
	if receipts, _ := s.PluginReceipts("id.gone"); len(receipts) != 1 {
		t.Fatalf("the host's receipts are not the plugin's data and stay: %d", len(receipts))
	}
	if failed := checkSidecarsOn(s.DB()); len(failed) != 0 {
		t.Fatalf("sidecars disagree after the removal: %v", failed)
	}
	if after, _ := s.PluginsStored(); len(after) != 1 || after[0] != want[1] {
		t.Fatalf("what the plugins hold after the removal = %+v", after)
	}

	// A second removal finds nothing and says so with zeroes; no plugin
	// named is refused; a frozen record refuses the act whole.
	if again, err := s.RemovePluginStored("id.gone"); err != nil || again != (PluginStored{PluginID: "id.gone"}) {
		t.Fatalf("a second removal: %+v %v", again, err)
	}
	if _, err := s.RemovePluginStored(""); err == nil {
		t.Fatal("a removal that names no plugin was accepted")
	}
	s.SetFrozen(true, "test")
	var frozen *FrozenError
	if _, err := s.RemovePluginStored("id.kept"); !errors.As(err, &frozen) {
		t.Fatalf("a removal under SAFE: %v", err)
	}
	s.SetFrozen(false, "")
	if got := pluginMemoryIDs(t, s); got != "k1" {
		t.Fatalf("a refused removal took something: %q", got)
	}
}

// THE OPERATOR READS EVERYTHING A PLUGIN STORED, WHOLE, PAGE BY PAGE.
// Before, only the plugin read its own record, and the operator
// was shown a count. Every memory is on some page, current, replaced and
// temporary alike, newest first; a page says how many there are and where
// the next begins; twenty rows is a page, or fewer once their text has
// passed the page's byte bound, and a row longer than the bound is a page
// by itself. Nothing is cut and nothing is reinforced by the read.
func TestTheOperatorReadsEverythingAPluginStoredPageByPage(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	const n = 45
	for i := 1; i <= n; i++ {
		old := ""
		if i == 2 {
			old = "m01" // the oldest memory is a replaced one
		}
		remembered(t, s, "id.read", fmt.Sprintf("m%02d", i), old, at.Add(time.Duration(i)*time.Minute))
	}
	remembered(t, s, "id.other", "not-this-plugins", "", at)

	var read []string
	before, pages := int64(0), 0
	for {
		page, total, next, err := s.PluginStoredMemories("id.read", before)
		if err != nil {
			t.Fatal(err)
		}
		if total != n {
			t.Fatalf("page %d says the plugin holds %d memories, want %d", pages+1, total, n)
		}
		if want := PluginStoredPageRows; next != 0 && len(page) != want {
			t.Fatalf("page %d holds %d rows and says more follow; a page that is not the last holds %d", pages+1, len(page), want)
		}
		for _, m := range page {
			read = append(read, m.ID)
			if m.Text != "memory "+m.ID || m.CreatedAt.IsZero() {
				t.Fatalf("a memory is not read whole: %+v", m)
			}
			if (m.ID == "m01") != (m.SupersededBy != "") || (m.ID == "m01" && m.SupersededBy != "m02") {
				t.Fatalf("the replaced memory, and only it, names what replaced it: %+v", m)
			}
		}
		pages++
		if next == 0 {
			break
		}
		before = next
	}
	if pages != 3 || len(read) != n || read[0] != "m45" || read[n-1] != "m01" {
		t.Fatalf("read %d memories on %d pages, from %s to %s; want all %d on 3 pages, newest first", len(read), pages, read[0], read[len(read)-1], n)
	}
	if _, ok, _ := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: "m45"}); ok {
		t.Fatal("the operator's read was recorded as the plugin's recall")
	}

	// The byte bound: three texts of 100 KiB are two pages, and one of
	// 300 KiB is a page by itself, whole.
	long := strings.Repeat("x", 100<<10)
	for _, id := range []string{"b1", "b2", "b3"} {
		if err := s.PluginMemoryAdd(PluginScope{PluginID: "id.big"}, PluginMemory{ID: id, Text: long, CreatedAt: at}, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PluginMemoryAdd(PluginScope{PluginID: "id.big"}, PluginMemory{ID: "b4", Text: long + long + long, CreatedAt: at}, 0, ""); err != nil {
		t.Fatal(err)
	}
	var sizes []int
	for before = 0; ; {
		page, total, next, err := s.PluginStoredMemories("id.big", before)
		if err != nil || total != 4 {
			t.Fatalf("the long memories: total %d %v", total, err)
		}
		for _, m := range page {
			if len(m.Text) != 100<<10 && len(m.Text) != 300<<10 {
				t.Fatalf("a long memory was cut to %d bytes", len(m.Text))
			}
		}
		sizes = append(sizes, len(page))
		if next == 0 {
			break
		}
		before = next
	}
	if fmt.Sprint(sizes) != "[1 2 1]" {
		t.Fatalf("pages of the long memories hold %v rows, want the 300 KiB one alone, then two, then one", sizes)
	}

	// Keys: every row, the value whole, a running activation's own rows
	// and its deletion marker said as what they are.
	shared, live := PluginScope{PluginID: "id.read"}, PluginScope{PluginID: "id.read", Activation: "live"}
	if err := s.PluginKVPut(shared, "cursor", "42", false, 16, 1<<16); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(shared, "kept", "a value", false, 16, 1<<16); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(live, "scratch", "x", true, 16, 1<<16); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.PluginKVDelete(live, "kept", true); err != nil || !deleted {
		t.Fatalf("a temporary activation's delete of a persistent key: %v %v", deleted, err)
	}
	keys, total, next, err := s.PluginStoredKeys("id.read", 0)
	if err != nil || total != 4 || next != 0 || len(keys) != 4 {
		t.Fatalf("the keys: %+v total %d next %d %v", keys, total, next, err)
	}
	got := map[string]PluginStoredKey{}
	for _, k := range keys {
		if k.UpdatedAt.IsZero() {
			t.Fatalf("a key without the time it was stored: %+v", k)
		}
		k.UpdatedAt = time.Time{}
		if k.DeletionMarker {
			got["marker:"+k.Key] = k
		} else {
			got[k.Key] = k
		}
	}
	for name, want := range map[string]PluginStoredKey{
		"cursor":      {Key: "cursor", Value: "42"},
		"kept":        {Key: "kept", Value: "a value"},
		"scratch":     {Key: "scratch", Value: "x", Temp: true},
		"marker:kept": {Key: "kept", Temp: true, DeletionMarker: true},
	} {
		if got[name] != want {
			t.Fatalf("%s is read as %+v, want %+v", name, got[name], want)
		}
	}
}

// THE PRUNE GOES IN BATCHES, OLDEST FIRST, AND STOPS AT A FREEZE BETWEEN
// THEM. The first pass over a record with a large backlog deleted it all
// in one transaction under the store's lock. With
// batches of two, a chain of six replaced rows is pruned whole, each row
// before the row that replaced it; and a freeze that arrives after the
// first batch stops the pass there, with what went said.
func TestThePruneGoesInBatchesAndStopsAtAFreezeBetweenThem(t *testing.T) {
	was := pluginMemoryPruneBatch
	pluginMemoryPruneBatch = 2
	t.Cleanup(func() { pluginMemoryPruneBatch = was })
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	chain := func(s *Store, plugin string) {
		prev := ""
		for i := 1; i <= 7; i++ {
			id := fmt.Sprintf("%s%d", plugin, i)
			remembered(t, s, plugin, id, prev, at)
			prev = id
		}
	}
	later := at.Add(DefaultPluginMemoryReplacedRetention + time.Hour)

	s := testStore(t)
	chain(s, "a")
	conn, err := s.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	n, err := prunePluginMemoriesReplaced(t.Context(), conn, later, DefaultPluginMemoryReplacedRetention, nil)
	conn.Close()
	if err != nil || n != 6 {
		t.Fatalf("pruned %d (%v) in batches of two, want the six replaced rows", n, err)
	}

	f := testStore(t)
	chain(f, "b")
	conn, err = f.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	asked := 0
	frozen := errors.New("the record is frozen")
	n, err = prunePluginMemoriesReplaced(t.Context(), conn, later, DefaultPluginMemoryReplacedRetention, func() error {
		if asked++; asked > 1 {
			return frozen
		}
		return nil
	})
	conn.Close()
	if !errors.Is(err, frozen) || n != 2 {
		t.Fatalf("a freeze after the first batch: pruned %d (%v), want 2 and the freeze", n, err)
	}
}
