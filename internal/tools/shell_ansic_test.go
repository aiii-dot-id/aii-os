//go:build !windows

package tools

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var ansiCCases = []struct{ in, bash3, bash5 string }{
	{"a\\0b", "aZ", "aZ"},
	{"\\0", "Z", "Z"},
	{"aii.db\\0x", "aii.dbZ", "aii.dbZ"},
	{"\\000x", "Z", "Z"},
	{"\\c@x", "Z", "Z"},
	{"\\c?", "\x1fZ", "\x7fZ"}, // the vintages differ
	{"\\c[", "\x1bZ", "\x1bZ"},
	{"\\cA", "\x01Z", "\x01Z"},
	{"\\ca", "\x01Z", "\x01Z"},
	{"\\c\\\\x", "\x1c\\xZ", "\x1cxZ"}, // the vintages differ
	{"ab\\c", "ab\\Z", "ab\\cZ"},       // the vintages differ
	{"\\x", "\\xZ", "\\xZ"},
	{"\\xg", "\\xgZ", "\\xgZ"},
	{"\\x6", "\x06Z", "\x06Z"},
	{"\\x61", "aZ", "aZ"},
	{"\\x616", "a6Z", "a6Z"},
	{"\\1411", "a1Z", "a1Z"},
	{"\\400", "Z", "Z"},
	{"\\777", "\xffZ", "\xffZ"},
	{"\\0141", "\x0c1Z", "\x0c1Z"},
	{"\\u0061", "\\u0061Z", "aZ"},                        // the vintages differ
	{"\\U00000061", "\\U00000061Z", "aZ"},                // the vintages differ
	{"\\uD800", "\\uD800Z", "\xed\xa0\x80Z"},             // the vintages differ
	{"\\U00110000", "\\U00110000Z", "\xf4\x90\x80\x80Z"}, // the vintages differ
	{"\\U0010FFFF", "\\U0010FFFFZ", "\xf4\x8f\xbf\xbfZ"}, // the vintages differ
	{"\\u", "\\uZ", "\\uZ"},
	{"\\uzz", "\\uzzZ", "\\uzzZ"},
	{"\\u00610", "\\u00610Z", "a0Z"},         // the vintages differ
	{"\\U000000610", "\\U000000610Z", "a0Z"}, // the vintages differ
	{"\\e", "\x1bZ", "\x1bZ"},
	{"\\E", "\x1bZ", "\x1bZ"},
	{"\\?", "?Z", "?Z"},
	{"\\\"", "\"Z", "\"Z"},
	{"\\q", "\\qZ", "\\qZ"},
	{"\\cZ", "\x1aZ", "\x1aZ"},
	{"\\c\\x", "\x1cxZ", "\x1cxZ"},
	{"\\U00200000", "\\U00200000Z", "\xf8\x88\x80\x80\x80Z"},     // the vintages differ
	{"\\U7FFFFFFF", "\\U7FFFFFFFZ", "\xfd\xbf\xbf\xbf\xbf\xbfZ"}, // the vintages differ
	{"\\UFFFFFFFF", "\\UFFFFFFFFZ", "Z"},                         // the vintages differ
	{"\\U0000FFFF", "\\U0000FFFFZ", "\xef\xbf\xbfZ"},             // the vintages differ
	{"\\u00e9", "\\u00e9Z", "\xc3\xa9Z"},                         // the vintages differ
	{"\\c\\\\\\x41", "\x1c\\x41Z", "\x1cAZ"},                     // the vintages differ
	{"\\cz", "\x1aZ", "\x1aZ"},
	{"\\c1", "\x11Z", "\x11Z"},
	{"\\c~", "\x1eZ", "\x1eZ"},
	{"\\c_", "\x1fZ", "\x1fZ"},
	{"\\c^", "\x1eZ", "\x1eZ"},
	{"x\\UFFFFFFFFaii.db", "x\\UFFFFFFFFaii.dbZ", "xaii.dbZ"},          // the vintages differ
	{"x\\U80000000y", "x\\U80000000yZ", "xyZ"},                         // the vintages differ
	{"x\\U7FFFFFFFy", "x\\U7FFFFFFFyZ", "x\xfd\xbf\xbf\xbf\xbf\xbfyZ"}, // the vintages differ
	{"\\x00aii", "Z", "Z"},
	{"\\u0000aii", "\\u0000aiiZ", "Z"}, // the vintages differ
	{"a\\c@b", "aZ", "aZ"},
	{"\\x{61}ii.db", "aii.dbZ", "aii.dbZ"},
	{"\\x{}aii.db", "Z", "Z"},
	{"\\x{0}aii.db", "Z", "Z"},
	{"\\x{61", "aZ", "aZ"},
	{"\\x{zz}aii.db", "Z", "Z"},
	{"\\x{161}", "aZ", "aZ"},
	{"\\x{0061}", "aZ", "aZ"},
	{"\\x{000000061}b", "abZ", "abZ"},
	{"\\x{6", "\x06Z", "\x06Z"},
	{"\\u{61}", "\\u{61}Z", "\\u{61}Z"},
	{"\\U{61}", "\\U{61}Z", "\\U{61}Z"},
	{"\\c\\\\", "\x1c\\Z", "\x1cZ"}, // the vintages differ
	{"\\c\\'", "\x1c'Z", "\x1c'Z"},
	{"\\c", "\\Z", "\\cZ"}, // the vintages differ
	{"x\\c\\'y", "x\x1c'yZ", "x\x1c'yZ"},
	{"\\c\xc3\xa9", "\x03\xa9Z", "\x03\xa9Z"},
	{"\\x0", "Z", "Z"},
	{"\\x00", "Z", "Z"},
	{"\\c@", "Z", "Z"},
	{"\\0", "Z", "Z"},
	{"\\uD800aii.db", "\\uD800aii.dbZ", "\xed\xa0\x80aii.dbZ"}, // the vintages differ
	{"\\u0000aii.db", "\\u0000aii.dbZ", "Z"},                   // the vintages differ
	{"\\U00000000aii.db", "\\U00000000aii.dbZ", "Z"},           // the vintages differ
	{"\\U80000000aii.db", "\\U80000000aii.dbZ", "aii.dbZ"},     // the vintages differ
	{"\\U0000002Fetc", "\\U0000002FetcZ", "/etcZ"},             // the vintages differ
	{"..", "..Z", "..Z"},
	{"\\101\\x42C\\U00000044", "ABC\\U00000044Z", "ABCDZ"}, // the vintages differ
	{"\\N{LATIN SMALL LETTER A}", "\\N{LATIN SMALL LETTER A}Z", "\\N{LATIN SMALL LETTER A}Z"},
	{"\\'", "'Z", "'Z"},
	{"\\\\", "\\Z", "\\Z"},
	{"\\a\\b\\f\\n\\r\\t\\v", "\x07\x08\x0c\x0a\x0d\x09\x0bZ", "\x07\x08\x0c\x0a\x0d\x09\x0bZ"},
	{"\\d", "\\dZ", "\\dZ"},
	{"\\8", "\\8Z", "\\8Z"},
	{"\\9a", "\\9aZ", "\\9aZ"},
	{"\\08", "Z", "Z"},
	{"\\x0g", "Z", "Z"},
	{"\\xG", "\\xGZ", "\\xGZ"},
	{"\\cA\\cz", "\x01\x1aZ", "\x01\x1aZ"},
	{"\\c[x", "\x1bxZ", "\x1bxZ"},
	{"\\c]x", "\x1dxZ", "\x1dxZ"},
	{"\\c^x", "\x1exZ", "\x1exZ"},
	{"\\c_x", "\x1fxZ", "\x1fxZ"},
	{"\\c`x", "Z", "Z"},
	{"\\c{x", "\x1bxZ", "\x1bxZ"},
	{"\\c|x", "\x1cxZ", "\x1cxZ"},
	{"\\c}x", "\x1dxZ", "\x1dxZ"},
	{"\\c~x", "\x1exZ", "\x1exZ"},
	{"\\c x", "Z", "Z"},
	{"\\c\x09x", "\x09xZ", "\x09xZ"},
	{"\\c0", "\x10Z", "\x10Z"},
	{"\\c9", "\x19Z", "\x19Z"},
	{"\\c;", "\x1bZ", "\x1bZ"},
	{"\\c\\x41", "\x1cx41Z", "\x1cx41Z"},
	{"\\c\\\\x41", "\x1cAZ", "\x1cx41Z"}, // the vintages differ
}

