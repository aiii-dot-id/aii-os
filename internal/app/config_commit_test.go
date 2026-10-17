package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A CHANGE CHECKED AGAINST A CONFIGURATION THAT MOVED IS REFUSED WHOLE.
// A card's key and a profile edit are each derived from a snapshot; when
// another writer committed first, the commit answers errConfigMoved and
// saves nothing, swaps nothing, and does none of its custody work.
func TestACommitAgainstAMovedConfigurationSavesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	a := New(&Config{SourcePath: path})
	if _, err := saveConfig(a.cfg); err != nil {
		t.Fatal(err)
	}
	orig := a.configSnapshot()
	if err := a.commitConfig(orig, func(c *Config) { c.Dashboard.Expert = true }, nil, nil); err != nil {
		t.Fatalf("the first writer: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	custody, settled := false, false
	err = a.commitConfig(orig, func(c *Config) { c.Dashboard.Port = 4242 },
		func() error { custody = true; return nil }, func() { settled = true })
	if !errors.Is(err, errConfigMoved) {
		t.Fatalf("a commit against a moved configuration: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || a.configSnapshot().Dashboard.Port == 4242 || custody || settled {
		t.Fatalf("the refused commit saved=%v swapped=%v custody=%v settle=%v",
			!bytes.Equal(before, after), a.configSnapshot().Dashboard.Port == 4242, custody, settled)
	}
	if !a.configSnapshot().Dashboard.Expert {
		t.Fatal("the first writer's change was lost")
	}
}
