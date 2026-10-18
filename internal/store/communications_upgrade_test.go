package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func communicationsFixture(t *testing.T, path, fixture string) {
	t.Helper()
	st, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	raw := openRawForTest(t, path)
	defer raw.Close()
	ddl, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA foreign_keys=OFF; DROP TABLE inbound; DROP TABLE outbox;" + string(ddl)); err != nil {
		t.Fatal(err)
	}
	old := fixture == "communications-before-labels.sql"
	if old {
		_, err = raw.Exec(`INSERT INTO correspondents VALUES ('person-1','quinn',NULL,9007199254740993);
INSERT INTO reach VALUES ('secondary','person-1','email','new@example.test',9,0,1),('primary','person-1','signal','+1555',0,1,2);
INSERT INTO outbox(id,to_role,to_identity,content,created_ms) VALUES ('pending','peer','person-1','hello',11),('orphan','peer','missing-id','keep me',12);
INSERT INTO outbox_route VALUES ('pending','signal');
INSERT INTO inbound VALUES ('unread','signal','+1555','person-1','known','new mail',42,0),('read','signal','+1555','person-1','known','old mail',43,1);`)
	} else {
		_, err = raw.Exec(`INSERT INTO reach VALUES ('quinn','email','new@example.test',9,0,1),('quinn','signal','+1555',0,1,2);
INSERT INTO outbox(id,to_role,to_identity,content,created_ms) VALUES ('pending','peer','quinn','hello',11);
INSERT INTO inbound VALUES ('unread','signal','+1555','new mail',42,0),('read','signal','+1555','old mail',43,1);`)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestCommunicationsUpgradePreservesHistoricalMeaning(t *testing.T) {
	for _, fixture := range []string{"communications-before-labels.sql", "communications-before-config.sql"} {
		t.Run(fixture, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "aii.db")
			communicationsFixture(t, path, fixture)
			for pass := 0; pass < 2; pass++ {
				st, err := OpenAndReplay(context.Background(), path, MirrorHead{}, func(func(*ledger.Event) error) error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				contacts, issues, err := st.LegacyContacts()
				want := []LegacyContact{{"quinn", "signal", "+1555", true}, {"quinn", "email", "new@example.test", false}}
				if err != nil || len(issues) != 0 || !reflect.DeepEqual(contacts, want) {
					t.Fatalf("contacts/order/wake lost: %+v %v %v", contacts, issues, err)
				}
				mail, err := st.UndeliveredMessages()
				if err != nil || len(mail) == 0 || mail[0].ToIdentity != "quinn" || mail[0].Content != "hello" || mail[0].DeliveredVia != "" {
					t.Fatalf("recipient/content/outcome changed: %+v %v", mail, err)
				}
				if fixture == "communications-before-labels.sql" {
					if mail[0].RequestedChannel != "signal" || len(mail) != 2 || mail[1].RecipientError == "" || mail[1].Content != "keep me" {
						t.Fatalf("restriction/unresolved mail lost: %+v", mail)
					}
				}
				arrivals, err := st.InboundSince(0)
				if err != nil || len(arrivals) != 1 || arrivals[0].Body != "new mail" || arrivals[0].ReceivedMs != 42 {
					t.Fatalf("inbox lost or read mail redelivered: %+v %v", arrivals, err)
				}
				source := readCommunicationArchiveForTest(t, st)
				if len(source["inbound"]) != 2 || source["inbound"][1].integer("seen") != 1 {
					t.Fatal("historical source lost")
				}
				if fixture == "communications-before-labels.sql" && source["correspondents"][0].integer("created_ms") != 9007199254740993 {
					t.Fatal("source integer precision lost")
				}
				if pass == 1 && len(st.schemaConversions) != 0 {
					t.Fatalf("upgrade repeated: %+v", st.schemaConversions)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCommunicationsUpgradeFailureRestoresOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	communicationsFixture(t, path, "communications-before-labels.sql")
	fault := errors.New("replay interrupted after communications conversion")
	_, err := OpenAndReplay(context.Background(), path, MirrorHead{}, func(func(*ledger.Event) error) error { return fault })
	if !errors.Is(err, fault) {
		t.Fatalf("fault not reached: %v", err)
	}
	db := openRawForTest(t, path)
	defer db.Close()
	var name string
	if err := db.QueryRow("SELECT to_identity FROM outbox WHERE id='pending'").Scan(&name); err != nil || name != "person-1" {
		t.Fatalf("partial recipient commit: %q %v", name, err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM reach").Scan(&n); err != nil || n != 2 {
		t.Fatalf("source retired on failure: %d %v", n, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM runtime_meta WHERE key=? OR substr(key,1,?)=?", communicationsBeforeUpgrade, len(communicationsBeforeUpgrade)+1, communicationsBeforeUpgrade+"/").Scan(&n); err != nil || n != 0 {
		t.Fatalf("archive committed separately: %d %v", n, err)
	}
}

func TestCommunicationsUpgradeCarryReportsPreservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	communicationsFixture(t, path, "communications-before-labels.sql")
	report := CarryReport{}
	st, err := openSchema(context.Background(), path, &MirrorHead{}, func(func(*ledger.Event) error) error { return nil }, &report, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if report.Converted["reach"].Before != 2 || report.Converted["runtime_meta"].After != 8 || report.Ephemeral["outbox"] != 2 {
		t.Fatalf("false carry receipt: %+v", report)
	}
}

func TestCommunicationsUpgradeCompressedRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	communicationsFixture(t, path, "communications-before-labels.sql")
	compressed := filepath.Join(t.TempDir(), "compressed.db")
	if published, err := ConvertDatabase(t.Context(), path, compressed, true); err != nil || !published {
		t.Fatalf("compression failed: %v", err)
	}
	beforeDB, err := OpenReadOnly(compressed)
	if err != nil {
		t.Fatal(err)
	}
	before, err := readRuntimeFacts(t.Context(), beforeDB.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := beforeDB.Close(); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "ledger.jsonl")
	if err := os.WriteFile(record, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, after, err := ProveRestore(t.Context(), compressed, record, filepath.Join(t.TempDir(), "proof"), MirrorHead{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := before.DiffersAfterRestore(after); diff != "" {
		t.Fatal(diff)
	}
	if before.Differs(after.RuntimeFacts) == "" {
		t.Fatal("conversion falsely reported as unchanged")
	}
	st, err := OpenAndReplay(t.Context(), compressed, MirrorHead{}, func(func(*ledger.Event) error) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	contacts, issues, err := st.LegacyContacts()
	if err != nil || len(issues) != 0 || len(contacts) != 2 {
		t.Fatalf("compressed upgrade lost contacts: %+v %v %v", contacts, issues, err)
	}
}

func TestCommunicationsUpgradeKeepsAmbiguousContactData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	communicationsFixture(t, path, "communications-before-labels.sql")
	raw := openRawForTest(t, path)
	if _, err := raw.Exec(`INSERT INTO correspondents VALUES ('person-2','QUINN',NULL,1);
INSERT INTO reach VALUES ('collision','person-2','signal','+1999',0,0,1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	st, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	contacts, issues, err := st.LegacyContacts()
	if err != nil || len(contacts) != 0 || len(issues) != 3 {
		t.Fatalf("colliding names silently merged: %+v %v %v", contacts, issues, err)
	}
	source := readCommunicationArchiveForTest(t, st)
	if len(source["reach"]) != 3 {
		t.Fatal("unresolved contact rows discarded")
	}
	mail, err := st.UndeliveredMessages()
	if err != nil || mail[0].RecipientError == "" {
		t.Fatalf("ambiguous recipient was released: %+v %v", mail, err)
	}
}

func TestCommunicationsUpgradeRetainsUnrepresentableValuesAndPins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	communicationsFixture(t, path, "communications-before-labels.sql")
	raw := openRawForTest(t, path)
	if _, err := raw.Exec("UPDATE outbox_route SET channel=X'FF'; UPDATE correspondents SET created_ms=X'00FF',label=CAST(X'FF' AS TEXT); ALTER TABLE inbound ADD COLUMN extra REAL; UPDATE inbound SET extra=1.0"); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	st, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	source := readCommunicationArchiveForTest(t, st)
	row := source["correspondents"][0]
	if !source["inbound"][0]["extra"].Real {
		t.Fatal("REAL storage class lost in retained source")
	}
	var text, blob []byte
	if err := json.Unmarshal(row["label"].JSON, &text); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(row["created_ms"].JSON, &blob); err != nil {
		t.Fatal(err)
	}
	if !row["label"].TextBytes || !row["created_ms"].Blob || !reflect.DeepEqual(text, []byte{255}) || !reflect.DeepEqual(blob, []byte{0, 255}) {
		t.Fatalf("source bytes changed: %+v", row)
	}
	mail, err := st.UndeliveredMessages()
	if err != nil || mail[0].RecipientError == "" {
		t.Fatalf("invalid channel became unrestricted delivery: %+v %v", mail, err)
	}
}

// Aggregation is test-only; production preserves and reads one history row at a time.
func readCommunicationArchiveForTest(t *testing.T, st *Store) communicationSource {
	t.Helper()
	source := communicationSource{}
	for _, table := range []string{"correspondents", "reach", "outbox_route", "outbox", "inbound"} {
		if err := st.visitCommunicationArchive(table, func(row communicationRow) error { source[table] = append(source[table], row); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	return source
}
