//go:build !(darwin && !ios)

package tools

import (
	"strings"
	"testing"
)

func TestOffMacTheShellDescriptionAndArgvAreUnchanged(t *testing.T) {
	if shellWallDescription != "" {
		t.Fatalf("off macOS the description must gain nothing; got %q", shellWallDescription)
	}
	st := &ShellTool{}
	desc := st.Description()
	// The description is exactly the dialect line and the args, no wall note.
	if strings.Contains(desc, "operating system's wall") || strings.Contains(desc, "pgrep") {
		t.Fatalf("off macOS the description names the wall: %q", desc)
	}

	bin, argv := shellInvocation()
	argv = append(argv, "echo hi")
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
