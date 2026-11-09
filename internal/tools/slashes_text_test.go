package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A SLASHES-ONLY PIECE OF A TEXT ARGUMENT IS TEXT (the
// wall once refused "echo 'a // b comment'" as naming the root): under echo
// and printf, which never run or open their arguments, such a piece is
// not judged. A whole word of slashes still names the root, under those
// commands too; under any command that may run its argument the piece is
// judged as before — sed among them (its e command runs a shell), and a
// substitution inside double quotes, which runs as it does outside them
// (a review, 2026-11-07).
func TestASlashesOnlyPieceOfATextArgumentIsText(t *testing.T) {
	r, policy, home := newWordWall(t)
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{
		`echo 'a // b comment'`, `echo "a // b"`, `echo 'a / b'`, `printf '%s\n' 'x // y'`, `echo "a (b) // c"`,
		`printf '%s // %s' a b`, `echo 'a $(x) // b'`, `echo 'a ${x} // b'`,
	} {
		if why, _ := r.shellWallFrom(policy, home, cmd); why != "" {
			t.Errorf("%q: slashes in text were refused as a place: %s", cmd, why)
		}
	}
	for _, cmd := range []string{
		`echo '//'`, `echo //`, `ls //`, `ls /`, `echo "// a comment"`, `sh -c 'ls //'`, `bash -c 'echo a // b'`,
		`xargs -0 'ls //'`, `eval 'ls //'`, `echo 'a // b' | sh`, `echo 'a // b' > notes.txt; ls //`,
		`echo "$(ls //)"`, "echo \"`ls //`\"", `echo "a $(ls //) b"`, `echo "${x:-/} // y"`, `sed -e 's|//|x|' notes.txt`, `sed 'e ls //' notes.txt`, `sed 's/a/b/e' //`,
	} {
		why, _ := r.shellWallFrom(policy, home, cmd)
		if why == "" || !strings.Contains(why, "/") {
			t.Errorf("%q: the root was not refused: %q", cmd, why)
		}
	}
}
