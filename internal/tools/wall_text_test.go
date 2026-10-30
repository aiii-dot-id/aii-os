package tools

import (
	"reflect"
	"strconv"
	"testing"
)

// wall_text_test.go — the questions the wall's refusal lines ask, with
// this system's facts handed in, so that Windows' answers are tested on
// every system. What the refusal says on Windows itself, through the
// shell tool, is wall_text_windows_test.go's, run on a Windows machine.

func TestASearchPatternSpelledLikeARootIsNamedTheGrepTool(t *testing.T) {
	for _, c := range []struct {
		words   []string
		refused string
		spelled bool // spelledRoot(refused), as Windows answers it
		named   bool
	}{
		{[]string{"grep", "-n", `a \d+`, "notes.txt"}, `\d+`, true, true},
		{[]string{"rg", `\bTODO\b`, "notes.txt"}, `\bTODO\b`, true, true},
		{[]string{"grep", "-rn", "w:t", "letter.xml"}, "w:t", true, true},
		{[]string{"rg", "-F", "//w:p", "document.xml"}, "//w:p", true, true},
		// Unchanged off Windows and on it: a slash is asked about, and
		// anything else that is not spelled like a root is not.
		{[]string{"grep", "-rn", "// TODO", "."}, "// TODO", false, true},
		{[]string{"grep", "-n", `a \d+`, "notes.txt"}, `\d+`, false, false},
		// Not a search, or the search program itself refused.
		{[]string{"type", `\outside\x`}, `\outside\x`, true, false},
		{[]string{"/opt/tools/rg", "-F", "/api", "."}, "/opt/tools/rg", true, false},
	} {
		got := searchHintFor(c.words, c.refused, c.spelled)
		want := ""
		if c.named {
			want = "\nif that is search text and not a path, the grep tool takes any pattern: grep pattern=" + strconv.Quote(c.refused) + ` path="…"`
		}
		if got != want {
			t.Errorf("%q refused %q (spelled like a root: %v):\n got %q\nwant %q", c.words, c.refused, c.spelled, got, want)
		}
	}
}

var wallTextRows = []struct {
	command string
	refused string
	spelled bool
	windows bool // a PowerShell row; otherwise bash's
	hinted  bool
}{
	// New: a whole quoted argument spelled like a root.
	{`'a  b' -replace '\s+',' '`, `\s+`, true, true, true},
	{`$x.SelectNodes('//w:t', $ns)`, `//w:t`, true, true, true},
	{`Get-Date -Format 'h:mm'`, `h:mm`, true, true, true},
	{`$s -replace "\d+",'#'`, `\d+`, true, true, true},
	// New, the consequence the operator accepted: a path outside the
	// sandbox written that way in quotes is told the same.
	{`type '\outside\x'`, `\outside\x`, true, true, true},
	{`Get-Content '/etc/hosts'`, `/etc/hosts`, true, true, true},
	// Withheld: a word of the command itself, unquoted or quoted in part,
	// or unquoted anywhere in the command; and a full path.
	{`type \outside\x`, `\outside\x`, true, true, false},
	{`type '\outside'\x`, `\outside\x`, true, true, false},
	{`echo '\outside\x'; type \outside\x`, `\outside\x`, true, true, false},
	{`type \outside\x; echo '\outside\x'`, `\outside\x`, true, true, false},
	{`type 'C:\outside\x'`, `C:\outside\x`, false, true, false},
	{`cat /etc/passwd`, `/etc/passwd`, true, true, false},
	{`/opt/tools/rg -F '/api' .`, `/opt/tools/rg`, true, true, false},
	// As before (wall/0001): a piece of a quoted argument is told.
	{`$x -replace '<w:p[ >]',' '`, `w:p[`, true, true, true},
	{`cat '/etc/hosts'`, `/etc/hosts`, false, false, false},
	{`cat /etc/hosts`, `/etc/hosts`, false, false, false},
	{`cat \/etc/hosts`, `/etc/hosts`, false, false, false},
	{`python3 -c "print('/etc/hosts')"`, `/etc/hosts`, false, false, true},
	{`ls /`, `/`, false, false, true},
}

