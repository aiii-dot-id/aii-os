package install

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// awkwardWords are argument words a slot's path can hold that each shell
// would split, expand or end early if they were not quoted for it.
var awkwardWords = []string{
	"identity-0",
	"/Users/a person/AII OS/identity-0",
	`C:\Users\a person\AppData\Local\AII OS\identity-0`,
	"it's the slot",
	"John’s Mac ‘quoted’ ‚low‛",
	`dollar $HOME and $(cmd) and ${x}`,
	"back`tick`",
	`a\back\slash\`,
	`semi;colon & amp | pipe < in > out (paren) ^caret`,
	"star * question ? brace {a,b} tilde ~x hash #x bang !x equals =x",
	"tab\there",
	`"double"`,
}

func TestAPOSIXCommandIsReadAsItsWordsBySh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX shell on Windows")
	}
	if POSIX.Name() != "sh, bash or zsh" {
		t.Fatalf("the shell is named %q", POSIX.Name())
	}
	for _, w := range awkwardWords {
		line, ok := POSIX.Command("/Applications/AII OS.app/Contents/MacOS/aii", "dashboard-token", "-dir", w)
		if !ok {
			t.Fatalf("%q cannot be typed into sh", w)
		}
		out, err := exec.Command("/bin/sh", "-c", "set -- "+line+`; printf '%s\0' "$@"`).Output()
		if err != nil {
			t.Fatalf("sh refused %s: %v", line, err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		want := []string{"/Applications/AII OS.app/Contents/MacOS/aii", "dashboard-token", "-dir", w}
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("sh reads %s as %q, want %q", line, got, want)
		}
	}
}

// IN POWERSHELL, THE SAME (I4). The rule, everywhere: single quotes, each
// single quote doubled and each typographic single quote doubled too (they
// end a PowerShell string as well), a program at a quoted path called with
// &. Where PowerShell is present (every Windows host), its own parser reads
// each printed command back as its words.
func TestAPowerShellCommandIsReadAsItsWordsByPowerShell(t *testing.T) {
	if PowerShell.Name() != "PowerShell" {
		t.Fatalf("the shell is named %q", PowerShell.Name())
	}
	for word, want := range map[string]string{
		"identity-0":                 "identity-0",
		`C:\Users\a person\identity`: `'C:\Users\a person\identity'`,
		"it's":                       "'it''s'",
		"John’s ‘x’":                 "'John’’s ‘‘x’’'",
		"$HOME `x` \"q\"":            "'$HOME `x` \"q\"'",
	} {
		if got, ok := PowerShell.Word(word); !ok || got != want {
			t.Errorf("PowerShell word for %q: %q, want %q", word, got, want)
		}
	}
	if line, _ := PowerShell.Command(`C:\Program Files\AII OS\aii.exe`, "dashboard-token", "-dir", `C:\x y`); line != `& 'C:\Program Files\AII OS\aii.exe' dashboard-token -dir 'C:\x y'` {
		t.Errorf("a program at a quoted path: %s", line)
	}
	if line, _ := PowerShell.Command("aii", "dashboard-token"); line != "aii dashboard-token" {
		t.Errorf("a program by name: %s", line)
	}

	ps, err := exec.LookPath("pwsh")
	if err != nil {
		ps, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Log("no PowerShell on this host: the rule is checked, not PowerShell's reading of it")
		return
	}
	const program = `C:\Program Files\AII OS\aii.exe`
	const read = `[Console]::OutputEncoding = [Text.Encoding]::UTF8; $e = $null; ` +
		`$a = [System.Management.Automation.Language.Parser]::ParseInput($env:AII_PRINTED_LINE, [ref]$null, [ref]$e); ` +
		`if ($e.Count) { 'PARSE ERROR: ' + $e[0].Message; exit 1 }; ` +
		`$c = $a.Find({ param($n) $n -is [System.Management.Automation.Language.CommandAst] }, $true); ` +
		`foreach ($x in $c.CommandElements) { if ($x -is [System.Management.Automation.Language.StringConstantExpressionAst]) { $x.Value } else { $x.Extent.Text } }`
	for _, w := range awkwardWords {
		line, ok := PowerShell.Command(program, "dashboard-token", "-dir", w)
		if !ok {
			t.Fatalf("%q cannot be typed into PowerShell", w)
		}
		cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", read)
		cmd.Env = append(os.Environ(), "AII_PRINTED_LINE="+line)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("PowerShell did not read %s: %v\n%s", line, err, out)
		}
		got := strings.Split(strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n")
		want := []string{program, "dashboard-token", "-dir", w}
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("PowerShell reads %s as %q, want %q", line, got, want)
		}
	}
}

func TestACommandPromptCommandIsReadAsItsWordsByCmd(t *testing.T) {
	if Cmd.Name() != "Command Prompt" {
		t.Fatalf("the shell is named %q", Cmd.Name())
	}
	for word, want := range map[string]string{
		"identity-0":                     "identity-0",
		`C:\Users\a person\identity`:     `"C:\Users\a person\identity"`,
		`C:\it's & (this) ^ <that> | so`: `"C:\it's & (this) ^ <that> | so"`,
		`C:\ends\`:                       `"C:\ends\\"`,
	} {
		if got, ok := Cmd.Word(word); !ok || got != want {
			t.Errorf("Command Prompt word for %q: %q, want %q", word, got, want)
		}
	}
	for _, word := range []string{`C:\100%\x`, `C:\%PATH%`, `C:\Hello!\x`, `C:\a!PATH!b ^ c`, `a "q"`, "a\nb"} {
		if got, ok := Cmd.Word(word); ok {
			t.Errorf("%q has a Command Prompt form, %q, that it would not read as typed", word, got)
		}
	}
	if line, _ := Cmd.Command(`C:\Program Files\AII OS\aii.exe`, "dashboard-token", "-dir", `C:\x y`); line != `"C:\Program Files\AII OS\aii.exe" dashboard-token -dir "C:\x y"` {
		t.Errorf("a program at a quoted path: %s", line)
	}
	if _, ok := Cmd.Command("aii", "dashboard-token", "-dir", `C:\100%`); ok {
		t.Error("a command with a word Command Prompt cannot take was offered for it")
	}
}
