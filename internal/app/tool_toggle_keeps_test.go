package app

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// A TOOL SWITCH CHANGES ONE NAME IN THE SAVED LIST. The list was rebuilt
// from the tools registered at the moment of the toggle. A plugin's
// operation the operator had switched off, whose plugin was stopped, held
// or not yet started, is not registered then: it fell out of the file,
// and after the next restart it was offered to the model again.
func TestAToolSwitchKeepsASwitchedOffOperationThatIsNotLoaded(t *testing.T) {
	a, _ := newSandboxApp(t)
	// Switched off earlier; the plugin is not running, so the registry
	// does not hold the operation now.
	a.cfg.Tools.Disabled = []string{"pl_mail_send"}

	saved := func() []string {
		t.Helper()
		raw, err := os.ReadFile(a.cfg.SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Tools struct {
				Disabled []string `json:"disabled"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
		return file.Tools.Disabled
	}

	if err := a.setToolEnabled("read", false); err != nil {
		t.Fatal(err)
	}
	if got := saved(); !slices.Contains(got, "pl_mail_send") || !slices.Contains(got, "read") || len(got) != 2 {
		t.Fatalf("A SWITCHED-OFF OPERATION FELL OUT OF THE FILE: after switching read off the saved list is %v, want pl_mail_send and read", got)
	}
	if a.toolReg.ToolEnabled("read") {
		t.Fatal("read was saved as off and is still on")
	}
	if err := a.setToolEnabled("read", true); err != nil {
		t.Fatal(err)
	}
	if got := saved(); len(got) != 1 || got[0] != "pl_mail_send" {
		t.Fatalf("after switching read back on the saved list is %v, want pl_mail_send alone", got)
	}
	// Switching off what is already off does not list it twice.
	for i := 0; i < 2; i++ {
		if err := a.setToolEnabled("read", false); err != nil {
			t.Fatal(err)
		}
	}
	if got := saved(); len(got) != 2 {
		t.Fatalf("switching read off twice saved %v", got)
	}
}
