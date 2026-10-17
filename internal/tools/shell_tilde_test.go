//go:build !windows

package tools

import (
	"strings"
	"testing"
)

// A TILDE IN THE MIDDLE OF A WORD IS NOT A HOME. The shell's check refused
// every command that held a tilde anywhere, "which this check cannot
// resolve", so a revision written the usual way (`git diff HEAD~1`) was
// refused, and a resident had to learn to write `HEAD^` instead. A shell
// expands a tilde at the start of a word and after the ':' or '=' of an
// assignment; in the middle of a word it is a character. Every tilde that
// could be a home is still refused.
func TestATildeInTheMiddleOfAWordIsNotAHome(t *testing.T) {
	r, home, _ := cwdRig(t)
	const refusal = "references ~ or $HOME"
	for _, cmd := range []string{
		"git diff HEAD~1",
		"git log --oneline main~3..HEAD",
		"git show v1.0~2:README.md",
		"git rev-parse HEAD^~",
		"ls notes.txt~ draft_~ a-~",
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); strings.Contains(why, refusal) {
			t.Errorf("a tilde in the middle of a word was read as a home: %q: %s", cmd, why)
		}
	}
	for _, cmd := range []string{
		"~",
		"cat ~/notes.txt",
		"ls ~root",
		"cd ~; ls",
		"x=~/secrets; cat $x",
		"PATH=/bin:~/bin ls",
		"echo {~/a,b}",
		// An empty alternative leaves the tilde at the start of a word, so
		// a tilde after a closing brace stays refused, and with it the
		// rarer revision form HEAD@{1}~2.
		"echo {,x}~/y",
		"git rev-parse HEAD@{1}~2",
		"cat <~/in >~/out",
		"cat $(echo ~/notes.txt)",
		"cat \"$HOME/notes.txt\"",
		"cat a/~/b",
		"echo hi|cat ~/x",
		"cat '~/x'",
	} {
		if why, _ := r.shellWallFrom(r.policy, home, cmd); !strings.Contains(why, refusal) {
			t.Errorf("A TILDE THAT MAY BE A HOME WAS NOT REFUSED: %q (said: %q)", cmd, why)
		}
	}
}
