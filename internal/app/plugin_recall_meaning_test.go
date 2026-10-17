package app

import (
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// The application hands its plugin broker what reads memories by meaning.
// The broker and the layer each had their half for a year of releases and
// nothing joined them: a plugin's memory.recall answered "no source" on
// every identity, whatever source the identity's own recall was reading by.
// This is the joint, held where it is made.
func TestThePluginBrokerIsHandedTheSourceOfVectors(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	cfg := defaultConfig()
	cfg.Tools.CWD = filepath.Join(root, "home")
	cfg.SourcePath = filepath.Join(root, "config", "config.json")
	cfg.Identity.LedgerPath = filepath.Join(root, "data", "ledger.jsonl")
	cfg.Identity.KeyPath = filepath.Join(root, "keys", "identity.sec")
	cfg.Identity.DBPath = filepath.Join(root, "data", "aii.db")
	a := &App{cfg: cfg}
	opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Broker == nil {
		t.Fatal("the application built no plugin broker")
	}
	if !opts.Broker.ReadsByMeaning() {
		t.Fatal("the application's plugin broker was handed no source of vectors: a plugin's recall can never be read by meaning")
	}
}
