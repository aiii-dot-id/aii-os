package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func TestTheWindowsFirstStartSaysItsCommandIsForPowerShell(t *testing.T) {
	aii := filepath.Join(`C:\Program Files`, "AII OS", "aii.exe")
	dir := filepath.Join(t.TempDir(), "John’s slot")
	note := firstStartTokenNote(aii, dir)
	want, _ := install.PowerShell.Command(aii, "dashboard-token", "-dir", dir)
	if !strings.Contains(note, "run this in PowerShell:\n\n"+want+"\n") || !strings.HasPrefix(want, "& '") {
		t.Fatalf("the note is not PowerShell's command, or does not name PowerShell:\n%s\nwant the line\n%s", note, want)
	}
}

func TestAMessageBoxShowsItsTextAsWrittenAndRunsNothing(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "ran")
	t.Setenv("AII_PROBE", probe)
	note := firstStartTokenNote(filepath.Join(`C:\Program Files`, "AII OS", "aii.exe"), filepath.Join(t.TempDir(), "John’s slot"))
	const show = `[Console]::OutputEncoding = [Text.Encoding]::UTF8; [Console]::Out.Write($env:AII_BOX_TITLE + [char]10 + $env:AII_BOX_TEXT)`
	if !strings.Contains(boxScript, "$env:AII_BOX_TEXT") || !strings.Contains(boxScript, "$env:AII_BOX_TITLE") {
		t.Fatalf("the box does not read its text from the environment: %s", boxScript)
	}
	for _, msg := range []string{
		note,
		"a’+(New-Item -ItemType File -Path $env:AII_PROBE)+’",
		"a‘+(New-Item -ItemType File -Path $env:AII_PROBE)+‘",
		"a'+(New-Item -ItemType File -Path $env:AII_PROBE)+'",
		"$(New-Item -ItemType File -Path $env:AII_PROBE)",
		"the last lines of boot.log\n\"quoted\" `tick` ‚low‛ $HOME",
	} {
		out, err := boxCommand(show, "AII OS", msg).Output()
		if err != nil {
			t.Fatalf("PowerShell did not run the box's command for %q: %v", msg, err)
		}
		if got := strings.TrimPrefix(string(out), "\uFEFF"); got != "AII OS\n"+msg {
			t.Errorf("the box would show\n%q\nfor the message\n%q", got, msg)
		}
	}
	if _, err := os.Stat(probe); err == nil {
		t.Fatal("a message ran as PowerShell: the probe was made")
	}
}

func TestALongMessageIsCutForItsBox(t *testing.T) {
	long := strings.Repeat("é", 40<<10) // two bytes each: 80 KiB
	got := envText(long)
	if len(got) > maxBoxText || !strings.HasSuffix(got, "…") || !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) || !utf8.ValidString(got) {
		t.Fatalf("a long message was not cut to its box: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	const show = `[Console]::OutputEncoding = [Text.Encoding]::UTF8; [Console]::Out.Write($env:AII_BOX_TEXT.Length)`
	out, err := boxCommand(show, "AII OS", long).Output()
	if err != nil || strings.TrimPrefix(strings.TrimSpace(string(out)), "\uFEFF") == "0" {
		t.Fatalf("the box was not handed the cut message: %v %q", err, out)
	}
}
