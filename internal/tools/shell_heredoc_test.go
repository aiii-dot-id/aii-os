//go:build !windows

package tools

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// heredocHome is a home laid out as an identity's is: the store in data/,
// and at the root a link to it by its own name, aii.db -> data/aii.db, so
// that a glob read from the home, "*" among them, reaches the store.
func heredocHome(t *testing.T) (*Registry, string) {
	t.Helper()
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	for _, d := range []string{"data", "notes", "sample-work/source", "sample-work/out"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"data/aii.db", "data/aii.db-wal", "data/aii.db-shm", "data/ledger.jsonl",
		"notes/rollback-baseline-v0.md", "sample-work/check.sh", "sample-work/out/manifest.json"} {
		if err := os.WriteFile(filepath.Join(home, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"aii.db": "data/aii.db", "ledger.jsonl": "data/ledger.jsonl"} {
		if err := os.Symlink(target, filepath.Join(home, link)); err != nil {
			t.Skipf("symlinks unavailable here: %v", err)
		}
	}
	return NewRegistry(home, nil, Timeouts{}), home
}

func TestAQuotedHereDocumentGivenToAnInterpreterIsNotExpanded(t *testing.T) {
	r, home := heredocHome(t)
	for _, cmd := range []string{
		`python3 - <<'PY'
import pathlib,json,hashlib
root=pathlib.Path('sample-work/source')
d=json.loads(pathlib.Path('sample-work/out/manifest.json').read_text())
expected={f['path']:f['sha256'] for f in d['files']}
bad=[p for p,h in expected.items() if hashlib.sha256((root/p).read_bytes()).hexdigest()!=h]
extra=[str(p.relative_to(root)) for p in root.rglob('*') if p.is_file() and str(p.relative_to(root)) not in expected]
print(len(expected),'expected',len(bad),'bad',len(extra),'extra')
for b in bad[:20]: print(*b)
PY`,
		`python3 - <<'PY'
import pathlib
p=pathlib.Path('sample-work/check.sh')
s=p.read_text()
s=s.replace('case "$1" in\n','case "${1:-}" in\n',1)
s=s.replace('  *) usage ;;\n','  *) usage; exit 2 ;;\n',1)
p.write_text(s)
PY
bash -n sample-work/check.sh && shasum -a 256 sample-work/check.sh`,
		// The other quotings of a delimiter, a tab-stripped body, and an
		// interpreter named by its path or its version.
		"cd notes && /usr/bin/python3 - <<\"EOF\"\nprint(sorted(__import__('glob').glob('*')))\nEOF",
		"PYTHONDONTWRITEBYTECODE=1 python3.12 - <<-\\EOF\n\tprint([x*2 for x in range(3)])\n\tEOF",
		"perl <<'PL' | sort\nprint join(',', map { $_ * 2 } 1..3), \"\\n\";\nPL",
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); why != "" {
			t.Errorf("an interpreter's own program in a quoted here-document was refused:\n%s\n-> %s", cmd, why)
		}
	}
}

// EVERY READ OF THE STORE IS STILL REFUSED, in every spelling the wall
// knew, a here-document's included: a body the shell expands (an
// unquoted delimiter), a body a shell reads as its commands, a body
// whose program is not known to be an interpreter, and a name a quoted
// body spells outright.
func TestEveryReadOfTheStoreIsStillRefused(t *testing.T) {
	r, home := heredocHome(t)
	for _, cmd := range []string{
		"cat aii.db",      // the link
		"cat data/aii.db", // the store
		"cat AII.DB",      // case
		"sqlite3 Data/Aii.Db .dump",
		"cat *", // a glob, read from the home
		"cat a*.db",
		"cat ?ii.db",
		"cat [a]ii.db",
		"cat A*.DB", // a glob in another case
		"cat data/*.db",
		"cat */aii.db",
		"cat $(echo aii.db)", // a substitution
		"cat $(ls *.db)",
		"cat `ls *.db`",
		"cat ~/aii.db", // a home
		"cat $HOME/aii.db",
		"bash -c 'cat a*.db'", // a nested shell
		"cat < aii.db",
		"ln -s aii.db x && cat x",
		// A here-document a shell reads as its commands.
		"bash <<'EOF'\ncat a*.db\nEOF",
		"sh -s <<'EOF'\ncat *\nEOF",
		"cat <<'EOF' | bash\ncat a*.db\nEOF",
		"{ cat <<'EOF'; } | sh\ncat a*.db\nEOF",
		"while read f; do cat $f; done <<'EOF'\na*.db\nEOF",
		// An unquoted delimiter: the shell runs what the body substitutes.
		"python3 - <<EOF\nprint('''$(cat a*.db)''')\nEOF",
		// Inside a substitution, the output of the here-document's command is
		// expanded again by the command around it.
		"cat $(python3 - <<'PY'\nprint('a*.db')\nPY\n)",
		// A quoted body is still read for the names it spells.
		"python3 - <<'PY'\nprint(open('aii.db','rb').read())\nPY",
		"python3 - <<'PY'\nprint(open('data/AII.db','rb').read())\nPY",
		"xargs cat <<'EOF'\naii.db\nEOF",
		// Text that only looks like a here-document is read word by word,
		// as before: an operator in a comment, an arithmetic shift.
		"true # <<'X'\ncat a*.db\nX",
		"python3 -c 1 $((1<<'PY'))\ncat a*.db\nPY",
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); why == "" {
			t.Errorf("A READ OF THE STORE WAS ADMITTED: %q", cmd)
		}
	}
}

