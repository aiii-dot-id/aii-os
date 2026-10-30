//go:build windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTextSpelledLikeARootIsRefusedAndToldTheWayOn(t *testing.T) {
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	if err := os.WriteFile(filepath.Join(home, "letter.docx"), []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(home, nil, Timeouts{})
	const line = "if that is text and not a path (a division, a string in a program, a JSON body), this check cannot tell: put the program or the text in a file with the write tool and name the file in the command"
	outside := filepath.Join(filepath.VolumeName(home)+`\`, "aii-wall-text-outside", "secret.txt")
	for _, c := range []struct {
		command string
		hinted  bool
	}{
		// Text INSIDE a quoted argument: the refused word is a piece of it.
		{`$x -replace '<w:p[ >]',' '`, true},
		{`[regex]::Matches($x,'<w:t[^>]*>([^<]*)</w:t>')`, true},
		{`$x -replace '</w:p>',' '`, true},
		{`python -c "print(10 // 3)"`, true},
		{`$x -replace '\s+',' '`, true},
		{`([xml]$x).SelectNodes('//w:t',$ns)`, true},
		// A word of the command itself reads as the path it is.
		{`type \aii-wall-text-outside\secret.txt`, false},
		{`type C:secret.txt`, false},
		{`Get-Content /etc/hosts`, false},
		{`type ` + outside, false},
	} {
		res, err := r.Execute(context.Background(), "shell", map[string]any{"command": c.command})
		if err != nil {
			t.Fatalf("%q: %v", c.command, err)
		}
		if !res.Refused || res.ReasonCode != ReasonShellWall || !strings.Contains(res.Error, "is outside the sandbox") {
			t.Errorf("%q must still be refused by the wall: %+v", c.command, res)
			continue
		}
		if got := strings.Contains(res.Error, line); got != c.hinted {
			t.Errorf("%q: the line is offered=%v, want %v:\n%s", c.command, got, c.hinted, res.Error)
		}
	}
}

func TestAWholeQuotedArgumentSpelledLikeARootIsToldTheWayOnWindows(t *testing.T) {
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	r := NewRegistry(home, nil, Timeouts{})
	const wall = " (best-effort check; run under a container for a hard boundary)"
	const line = "\nif that is text and not a path (a division, a string in a program, a JSON body), this check cannot tell: put the program or the text in a file with the write tool and name the file in the command"
	for _, c := range wallTextRows {
		if !c.windows {
			continue
		}
		if got := spelledRoot(c.refused); got != c.spelled {
			t.Errorf("spelledRoot(%q) = %v here; the table says %v", c.refused, got, c.spelled)
		}
		res, err := r.Execute(context.Background(), "shell", map[string]any{"command": c.command})
		if err != nil {
			t.Fatalf("%q: %v", c.command, err)
		}
		if !res.Refused || res.ReasonCode != ReasonShellWall || !strings.HasPrefix(res.Error, "access denied: "+c.refused+" is outside the sandbox ") {
			t.Errorf("%q must still be refused by the wall for %q: %+v", c.command, c.refused, res)
			continue
		}
		want := wall
		if c.hinted {
			want = wall + line
		}
		if !strings.HasSuffix(res.Error, want) {
			t.Errorf("%q: the line is offered=%v, want %v:\n%s", c.command, strings.HasSuffix(res.Error, line), c.hinted, res.Error)
		}
	}
}

func TestASearchPatternSpelledLikeARootNamesTheGrepTool(t *testing.T) {
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	r := NewRegistry(home, nil, Timeouts{})
	const text = "if that is text and not a path"
	for cmd, refused := range map[string]string{
		`grep -n 'a \d+' notes.txt`:  `\d+`,
		`rg '\bTODO\b' notes.txt`:    `\bTODO\b`,
		`grep -rn 'w:t' letter.xml`:  `w:t`,
		`rg -F '//w:p' document.xml`: `//w:p`,
	} {
		res, err := r.Execute(context.Background(), "shell", map[string]any{"command": cmd})
		if err != nil {
			t.Fatalf("%q: %v", cmd, err)
		}
		if !res.Refused || res.ReasonCode != ReasonShellWall || !strings.Contains(res.Error, refused+" is outside the sandbox") {
			t.Errorf("%q must still be refused by the wall for %q: %+v", cmd, refused, res)
			continue
		}
		grepLine := "\nif that is search text and not a path, the grep tool takes any pattern: grep pattern=" + strconv.Quote(refused) + ` path="…"`
		if !strings.HasSuffix(res.Error, grepLine) || strings.Contains(res.Error, text) {
			t.Errorf("%q: want the grep tool's line and no other:\n%s", cmd, res.Error)
		}
	}
}

// AND THE WAY IT NAMES IS ONE, ON WINDOWS. The pattern the wall refused
// as a path, put in a file with the write tool and named in the command,
// is used: the text is read from the file, where the wall reads none of
// it. (A PowerShell script file is another matter: whether this machine
// runs one is its execution policy's to say, and PowerShell says it.)
func TestTheWayOnForRefusedTextIsOneOnWindows(t *testing.T) {
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	r := NewRegistry(home, nil, Timeouts{})
	run := func(tool string, args map[string]any) Result {
		t.Helper()
		res, err := r.Execute(context.Background(), tool, args)
		if err != nil {
			t.Fatalf("%s %v: %v", tool, args, err)
		}
		return res
	}
	const direct = `'a  b   c' -replace '\s+','-'`
	if res := run("shell", map[string]any{"command": direct}); !res.Refused || res.ReasonCode != ReasonShellWall {
		t.Fatalf("the fixture's pattern was not refused by the wall, so the way on proves nothing: %+v", res)
	}
	if res := run("write", map[string]any{"file_path": "pattern.txt", "content": `\s+`}); res.Failed() {
		t.Fatalf("the pattern could not be put in a file: %+v", res)
	}
	res := run("shell", map[string]any{"command": `'a  b   c' -replace (Get-Content pattern.txt),'-'`})
	if res.Failed() || strings.TrimSpace(res.Output) != "a-b-c" {
		t.Fatalf("the pattern read from its file answered %q (%+v), want a-b-c", res.Output, res)
	}
}
