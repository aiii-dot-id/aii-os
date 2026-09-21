package app

import (
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
func TestANewIdentityKeepsTheOperatorsFilesInConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	want := filepath.Join(ConfigDirName, ConfigFileName)
	if got := DefaultConfigPath(); got != want {
		t.Fatalf("a fresh install resolves to %q, want %q", got, want)
	}

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("first boot: %v", err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("first boot did not write %s: %v", want, err)
	}
	if got := providerFilePath(cfg.SourcePath); got != filepath.Join(ConfigDirName, ProvidersFileName) {
		t.Fatalf("providers.json must follow config.json, got %q", got)
	}
}

// .
// .
// .
func TestAnIdentityInstalledBeforeTheMoveStillBoots(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(ConfigFileName, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := DefaultConfigPath(); got != ConfigFileName {
		t.Fatalf("an existing config beside the binary must win, got %q", got)
	}
	cfg, err := LoadConfig(DefaultConfigPath())
	if err != nil {
		t.Fatalf("the old location must still load: %v", err)
	}
	if got := providerFilePath(cfg.SourcePath); got != ProvidersFileName {
		t.Fatalf("providers.json must stay beside the config that loaded, got %q", got)
	}

	// .
	if err := os.MkdirAll(ConfigDirName, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ConfigFileName, filepath.Join(ConfigDirName, ConfigFileName)); err != nil {
		t.Fatal(err)
	}
	if got, want := DefaultConfigPath(), filepath.Join(ConfigDirName, ConfigFileName); got != want {
		t.Fatalf("after the move it must resolve to %q, got %q", want, got)
	}
}

// .
// .
func TestConfigPathInAnswersForADirectoryItDoesNotEnter(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, ConfigDirName, ConfigFileName)
	if got := ConfigPathIn(home); got != want {
		t.Fatalf("a fresh directory resolves to %q, want %q", got, want)
	}
	legacy := filepath.Join(home, ConfigFileName)
	if err := os.WriteFile(legacy, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ConfigPathIn(home); got != legacy {
		t.Fatalf("an existing config must win, got %q", got)
	}
}
