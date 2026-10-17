package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestTheFloorKeepsTheRunningProgramsPlace(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		t.Fatal(err)
	}
	app, _ := stockSlotApp(t, map[string]string{"notes.md": "mine"})
	policy := app.ensureRing5Policy()
	if rule := policy.Covered(exe); rule == nil || rule.ID != "sub.binary" {
		t.Fatalf("the floor must keep the running program %s, got %+v", exe, rule)
	}
	floor := policy.LocalFloor()
	if !strings.Contains(floor, "- **"+filepath.Base(exe)+"** — Your running program is your body") || strings.Contains(floor, "aii-os") {
		t.Fatalf("the floor must name the program by its own name, and no aii-os:\n%s", floor)
	}

	// A grant that holds the program is refused; one beside it is not.
	if why := app.toolReg.RootRejectionReason(filepath.Dir(exe)); why == "" {
		t.Fatalf("a grant holding the running program %s was accepted", exe)
	}
	if why := app.toolReg.RootRejectionReason(t.TempDir()); why != "" {
		t.Fatalf("an unrelated grant was refused: %s", why)
	}

	// A home that holds the program: the program is refused as the
	// floor's place, and the rest of that home is the identity's.
	reg := tools.NewRegistry(filepath.Dir(exe), policy, tools.Timeouts{})
	var sub *tools.SubstrateError
	if _, _, err := reg.Admit("read", exe); !errors.As(err, &sub) || sub.Rule.ID != "sub.binary" {
		t.Fatalf("Admit of the running program must be refused by sub.binary, got %v", err)
	}
	if _, _, err := reg.Admit("write", filepath.Join(filepath.Dir(exe), "notes.md")); err != nil {
		t.Fatalf("the rest of a home that holds the program is the identity's: %v", err)
	}
}
