package packagefmt

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// THE READER SAYS THE WHOLE OF A TREE PAST THE MEMBER ALLOWANCE, FROM ITS
// INVENTORY. An archive may carry its files, a quarter as many directories
// again, its root and its inventory. The walker refused the member that
// passed that count, in words about members, after the ones before it were
// written. The inventory names every file and so every directory, so the
// refusal is made when it is read: nothing is written, and it carries the
// counts and the least files ceiling that admits the tree.
func TestTheInventorySaysWhenATreeIsPastTheMemberAllowance(t *testing.T) {
	files := []treeFile{{rel: "a/one.py", content: []byte("x = 1\n")}, {rel: "b/two.py", content: []byte("y = 2\n")}}
	archive, digest := buildTree(t, "cp1", files, nil)
	for _, maxFiles := range []int{2, 3} {
		dest := t.TempDir()
		_, err := ExtractTree(bytes.NewReader(archive), digest, TreeLimits{MaxFiles: maxFiles}, dest)
		var over *MemberAllowanceError
		if !errors.As(err, &over) {
			t.Fatalf("a files ceiling of %d: want the member allowance's own refusal, got %v", maxFiles, err)
		}
		if want := (MemberAllowanceError{Files: 2, Directories: 2, MaxFiles: maxFiles, Least: 4}); *over != want {
			t.Errorf("a files ceiling of %d: the refusal carries %+v, want %+v", maxFiles, *over, want)
		}
		var refused *Error
		if !errors.As(err, &refused) || refused.Reason != ReasonCeilingExceeded {
			t.Errorf("a files ceiling of %d: the refusal is not a ceiling's: %v", maxFiles, err)
		}
		if !strings.Contains(err.Error(), "2 files in 2 directories") || !strings.Contains(err.Error(), "the least that admits this tree is 4") {
			t.Errorf("a files ceiling of %d: the refusal reads %q", maxFiles, err)
		}
		if written, _ := os.ReadDir(dest); len(written) != 0 {
			t.Errorf("a files ceiling of %d: %d entries were written before the tree was refused", maxFiles, len(written))
		}
	}
	rep, err := ExtractTree(bytes.NewReader(archive), digest, TreeLimits{MaxFiles: 4}, t.TempDir())
	if err != nil || rep.Files != 2 {
		t.Fatalf("a files ceiling of 4 admits two files in two directories: %+v, %v", rep, err)
	}
}

// The count the inventory is held to is the walker's own ceiling, less the
// root and the inventory: the two cannot drift apart.
func TestTheMemberAllowanceIsTheWalkersCeiling(t *testing.T) {
	for _, maxFiles := range []int{1, 2, 3, 4, 606, 32768} {
		l := TreeLimits{MaxFiles: maxFiles}.filled()
		if got, want := l.tarLimits().semanticMembers, memberAllowance(maxFiles)+2; got != want {
			t.Errorf("a files ceiling of %d: the walker admits %d members, the allowance and two more are %d", maxFiles, got, want)
		}
	}
}

// The least files ceiling is the least: it admits the tree, and one fewer
// does not.
func TestLeastFilesCeilingIsTheLeast(t *testing.T) {
	admits := func(ceiling, files, dirs int) bool { return ceiling >= files && files+dirs <= memberAllowance(ceiling) }
	for files := 0; files <= 60; files++ {
		for dirs := 0; dirs <= 60; dirs++ {
			m := leastFilesCeiling(files, dirs)
			if !admits(m, files, dirs) {
				t.Fatalf("%d files, %d directories: a ceiling of %d does not admit them", files, dirs, m)
			}
			if m > 0 && admits(m-1, files, dirs) {
				t.Fatalf("%d files, %d directories: %d is not the least, %d admits them", files, dirs, m, m-1)
			}
		}
	}
	// The tree the question came from: 606 files in 365 directories.
	if got := leastFilesCeiling(606, 365); got != 777 {
		t.Fatalf("606 files in 365 directories need a files ceiling of 777; got %d", got)
	}
}
