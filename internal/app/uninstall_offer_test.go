package app

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTheItemIsOfferedOnlyFromABundleOnMacOS(t *testing.T) {
	saved := uninstallProgram
	t.Cleanup(func() { uninstallProgram = saved })
	bundle := filepath.Join(t.TempDir(), "AII OS.app")
	for program, want := range map[string]bool{bundle: runtime.GOOS == "darwin", filepath.Join(t.TempDir(), "aii"): false} {
		p := program
		uninstallProgram = func() (string, error) { return p, nil }
		if got := uninstallBundle() != ""; got != want {
			t.Errorf("running from %s on %s, offered = %v, want %v", p, runtime.GOOS, got, want)
		}
	}
	uninstallProgram = func() (string, error) { return "", errors.New("no program") }
	if uninstallBundle() != "" {
		t.Error("a program that does not resolve offered the item")
	}
}

// THE IDENTITY'S ACT OPENS ITS OWN BUNDLE THROUGH THE SEAM, and only where
// the item is offered: never a bare binary, never on another row, and
// never through open directly (the seam is the one way the host starts
// the app; this test's stands in for LaunchServices).
func TestTheIdentitysActOpensItsBundleThroughTheSeam(t *testing.T) {
	savedProgram, savedOpen := uninstallProgram, openUninstall
	t.Cleanup(func() { uninstallProgram, openUninstall = savedProgram, savedOpen })
	var opened []string
	openUninstall = func(b string) error { opened = append(opened, b); return nil }
	bundle := filepath.Join(t.TempDir(), "AII OS.app")
	uninstallProgram = func() (string, error) { return bundle, nil }
	a := &App{}
	err := a.openUninstallApp()
	if runtime.GOOS == "darwin" {
		if err != nil || len(opened) != 1 || opened[0] != bundle {
			t.Fatalf("the act: %v, opened %q", err, opened)
		}
	} else if !errors.Is(err, errUninstallNotOffered) || len(opened) != 0 {
		t.Fatalf("on %s the act must refuse and open nothing: %v %q", runtime.GOOS, err, opened)
	}
	opened = nil
	uninstallProgram = func() (string, error) { return filepath.Join(t.TempDir(), "aii"), nil }
	if err := a.openUninstallApp(); !errors.Is(err, errUninstallNotOffered) || len(opened) != 0 {
		t.Fatalf("from a bare binary the act must refuse and open nothing: %v %q", err, opened)
	}
}
