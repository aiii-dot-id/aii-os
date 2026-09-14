package genesis

import (
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestBirthDoesNotMintRing1(t *testing.T) {
	dir := t.TempDir()
	root, bundle := mintTestRing0(t, "# Constitution\nHonesty.")

	res, err := Birth(&BirthConfig{
		Name:        "Blank",
		Ring0Bundle: bundle, Root: root.Env,
		KeyPath:    filepath.Join(dir, "id.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Ledger.Close()

	events, err := ledger.ReadAll(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != ledger.EventRing0Genesis {
		t.Fatalf("birth events = %+v, want only ring0.genesis", events)
	}
}