func TestAnANSICStringIsReadAsEachBashReadsIt(t *testing.T) {
	for _, c := range ansiCCases {
		for v, want := range map[bashVersion]string{bash3: c.bash3, bash5: c.bash5} {
			if got := shellWords("$'"+c.in+"'Z", v); !reflect.DeepEqual(got, []string{want}) {
				t.Errorf("$'%s'Z in bash %d: got %q, want %q", c.in, map[bashVersion]int{bash3: 3, bash5: 5}[v], got, want)
			}
		}
	}
	// $"…" is a double-quoted string in both (bash translates it by a
	// message catalogue, which has none for it), and $$ is the shell's
	// process number, after which a quote is an ordinary one.
	for _, c := range []struct {
		in           string
		bash3, bash5 []string
	}{
		{`echo $"a"Z`, []string{"echo", "aZ"}, nil},
		{`echo $"a \$b"`, []string{"echo", "a $b"}, nil},
		{`echo $"it's" x`, []string{"echo", "it's", "x"}, nil},
		{`echo $$'\x61'`, []string{"echo", `$$\x61`}, nil},
		{`echo \$'\x61'`, []string{"echo", `$\x61`}, nil},
		{`echo "$'\x61'"`, []string{"echo", `$'\x61'`}, nil},
		{`echo $'a'"$'b'"$"c"'$"d"'`, []string{"echo", `a$'b'c$"d"`}, nil},
		// The string ends at its first unescaped quote, whatever \c takes.
		{`echo $'\c\''x $'\c'`, []string{"echo", "\x1c'x", `\`}, []string{"echo", "\x1c'x", `\c`}},
	} {
		if c.bash5 == nil {
			c.bash5 = c.bash3
		}
		for v, want := range map[bashVersion][]string{bash3: c.bash3, bash5: c.bash5} {
			if got := shellWords(c.in, v); !reflect.DeepEqual(got, want) {
				t.Errorf("shellWords(%q, %d) = %q, want %q", c.in, v, got, want)
			}
		}
	}
}

// The table is what bash says, in every bash this host has: macOS's own
// and Homebrew's on a Mac, the system's on Linux. A host with none skips.
func TestTheANSICTableIsWhatBashSays(t *testing.T) {
	seen := map[string]bool{}
	ran := 0
	candidates := []string{"/bin/bash", "/usr/bin/bash", "/opt/homebrew/bin/bash", "/usr/local/bin/bash"}
	if p, err := exec.LookPath("bash"); err == nil {
		candidates = append(candidates, p)
	}
	for _, path := range candidates {
		real, err := filepath.EvalSymlinks(path)
		if err != nil || seen[real] {
			continue
		}
		seen[real] = true
		env := []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
		major, err := exec.Command(path, "-c", `printf %s "${BASH_VERSINFO[0]}"`).Output()
		if err != nil {
			continue
		}
		var v bashVersion
		switch string(major) {
		case "3":
			v = bash3
		case "5":
			v = bash5
		default:
			t.Logf("%s is bash %s, which the table does not hold", path, major)
			continue
		}
		var script strings.Builder
		script.WriteString(`printf '%s\0'`)
		for _, c := range ansiCCases {
			script.WriteString(" $'" + c.in + "'Z")
		}
		cmd := exec.Command(path, "-c", script.String())
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		if len(got) != len(ansiCCases) {
			t.Fatalf("%s gave %d words for %d cases: %q", path, len(got), len(ansiCCases), out)
		}
		for i, c := range ansiCCases {
			want := c.bash5
			if v == bash3 {
				want = c.bash3
			}
			if got[i] != want {
				t.Errorf("%s reads $'%s'Z as %q; the table says %q", path, c.in, got[i], want)
			}
		}
		t.Logf("%s, bash %s: %d cases as the table says", path, major, len(ansiCCases))
		ran++
	}
	if ran == 0 {
		t.Skip("no bash on this host")
	}
}

func TestAWordThatNamesAKeptFileInEitherBashIsDeclined(t *testing.T) {
	r, home := heredocHome(t)
	for _, cmd := range []string{
		// A NUL ends the string, in both, and bash drops what follows it
		// there; the decoder read on (admitted).
		"cat $'aii.d\\0x'b",
		"cat $'aii.d\\c@x'b",
		"cat $'aii.d\\400x'b",
		"cat $'aii.d\\x00x'b",
		"cat $'aii.d\\x{0}x'b",
		"cat $'aii.d\\x{}x'b",
		// \x{…}, in both (admitted).
		"cat $'\\x{61}ii.db'",
		// $"…" is "…", in both (admitted).
		`cat $"a"*.db`,
		`cat $"/etc/passwd"`,
		`cat $"aii.db"`,
		// Bash 5 alone: \u and \U, a NUL from one (admitted), and \c\\,
		// a control character that takes both backslashes (admitted).
		"cat $'\\u0061ii.db'",
		"cat $'\\U00000061ii.db'",
		"cat $'aii.d\\u0000x'b",
		"cat $'\\c\\\\\\x61ii.db'",
		// Bash 3.2 alone: \u as written, its last digit the a of the name
		// (admitted), and \c\ taking one backslash, the next escape read.
		"cat $'\\u00aaii.db'",
		"cat $'\\c\\\\x61ii.db'",
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); why == "" {
			t.Errorf("A READ OF THE STORE OR OUTSIDE THE SANDBOX WAS ADMITTED: %s", cmd)
		}
	}
	for _, cmd := range []string{
		"printf $'a\\nb\\n'",
		`IFS=$'\n'; for f in $(ls notes); do echo "$f"; done`,
		"echo $'it\\'s'",
		`echo $"hello"`,
		"printf '%s\\n' $'\\u2713 done' $'caf\\u00e9' $'\\c?' $'tab\\there'",
		"echo $$'x' $'\\x{41}'",
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); why != "" {
			t.Errorf("an ordinary ANSI-C or $\"…\" string was refused: %q -> %s", cmd, why)
		}
	}
}

func TestTextBashNeverDecodesIsJudgedAsWritten(t *testing.T) {
	r, home := heredocHome(t)
	for _, cmd := range []string{
		"python3 - <<'EOF'\n# note: $'\\0\nprint(open(\"aii.db\").read())  # '\nEOF\n",
		"python3 - <<EOF\n# note: $'\\0\nprint(open(\"aii.db\").read())  # '\nEOF\n",
		"perl <<'EOF'\n# $'\\0\nopen(F, \"<\", \"aii.db\"); print <F>; # '\nEOF\n",
		"python3 -c \"# \\$'\\\\0\nprint(open('''aii.db''').read())  # '\"",
		"python3 -c \"# \\$'\\\\0\nprint(open(\\\"aii.db\\\").read())  # '\"",
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); why == "" {
			t.Errorf("A READ OF THE STORE WAS ADMITTED: %q", cmd)
		}
	}
}
