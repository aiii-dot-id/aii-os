package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// project.json claimed to be written atomically. It was written in an
// order that survives a crash, in a way that does not survive a power
// loss, under a name two writers would collide on.

// The fixed ".tmp" name was the collision: two runtimes sharing a
// projects root wrote the same path and renamed each other's bytes into
// place. writeManifest is a free function, so this reaches it directly
// rather than through the Manager's mutex, which is exactly the
// cross-process case the mutex never covered.
func TestConcurrentWritersDoNotCorruptTheManifest(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mf := Manifest{Name: "ledger work", State: "open", CreatedBy: "identity"}
			if err := writeManifest(dir, &mf); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("a concurrent write failed: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		t.Fatalf("no manifest survived 16 concurrent writers: %v", err)
	}
	var mf Manifest
	if err := json.Unmarshal(b, &mf); err != nil {
		t.Fatalf("the manifest is not valid JSON — writers interleaved: %v\n%s", err, b)
	}
	if mf.Name != "ledger work" {
		t.Fatalf("the manifest lost its content: %+v", mf)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp debris survived: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Fatalf("the directory holds more than its manifest: %v", entries)
	}
}

func TestRepeatedRewritesLeaveNoDebris(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("ledger work", "the durable half", "identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := m.SetState(p.ID, "closed"); err != nil {
			t.Fatal(err)
		}
		if _, err := m.SetState(p.ID, "open"); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != manifestName {
		t.Fatalf("the project directory holds more than its manifest: %v", entries)
	}
	got, err := m.Load(p.ID)
	if err != nil {
		t.Fatalf("the manifest did not survive repeated rewrites: %v", err)
	}
	if got.State != "open" || got.Name != "ledger work" {
		t.Fatalf("the manifest lost its content: %+v", got.Manifest)
	}
	// The operator must be able to read it; CreateTemp makes 0600.
	info, err := os.Stat(filepath.Join(p.Dir, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("the manifest kept CreateTemp's private mode: %v", info.Mode().Perm())
	}
}

// A directory whose manifest is corrupt is skipped — out loud (R18) —
// and must not take the healthy projects beside it down.
func TestAnUnreadableProjectIsSkippedNotHidden(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	good, err := m.Create("real work", "", "identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "broken")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, manifestName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != good.ID {
		t.Fatalf("one broken manifest changed which projects exist: %+v", list)
	}
}

// A write that cannot even begin reports the failure; it does not
// return a Project the caller will believe in.
func TestAWriteThatCannotBeginIsAnError(t *testing.T) {
	if err := writeManifest(filepath.Join(t.TempDir(), "no-such-dir"), &Manifest{}); err == nil {
		t.Fatal("writing into a directory that does not exist reported success")
	}
}
