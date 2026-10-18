package install

import (
	"os"
	"path/filepath"
	"testing"
)

// A fresh install is laid out the new way: the operator's files go in
// config/, not loose beside the binary.
func TestAFreshInstallKeepsTheOperatorsFileInConfig(t *testing.T) {
	dir := t.TempDir()
	if err := writeConfig(dir, 8080); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ConfigDirName, ConfigFileName)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("a fresh install did not write %s: %v", want, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ConfigFileName)); err == nil {
		t.Fatal("a fresh install still wrote a config loose beside the binary")
	}
}

// Anything that needs to reach a running identity reads its config for
// the port — so it has to find the config wherever that identity keeps
// it, in the new home and the old one alike.
func TestConfiguredPortFindsTheConfigInEitherHome(t *testing.T) {
	const slot = 3
	body := []byte(`{"dashboard":{"port":9123}}`)

	t.Run("new home", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ConfigDirName), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ConfigDirName, ConfigFileName), body, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ConfiguredPort(dir, slot); got != 9123 {
			t.Fatalf("port from config/: got %d, want 9123", got)
		}
	})

	t.Run("installed before the move", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ConfigFileName), body, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ConfiguredPort(dir, slot); got != 9123 {
			t.Fatalf("port from a config beside the binary: got %d, want 9123", got)
		}
	})

	t.Run("no config at all falls back to the slot", func(t *testing.T) {
		if got := ConfiguredPort(t.TempDir(), slot); got != Port(slot) {
			t.Fatalf("got %d, want the slot port %d", got, Port(slot))
		}
	})
}

// An identity that has been running for weeks must not stop booting
// over a directory it never heard of.
func TestAConfigFromBeforeTheMoveStillWins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := ConfigPathIn(dir), filepath.Join(dir, ConfigFileName); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