func TestAWholeQuotedArgumentSpelledLikeARootIsToldTheWayOn(t *testing.T) {
	for _, c := range wallTextRows {
		words := shellWordsQuotedIn(c.command, !c.windows)
		if got := textHintOffered(words, c.refused, c.spelled); got != c.hinted {
			t.Errorf("%q refused %q (spelled like a root: %v): the line is offered=%v, want %v (words %+v)",
				c.command, c.refused, c.spelled, got, c.hinted, words)
		}
	}
}

// shellWordsQuoted CUTS AS shellWords DOES, and says which words were
// wholly quoted. One cutter serves both (shellWordsIn); the texts are held
// to shellWords' own on this system's dialect (PowerShell where the
// Windows build runs this, bash elsewhere), and the quoted fact is read in
// both dialects on every system, bash's own quoted forms ($'…', $"…") and
// a line joined by a backslash among them.
func TestShellWordsQuotedCutsAsShellWordsDoes(t *testing.T) {
	commands := []string{
		`cat notes.md`, `cat 'My Projects/notes.md'`, `cat "My Projects/notes.md"`,
		`cat x|head -1`, `(cat x)`, `cat x;`, `cat a&&cat b`, `pro"vi"ders.json`,
		`echo "a; cat b"`, `pro\viders.json`, `cat a\ b`, `echo "a \"b\" c"`, `echo '\x'\y`,
		`'a  b' -replace '\s+',' '`, `$x.SelectNodes('//w:t', $ns)`, "echo `date`",
		`echo '' x ""`, `'unterminated`, `echo "\$HOME"`,
	}
	for _, cmd := range commands {
		var texts []string
		for _, w := range shellWordsQuoted(cmd, "") {
			texts = append(texts, w.text)
		}
		if want := shellWords(cmd, shellVersions(cmd)[0]); !reflect.DeepEqual(texts, want) {
			t.Errorf("%q: shellWordsQuoted cut %q, shellWords %q", cmd, texts, want)
		}
	}
	type w = shellWord
	for _, c := range []struct {
		cmd     string
		escapes bool
		want    []shellWord
	}{
		{`type '\x'`, false, []w{{"type", false}, {`\x`, true}}},
		{`type "\x"`, false, []w{{"type", false}, {`\x`, true}}},
		{`type \x`, false, []w{{"type", false}, {`\x`, false}}},
		{`type '\x'y`, false, []w{{"type", false}, {`\xy`, false}}},
		{`type '\x''y'`, false, []w{{"type", false}, {`\xy`, true}}},
		{`f('h:mm',$n)`, false, []w{{"f", false}, {"h:mm", true}, {"$n", false}}},
		{`echo '' x`, false, []w{{"echo", false}, {"x", false}}},
		{`cat \/etc/hosts`, true, []w{{"cat", false}, {"/etc/hosts", false}}},
		{`cat \/'etc/hosts'`, true, []w{{"cat", false}, {"/etc/hosts", false}}},
		{`cat "/etc/\$x"`, true, []w{{"cat", false}, {"/etc/$x", true}}},
		{`cat '/etc/hosts'`, true, []w{{"cat", false}, {"/etc/hosts", true}}},
		{`cat $'\x2fetc/hosts'`, true, []w{{"cat", false}, {"/etc/hosts", true}}},
		{`cat $"/etc/hosts"`, true, []w{{"cat", false}, {"/etc/hosts", true}}},
		{`cat $$'/x'`, true, []w{{"cat", false}, {"$$/x", false}}},
		{"cat '/etc/'\\\n'hosts'", true, []w{{"cat", false}, {"/etc/hosts", true}}},
		{"cat '/etc/'\\\nhosts", true, []w{{"cat", false}, {"/etc/hosts", false}}},
	} {
		if got := shellWordsQuotedIn(c.cmd, c.escapes); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q (escapes %v): got %+v, want %+v", c.cmd, c.escapes, got, c.want)
		}
	}
}
