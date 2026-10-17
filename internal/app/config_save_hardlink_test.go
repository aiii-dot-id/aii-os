package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func TestASavedConfigurationLeavesNothingOfItUnderAnotherName(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.json", "providers.json"} {
		path := filepath.Join(dir, name)
		second := filepath.Join(dir, "home", "second-"+name)
		if err := os.MkdirAll(filepath.Dir(second), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := writeFileAtomic(path, []byte(`{"api_key":"THE EARLIER KEY"}`)); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(path, second); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		policy := firewall.DefaultPolicy()
		policy.SetPlaces([]*firewall.Rule{{ID: "sub.config", Kind: firewall.KindSubstrate, Path: path, Reason: "kept", Enforced: true}})
		if policy.Covered(second) == nil {
			t.Fatalf("before the save, the second name of %s is the kept file", name)
		}
		if published, err := writeFileAtomic(path, []byte(`{"api_key":"THE NEW KEY"}`)); err != nil || !published {
			t.Fatalf("the save of %s: published=%v err=%v", name, published, err)
		}
		if got, _ := os.ReadFile(path); string(got) != `{"api_key":"THE NEW KEY"}` {
			t.Errorf("%s holds what was saved: %q", name, got)
		}
		if got, err := os.ReadFile(second); err != nil || len(got) != 0 {
			t.Errorf("after the save, the second name of %s holds nothing of what it held: %q %v", name, got, err)
		}
	}
	single := filepath.Join(dir, "single.json")
	for _, body := range []string{"{}", `{"x":1}`} {
		if published, err := writeFileAtomic(single, []byte(body)); err != nil || !published {
			t.Fatalf("a save of a file with one name: published=%v err=%v", published, err)
		}
		if got, _ := os.ReadFile(single); string(got) != body {
			t.Errorf("a file with one name holds what was saved: %q", got)
		}
	}
}

// A ROLLBACK THAT PUTS THE CONFIGURATION BACK LEAVES NOTHING OF THE NEWER
// FILE UNDER ANOTHER NAME. The rollback renamed the copy kept before the
// update over config.json with a plain rename, so a hard link to the
// newer file held it after, no longer the kept file. It now puts the copy
// back through atomicfile.Replace, which empties the file it replaces
// when that file still has a name. The newer file's bytes are still set
// aside beside it, as before.
func TestARollbackLeavesNothingOfTheNewerConfigurationUnderAnotherName(t *testing.T) {
	path := writeConfigFile(t)
	keepConfigBeforeUpdate(path, updates.ErrBootRecoveryPending)
	newer := `{"prompt":{"recent_turns":20},"api_key":"THE NEWER KEY"}`
	if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(filepath.Dir(path), "second.txt")
	if err := os.Link(path, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if err := restoreConfigBeforeUpdate(path); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); got != before {
		t.Errorf("the rollback puts back the configuration from before the update: %q", got)
	}
	if got := readText(t, second); got != "" {
		t.Errorf("the other name of the newer file holds nothing of it after the rollback: %q", got)
	}
	if got := readText(t, path+configRolledBack); got != newer {
		t.Errorf("the newer file's bytes are set aside beside it, as before: %q", got)
	}
}

func TestADroppedCopyOfTheConfigurationLeavesNothingUnderAnotherName(t *testing.T) {
	path := writeConfigFile(t)
	keepConfigBeforeUpdate(path, updates.ErrBootRecoveryPending)
	second := filepath.Join(filepath.Dir(path), "second.txt")
	if err := os.Link(path+configBeforeUpdate, second); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	dropConfigBeforeUpdate(path, nil)
	if _, err := os.Stat(path + configBeforeUpdate); !os.IsNotExist(err) {
		t.Errorf("the copy is removed: %v", err)
	}
	if got := readText(t, second); got != "" {
		t.Errorf("another name of the copy holds nothing of it: %q", got)
	}
}

func TestAnEmptyCopyOfTheConfigurationIsNoCopy(t *testing.T) {
	path := writeConfigFile(t)
	if err := os.WriteFile(path+configBeforeUpdate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreConfigBeforeUpdate(path); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); got != before {
		t.Errorf("a rollback over an empty copy leaves config.json as it is: %q", got)
	}
	keepConfigBeforeUpdate(path, updates.ErrBootRecoveryPending)
	if got := readText(t, path+configBeforeUpdate); got != before {
		t.Errorf("an update's first boot writes the copy over an empty one: %q", got)
	}
}
