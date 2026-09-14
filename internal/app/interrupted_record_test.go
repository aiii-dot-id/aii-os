package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
func TestAnInterruptedTurnIsMarkedBesideItNotInsideIt(t *testing.T) {
	dir := t.TempDir()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crypto.SaveKeyPair(kp, filepath.Join(dir, "identity.sec")); err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lg.Close() })
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.store = st
	a.rings = ring.NewManager()
	a.engine = identity.NewEngine(st, &ledgerAdapter{Ledger: lg, kp: kp, st: st}, a.rings, nil)

	const said = "I have read the file and I think the bug is in the parser."
	a.recordInterruptedTurn(conversation.Result{Spoken: said, Interrupted: "stopped by the operator"})

	turns, err := st.RecentTurnsIncludingSystem(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("want the reply and its marker as TWO turns, got %d: %+v", len(turns), turns)
	}

	var resident, system string
	for _, turn := range turns {
		switch turn.Role {
		case "resident":
			resident = turn.Content
		case "system":
			system = turn.Content
		}
	}
	if resident != said {
		t.Fatalf("the identity's words were not preserved verbatim: %q", resident)
	}
	if strings.Contains(resident, "incomplete") {
		t.Fatalf("THE SUBSTRATE SPOKE IN THE IDENTITY'S VOICE — the marker is inside their reply: %q", resident)
	}
	if system == "" {
		t.Fatal("an incomplete reply with no marker reads as a finished answer")
	}
	if !strings.Contains(system, "stopped by the operator") {
		t.Fatalf("the marker must name what ended the turn: %q", system)
	}
}

// .
func TestASilentInterruptionRecordsNothing(t *testing.T) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	a.recordInterruptedTurn(conversation.Result{})
}
