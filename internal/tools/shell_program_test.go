//go:build !windows

package tools

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// keepsPlaces pins whether the shell runs behind a wall that holds the
// kept places (shellWallKeepsPlaces) for one test, wherever it runs.
func keepsPlaces(t *testing.T, walled bool) {
	t.Helper()
	prev := shellWallKeepsPlaces
	shellWallKeepsPlaces = func() bool { return walled }
	t.Cleanup(func() { shellWallKeepsPlaces = prev })
}

// keptHome is heredocHome with the stock places kept: its data directory
// among them, so that data read from the home is the kept data.
func keptHome(t *testing.T) (*Registry, string) {
	t.Helper()
	_, home := heredocHome(t)
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, filepath.Join(home, "profile")))
	return NewRegistry(home, policy, Timeouts{}), home
}

// programsOfCode are inline programs whose every word that trips the
// check is written as code: a variable named data, pathlib's /, a /
// between numbers, each beside a string, a comment or a field that a
// reading by the shell's quotes alone gets wrong.
var programsOfCode = []string{
	"python3 - <<'PY'\ndata = 1\nprint(data)\nPY",
	"python3 - <<'PY'\nx = data\nPY",
	"python3 - <<'PY'\nfrom pathlib import Path\np = Path('.')\nprint(p / 'x')\nPY",
	"python3 - <<'PY'\nprint(1 / 2)\nPY",
	"python3 - <<'PY'\n# it's data\ndata = 1\nprint(f'{data} rows')  # don't\nPY",
	"python3 - <<'PY'\ndoc = '''it's a \"quoted\" note'''\ndata = doc\nprint(data)\nPY",
	"python3 - <<'PY'\nd = {'k': 1}\ndata = d\nprint(f\"{data['k']:>{4}}\")\nPY",
	"python3 - <<'PY'\ndata = 'it\\'s'\nprint(data)\nPY",
	"ruby - <<'RB'\ndata = 1\nputs \"#{data} rows\" # it's\nRB",
	"node - <<'JS'\nconst data = 1 // it's\nconsole.log(`${data} rows`, /* it's */ data)\nJS",
}

func TestBehindAWallAnInlineProgramsCodeIsNoPath(t *testing.T) {
	r, home := keptHome(t)
	look := r.policy.Look()
	quoted := []string{
		"python3 - <<'PY'\nopen('data/aii.db')\nPY",
		"python3 - <<'PY'\nimport os\nos.listdir('data')\nPY",
		"python3 - <<'PY'\nprint(open('" + home + "/data/aii.db').read())\nPY",
		"python3 - <<'PY'\ndata = 1\nopen('data')\nPY",
		// A quote in a comment, which the shell's reading takes for one.
		"python3 - <<'PY'\ndata = 1\n# don't\nopen('data')\nPY",
		// An escaped quote, which ends no string.
		"python3 - <<'PY'\ndata = 1\nopen('\\' data ')\nPY",
		// A string nested in a field (Python 3.12), Ruby's and a template's.
		"python3 - <<'PY'\ndata = 1\nprint(f'{ 'data' }')\nPY",
		"ruby - <<'RB'\ndata = 1\nputs \"#{ \"data\" }\"\nRB",
		"node - <<'JS'\nconst data = 1\nconsole.log(`${ `data` }`)\nJS",
		// What the reader cannot read is asked whole.
		"node - <<'JS'\nconst data = 1\nconsole.log(data / 2)\nJS",
		"perl <<'PL'\nprint data;\nPL",
	}
	commands := []string{
		"ls data",
		"cat <<'EOF'\ndata\nEOF",
		"bash <<'EOF'\nls data\nEOF",
		"python3 script.py <<'EOF'\ndata = 1\nEOF",
		"python3 -c 'import sys' <<'EOF'\ndata = 1\nEOF",
		"python3 - <<EOF\ndata = 1\nEOF",
	}
	t.Run("walled", func(t *testing.T) {
		keepsPlaces(t, true)
		for _, cmd := range programsOfCode {
			if why, _ := r.shellWallFrom(look, home, cmd); why != "" {
				t.Errorf("a word the program writes as code was refused behind the wall: %q\n%s", cmd, why)
			}
		}
		for _, cmd := range append(quoted, commands...) {
			if why, _ := r.shellWallFrom(look, home, cmd); why == "" {
				t.Errorf("A QUOTED OR COMMAND WORD WAS ADMITTED behind the wall: %q", cmd)
			}
		}
		res, err := r.Execute(t.Context(), "shell", map[string]any{"command": quoted[1]})
		if err != nil || !res.Refused || res.ReasonCode != ReasonShellWall || !strings.HasSuffix(res.Error, literalWordRelativeWords) {
			t.Fatalf("a quoted word refused behind the wall does not name the way past: %+v %v", res, err)
		}
	})
	t.Run("unwalled", func(t *testing.T) {
		keepsPlaces(t, false)
		for _, cmd := range append(append(append([]string(nil), programsOfCode[:4]...), quoted...), commands...) {
			if why, _ := r.shellWallFrom(look, home, cmd); why == "" {
				t.Errorf("A WORD WAS ADMITTED where no wall holds the kept places: %q", cmd)
			}
		}
	})
}

