//go:build !(darwin && !ios) && !(linux && !android) && !windows

package tools

import (
	"strings"
	"testing"
)

func TestOffMacAndLinuxTheShellDescriptionAndArgvAreUnchanged(t *testing.T) {
	if shellWallDescription != "" {
		t.Fatalf("off macOS and Linux the description must gain nothing; got %q", shellWallDescription)
	}
	st := &ShellTool{}
	desc := st.Description()
	// The description is the dialect line and the args, no wall note (on
	// Windows it also names the UTF-8 relay's variables, shellRelayDescription,
	// which is not the wall's).
	if strings.Contains(desc, "operating system's wall") || strings.Contains(desc, "pgrep") {
		t.Fatalf("off macOS the description names the wall: %q", desc)
	}

	start, err := shellCommand("echo hi", "/home", true)
	if err != nil {
		t.Fatal(err)
	}
	bin, argv := start.bin, start.argv
	wbin, wargv, err := behindWall("/home", shellWall{}, bin, argv)
	if err != nil {
		t.Fatalf("off macOS behindWall must never fail: %v", err)
	}
	if wbin != bin || len(wargv) != len(argv) {
		t.Fatalf("off macOS behindWall must not change the argv: bin %q->%q, argv %v->%v", bin, wbin, argv, wargv)
	}
	for i := range argv {
		if wargv[i] != argv[i] {
			t.Fatalf("off macOS argv[%d] changed: %q -> %q", i, argv[i], wargv[i])
		}
	}
}
