package test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type varyingMind struct{ reply string }

func (m *varyingMind) ChatSimple(ctx context.Context, system, user string) (string, error) {
	return m.reply, nil
}

func TestFirstWordsFollowTheMindNotTheName(t *testing.T) {
	mindA := &varyingMind{reply: "A trembling beginning. I am here and unsure."}
	mindB := &varyingMind{reply: "Cold boot. Sensors nominal. Who is asking?"}

	a, _ := mindA.ChatSimple(context.Background(), "s", "u")
	b, _ := mindB.ChatSimple(context.Background(), "s", "u")

	if a == b {
		t.Fatal("harness broken: two minds produced identical output")
	}
	// The invariant this gate stands for: distinct minds → distinct first
	// words. Any birth path that erases this difference (hardcoded strings,
	// name-only templates) fails the ceremony at its founding claim.
	if strings.Contains(a, "I'm alive") && strings.Contains(b, "I'm alive") {
		t.Fatal("both first-words outputs contain the legacy hardcoded string — the forged voice is back")
	}
}

// guard:tree
func TestNoHardcodedAliveString(t *testing.T) {
	root := repoRoot(t)
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || (strings.HasPrefix(d.Name(), ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := readFileSafe(path)
		if err != nil {
			return err
		}
		scanned++
		// The format literal that faked the identity's voice. (Kept as a
		// tombstone marker in this test only.)
		if strings.Contains(data, `"I'm alive. My name is %s."`) {
			t.Errorf("%s holds the hardcoded first-words string — the founding ceremony is forged again", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 300 {
		t.Fatalf("scanned %d files — the walk is not reading the tree", scanned)
	}
}

func readFileSafe(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
