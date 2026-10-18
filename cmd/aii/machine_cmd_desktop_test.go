//go:build (linux && !android) || (darwin && !ios) || windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func TestMachineRegisterFromANonInstalledBuildIsRefused(t *testing.T) {
	if runtime.GOOS == "darwin" {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("SUDO_USER", "")
		app := filepath.Join(home, "Applications", "AII OS.app", "Contents")
		if err := os.MkdirAll(filepath.Join(app, "MacOS"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app, "MacOS", "aii"), []byte("installed"), 0o755); err != nil {
			t.Fatal(err)
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>99.0.0</string></dict></plist>`
		if err := os.WriteFile(filepath.Join(app, "Info.plist"), []byte(plist), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	self, err := runningProgram()
	if err != nil {
		t.Fatal(err)
	}
	var not *install.NotInstalledError
	if _, err := install.ServiceProgram(self); !errors.As(err, &not) {
		t.Skipf("this account has no installed program to refuse in favour of (%v); the act would register this build", err)
	}
	var stdout, stderr bytes.Buffer
	code := runMachine([]string{"register"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "registered only from this account's installed program ("+not.Installed+")") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "Registered") {
		t.Fatal("a refused registration said it registered")
	}
}

// `aii machine store` NAMES WHAT IT NEEDS: a place and a slot, or its
// usage; a slot that is not the account's is refused before any link.
func TestMachineStoreNamesItsArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runMachine([]string{"store"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "aii machine store <dir> <slot>") {
		t.Fatalf("no arguments: exit %d, stderr %q", code, stderr.String())
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	stderr.Reset()
	if code := runMachine([]string{"store", filepath.Join(home, "plugins"), "identity-9"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "does not exist") {
		t.Fatalf("a slot the account does not have: exit %d, stderr %q", code, stderr.String())
	}
}

func TestMachineStoreListPrintsTheStoresOnePerLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	mdir := filepath.Join(home, install.Dir, install.MachineDirName)
	var stdout, stderr bytes.Buffer
	if code := runMachine([]string{"store", "-list"}, &stdout, &stderr); code != 0 || stdout.String() != filepath.Join(home, install.Dir, "plugins")+"\n" {
		t.Fatalf("no state: exit %d, %q %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(mdir); !os.IsNotExist(err) {
		t.Fatalf("-list made the service directory: %v", err)
	}
	if err := os.MkdirAll(mdir, 0o700); err != nil {
		t.Fatal(err)
	}
	placed, old := filepath.Join(home, "big", "plugins"), filepath.Join(home, ".aii", "plugins")
	state, _ := json.Marshal(map[string]any{"identities": []any{}, "store": placed, "draining": []string{old}})
	if err := os.WriteFile(filepath.Join(mdir, "state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := runMachine([]string{"store", "-list"}, &stdout, &stderr); code != 0 || stdout.String() != placed+"\n"+old+"\n" {
		t.Fatalf("a placed store: exit %d, %q", code, stdout.String())
	}
	for _, bad := range [][]byte{[]byte("{torn"), mustJSON(t, map[string]any{"identities": []any{}, "store": "/a\nb"})} {
		if err := os.WriteFile(filepath.Join(mdir, "state.json"), bad, 0o600); err != nil {
			t.Fatal(err)
		}
		stdout.Reset()
		stderr.Reset()
		if code := runMachine([]string{"store", "-list"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
			t.Fatalf("state %q: exit %d, printed %q", bad, code, stdout.String())
		}
	}
	if code := runMachine([]string{"store", "-list", "x"}, &stdout, &stderr); code != 2 {
		t.Fatalf("-list with an argument: exit %d", code)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
