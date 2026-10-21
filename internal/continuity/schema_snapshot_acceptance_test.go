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
		columns, prior := snapshotTableDigest(t, old.DB(), table, nil)
		_, after := snapshotTableDigest(t, current.DB(), table, columns)
		if prior != after {
			t.Errorf("recorded runtime values changed in %s", table)
		}
		checked++
	}
	t.Logf("verified signed snapshot through seq %d; runtime values preserved across %d tables; source unchanged", receipt.LastSeq, checked)
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

func snapshotTableDigest(t *testing.T, db *sql.DB, table string, columns []string) ([]string, [32]byte) {
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

// A RETIRED ROW IS CARRIED AS WRITTEN. The standing offer (retired:
// a request's tool list is derived, never chosen) left a
// runtime_meta row in every identity that offered an operation. Opening
// neither reads nor rewrites it, in either of the forms it was written in,
// so the snapshot proof needs no conversion oracle.
func TestARetiredStandingOfferRowIsCarriedAsWritten(t *testing.T) {
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
	cols, before := snapshotTableDigest(t, st.DB(), "runtime_meta", nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, after := snapshotTableDigest(t, st.DB(), "runtime_meta", cols); after != before {
		t.Fatal("opening changed a retired runtime row")
	}
}
