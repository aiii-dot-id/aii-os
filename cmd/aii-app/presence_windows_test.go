//go:build windows

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
	"golang.org/x/sys/windows"
)

func launcherSlot(t *testing.T) (string, int) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "identity-1")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port
	path := install.ConfigPathIn(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"dashboard":{"port":%d}}`, port)), 0600); err != nil {
		t.Fatal(err)
	}
	return dir, port
}

func holdLauncherSlot(t *testing.T, dir string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(install.StopChannelNames(dir)[1])
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateEvent(nil, 1, 0, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	return h
}

func TestLauncherReopensItsSlotWithoutWMICOrStartingAnotherProcess(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir, port := launcherSlot(t)
	h := holdLauncherSlot(t, dir)
	var opened []string
	openURL := func(url string) error { opened = append(opened, url); return nil }
	missingExecutable := filepath.Join(dir, "not-a-real-daemon.exe")
	if err := runSlot(missingExecutable, dir, 1, true, openURL, serving); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 0 {
		t.Fatal("sign-in opened a browser")
	}
	if err := runSlot(missingExecutable, dir, 1, false, openURL, serving); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != fmt.Sprintf("http://127.0.0.1:%d", port) {
		t.Fatalf("did not reopen the configured dashboard: %v", opened)
	}
	if _, err := os.Stat(filepath.Join(dir, "log")); !os.IsNotExist(err) {
		t.Fatalf("an already-running slot entered the process-start path: %v", err)
	}
	if state, err := windows.WaitForSingleObject(h, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("opening the dashboard stopped its identity: %d %v", state, err)
	}
}

func TestLauncherDoesNotOpenAnUnidentifiedPortOrMistakeAPrefixSlot(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir, _ := launcherSlot(t)
	holdLauncherSlot(t, filepath.Join(filepath.Dir(dir), "identity-10"))
	opened := false
	err := runSlot(filepath.Join(dir, "missing.exe"), dir, 1, false, func(string) error { opened = true; return nil }, serving)
	if err == nil || opened || !strings.Contains(err.Error(), "could not be identified") {
		t.Fatalf("unidentified port: opened=%v error=%v", opened, err)
	}
	if strings.Contains(err.Error(), "This identity is not running") {
		t.Fatal("missing evidence was reported as proof the identity is stopped")
	}
	if _, err := os.Stat(filepath.Join(dir, "log")); !os.IsNotExist(err) {
		t.Fatal("a port conflict tried to start another process")
	}
}

func TestLauncherKeepsTheStartPathWhenNoSlotProcessExists(t *testing.T) {
	dir := t.TempDir()
	// The unused-port answer is a seam, not a reserve/close/dial race. The
	// absent executable witnesses the ordinary start branch without launching
	// a process or touching an installed identity.
	err := runSlot(filepath.Join(dir, "missing.exe"), dir, 0, true, func(string) error { t.Fatal("browser opened"); return nil }, func(int) bool { return false })
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the normal start path was blocked or misreported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "log", "boot.log")); err != nil {
		t.Fatalf("the ordinary start path did not prepare its log: %v", err)
	}
}
