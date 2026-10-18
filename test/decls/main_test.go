package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// THE TOOL TELLS A MOVE FROM A CHANGE. That is the whole of what it is
// for: a file split leaves the plain listing as it was; a verbatim move
// leaves the -text listing as it was too; and a declaration that lost a
// byte, or its doc comment, on the way shows in the -text listing on its
// own line and nowhere else.
func TestTheListingTellsAMoveFromAChange(t *testing.T) {
	const fn = "// F answers one.\nfunc F() int { return 1 }\n"
	const rest = "type T struct{}\n\nfunc (t *T) M() {}\n\ntype G[K comparable, V any] struct{}\n\nfunc (g G[K, V]) N() {}\n\nvar V = 1\n\nconst C = 2\n"
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	listing := func(dir, mode string) []string {
		t.Helper()
		out, err := list(dir, mode)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	before := t.TempDir()
	write(before, "a.go", "package p\n\n"+fn+"\n"+rest)
	write(before, "a_test.go", "package p\n\nfunc onlyInATest() {}\n")
	want := []string{"const C", "func *T.M", "func F", "func G.N", "type G", "type T", "var V"}
	if got := listing(before, ""); !slices.Equal(got, want) {
		t.Fatalf("the listing is %q, want %q", got, want)
	}

	// A verbatim move: F, with its doc comment, into a file of its own.
	moved := t.TempDir()
	write(moved, "a.go", "package p\n\n"+rest)
	write(moved, "f.go", "package p\n\n"+fn)
	if got := listing(moved, ""); !slices.Equal(got, want) {
		t.Errorf("a file split changed the plain listing: %q", got)
	}
	if got, was := listing(moved, "-text"), listing(before, "-text"); !slices.Equal(got, was) {
		t.Errorf("a verbatim move changed the -text listing:\nbefore %q\nafter  %q", was, got)
	}

	// Not a move: one byte of the body differs, or the doc comment stayed
	// behind. Each shows on F's line and on no other.
	for name, body := range map[string]string{
		"a changed body":       "// F answers one.\nfunc F() int { return 2 }\n",
		"a lost doc comment":   "func F() int { return 1 }\n",
		"a reworded doc":       "// F answers 1.\nfunc F() int { return 1 }\n",
		"a changed whitespace": "// F answers one.\nfunc F() int  { return 1 }\n",
	} {
		changed := t.TempDir()
		write(changed, "a.go", "package p\n\n"+rest)
		write(changed, "f.go", "package p\n\n"+body)
		if got := listing(changed, ""); !slices.Equal(got, want) {
			t.Errorf("%s: the plain listing changed: %q", name, got)
		}
		was, got := listing(before, "-text"), listing(changed, "-text")
		var differ []int
		for i := range was {
			if was[i] != got[i] {
				differ = append(differ, i)
			}
		}
		if !slices.Equal(differ, []int{2}) {
			t.Errorf("%s: the -text listing differs on lines %v, want only F's (2):\nbefore %q\nafter  %q", name, differ, was, got)
		}
	}

	// -pos names the file and the lines, the doc comment's line not among them.
	if got := listing(moved, "-pos"); got[2] != "func F\tf.go:4-4" {
		t.Errorf("-pos places F at %q", got[2])
	}
}
