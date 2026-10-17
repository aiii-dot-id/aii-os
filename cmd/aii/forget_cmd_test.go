//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/machine"
)

// managerHome is a home of the test's own, short enough for a socket's
// address under it, with a service manager on PATH that answers every
// request with success and reaches nothing: PATH is that directory alone,
// so a fake that went missing leaves the verb unable to run any manager at
// all, never able to reach the operator's own.
func managerHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "aiiu")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	if r, err := filepath.EvalSymlinks(home); err == nil {
		home = r
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if runtime.GOOS == "linux" {
		rt := filepath.Join(home, "run")
		if err := os.Mkdir(rt, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", rt)
	}
	bin := t.TempDir()
	for _, name := range []string{"launchctl", "systemctl", "loginctl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	// The home is taken for the account's: unregister acts on the
	// account's own registrations (install.OwnAccount).
	t.Cleanup(install.AccountHomeForTest(home))
	return home
}

// bornSlot makes slot n under root with an identity key, as birth leaves it.
func bornSlot(t *testing.T, root string, n int) (string, *crypto.KeyPair) {
	t.Helper()
	dir, err := install.Create(root, n)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := crypto.SaveKeyPair(kp, filepath.Join(dir, "data", "identity.sec")); err != nil {
		t.Fatal(err)
	}
	return dir, kp
}

func TestUnregisterLetsTheMachineServiceForgetTheSlot(t *testing.T) {
	home := managerHome(t)
	root := filepath.Join(home, install.Dir)
	dir, kp := bornSlot(t, root, 5)
	mdir := filepath.Join(root, install.MachineDirName)
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan struct{})
	go func() { defer close(ended); _ = machine.NewServer(machine.Options{Dir: mdir}).Serve(ctx) }()
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			cancel()
			<-ended
		}
	}
	t.Cleanup(stop)
	if _, err := machine.Register(t.Context(), mdir, dir, kp, "", 10*time.Second); err != nil {
		t.Fatalf("the service did not record the slot: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := runUnregister([]string{"identity-5"}, &stdout, &stderr); code != 0 {
		t.Fatalf("unregister exited %d: %s%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "note: the machine service let go of identity-5") {
		t.Errorf("unregister did not say the service let go of the slot: %q", stderr.String())
	}
	slots, err := machine.RecordedSlots(mdir)
	if err != nil {
		t.Fatal(err)
	}
	if real, _ := filepath.EvalSymlinks(dir); slices.Contains(slots, real) || slices.Contains(slots, dir) {
		t.Fatalf("the slot is still recorded after unregister: %v", slots)
	}

	// No service answers: a note, and the unregister stands.
	stop()
	stdout.Reset()
	stderr.Reset()
	if code := runUnregister([]string{"identity-5"}, &stdout, &stderr); code != 0 {
		t.Fatalf("unregister with no service exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Unregistered ") || !strings.Contains(stderr.String(), "note: the machine service did not let go of identity-5") {
		t.Fatalf("unregister with no service: stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "identity.sec")); err != nil {
		t.Fatalf("the slot's key did not survive unregister: %v", err)
	}
}