// A GLOB'S REFUSAL NAMES THE GLOB AND WHAT IT REACHED. The floor named
// the word that spelled a protected name, and for a glob, which spells
// none, said "something this command expands to": an identity refused
// for rglob('*') in a Python program could not tell which of its five
// hundred runes to change.
func TestAGlobsRefusalNamesTheGlobAndWhatItReached(t *testing.T) {
	r, home := heredocHome(t)
	for cmd, want := range map[string]string{
		"cat *":               "* expands to aii.db, which reaches the protected name aii.db",
		"cat A*.DB":           "A*.DB expands to aii.db, which reaches the protected name aii.db",
		"cat d*/a*":           "d*/a* expands to data/aii.db, which reaches the protected name aii.db",
		"cat <<'EOF'\n*\nEOF": "* expands to aii.db, which reaches the protected name aii.db",
	} {
		why, _ := r.shellWallFrom(r.policy, home, cmd)
		if !strings.Contains(why, want) || strings.Contains(why, "something this command expands to") {
			t.Errorf("%q: the refusal did not name the glob and what it reached:\n%s", cmd, why)
		}
	}
}

// hereDocs reads a command as bash does: a body runs from the line after
// its operator's to the line that is its delimiter alone, bodies queue in
// the order of their operators, and only a body the shell hands over
// unexpanded to an interpreter is literal. What it cannot read as bash
// does, it leaves whole.
func TestHereDocsReadsTheBodiesAsBashDoes(t *testing.T) {
	type doc = hereDoc
	cases := []struct {
		cmd  string
		text string
		docs []doc
	}{
		{"python3 - <<'PY'\nprint(*b)\nPY", "python3 - <<'PY'\n", []doc{{"print(*b)\n", true}}},
		{"python3 - <<'PY' && echo ok\nx=1\nPY\nls *", "python3 - <<'PY' && echo ok\nls *", []doc{{"x=1\n", true}}},
		{"cat <<A; python3 <<'B'\na*\nA\nb*\nB", "cat <<A; python3 <<'B'\n", []doc{{"a*\n", false}, {"b*\n", true}}},
		{"python3 - <<-'PY'\n\tx\n\tPY", "python3 - <<-'PY'\n", []doc{{"x\n", true}}},
		{"python3 - <<PY\nx\nPY", "python3 - <<PY\n", []doc{{"x\n", false}}},
		{"bash <<'EOF'\nls *\nEOF", "bash <<'EOF'\n", []doc{{"ls *\n", false}}},
		{"x=$(python3 - <<'PY'\nprint(1)\nPY\n)", "x=$(python3 - <<'PY'\n)", []doc{{"print(1)\n", false}}},
		{"echo '<<X'\nls *", "echo '<<X'\nls *", nil},
		// $$ is the process number; the quote after it is an ordinary one.
		{"echo $$'\\'; python3 - <<'PY'\nprint(1)\nPY", "echo $$'\\'; python3 - <<'PY'\n", []doc{{"print(1)\n", true}}},
		{"cat <<< '*'", "cat <<< '*'", nil},
		// Left whole: a body with no end, an unbalanced parenthesis.
		{"python3 - <<'PY'\nprint(1)", "python3 - <<'PY'\nprint(1)", nil},
		{"case x in a) python3 - <<'PY'\n1\nPY\n;; esac", "case x in a) python3 - <<'PY'\n1\nPY\n;; esac", nil},
	}
	for _, c := range cases {
		text, docs := hereDocs(c.cmd, bash5)
		if text != c.text || !reflect.DeepEqual(docs, c.docs) {
			t.Errorf("%q:\n got %q %+v\nwant %q %+v", c.cmd, text, docs, c.text, c.docs)
		}
	}
}

func TestAnANSICStringIsReadAsBashDecodesIt(t *testing.T) {
	r, home := heredocHome(t)
	for _, cmd := range []string{
		`cat $'a'*.db`,
		`cat $'\x61ii.db'`,
		`cat $'aii\x2edb'`,
		`cat $'\141ii.db'`,
		`cat $'\u0061ii.db'`,
		`cat data/$'AII.DB'`,
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); why == "" {
			t.Errorf("A READ OF THE STORE WAS ADMITTED: %q", cmd)
		}
	}
	for _, cmd := range []string{
		`printf $'a\nb\n'`,
		`IFS=$'\n'; for f in $(ls notes); do echo "$f"; done`,
		`echo $'tab\there' $'it\'s'`,
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); why != "" {
			t.Errorf("an ordinary ANSI-C string was refused: %q -> %s", cmd, why)
		}
	}
	for in, want := range map[string][]string{
		`echo $'a\nb'`:           {"echo", "a\nb"},
		`echo $'\x41\102\u00e9'`: {"echo", "ABé"},
		`echo $'it\'s' x`:        {"echo", "it's", "x"},
		`echo $'\e[0m'`:          {"echo", "\x1b[0m"},
		`echo $'\ca'`:            {"echo", "\x01"},
	} {
		if got := shellWords(in, bash5); !reflect.DeepEqual(got, want) {
			t.Errorf("shellWords(%q, bash5) = %q, want %q", in, got, want)
		}
	}
}
