package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func historicalCommunications(t *testing.T, dbPath string) {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ddl, err := os.ReadFile("../store/testdata/communications-before-labels.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA foreign_keys=OFF; DROP TABLE inbound; DROP TABLE outbox;" + string(ddl)); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO correspondents VALUES ('person-1','quinn',NULL,1);
INSERT INTO reach VALUES ('email','person-1','email','old@example.test',5,0,1),('signal','person-1','signal','+1555',0,1,2);
INSERT INTO outbox(id,to_role,to_identity,content,created_ms) VALUES ('pending','peer','person-1','hello',11),('unknown','peer','unknown','not to be sent',12);
INSERT INTO outbox_route VALUES ('pending','signal');`); err != nil {
		t.Fatal(err)
	}
}

func communicationsApp(t *testing.T, contacts string) *App {
	t.Helper()
	a := liveApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "aii.db")
	if err := a.store.Close(); err != nil {
		t.Fatal(err)
	}
	historicalCommunications(t, path)
	st, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	a.store = st
	t.Cleanup(func() { st.Close() })
	cfg := defaultConfig()
	cfg.SourcePath = a.cfg.SourcePath
	cfg.Contacts = nil
	cfg.contactsPresent = false
	if contacts != "" {
		if err := json.Unmarshal([]byte(contacts), &cfg.Contacts); err != nil {
			t.Fatal(err)
		}
		cfg.contactsPresent = true
	}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a.cfg, err = LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestCommunicationsUpgradeConfigOwnershipAndDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, contacts string
		want           int
	}{
		{"absent", "", 2}, {"empty", "[]", 0}, {"null", "null", 0},
		{"current", `[{"name":"quinn","channel":"signal","address":"+1999","wake":false}]`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := communicationsApp(t, tc.contacts)
			before, err := os.ReadFile(a.cfg.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.importLegacyContacts(saveConfig); err != nil {
				t.Fatal(err)
			}
			got := a.configSnapshot().Contacts
			if len(got) != tc.want {
				t.Fatalf("wrong contacts: %+v", got)
			}
			if tc.name == "absent" && (!got[0].Wake || got[0].Channel != "signal" || got[1].Wake) {
				t.Fatalf("order/wake changed: %+v", got)
			}
			if tc.name == "current" && (got[0].Address != "+1999" || got[0].Wake) {
				t.Fatalf("explicit contact overwritten: %+v", got)
			}
			after, err := os.ReadFile(a.cfg.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if tc.contacts != "" && string(before) != string(after) {
				t.Fatal("explicit config was rewritten")
			}
			reloaded, err := LoadConfig(a.cfg.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, reloaded.Contacts) {
				t.Fatal("contacts not durable")
			}
			// Revoke everything after successful import. Restart cannot resurrect it.
			if tc.name == "absent" {
				reloaded.Contacts = []Contact{}
				if _, err := saveConfig(reloaded); err != nil {
					t.Fatal(err)
				}
				a.cfg = reloaded
				if err := a.importLegacyContacts(saveConfig); err != nil {
					t.Fatal(err)
				}
				if len(a.cfg.Contacts) != 0 {
					t.Fatal("revoked contacts resurrected")
				}
			}
		})
	}
}

func TestCommunicationsUpgradeRestrictsDelivery(t *testing.T) {
	a := communicationsApp(t, "")
	if err := a.importLegacyContacts(saveConfig); err != nil {
		t.Fatal(err)
	}
	email := installReasonedAdapter(t, a, "org.example.email", "email", "", "")
	signal := installReasonedAdapter(t, a, "org.example.signal", "signal", "", "")
	// Reverse current preferences; an old channel restriction still wins.
	a.cfg.Contacts[0], a.cfg.Contacts[1] = a.cfg.Contacts[1], a.cfg.Contacts[0]
	// A coincidentally matching new name must not release unresolved old mail.
	contact(t, a, Contact{Name: "unknown", Channel: "email", Address: "wrong@example.test"})
	mail := outboxRow(t, a, "pending")
	if err := a.deliverOne(context.Background(), mail, a.channelRoutes(context.Background())); err != nil {
		t.Fatal(err)
	}
	if len(signal.calls) != 1 || len(email.calls) != 0 {
		t.Fatalf("channel restriction lost: signal=%v email=%v", signal.calls, email.calls)
	}
	if err := a.deliverOne(context.Background(), outboxRow(t, a, "unknown"), a.channelRoutes(context.Background())); err == nil {
		t.Fatal("unresolved recipient was guessed")
	}
	if len(email.calls) != 0 {
		t.Fatal("unresolved mail was sent")
	}
	// No matching adapter means no fallback and no fabricated delivery attempt.
	mail.ID = "not-persisted"
	if err := a.deliverOne(context.Background(), mail, map[string]channelRoute{"email": {Send: email.name, Plugin: "org.example.email"}}); err == nil {
		t.Fatal("missing pinned adapter accepted")
	}
	if len(email.calls) != 0 {
		t.Fatal("fallback used despite restriction")
	}
}

func TestCommunicationsUpgradeConfigFailureRetriesWithoutDataLoss(t *testing.T) {
	a := communicationsApp(t, "")
	fault := errors.New("injected config persistence failure")
	if err := a.importLegacyContacts(func(*Config) (bool, error) { return false, fault }); !errors.Is(err, fault) {
		t.Fatalf("failure hidden: %v", err)
	}
	if len(a.cfg.Contacts) != 0 {
		t.Fatal("unpersisted contacts activated")
	}
	if err := a.importLegacyContacts(saveConfig); err != nil {
		t.Fatal(err)
	}
	if len(a.cfg.Contacts) != 2 {
		t.Fatal("retry lost source")
	}
}

func TestCommunicationsUpgradeDoesNotRecreateMissingConfig(t *testing.T) {
	a := communicationsApp(t, "")
	path := a.cfg.SourcePath
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := a.importLegacyContacts(saveConfig); err == nil {
		t.Fatal("missing config was treated as an import destination")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config silently recreated: %v", err)
	}
	if len(a.configSnapshot().Contacts) != 0 {
		t.Fatal("missing config activated contacts")
	}
}

func TestCommunicationsUpgradeRunsAtNormalBoot(t *testing.T) {
	dir := t.TempDir()
	key, record, db := birthFixture(t, dir, "CommunicationsUpgrade")
	buildPriorProjection(t, record, db)
	historicalCommunications(t, db)
	cfg := safebootConfig(t, dir, "CommunicationsUpgrade", key, record, db)
	cfg.Contacts = nil
	cfg.contactsPresent = false
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	a := New(loaded)
	defer stopApp(t, a)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("intact history made SAFE: %s", reason)
	}
	if !a.live.Load() || len(a.configSnapshot().Contacts) != 2 {
		t.Fatal("normal boot did not complete contact import")
	}
}

func TestCommunicationsUpgradeContactPresenceHasOneDecoder(t *testing.T) {
	seed := defaultConfig()
	if !seed.contactsPresent {
		t.Fatal("embedded contact presence was not decoded")
	}
	for _, tc := range []struct {
		raw     string
		present bool
	}{
		{`{}`, false}, {`{"contacts":null}`, true}, {`{"contacts":[]}`, true},
	} {
		cfg := defaultConfig()
		if err := json.Unmarshal([]byte(tc.raw), cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.contactsPresent != tc.present {
			t.Fatalf("presence for %s = %v", tc.raw, cfg.contactsPresent)
		}
	}
	a, b := *seed, *seed
	a.contactsPresent = false
	blankLiveAppliable(&a)
	blankLiveAppliable(&b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("decode provenance announced as a restart-only setting")
	}
}
