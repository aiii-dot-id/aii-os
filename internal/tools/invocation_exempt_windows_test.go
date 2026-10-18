package tools

import "testing"

func TestTheWindowsWallLetsSystem32ThroughAndNothingBeside(t *testing.T) {
	t.Setenv("SystemRoot", `C:\Windows`)
	for path, want := range map[string]bool{
		`C:\Windows\System32\where.exe`:                             true,
		`c:\windows\system32\WHERE.EXE`:                             true,
		`C:\Windows\System32`:                                       true,
		`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`: true,
		`C:\Windows\Temp\note.txt`:                                  false,
		`C:\Windows\System32\..\Temp\note.txt`:                      false,
		`C:\Windows\System32x\tool.exe`:                             false,
		`C:\Windows`:                                                false,
		`C:\Program Files\Git\cmd\git.exe`:                          false,
		`System32\where.exe`:                                        false,
		`D:\Windows\System32\where.exe`:                             false,
		`C:\Users\someone\AppData\Local\Programs\AII OS\aii.exe`:    false,
	} {
		if got := invocationExempt(path); got != want {
			t.Errorf("invocationExempt(%q) = %v, want %v", path, got, want)
		}
	}
	t.Setenv("SystemRoot", "")
	if invocationExempt(`C:\Windows\System32\where.exe`) {
		t.Error("with no SystemRoot nothing is exempt")
	}
}
