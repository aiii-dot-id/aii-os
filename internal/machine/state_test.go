package machine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// STATE.JSON IS WRITTEN WHOLE OR NOT AT ALL. What a crash between the
// temporary file and its rename leaves — the old file, and a temporary
// beside it — reads as the old state; the next save replaces the old one
// and leaves no temporary. A member this build does not know survives a
// save, so a program run after a newer one drops nothing the newer wrote.
func TestTheStateFileSurvivesACrashBeforeItsRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, stateFileName)
	old := State{Identities: []Record{{Slot: "/s/identity-0", Identity: "aa", PublicKey: "k", How: RecordedByRegister, At: "2026-10-09T00:00:00Z"}}}
	if err := saveState(path, old); err != nil {
		t.Fatal(err)
	}
	// The crash: a whole new state written to the temporary name the
	// writer uses, and never renamed.
	crashed := filepath.Join(dir, "."+stateFileName+".tmp-123456")
	if err := os.WriteFile(crashed, []byte(`{"identities":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(path)
	if err != nil || len(got.Identities) != 1 || got.Identities[0].Identity != "aa" {
		t.Fatalf("after a crash before the rename the old state must read: %+v, %v", got, err)
	}

	raw := strings.Replace(mustRead(t, path), `"identities"`, `"references": {"kept": true}, "identities"`, 1)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveState(path, got.with(Record{Slot: "/s/identity-1", Identity: "bb", PublicKey: "k2", How: RecordedAtFirstLink, At: "2026-10-09T00:00:01Z"})); err != nil {
		t.Fatal(err)
	}
	after := mustRead(t, path)
	if !strings.Contains(after, `"kept": true`) || !strings.Contains(after, `"bb"`) || !strings.Contains(after, `"aa"`) {
		t.Fatalf("a save dropped what it does not know, or a record: %s", after)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+stateFileName+".tmp-") && e.Name() != filepath.Base(crashed) {
			t.Errorf("a save left its temporary behind: %s", e.Name())
		}
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("state.json mode = %v, want 0600", st.Mode().Perm())
		}
	}
}

// A STATE FILE THAT DOES NOT READ IS A REFUSAL, never an empty state: a
// service that started from nothing would self-register over records it
// could not read.
func TestAStateFileThatDoesNotReadRefusesTheService(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := NewServer(Options{Dir: dir}).Serve(ctx)
	if err == nil || !strings.Contains(err.Error(), "does not read") {
		t.Fatalf("a torn state file: want a refusal, got %v (nil: the service served from an empty state)", err)
	}
}

// A RECORD THAT CANNOT BE MADE DURABLE IS NOT MADE: the link is refused
// with its reason, the service's memory keeps the old state, and once the
// directory is writable again the same identity records itself.
func TestARecordThatCannotBeKeptIsNotAcknowledged(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a directory mode does not stop this writer here (Windows ACLs, or root)")
	}
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-0")
	kp := newKey(t)
	r := serve(t, Options{Dir: dir, StartupSlots: startupSlots(slot)})
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	l := dialRaw(t, dir)
	if _, err := l.ask(MsgHandshake, l.signed(kp, slot, false)); refusal(err) != ReasonNotRecorded {
		t.Fatalf("an unwritable state: want %s, got %v", ReasonNotRecorded, err)
	}
	r.srv.mu.Lock()
	n := len(r.srv.state.Identities)
	r.srv.mu.Unlock()
	if n != 0 {
		t.Fatalf("a record that was not kept is in the service's memory")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	l = dialRaw(t, dir)
	if _, err := l.ask(MsgHandshake, l.signed(kp, slot, false)); err != nil {
		t.Fatalf("once writable the identity records itself: %v", err)
	}
}
