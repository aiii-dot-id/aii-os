package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// runInit, runSlots and runRegister were all at 0%: the three verbs a
// beta user actually types were proven only by hand. These drive the
// real commands against a temporary home, so the paths a person walks
// on day one are the paths under test.

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// SUDO_USER would send install.Root at a real account's home; an
	// inherited one from the environment must not reach the test.
	t.Setenv("SUDO_USER", "")
	return home
}

func TestStopReportsTheActualSlotInspectionFailure(t *testing.T) {
	home := tempHome(t)
	// A file where the installation directory would be: this is not a
	// missing identity, and the filesystem's actual cause must survive.
	if err := os.WriteFile(filepath.Join(home, install.Dir), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, want := os.Stat(filepath.Join(home, install.Dir, "identity-0"))
	if want == nil {
		t.Fatal("fixture did not fail inspection")
	}
	var stderr strings.Builder
	code := runStop([]string{"identity-0"}, io.Discard, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), want.Error()) {
		t.Fatalf("inspection cause was hidden: exit=%d output=%q want=%v", code, stderr.String(), want)
	}
}

// THE FIRST SLOT IS MADE BY THE PERSON THE IDENTITY BELONGS TO. The
// package installs code; this makes the world.
func TestInitCreatesTheFirstSlot(t *testing.T) {
	home := tempHome(t)

	if code := runInit([]string{"--no-start"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("aii init exited %d", code)
	}
	slot := filepath.Join(home, install.Dir, install.SlotName(0))
	if _, err := os.Stat(slot); err != nil {
		t.Fatalf("the first slot was not created: %v", err)
	}
	// A slot with no config is not a world an identity can be born into.
	if _, err := os.Stat(install.ConfigPathIn(slot)); err != nil {
		t.Fatalf("the slot carries no config: %v", err)
	}
}

// SLOTS ARE NUMBERED AND DO NOT COLLIDE. A second init must make a
// second world rather than reopening the first.
func TestInitCreatesASecondSlotBesideTheFirst(t *testing.T) {
	home := tempHome(t)

	if code := runInit([]string{"--no-start"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("first init exited %d", code)
	}
	if code := runInit([]string{"--no-start"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("second init exited %d", code)
	}
	for _, n := range []int{0, 1} {
		if _, err := os.Stat(filepath.Join(home, install.Dir, install.SlotName(n))); err != nil {
			t.Fatalf("slot %d missing: %v", n, err)
		}
	}
}

// slots LISTS WHAT INIT MADE. An operator who cannot see their slots
// cannot choose between them.
func TestSlotsListsWhatInitCreated(t *testing.T) {
	tempHome(t)
	if code := runSlots(nil, io.Discard, io.Discard); code != 0 {
		t.Fatalf("aii slots exited %d on an empty home — nothing to list is not an error", code)
	}
	if code := runInit([]string{"--no-start"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("init exited %d", code)
	}
	if code := runSlots(nil, io.Discard, io.Discard); code != 0 {
		t.Fatalf("aii slots exited %d after a slot existed", code)
	}
}

// A HOME THAT CANNOT BE WRITTEN IS AN HONEST FAILURE, not a crash and
// not a silent success.
func TestInitFailsWhenTheHomeCannotBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		// os.Chmod moves only the read-only ATTRIBUTE on Windows and
		// says nothing about a directory's writability, so the
		// precondition this test needs cannot be created that way —
		// init then succeeds and the test reads as a product failure
		// when it is really a harness one. Expressing it would take a
		// deny-write DACL, which risks leaving an undeletable TempDir.
		t.Skip("chmod cannot express an unwritable directory on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unwritable directory is still writable")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(home, 0o700) })

	if code := runInit([]string{"--no-start"}, io.Discard, io.Discard); code == 0 {
		t.Fatal("aii init reported success into a home it cannot write")
	}
}

func TestInitTellsHowToReadTheAccessToken(t *testing.T) {
	home := tempHome(t)
	var out strings.Builder
	if code := runInit([]string{"--no-start"}, &out, io.Discard); code != 0 {
		t.Fatalf("aii init exited %d", code)
	}
	slot := filepath.Join(home, install.Dir, install.SlotName(0))
	for _, want := range []string{
		"dashboard asks for its access token, once in each browser",
		tokenLines(slot),
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("aii init does not say %q:\n%s", want, out.String())
		}
	}
}

func tokenLines(dir string) string {
	if runtime.GOOS == "windows" {
		ps, _ := install.PowerShell.Command("aii", "dashboard-token", "-dir", dir)
		lines := "Print the token with this, in PowerShell:\n  " + ps + "\n"
		if c, ok := install.Cmd.Command("aii", "dashboard-token", "-dir", dir); ok {
			lines += "or in Command Prompt:\n  " + c + "\n"
		}
		return lines
	}
	sh, _ := install.POSIX.Command("aii", "dashboard-token", "-dir", dir)
	return "Print the token with this, in sh, bash or zsh:\n  " + sh + "\n"
}

// THE INIT WORDS ARE TYPED INTO THE SHELL THEY NAME (I4). For a slot whose
// path holds a space, a quote and a dollar, each printed line is the shell's
// own form; on this system's POSIX shell it is read back as its words.
func TestTheInitWordsAreForTheShellTheyName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "it's $HOME", "identity-0")
	words := tokenWords(dir, "The")
	if !strings.Contains(words, tokenLines(dir)) {
		t.Fatalf("the words are not each shell's own form:\n%s\nwant\n%s", words, tokenLines(dir))
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(words, "in PowerShell:") || !strings.Contains(words, "or in Command Prompt:") {
			t.Fatalf("Windows words do not name both shells:\n%s", words)
		}
		return
	}
	line := strings.TrimPrefix(strings.Split(words, "\n")[3], "  ")
	out, err := exec.Command("/bin/sh", "-c", "set -- "+line+`; printf '%s\n' "$@"`).Output()
	if err != nil {
		t.Fatalf("sh refused %s: %v", line, err)
	}
	if want := "aii\ndashboard-token\n-dir\n" + dir + "\n"; string(out) != want {
		t.Fatalf("sh reads %s as\n%s\nwant\n%s", line, out, want)
	}
}
