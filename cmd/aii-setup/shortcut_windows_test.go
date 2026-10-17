package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAShortcutIsWrittenAsNamedWhateverQuotesItsPathsHold(t *testing.T) {
	t.Chdir(t.TempDir()) // where a command run from a name would write its file
	dir := filepath.Join(t.TempDir(), "John’s menu ’+(Set-Content aii-probe.txt x)+’ it's")
	target := filepath.Join(t.TempDir(), "it's ‘the’ program.exe")
	if err := os.WriteFile(target, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := shortcut(dir, "AII OS", target, filepath.Dir(target)); err != nil {
		t.Fatalf("the shortcut was not written: %v", err)
	}
	lnk := filepath.Join(dir, "AII OS.lnk")
	read := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"[Console]::OutputEncoding = [Text.Encoding]::UTF8; $s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:AII_SHORTCUT); [Console]::Out.Write($s.TargetPath + [char]10 + $s.WorkingDirectory)")
	read.Env = append(os.Environ(), "AII_SHORTCUT="+lnk)
	out, err := read.Output()
	if err != nil {
		t.Fatalf("read %s: %v", lnk, err)
	}
	if got, want := strings.TrimPrefix(string(out), "\uFEFF"), target+"\n"+filepath.Dir(target); got != want {
		t.Fatalf("the shortcut points at\n%q\nwant\n%q", got, want)
	}
	if _, err := os.Stat("aii-probe.txt"); err == nil {
		t.Fatal("a path ran as PowerShell: its command wrote aii-probe.txt")
	}
}