// WITH THE SANDBOX OFF, PATHLIB'S / IS NO PATH BEHIND THE WALL. It was
// refused as the root that holds the home (TestAnAbsoluteWordOfAnInline-
// ProgramIsToldNoCwd); a quoted root still is.
func TestWithTheSandboxOffAProgramsSlashIsNoPathBehindTheWall(t *testing.T) {
	g := newOffRig(t)
	g.r.SetSandboxOff(true)
	look := g.r.policy.Look()
	program := "python3 - <<'PY'\nfrom pathlib import Path\np = Path('.')\nprint(p / 'x')\nPY"
	keepsPlaces(t, true)
	if why, _ := g.r.shellWallFrom(look, g.home, program); why != "" {
		t.Fatalf("pathlib's / was refused behind the wall: %s", why)
	}
	quoted := "python3 - <<'PY'\nimport os\nprint(os.listdir('/'))\nPY"
	if why, _, literal := g.r.shellWallWord(look, g.home, quoted); literal != "/" || !strings.Contains(why, holdsSubstrateWhy) {
		t.Fatalf("a quoted / was not refused as the root that holds the home: %q %q", why, literal)
	}
	keepsPlaces(t, false)
	if why, _ := g.r.shellWallFrom(look, g.home, program); why == "" {
		t.Fatal("pathlib's / was admitted where no wall holds the kept places")
	}
}

func TestOnlyAWallThatHoldsTheKeptPlacesSkipsAProgramsCode(t *testing.T) {
	r, home := keptHome(t)
	look := r.policy.Look()
	for _, root := range []bool{false, true} {
		prev := shellAccountRoot
		shellAccountRoot = func() bool { return root }
		want := runtime.GOOS == "darwin" || runtime.GOOS == "linux" && !root
		if got := shellWallKeepsPlaces(); got != want {
			t.Errorf("root %v on %s: the wall holds the kept places %v, want %v", root, runtime.GOOS, got, want)
		}
		why, _ := r.shellWallFrom(look, home, programsOfCode[0])
		if (why == "") != want {
			t.Errorf("root %v on %s: a program's variable named data was judged %q", root, runtime.GOOS, why)
		}
		shellAccountRoot = prev
	}
}

