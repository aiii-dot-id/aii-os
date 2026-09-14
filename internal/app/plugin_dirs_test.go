package app

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
func TestPluginDataRootIsAbsoluteForARelativeLedger(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got := pluginDataRoot("data/ledger.jsonl")
	if !filepath.IsAbs(got) {
		t.Fatalf("plugin data root %q is not absolute", got)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(dir, "data"))
	gotReal, _ := filepath.EvalSymlinks(got)
	if want != "" && gotReal != "" && gotReal != want {
		t.Fatalf("plugin data root = %q, want %q", gotReal, want)
	}
	if abs := pluginDataRoot(filepath.Join(dir, "ledger.jsonl")); abs != dir {
		t.Fatalf("an absolute ledger keeps its directory: %q", abs)
	}
}
