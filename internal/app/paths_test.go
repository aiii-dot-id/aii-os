package app

import (
	"github.com/aiii-dot-id/aii-os/internal/install"
	"os"
	"path/filepath"
	"testing"
)

// THE OPERATOR'S FILES LIVE TOGETHER. A new identity keeps config.json
// and providers.json in config/, beside data/ and log/ — one directory
// holding what the identity may read and must not rewrite.
func TestANewIdentityKeepsTheOperatorsFilesInConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	want := filepath.Join(ConfigDirName, ConfigFileName)
	if got := install.ConfigPathIn(""); got != want {
		t.Fatalf("a fresh install resolves to %q, want %q", got, want)
	}

	cfg, err := LoadConfig("") // FIRSTBOOT
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

func TestAnIdentityInstalledBeforeTheMoveStillBoots(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(ConfigFileName, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := install.ConfigPathIn(""); got != ConfigFileName {
		t.Fatalf("an existing config beside the binary must win, got %q", got)
	}
	cfg, err := LoadConfig(install.ConfigPathIn(""))
	if err != nil {
		t.Fatalf("the old location must still load: %v", err)
	}
	if got := providerFilePath(cfg.SourcePath); got != ProvidersFileName {
		t.Fatalf("providers.json must stay beside the config that loaded, got %q", got)
	}

	// And once it has moved, the new home is what answers.
	if err := os.MkdirAll(ConfigDirName, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ConfigFileName, filepath.Join(ConfigDirName, ConfigFileName)); err != nil {
		t.Fatal(err)
	}
	if got, want := install.ConfigPathIn(""), filepath.Join(ConfigDirName, ConfigFileName); got != want {
		t.Fatalf("after the move it must resolve to %q, got %q", want, got)
	}
}

// ConfigPathIn answers for a directory the caller never enters — what
// the subcommands need, since they take -dir and do not chdir.
func TestConfigPathInAnswersForADirectoryItDoesNotEnter(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, ConfigDirName, ConfigFileName)
	if got := install.ConfigPathIn(home); got != want {
		t.Fatalf("a fresh directory resolves to %q, want %q", got, want)
	}
	legacy := filepath.Join(home, ConfigFileName)
	if err := os.WriteFile(legacy, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := install.ConfigPathIn(home); got != legacy {
		t.Fatalf("an existing config must win, got %q", got)
	}
}
