package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/workrecord"
)

// workHome is an identity's home as the export meets it: its config naming
// port, a database holding one work session with one call, and its record's
// lock file. The database is closed: what reads it next is the test's.
func workHome(t *testing.T, port int, token string) (home, db string) {
	t.Helper()
	home = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"dashboard":{"port":%d,"require_token":%t,"access_token":%q}}`, port, token != "", token)
	if err := os.WriteFile(filepath.Join(home, "config", "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	db = filepath.Join(home, "data", "aii.db")
	st, err := store.NewForTest(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.StartWorkSession(t.Context(), "ws_home", "the home's session"); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordToolStart("turn-1", 1, "main", "m", "", "shell", `{"command":"ls"}`); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordToolDoneMeasured("turn-1", 1, "shell", `{"command":"ls"}`, "home-output", interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", ledger.LedgerDirLockName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return home, db
}

func workFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func runExport(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := runWork(append([]string{"export"}, args...), &out, &errs)
	return code, out.String(), errs.String()
}

func TestAStoppedIdentitysWorkRecordIsExportedFromItsHome(t *testing.T) {
	home, _ := workHome(t, workFreePort(t), "")
	out := filepath.Join(t.TempDir(), "work.jsonl")
	code, stdout, stderr := runExport(t, "-dir", home, "-o", out)
	if code != 0 {
		t.Fatalf("export: %d %s", code, stderr)
	}
	if strings.TrimSpace(stdout) != out || !strings.Contains(stderr, "read from the stopped identity's home") || !strings.Contains(stderr, "may hold secrets") {
		t.Errorf("the command does not say where it wrote, how it read, and what the file holds: %q %q", stdout, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := workrecord.Check(bytes.NewReader(raw)); err != nil || n != 1 {
		t.Fatalf("the file is not one whole export: %d %v", n, err)
	}
	if !strings.Contains(string(raw), `"warning":"`+workrecord.Warning) || !strings.Contains(string(raw), "home-output") || !strings.Contains(string(raw), `"id":"ws_home"`) {
		t.Errorf("the file lacks its warning or its session:\n%s", raw)
	}
	if owner, err := fileperm.IsRestrictedToOwner(out); err != nil || !owner {
		t.Errorf("the file is open to others: %v %v", owner, err)
	}
	if code, _, stderr := runExport(t, "-dir", home, "-o", out); code != 1 || !strings.Contains(stderr, "already exists") {
		t.Errorf("a second export to the same file: %d %s", code, stderr)
	}
	if again, _ := os.ReadFile(out); !bytes.Equal(again, raw) {
		t.Error("the refused export changed the file")
	}
	if code, _, stderr := runExport(t, "-dir", home, "-session", "ws_nope", "-o", filepath.Join(t.TempDir(), "none.jsonl")); code != 1 || !strings.Contains(stderr, "no work session ws_nope") {
		t.Errorf("a session the record does not hold: %d %s", code, stderr)
	}
}

// A RUNNING IDENTITY WHOSE DASHBOARD DOES NOT ANSWER IS NOT READ AT ALL. Its
// record's lock is held, as a running identity holds it: the command reads
// nothing, says why, and leaves no file. Removing the lock's check opens the
// running identity's store from a second program.
func TestARunningIdentityIsNeverReadByASecondOpener(t *testing.T) {
	home, _ := workHome(t, workFreePort(t), "")
	lock, err := os.OpenFile(filepath.Join(home, "data", ledger.LedgerDirLockName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "work.jsonl")
	code, _, stderr := runExport(t, "-dir", home, "-o", out)
	if code != 1 || !strings.Contains(stderr, "is running") || !strings.Contains(stderr, "nothing was read") {
		t.Fatalf("a running identity without its dashboard: %d %s", code, stderr)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("the refused export left %d file(s) behind", len(left))
	}
}

func TestARunningIdentityIsReadThroughItsHost(t *testing.T) {
	const token = "a-token-of-honest-length-for-this-test-0123456789"
	var served bytes.Buffer
	src, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if err := src.StartWorkSession(t.Context(), "ws_live", "the live session"); err != nil {
		t.Fatal(err)
	}
	s := dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{WorkExport: func(ctx context.Context, since time.Time, session, project string, w io.Writer) error {
		_, err := workrecord.Write(ctx, io.MultiWriter(w, &served), src, nil, workrecord.Filter{Since: since, Session: session, Project: project}, time.Now())
		return err
	}})
	s.SetAccessToken(true, token)
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	_, portText, _ := net.SplitHostPort(bound)
	port, _ := strconv.Atoi(portText)
	home, db := workHome(t, port, token)
	if err := os.Remove(db); err != nil {
		t.Fatal(err)
	}
	key, err := install.WriteServingKey(home)
	if err != nil {
		t.Fatal(err)
	}
	s.SetServingKey(key)
	lock, err := os.OpenFile(filepath.Join(home, "data", ledger.LedgerDirLockName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "work.jsonl")
	code, _, stderr := runExport(t, "-dir", home, "-session", "ws_live", "-o", out)
	if code != 0 || !strings.Contains(stderr, "read through the running identity") {
		t.Fatalf("export through the host: %d %s", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, served.Bytes()) || !strings.Contains(string(raw), `"id":"ws_live"`) {
		t.Errorf("the file is not what the host served:\n%s", raw)
	}
	if strings.Contains(string(raw)+stderr, token) {
		t.Error("the token reached the file or the command's words")
	}
}
