package browsertest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A BROWSER IS FOUND WHERE THE SYSTEM PUTS IT, AND A SEARCH THAT FINDS
// NOTHING SAYS WHERE IT LOOKED. On PATH first, by each name the engine goes
// by; then where this system's installers put it — a macOS application
// bundle, Windows' Program Files. AII_BROWSER_ENGINES narrows the engines,
// and a search it left out says so.
func TestABrowserIsFoundWhereTheSystemPutsIt(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	t.Setenv("HOME", empty)
	t.Setenv("ProgramFiles", filepath.Join(empty, "Program Files"))
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("AII_BROWSER_ENGINES", "")

	// Nothing on PATH: every name is in what was looked at, and then the
	// system's own places.
	_, looked := Find(Chrome)
	for _, name := range onPath[Chrome] {
		if !strings.Contains(strings.Join(looked, "\n"), name+" on PATH") {
			t.Errorf("the search did not say it looked for %s on PATH: %q", name, looked)
		}
	}
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(strings.Join(looked, "\n"), "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome") {
			t.Errorf("the search did not look in the application bundle: %q", looked)
		}
	case "windows":
		want := filepath.Join(empty, "Program Files", `Google\Chrome\Application\chrome.exe`)
		if !strings.Contains(strings.Join(looked, "\n"), want) {
			t.Errorf("the search did not look under Program Files: %q", looked)
		}
	}

	// In the account's own place: found there, at its real path.
	var where string
	switch runtime.GOOS {
	case "darwin":
		where = filepath.Join(empty, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome")
	case "windows":
		where = filepath.Join(empty, "Program Files", "Google", "Chrome", "Application", "chrome.exe")
	}
	if where != "" {
		if err := os.MkdirAll(filepath.Dir(where), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(where, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got, _ := Find(Chrome); got != where {
			t.Errorf("Chrome installed at %s was found at %q", where, got)
		}
	}

	// On PATH: found there first.
	name := "chromium"
	if runtime.GOOS == "windows" {
		name = "chromium.exe"
	}
	onpath := filepath.Join(empty, name)
	if err := os.WriteFile(onpath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := Find(Chrome); got != onpath {
		t.Errorf("chromium on PATH was found at %q, want %q", got, onpath)
	}

	for _, blank := range []string{"", " ", ",", " , ,"} {
		t.Setenv("AII_BROWSER_ENGINES", blank)
		if !Allowed(Chrome) || !Allowed(Edge) || !Allowed(Firefox) {
			t.Errorf("AII_BROWSER_ENGINES=%q names no engine and did not admit every one", blank)
		}
	}
	t.Setenv("AII_BROWSER_ENGINES", "gecko-firefox, blink-edge")
	if Allowed(Chrome) || !Allowed(Firefox) || !Allowed(Edge) {
		t.Error("AII_BROWSER_ENGINES did not narrow the engines to the ones it names")
	}
	if why := Looked(Chrome, nil); !strings.Contains(why, "not among AII_BROWSER_ENGINES") {
		t.Errorf("a search the selector left out does not say so: %q", why)
	}
}

func TestTheWindowsNamesAreAskedForOnWindowsAlone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HOME", dir)
	t.Setenv("ProgramFiles", filepath.Join(dir, "Program Files"))
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("AII_BROWSER_ENGINES", "")
	for engine, name := range map[string]string{Chrome: "chrome", Edge: "msedge"} {
		file := name
		if runtime.GOOS == "windows" {
			file += ".exe"
		}
		onpath := filepath.Join(dir, file)
		if err := os.WriteFile(onpath, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, looked := Find(engine)
		asked := false
		for _, l := range looked {
			asked = asked || l == name+" on PATH"
		}
		if runtime.GOOS == "windows" {
			if got != onpath || !asked {
				t.Errorf("%s: %s on PATH was not found on Windows: %q (looked %q)", engine, name, got, looked)
			}
			continue
		}
		if got == onpath || asked {
			t.Errorf("%s: %s on PATH was asked for off Windows: found %q, looked %q", engine, name, got, looked)
		}
	}
}
