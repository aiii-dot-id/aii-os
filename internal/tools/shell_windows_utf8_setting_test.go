package tools

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

// THE OPERATOR'S SWITCH REACHES THE SHELL AT EACH CALL
// (tools.shell_windows_utf8). The registry's shell asks the wired setting
// every time, so a change applies to the next call with no restart; with
// nothing wired it is on, the default. Off Windows the description does
// not change with it: no relay stands before the shell there. Red with the
// registry's shell built without the setting: the switch turned off still
// reads on.
func TestTheWindowsUTF8SwitchReachesTheShellAtEachCall(t *testing.T) {
	if !hostcap.Can(hostcap.Shell).Available {
		t.Skip("this system runs no shell")
	}
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	got, ok := r.Get("shell")
	if !ok {
		t.Fatal("no shell tool")
	}
	st := got.(*ShellTool)
	if !st.relayed() || !r.ShellWindowsUTF8() {
		t.Fatal("with nothing wired the relay is not on")
	}
	on := true
	r.SetShellWindowsUTF8(func() bool { return on })
	onDescription := st.Description()
	on = false
	if st.relayed() || r.ShellWindowsUTF8() {
		t.Fatal("the switch turned off still reads on at the next call")
	}
	if shellRelayDescription == "" && st.Description() != onDescription {
		t.Errorf("off Windows the description changed with the switch: %q / %q", onDescription, st.Description())
	}
	on = true
	if !st.relayed() {
		t.Fatal("the switch turned back on still reads off")
	}
}
