package app

import (
	"path/filepath"
	"testing"
)

// The plugin-plane directories are absolute even when the identity's
// config names its ledger relative to the identity directory, the way
// an installed slot does: they cross into children that do not share
// the host's working directory.
func TestPluginDataRootIsAbsoluteForARelativeLedger(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got := identityDataDir("data/ledger.jsonl")
	if !filepath.IsAbs(got) {
		t.Fatalf("plugin data root %q is not absolute", got)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(dir, "data"))
	gotReal, _ := filepath.EvalSymlinks(got)
	if want != "" && gotReal != "" && gotReal != want {
		t.Fatalf("plugin data root = %q, want %q", gotReal, want)
	}
	if abs := identityDataDir(filepath.Join(dir, "ledger.jsonl")); abs != dir {
		t.Fatalf("an absolute ledger keeps its directory: %q", abs)
	}
}
