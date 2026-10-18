//go:build (linux && !android) || (darwin && !ios) || windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/uninstall"
)

// fakeUninstall stands in an account for aii uninstall: one registered
// slot, a machine service directory in the test's home, and every act
// recorded; no manager is asked anything.
func fakeUninstall(t *testing.T) (calls *[]string, mdir string) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, install.Dir)
	mdir = filepath.Join(root, install.MachineDirName)
	slot := filepath.Join(root, "identity-0")
	for _, d := range []string{slot, mdir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var log []string
	calls = &log
	registered, machineRegistered := true, true
	saved := uninstallEnv
	uninstallEnv = func() uninstall.Env {
		return uninstall.Env{
			OwnAccount: func() error { return nil },
			Root:       func() (string, error) { return root, nil },
			MachineDir: func() (string, error) { return mdir, nil },
			StartupSlots: func() ([]install.StartupSlot, error) {
				if registered {
					return []install.StartupSlot{{Slot: "identity-0", Dir: slot}}, nil
				}
				return nil, nil
			},
			SlotDirs:    install.SlotDirs,
			Name:        func(string) string { return "Vega" },
			SlotKey:     func(string) (*crypto.KeyPair, error) { return nil, os.ErrNotExist },
			SlotService: func(string) (string, error) { return "", nil },
			SlotRunning: func(string) (bool, error) { return registered, nil },
			Unregister: func(s string) error {
				log = append(log, "unregister "+s)
				if os.Getenv("AII_TEST_UNREGISTER_FAILS") == "1" {
					return errors.New("the manager refused")
				}
				registered = false
				return nil
			},
			SlotGone: func(string) error { return nil },
			Forget:   func(context.Context, string, string, *crypto.KeyPair, string) error { return nil },
			MachineRegistration: func() (string, bool, error) {
				return "/opt/aii/aii", machineRegistered, nil
			},
			UnregisterMachine: func() error { log = append(log, "unregister machine"); machineRegistered = false; return nil },
			MachineGone:       func() error { return nil },
			ServiceRuns:       func(string) (bool, error) { return false, nil },
			StorePlaces:       func(string) ([]string, error) { return nil, nil },
			StoreSize:         func(string) int64 { return 0 },
			Remove: func(dir string) (machine.Removal, error) {
				log = append(log, "remove")
				return machine.Removal{Dir: dir, Gone: true, DirGone: true}, nil
			},
			Programs:  func() []install.InstalledProgram { return nil },
			Processes: func() ([]uninstall.Process, error) { return nil, nil },
			Leftovers: func(string) []string { return nil },
			Sweep:     func() ([]string, error) { return nil, nil },
			Shortcuts: func([]install.InstalledProgram) ([]string, []uninstall.Stay) { return nil, nil },
		}
	}
	t.Cleanup(func() { uninstallEnv = saved })
	return calls, mdir
}

func answer(t *testing.T, s string) {
	t.Helper()
	saved := uninstallInput
	uninstallInput = strings.NewReader(s)
	t.Cleanup(func() { uninstallInput = saved })
}

// AII UNINSTALL ASKS BEFORE IT ACTS: -plan changes nothing, and without
// -yes anything but "yes" changes nothing.
func TestUninstallAsksBeforeItActs(t *testing.T) {
	calls, _ := fakeUninstall(t)
	var stdout, stderr bytes.Buffer
	if code := runUninstall([]string{"-plan"}, &stdout, &stderr); code != 0 || len(*calls) != 0 || !strings.Contains(stdout.String(), "stop and unregister identity-0 (Vega)") {
		t.Fatalf("-plan: exit %d, calls %q, said %q", code, *calls, stdout.String())
	}
	for _, said := range []string{"", "no\n", "y\n", "YES please\n"} {
		answer(t, said)
		stdout.Reset()
		stderr.Reset()
		if code := runUninstall(nil, &stdout, &stderr); code != 1 || len(*calls) != 0 || !strings.Contains(stderr.String(), "nothing was changed") {
			t.Fatalf("answered %q: exit %d, calls %q, stderr %q", said, code, *calls, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Type yes to uninstall") {
			t.Fatalf("the question was not asked: %q", stdout.String())
		}
	}
	answer(t, "yes\n")
	stdout.Reset()
	if code := runUninstall(nil, &stdout, &stderr); code != 0 || !slices.Equal(*calls, []string{"unregister identity-0", "unregister machine", "remove"}) {
		t.Fatalf("answered yes: exit %d, calls %q, said %q", code, *calls, stdout.String())
	}
}

// -YES -JSON IS THE APP'S FACE: no question, and the report alone on
// standard output, format 1. A run that stops part way exits 3; a usage
// error 2.
func TestUninstallYesJSONWritesTheReportAlone(t *testing.T) {
	_, _ = fakeUninstall(t)
	var stdout, stderr bytes.Buffer
	if code := runUninstall([]string{"-yes", "-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var r uninstall.Report
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil || r.Format != 1 || len(r.Done) != 4 {
		t.Fatalf("the report does not read as format 1 with four steps done: %v %+v\n%s", err, r, stdout.String())
	}
	_, _ = fakeUninstall(t)
	t.Setenv("AII_TEST_UNREGISTER_FAILS", "1")
	stdout.Reset()
	if code := runUninstall([]string{"-yes", "-json"}, &stdout, &stderr); code != 3 {
		t.Fatalf("a run stopped part way exits %d, want 3", code)
	}
	if code := runUninstall([]string{"extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("a usage error exits %d, want 2", code)
	}
}

func TestTheShippedUninstallRefusesInAForeignHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probe binary's home is USERPROFILE there; internal/uninstall holds the refusal on every row")
	}
	bin := probeBinary(t)
	root, env := verbHome(t)
	code, stdout, stderr := runVerb(t, bin, t.TempDir(), env, "uninstall", "-plan")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "uninstall: this acts on this account's own registrations") || !strings.Contains(stderr, filepath.Dir(root)) {
		t.Fatalf("aii uninstall -plan in a foreign home: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
