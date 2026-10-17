//go:build (linux && !android) || (darwin && !ios) || windows

package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOnlyTheInstalledProgramRegistersTheMachineService(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("program"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	user, system, dev := mk("user-aii"), mk("system-aii"), mk("dev-aii")

	if _, err := serviceProgram(nil, user); !errors.Is(err, ErrNoInstalledProgram) {
		t.Fatalf("no installed program: want ErrNoInstalledProgram, got %v", err)
	}
	got, err := serviceProgram([]InstalledProgram{{Exe: user, Version: "0.1.15"}}, user)
	if err != nil || got != user {
		t.Fatalf("the installed program registers itself: %q, %v", got, err)
	}
	var not *NotInstalledError
	if _, err := serviceProgram([]InstalledProgram{{Exe: user, Version: "0.1.15"}}, dev); !errors.As(err, &not) || not.Installed != user || not.Running != dev {
		t.Fatalf("a development build must be refused, naming the installed program: %v", err)
	}

	both := []InstalledProgram{{Exe: user, Version: "0.1.15"}, {Exe: system, Version: "0.1.1"}}
	if got, err := serviceProgram(both, user); err != nil || got != user {
		t.Fatalf("the higher version runs the service: %q, %v", got, err)
	}
	if _, err := serviceProgram(both, system); !errors.As(err, &not) || not.Installed != user {
		t.Fatalf("the lower installed copy must be refused: %v", err)
	}
	both = []InstalledProgram{{Exe: user}, {Exe: system, Version: "0.1.1"}}
	if got, err := serviceProgram(both, system); err != nil || got != system {
		t.Fatalf("a copy whose version is unknown loses to one whose version is known: %q, %v", got, err)
	}
	// The same file by another name is the same program.
	link := filepath.Join(dir, "link-aii")
	if err := os.Symlink(user, link); err == nil {
		if got, err := serviceProgram([]InstalledProgram{{Exe: user}}, link); err != nil || got != user {
			t.Fatalf("a link to the installed program is the installed program: %q, %v", got, err)
		}
	}
}
