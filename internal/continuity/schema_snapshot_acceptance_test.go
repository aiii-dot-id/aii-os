package continuity

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// Opt-in acceptance over an operator-supplied stable snapshot, never a live
// identity directory. No identity starts, credentials load or workers run.
// The ordinary suite's synthetic historical fixtures remain mandatory.
func TestSchemaUpgradeOperatorSnapshot(t *testing.T) {
	dir := os.Getenv("AII_SCHEMA_SNAPSHOT")
	if dir == "" {
		t.Skip("set AII_SCHEMA_SNAPSHOT to an isolated complete snapshot")
	}
	before := snapshotFileHashes(t, dir)
	t.Cleanup(func() {
		if !reflect.DeepEqual(before, snapshotFileHashes(t, dir)) {
			t.Error("snapshot source changed during qualification")
		}
	})
	data, err := os.ReadFile(filepath.Join(dir, snapshotReceiptName))
	if err != nil {
		t.Fatal(err)
	}
	var receipt snapshotReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	r := New(Wires{})
	if err := r.proveSnapshotIn(t.Context(), Settings{}, dir, receipt, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// The production restore proof verifies the whole signed/witnessed chain,
	// its captured boundary and runtime counts. Additionally compare every
	// pre-existing cell of unchanged runtime tables, not only their counts.
	original, upgraded := filepath.Join(t.TempDir(), "before.db"), filepath.Join(t.TempDir(), "after.db")
	for _, dst := range []string{original, upgraded} {
		if err := copyFile(filepath.Join(dir, SnapshotDB), dst); err != nil {
			t.Fatal(err)
		}
	}
	old, err := store.OpenReadOnly(original)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	current, err := store.OpenAndReplay(t.Context(), upgraded, store.MirrorHead{Seq: receipt.LastSeq, Hash: receipt.LastHash}, func(yield func(*ledger.Event) error) error {
		return ledger.Stream(filepath.Join(dir, "ledger.jsonl"), yield)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	checked := 0
	for table := range receipt.Runtime.Ephemeral {
		// A semantically converted predecessor needs its converter-specific
		// proof, not an assertion that source and target representations match.
		if _, exists := store.Catalog[table]; !exists {
			t.Fatalf("snapshot includes converted predecessor %s; use its historical fixture proof", table)
		}
		columns, prior := snapshotTableDigest(t, old.DB(), table, nil, expectedSnapshotConversion(table))
		_, after := snapshotTableDigest(t, current.DB(), table, columns)
		if prior != after {
			t.Errorf("recorded runtime values changed in %s", table)
		}
		checked++
	}
	t.Logf("verified signed snapshot through seq %d; runtime values preserved across %d tables, including checked representation conversions; source unchanged", receipt.LastSeq, checked)
}

// Test oracle, not a migration registry: only the independently specified
// names-to-seats conversion is normalized here. Every other cell, including
// this row's key/timestamp and all other runtime_meta rows, stays byte-typed.
// Current and unrecognized JSON stays exact, just as opening promises.
func expectedSnapshotConversion(table string) func([]string, []any) error {
	if table != "runtime_meta" {
		return nil
	}
	return func(columns []string, values []any) error {
		key, value := -1, -1
		for i, name := range columns {
			switch name {
			case "key":
				key = i
			case "value":
				value = i
			}
		}
		if key < 0 || value < 0 || values[key] != "tools.standing_offer" {
			return nil
		}
		raw, ok := values[value].(string)
		if !ok || raw == "" {
			return nil
		}
		var current []store.StandingSeat
		if json.Unmarshal([]byte(raw), &current) == nil {
			return nil
		}
		var names []string
		if json.Unmarshal([]byte(raw), &names) != nil {
			return nil
		}
		seats := make([]store.StandingSeat, len(names))
		for i, name := range names {
			seats[i].Name = name // missing fingerprints are never inferred
		}
		encoded, err := json.Marshal(seats)
		if err != nil {
			return err
		}
		values[value] = string(encoded)
		return nil
	}
}

func snapshotFileHashes(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("snapshot member is not a regular file: %s", entry.Name())
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		out[path] = [32]byte(h.Sum(nil))
		return closeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func snapshotTableDigest(t *testing.T, db *sql.DB, table string, columns []string, normalize ...func([]string, []any) error) ([]string, [32]byte) {
	t.Helper()
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	if columns == nil {
		rows, err := db.Query("SELECT * FROM " + quote(table) + " LIMIT 0")
		if err != nil {
			t.Fatal(err)
		}
		columns, err = rows.Columns()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	quoted := make([]string, len(columns))
	for i, name := range columns {
		quoted[i] = quote(name)
	}
	rows, err := db.Query("SELECT " + strings.Join(quoted, ",") + " FROM " + quote(table))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values, into := make([]any, len(columns)), make([]any, len(columns))
	for i := range into {
		into[i] = &values[i]
	}
	var digests []string
	for rows.Next() {
		if err := rows.Scan(into...); err != nil {
			t.Fatal(err)
		}
		if len(normalize) != 0 && normalize[0] != nil {
			if err := normalize[0](columns, values); err != nil {
				t.Fatal(err)
			}
		}
		h := sha256.New()
		for _, value := range values {
			fmt.Fprintf(h, "%T:%q;", value, fmt.Sprint(value))
		}
		digests = append(digests, string(h.Sum(nil)))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(digests)
	return columns, sha256.Sum256([]byte(strings.Join(digests, "")))
}

func TestSnapshotRuntimeDigestChecksValuesAndTypesNotJustCounts(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE sample(value); INSERT INTO sample VALUES ('0012'), (NULL), (x'');`); err != nil {
		t.Fatal(err)
	}
	cols, before := snapshotTableDigest(t, db, "sample", nil)
	_, same := snapshotTableDigest(t, db, "sample", cols)
	if before != same {
		t.Fatal("identical read produced a different digest")
	}
	for _, change := range []string{
		`UPDATE sample SET value='0013' WHERE rowid=1`,
		`UPDATE sample SET value=12 WHERE rowid=1`,
		`UPDATE sample SET value='' WHERE value IS NULL`,
	} {
		if _, err := db.Exec(change); err != nil {
			t.Fatal(err)
		}
		_, after := snapshotTableDigest(t, db, "sample", cols)
		if before == after {
			t.Fatal("same row count concealed a changed runtime value/type")
		}
		before = after
	}
}

func TestSnapshotProofChecksOnlyTheDeclaredStandingOfferConversion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "before.db")
	st, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO runtime_meta VALUES
('tools.standing_offer','["second","first","second"]','choice-time'),
('other-owner','keep exactly','other-time')`); err != nil {
		t.Fatal(err)
	}
	cols, expected := snapshotTableDigest(t, st.DB(), "runtime_meta", nil, expectedSnapshotConversion("runtime_meta"))
	_, rawBefore := snapshotTableDigest(t, st.DB(), "runtime_meta", cols)
	if rawBefore == expected {
		t.Fatal("fixture did not require a representation conversion")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, actual := snapshotTableDigest(t, st.DB(), "runtime_meta", cols)
	if expected != actual {
		t.Fatal("qualification rejected the real approved conversion")
	}
	for _, change := range []string{
		`UPDATE runtime_meta SET value='lost' WHERE key='other-owner'`,
		`UPDATE runtime_meta SET updated_at='invented time' WHERE key='tools.standing_offer'`,
		`UPDATE runtime_meta SET value='[{"name":"first","print":"invented"}]' WHERE key='tools.standing_offer'`,
	} {
		tx, err := st.DB().Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(change); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		_, changed := snapshotTableDigest(t, st.DB(), "runtime_meta", cols)
		if changed == actual {
			t.Fatalf("qualification ignored a change outside the allowed mapping: %s", change)
		}
		actual = changed
	}
}

func TestSnapshotConversionOraclePreservesCurrentAndUnknownValues(t *testing.T) {
	for _, value := range []any{` [ ] `, `null`, `[{"name":"kept","print":"known","future":1}]`, `["unfinished"`, `{"unknown":true}`, []byte(`["blob is not text"]`)} {
		row := []any{"tools.standing_offer", value, "original choice time"}
		before := append([]any(nil), row...)
		if err := expectedSnapshotConversion("runtime_meta")([]string{"key", "value", "updated_at"}, row); err != nil || !reflect.DeepEqual(before, row) {
			t.Fatalf("current, unknown or differently typed bytes changed: %v %v", row, err)
		}
	}
}
