package app

import (
	"context"
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// .
// .
// .
// .
// .
// .
// .
func TestASectionWithdrawnBeforeItsCommitIsNeverRegistered(t *testing.T) {
	dir := t.TempDir()
	pkg := buildSectionVer(t, dir, "org.example.bound", "bound", "0.1.0", "Bound")
	t.Chdir(dir)
	ctx := context.Background()
	a := &App{sections: sections.NewRegistry()}
	h := pluginRuntime{a: a}
	stand := func(gen pluginfacility.Generation) (pluginfacility.Running, *pluginfacility.Lease) {
		t.Helper()
		ev, err := h.Verify(ctx, pkg)
		if err != nil {
			t.Fatal(err)
		}
		prep, err := h.Prepare(ctx, ev)
		if err != nil {
			t.Fatal(err)
		}
		lease := pluginfacility.NewLease(gen)
		run, err := h.Start(ctx, prep, lease)
		if err != nil {
			t.Fatal(err)
		}
		return run, lease
	}
	// .
	// .
	run, lease := stand(1)
	lease.Withdraw()
	if err := h.Redirect(nil, run); !errors.Is(err, sections.ErrWithdrawn) {
		t.Fatalf("a withdrawn section was committed: %v", err)
	}
	if _, ok := a.sections.Get("bound"); ok {
		t.Fatal("a withdrawn section is registered")
	}
	if _, err := h.Stop(ctx, run); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	run, lease = stand(2)
	if err := h.Redirect(nil, run); err != nil {
		t.Fatal(err)
	}
	if sec, ok := a.sections.Get("bound"); !ok || sec.Decl.Title != "Bound" {
		t.Fatal("an authorized section was not registered")
	}
	lease.Withdraw()
	if _, ok := a.sections.Get("bound"); ok {
		t.Error("a section its owner took back is still served")
	}
	if _, err := h.Stop(ctx, run); err != nil {
		t.Fatal(err)
	}
}
