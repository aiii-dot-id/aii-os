package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestAStartThatWouldChooseIdentityFilesStopsAtThePageAndTheOperatorChooses(t *testing.T) {
	home := t.TempDir()
	dead := filepath.Join(t.TempDir(), "DD60E10E-DEAD", "Documents")
	kept := filepath.Join(home, "data", "ledger.jsonl")
	stray := filepath.Join(home, "ledger.jsonl")
	keptDB := filepath.Join(home, "data", "aii.db")
	for path, body := range map[string]string{kept: "the record", stray: "a stray copy", keptDB: "its database",
		filepath.Join(home, "data", "segment-1-4.jsonl.gz"): "sealed history", filepath.Join(home, "data", "identity.sec"): "a key",
		filepath.Join(home, "data", "snapshot.key"): "a snapshot key"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := safebootConfig(t, home, "", filepath.Join(dead, "data", "identity.sec"), filepath.Join(dead, "data", "ledger.jsonl"), filepath.Join(dead, "data", "aii.db"))
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	choice := IdentityChoice{Home: home, Why: "mobile: ledger_path is ambiguous — " + kept + " and " + stray + " all exist in this container; this start does not choose between identity records", Files: []IdentityFiles{
		{Key: "ledger_path", Was: cfg.Identity.LedgerPath, Found: []string{kept, stray}},
		{Key: "db_path", Was: cfg.Identity.DBPath, Found: []string{keptDB}},
		{Key: "key_path", Was: cfg.Identity.KeyPath},
	}}
	a := New(cfg)
	if err := a.StartAuthenticatedEmbeddedAtChoice(choice); err != nil {
		t.Fatalf("the start did not stop at its page: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	reason, safe := a.SafeMode()
	if !safe || !strings.Contains(reason, safePostures[safeIdentityChoice].what) || !strings.Contains(reason, "ledger_path is ambiguous") || a.DashboardURL() == "" {
		t.Fatalf("not the page in its refused state: safe=%v url=%q reason=%q", safe, a.DashboardURL(), reason)
	}
	if a.ledger != nil || a.keyPair != nil {
		t.Fatal("a record was opened or a key loaded before the operator chose")
	}
	v := a.identityChoiceView()
	if v == nil || len(v.Files) != 3 || len(v.Files[0].Found) != 2 || v.Files[0].Found[0].Bytes != int64(len("the record")) || v.Files[2].Standard != filepath.Join(home, "data", "identity.sec") {
		t.Fatalf("the page does not name what was found: %+v", v)
	}

	if err := a.chooseIdentityFiles(map[string]string{"ledger_path": filepath.Join(dead, "data", "ledger.jsonl")}); err == nil || !strings.Contains(err.Error(), "Nothing was written") {
		t.Fatalf("a choice of a file that was not found: %v", err)
	}
	if on, err := ReadConfig(cfg.SourcePath); err != nil || on.Identity.LedgerPath != cfg.Identity.LedgerPath {
		t.Fatalf("a refused choice wrote the configuration: %+v %v", on, err)
	}
	if err := a.chooseIdentityFiles(map[string]string{"ledger_path": kept}); err == nil || !strings.Contains(err.Error(), "the database") {
		t.Fatalf("a choice with no pick for the database found: %v", err)
	}
	if err := a.chooseIdentityFiles(map[string]string{"ledger_path": kept, "db_path": keptDB}); err != nil {
		t.Fatal(err)
	}
	on, err := ReadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	// Inside the home, written relative to it as the defaults are.
	if on.Identity.LedgerPath != filepath.Join("data", "ledger.jsonl") || on.Identity.KeyPath != filepath.Join("data", "identity.sec") || on.Identity.DBPath != filepath.Join("data", "aii.db") {
		t.Fatalf("the choice is not what the configuration holds: %+v", on.Identity)
	}
	for path, body := range map[string]string{kept: "the record", stray: "a stray copy"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != body {
			t.Fatalf("%s was touched: %q %v", path, got, err)
		}
	}
}

func TestTheBootDecisionsRefusalsBringUpThePage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		put    []string // relative to the home
		record bool
	}{
		{"a record the configuration does not name", []string{"data/ledger.jsonl", "data/identity.sec"}, true},
		{"a key and no record", []string{"vault/identity.sec"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Chdir(home)
			for _, p := range tc.put {
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("lived"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := safebootConfig(t, home, "", filepath.Join(home, "vault", "identity.sec"), filepath.Join(home, "vault", "ledger.jsonl"), filepath.Join(home, "vault", "aii.db"))
			if _, err := saveConfig(cfg); err != nil {
				t.Fatal(err)
			}
			a := New(cfg)
			if err := a.StartEmbedded(); err != nil {
				t.Fatalf("THE START ENDED IN ONE LINE, not the page: %v", err)
			}
			t.Cleanup(func() { stopApp(t, a) })
			reason, safe := a.SafeMode()
			if !safe || !strings.Contains(reason, "refusing FIRSTBOOT") || !strings.Contains(reason, safePostures[safeIdentityChoice].what) || a.DashboardURL() == "" {
				t.Fatalf("not the page: safe=%v reason=%q", safe, reason)
			}
			v := a.identityChoiceView()
			if v == nil || len(v.Files) != 3 {
				t.Fatalf("the page does not name the files: %+v", v)
			}
			if fileExists(filepath.Join(home, "vault", "ledger.jsonl")) {
				t.Fatal("a record was made")
			}
			if tc.record {
				if got := v.Files[0].Found; len(got) != 1 || got[0].Path != filepath.Join(home, "data", "ledger.jsonl") {
					t.Fatalf("the record found is not named: %+v", v.Files[0])
				}
				if err := a.chooseIdentityFiles(map[string]string{"ledger_path": filepath.Join(home, "data", "ledger.jsonl"), "key_path": filepath.Join(home, "data", "identity.sec")}); err != nil {
					t.Fatal(err)
				}
				on, err := ReadConfig(cfg.SourcePath)
				if err != nil || on.Identity.LedgerPath != filepath.Join("data", "ledger.jsonl") || on.Identity.KeyPath != filepath.Join("data", "identity.sec") {
					t.Fatalf("the choice is not in the configuration: %+v %v", on, err)
				}
				return
			}
			if err := a.chooseIdentityFiles(map[string]string{}); err == nil || !strings.Contains(err.Error(), "nothing to choose") {
				t.Fatalf("a home with no record was offered a choice: %v", err)
			}
		})
	}
}

func TestTheIdentityFilesPageOffersTheChoiceAndNoExport(t *testing.T) {
	home := t.TempDir()
	dead := filepath.Join(t.TempDir(), "DD60E10E-DEAD", "Documents")
	kept, stray := filepath.Join(home, "data", "ledger.jsonl"), filepath.Join(home, "ledger.jsonl")
	for path, body := range map[string]string{kept: "the record", stray: "a stray copy", filepath.Join(home, "data", "aii.db"): "its database"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := safebootConfig(t, home, "", filepath.Join(dead, "data", "identity.sec"), filepath.Join(dead, "data", "ledger.jsonl"), filepath.Join(dead, "data", "aii.db"))
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if err := a.StartAuthenticatedEmbeddedAtChoice(IdentityChoice{Home: home, Why: "mobile: ledger_path is ambiguous", Files: []IdentityFiles{
		{Key: "ledger_path", Was: cfg.Identity.LedgerPath, Found: []string{kept, stray}},
		{Key: "db_path", Was: cfg.Identity.DBPath, Found: []string{filepath.Join(home, "data", "aii.db")}},
		{Key: "key_path", Was: cfg.Identity.KeyPath},
	}}); err != nil {
		t.Fatalf("the start did not stop at its page: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	base := a.DashboardURL()
	client := signedIn(t, base, a.dashboardToken())
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{"Origin": []string{"http://" + u.Host}}
	for _, c := range client.Jar.Cookies(u) {
		header.Add("Cookie", c.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+u.Host+"/ws?interaction_version=1", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("the page signed in could not open a socket: %v", err)
	}
	defer conn.CloseNow()
	ask := func(id, action string) *dashboard.ServerMessage {
		t.Helper()
		raw, _ := json.Marshal(dashboard.ClientMessage{Type: "backups", RequestID: id, Backups: &dashboard.ContinuityRequest{Action: action}})
		if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
		return readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.RequestID == id })
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","page_key":"`+client.Transport.(*pageKeyed).pageKey()+`"}`)); err != nil {
		t.Fatal(err)
	}

	status := ask("status", "status")
	if status.Type != "backups" || status.Backups == nil || status.Backups.View == nil || status.Backups.View.IdentityChoice == nil || len(status.Backups.View.IdentityChoice.Files) != 3 || len(status.Backups.View.IdentityChoice.Files[0].Found) != 2 {
		t.Fatalf("the page does not offer the choice of the files found: %+v", status)
	}
	if export := ask("export", "identity_export"); export.Type != "error" || !strings.Contains(export.Message, "unknown action") {
		said := ""
		if export.Backups != nil {
			said = export.Backups.Said + export.Backups.Error
		}
		t.Fatalf("THE PAGE STILL EXPORTS: an export asked for was answered %q %q %s", export.Type, export.Message, said)
	}
	if _, err := os.Stat(filepath.Join(home, "data", "exports")); !os.IsNotExist(err) {
		t.Fatalf("something was written into the home's data/exports: %v", err)
	}
}
