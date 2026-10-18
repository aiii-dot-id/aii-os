package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

const (
	fixtureFlagged  = "the words of a note written with private=true"
	fixtureOrdinary = "an ordinary note written by the same program"
)

func earlierDatabase(t *testing.T) (path, fixture string) {
	t.Helper()
	fixture = filepath.Join("testdata", "legacy-private-note")
	path = filepath.Join(t.TempDir(), "aii.db")
	raw := openRawForTest(t, path)
	for _, name := range []string{"schema.sql", "rows.sql"} {
		sqlText, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(sqlText)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// The fixture is what it says: the earlier program's row.
	var private, rawCol int
	if err := raw.QueryRow(`SELECT private, raw FROM experiences WHERE content = ?`, fixtureFlagged).Scan(&private, &rawCol); err != nil || private != 1 || rawCol != 0 {
		t.Fatalf("the fixture's flagged note is private=%d raw=%d (%v), want what the earlier program built: 1 and 0", private, rawCol, err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	return path, fixture
}

func TestAnUpgradeKeepsTheExclusionAndShowsTheNote(t *testing.T) {
	const flagged, ordinary = fixtureFlagged, fixtureOrdinary
	path, fixture := earlierDatabase(t)

	record := filepath.Join(fixture, "ledger.jsonl")
	source := func(yield func(*ledger.Event) error) error { return ledger.Stream(record, yield) }
	var head MirrorHead
	if err := source(func(evt *ledger.Event) error {
		head = MirrorHead{Seq: evt.Seq, Hash: evt.EntryHash()}
		return nil
	}); err != nil || head.Seq == 0 {
		t.Fatalf("the fixture's record does not read: %v (head %d)", err, head.Seq)
	}

	for start := 1; start <= 2; start++ {
		st, err := OpenForStartup(context.Background(), path, head, source)
		if err != nil {
			t.Fatalf("start %d of this program over the earlier program's database: %v", start, err)
		}
		cols, err := st.DB().Query(`SELECT name FROM pragma_table_info('experiences')`)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for cols.Next() {
			var name string
			if err := cols.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names[name] = true
		}
		cols.Close()
		if names["private"] || !names["unmetabolized"] {
			t.Fatalf("start %d: the experiences table must carry unmetabolized and no private column: %v", start, names)
		}

		exps, err := st.ListExperiences(10)
		if err != nil {
			t.Fatal(err)
		}
		byWords := map[string]Experience{}
		for _, e := range exps {
			byWords[e.Content] = e
		}
		if e, ok := byWords[flagged]; !ok || e.Raw != 0 || e.Unmetabolized != 1 {
			t.Fatalf("start %d: THE FLAGGED NOTE IS NOT LISTED WITH ITS WORDS AND ITS MARK (listed %v, raw %d, unmetabolized %d): %+v", start, ok, e.Raw, e.Unmetabolized, exps)
		}
		if e, ok := byWords[ordinary]; !ok || e.Raw != 1 || e.Unmetabolized != 0 {
			t.Fatalf("start %d: the ordinary note changed (listed %v, raw %d, unmetabolized %d)", start, ok, e.Raw, e.Unmetabolized)
		}
		queue, err := st.ListRawExperiences(10)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range queue {
			if e.Content == flagged {
				t.Fatalf("start %d: THE UPGRADE PUT A NOTE ITS AUTHOR KEPT OUT OF DREAM AND CONSOLIDATE INTO THEIR QUEUE", start)
			}
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// THE COLUMN IS CARRIED TO ITS NEW NAME, NOT DROPPED AND REFILLED. The
// declaration in schema.sql (renamed-from: private) is what an opening
// with no record to replay rests on: a tool that opens the database, or a
// start before its replay. Without it the derived table is recreated
// empty for a replay to refill, and until then the notes are not there.
func TestTheMarkColumnIsCarriedToItsNewNameWithItsRows(t *testing.T) {
	path, _ := earlierDatabase(t)
	st, err := New(path)
	if err != nil {
		t.Fatalf("this program opening the earlier program's database: %v", err)
	}
	defer st.Close()
	exps, err := st.ListExperiences(10)
	if err != nil {
		t.Fatal(err)
	}
	byWords := map[string]Experience{}
	for _, e := range exps {
		byWords[e.Content] = e
	}
	if e, ok := byWords[fixtureFlagged]; !ok || e.Raw != 0 || e.Unmetabolized != 1 {
		t.Fatalf("THE EARLIER PROGRAM'S ROWS WERE NOT CARRIED UNDER THE NEW NAME (listed %v, raw %d, unmetabolized %d): %+v", ok, e.Raw, e.Unmetabolized, exps)
	}
	if e, ok := byWords[fixtureOrdinary]; !ok || e.Raw != 1 || e.Unmetabolized != 0 {
		t.Fatalf("the ordinary note was not carried as it was (listed %v, raw %d, unmetabolized %d)", ok, e.Raw, e.Unmetabolized)
	}
}

// A SAFE START OVER A DATABASE FROM BEFORE THE RENAME STILL LISTS ITS
// EXPERIENCES. A first start after the upgrade that routes to SAFE before
// the schema is reconciled mounts the database read-only, as the earlier
// program left it: the column is still called private. Reading the new
// name there failed every experience read, and the SAFE page showed none.
// The mount reads the mark under the name the column has.
func TestASafeMountOfAnEarlierDatabaseListsItsExperiences(t *testing.T) {
	path, _ := earlierDatabase(t)
	st, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("the read-only mount of the earlier program's database: %v", err)
	}
	defer st.Close()
	exps, err := st.ListExperiences(10)
	if err != nil {
		t.Fatalf("A SAFE MOUNT OF A DATABASE FROM BEFORE THE RENAME CANNOT LIST EXPERIENCES: %v", err)
	}
	byWords := map[string]Experience{}
	for _, e := range exps {
		byWords[e.Content] = e
	}
	if e, ok := byWords[fixtureFlagged]; !ok || e.Unmetabolized != 1 {
		t.Fatalf("the flagged note is not listed with its words and its mark (listed %v, unmetabolized %d): %+v", ok, e.Unmetabolized, exps)
	}
	if e, ok := byWords[fixtureOrdinary]; !ok || e.Unmetabolized != 0 {
		t.Fatalf("the ordinary note is not listed as it is (listed %v, unmetabolized %d)", ok, e.Unmetabolized)
	}
	ends, err := st.TensionEnds([]string{byWords[fixtureFlagged].ID})
	if end := ends[byWords[fixtureFlagged].ID]; err != nil || end.Text != fixtureFlagged || !end.Unmetabolized {
		t.Fatalf("the contradiction view over the mount: %+v (%v)", end, err)
	}
	var stillEarlier int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('experiences') WHERE name='private'`).Scan(&stillEarlier); err != nil || stillEarlier != 1 {
		t.Fatalf("the mount changed the database it reads (private column present: %d, %v)", stillEarlier, err)
	}
}
