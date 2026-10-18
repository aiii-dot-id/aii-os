package install

import (
	"strings"
	"testing"
)

// THE TASK STATES EVERY SETTING THE SCHEDULER DEFAULTS WRONGLY for a
// resident service, and runs the person's program with their own token.
func TestTheMachineTaskStatesItsSettings(t *testing.T) {
	x := machineTaskXML("S-1-5-21-1-2-3-1001", `C:\Users\a\AppData\Local\Programs\AII OS\AII OS.exe`, "--machine", `C:\Users\a\AppData\Local\Programs\AII OS`)
	for _, want := range []string{
		"<UserId>S-1-5-21-1-2-3-1001</UserId>", "<LogonType>InteractiveToken</LogonType>", "<RunLevel>LeastPrivilege</RunLevel>",
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>", "<Priority>5</Priority>",
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>", "<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>", "<AllowStartOnDemand>true</AllowStartOnDemand>",
		`<Command>C:\Users\a\AppData\Local\Programs\AII OS\AII OS.exe</Command>`, "<Arguments>--machine</Arguments>",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("the task lacks %s", want)
		}
	}
	if strings.Contains(x, "<Triggers>") {
		t.Error("the task must have no trigger: it runs when an identity asks")
	}
	if x := machineTaskXML("S", `C:\a&b\<x>.exe`, "", `C:\`); !strings.Contains(x, `C:\a&amp;b\&lt;x&gt;.exe`) {
		t.Errorf("a path is not escaped: %s", x)
	}
}

// A SWITCHED-OFF SIGN-IN ENTRY IS READ AS WINDOWS RECORDS IT: an odd first
// byte in its StartupApproved value.
func TestASwitchedOffSignInEntryIsRead(t *testing.T) {
	for _, c := range []struct {
		b    []byte
		want bool
	}{{nil, false}, {[]byte{2, 0, 0, 0}, false}, {[]byte{6, 0, 0, 0}, false}, {[]byte{3, 0, 0, 0, 1, 2}, true}, {[]byte{7}, true}} {
		if got := startupApprovedDisabled(c.b); got != c.want {
			t.Errorf("startupApprovedDisabled(%v) = %v, want %v", c.b, got, c.want)
		}
	}
}