// AN INLINE PROGRAM IS READ AS ITS LANGUAGE READS IT. A word is code
// only when every rune of it stands outside every string and comment and
// no string of the program spells it; what the reader cannot read with
// certainty it reads as no code at all.
func TestProgramCodeReadsStringsAsTheLanguageDoes(t *testing.T) {
	const unread = "-"
	for _, c := range []struct {
		lang, body string
		code, not  string // a word read as code, and one not; unread: none
	}{
		{"python", "data = 1\nprint(data)\n", "data", "1x"},
		{"python", "x = 'data'\ny = data\n", "y", "data"},
		{"python", "# it's\nx = data\n# don't\n", "data", "its"},
		{"python", "s = '''it's'''\nx = data\n", "data", "it's"},
		{"python", "s = \"\"\"a\n'b'\n\"\"\"\nx = data\n", "data", "b"},
		{"python", "s = 'it\\'s data'\nx = data\n", "x", "data"},
		{"python", "s = r'x\\' data'\nx = data\n", "s", "data"},
		{"python", "print(f'{data}')\n", "data", "print(f"},
		{"python", "print(f'{x[\"data\"]}')\ndata = 1\n", "1", "data"},
		{"python", "print(f'{x['data']}')\ndata = 1\n", "1", "data"},
		{"python", "print(f'{v:>{w}} data')\ndata = 1\n", "w", "data"},
		{"python", "print(f'{{data}}')\ndata = 1\n", "data", "{data}"},
		{"python", "print(b'{' + data)\n", "data", "{"},
		{"python", "x = 'abc\n", unread, ""},
		{"python", "print(f'{x # c\n}')\n", unread, ""},
		{"python", "# -*- coding: utf-7 -*-\nx = data\n", unread, ""},
		{"python", "x = data\r\n", unread, ""},
		{"ruby", "data = 1\nputs \"#{data}\"\n", "data", "puts\"#{data}\""},
		{"ruby", "puts \"#{ \"data\" }\"\ndata = 1\n", "1", "data"},
		{"ruby", "puts 'it\\'s data'\nx = data\n", "x", "data"},
		{"ruby", "x = %q(data)\n", unread, ""},
		{"ruby", "x = data =~ /a/\n", unread, ""},
		{"ruby", "c = ?'\n", unread, ""},
		{"ruby", "x = $'\n", unread, ""},
		{"ruby", "x = <<~EOS\ndata\nEOS\n", unread, ""},
		{"ruby", "x = 1\n__END__\ndata\n", unread, ""},
		{"ruby", "=begin\n'\n=end\n", unread, ""},
		{"node", "const data = 1 // it's\n", "//", "its"},
		{"node", "/* it's */ const data = 1\n", "/*", "its"},
		{"node", "#!/usr/bin/env node\nconst data = 1\n", "data", "1x"},
		{"node", "console.log(`${ `data` }`)\nconst data = 1\n", "1", "data"},
		{"node", "console.log(`a\n${data}`)\n", "data", "a"},
		{"node", "const x = a / b\n", unread, ""},
		{"node", "x = 1 <!-- '\n", unread, ""},
		{"node", "--> '\nconst data = 1\n", unread, ""},
		{"node", "/* '\n", unread, ""},
		{"perl", "my $x = data;\n", unread, ""},
		{"php", "<?php echo data;\n", unread, ""},
		{"", "data\n", unread, ""},
	} {
		code := programCode(c.lang, c.body)
		if c.code == unread {
			if code != nil {
				t.Errorf("%s %q: read as code %v where it cannot be sure", c.lang, c.body, code)
			}
			continue
		}
		if code == nil || !code[c.code] || code[c.not] {
			t.Errorf("%s %q: code %v, want %q in it and %q not", c.lang, c.body, code, c.code, c.not)
		}
	}
}

// A HERE-DOCUMENT IS AN INTERPRETER'S WHOLE PROGRAM ONLY WHEN THE COMMAND
// GIVES IT NOTHING ELSE TO RUN. A script, -c, an option or a redirection
// before the program's own arguments makes the body input that a program
// reads, every word of which is asked.
func TestHereDocsNamesTheLanguageOfAWholeProgram(t *testing.T) {
	for cmd, want := range map[string]string{
		"python3 - <<'PY'\nx\nPY":                   "python",
		"python3 <<'PY'\nx\nPY":                     "python",
		"/usr/bin/python3.12 - a b <<'PY'\nx\nPY":   "python",
		"python3 - <<'PY' | tee out\nx\nPY":         "python",
		"PYTHONPATH=x python3 - <<'PY'\nx\nPY":      "python",
		"cat f; ruby - <<'RB'\nx\nRB":               "ruby",
		"node <<'JS'\nx\nJS":                        "node",
		"python3 script.py <<'PY'\nx\nPY":           "",
		"python3 <<'PY' script.py\nx\nPY":           "",
		"python3 -c 'import sys' <<'PY'\nx\nPY":     "",
		"python3 -u - <<'PY'\nx\nPY":                "",
		"python3 2>/dev/null - <<'PY'\nx\nPY":       "",
		"ruby -x <<'RB'\nx\nRB":                     "",
		"perl <<'PL'\nx\nPL":                        "",
		"python3 - <<PY\nx\nPY":                     "",
		"bash <<'EOF'\nx\nEOF":                      "",
		"python3 - <<'A' && ruby <<'B'\nx\nA\ny\nB": "python",
	} {
		_, docs := hereDocs(cmd, bash5)
		if len(docs) == 0 || docs[0].lang != want {
			t.Errorf("%q: %+v, want the first body's language %q", cmd, docs, want)
		}
	}
	if _, docs := hereDocs("python3 - <<'A' && ruby <<'B'\nx\nA\ny\nB", bash5); len(docs) != 2 || docs[1].lang != "ruby" {
		t.Errorf("the second command's body: %+v", docs)
	}
}
