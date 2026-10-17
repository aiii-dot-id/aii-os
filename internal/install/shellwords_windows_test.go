//go:build windows

package install

import (
	"encoding/binary"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func TestCommandPromptReadsAPrintedCommandAsItsWords(t *testing.T) {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	const program = `C:\Program Files\AII OS\aii.exe`
	tried := 0
	for _, w := range append(awkwardWords, `C:\ends with\`) {
		line, ok := Cmd.Command(program, "dashboard-token", "-dir", w)
		if !ok {
			continue // a word Command Prompt cannot take is not printed for it
		}
		tried++
		cmd := exec.Command(comspec)
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /u /d /v:off /c echo ` + line}
		out, err := cmd.Output()
		if err != nil || len(out)%2 != 0 {
			t.Fatalf("cmd refused %s: %v (%d bytes)", line, err, len(out))
		}
		units := make([]uint16, len(out)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(out[2*i:])
		}
		got, err := windows.DecomposeCommandLine(strings.TrimRight(string(utf16.Decode(units)), "\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{program, "dashboard-token", "-dir", w}
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("Command Prompt reads %s as %q, want %q", line, got, want)
		}
	}
	if tried < 8 {
		t.Fatalf("only %d of the awkward words were tried", tried)
	}
}
