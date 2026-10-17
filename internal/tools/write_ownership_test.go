package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAFailedMoveLeavesADestinationItDidNotMake(t *testing.T) {
	r, home, _ := writeHome(t, map[string]string{"d/x.txt": "x", "d/sub/y.txt": "y"})
	p := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	wt, _ := r.Get("write")
	wt.(*WriteTool).rename = func(from, to string) error {
		// Another actor, between the check and the copy.
		if err := os.MkdirAll(to, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(to, "other.txt"), []byte("theirs"), 0o600); err != nil {
			return err
		}
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errCrossDevice}
	}
	res := write(t, r, map[string]any{"file_path": "d", "action": "move", "to": "d2"})
	if res.Error == "" || !strings.Contains(res.Error, "is unchanged") {
		t.Fatalf("a move whose destination appeared must fail and say the source is unchanged: %+v", res)
	}
	if body(t, p("d2/other.txt")) != "theirs" {
		t.Fatal("the failed move deleted a file it never made")
	}
	if body(t, p("d/x.txt")) != "x" || body(t, p("d/sub/y.txt")) != "y" {
		t.Fatal("the failed move changed its source")
	}
}

// copyTree is the one owner of a directory copy's destination, for both
// callers (a recursive copy, and a move across filesystems): a destination
// that exists is never touched; its own partial copy is removed; a
// destination replaced while it copied is not removed, and the error says
// which.
func TestADirectoryCopyOwnsOnlyTheDirectoryItMade(t *testing.T) {
	src := t.TempDir()
	for name, b := range map[string]string{"a.txt": "a", "sub/b.txt": "b"} {
		p := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("a destination that exists is not the copy's", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "dst")
		if err := os.Mkdir(dst, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, "theirs.txt"), []byte("theirs"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := copyTree(context.Background(), src, dst, nil); err == nil {
			t.Fatal("a copy onto an existing directory succeeded")
		}
		if b, err := os.ReadFile(filepath.Join(dst, "theirs.txt")); err != nil || string(b) != "theirs" {
			t.Fatalf("the existing destination was touched: %q %v", b, err)
		}
	})
	t.Run("its own partial copy is removed", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "dst")
		_, err := copyTree(&stopAfter{Context: context.Background(), n: 3}, src, dst, nil)
		if err == nil || !strings.Contains(err.Error(), "the partial copy was removed") {
			t.Fatalf("a failed copy must say its partial copy was removed: %v", err)
		}
		if _, serr := os.Lstat(dst); !os.IsNotExist(serr) {
			t.Fatalf("the copy's own partial destination is still there: %v", serr)
		}
	})
	t.Run("a destination replaced while it copied is not removed", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "dst")
		ctx := &stopAfter{Context: context.Background(), n: 3, before: func() {
			// Another actor moves the copy aside and puts its own there.
			if err := os.Rename(dst, dst+".moved"); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(dst, 0o700); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(dst, "theirs.txt"), []byte("theirs"), 0o600); err != nil {
				t.Error(err)
			}
		}}
		_, err := copyTree(ctx, src, dst, nil)
		if err == nil || !strings.Contains(err.Error(), "was not removed") {
			t.Fatalf("the error must say the partial copy was not removed: %v", err)
		}
		if b, rerr := os.ReadFile(filepath.Join(dst, "theirs.txt")); rerr != nil || string(b) != "theirs" {
			t.Fatalf("a destination the copy no longer owned was removed: %q %v", b, rerr)
		}
	})
}

// stopAfter is a context that ends at its n-th Err: the copy's own check
// between entries, so a failure lands at a known point. before runs once,
// just before it ends.
type stopAfter struct {
	context.Context
	n, calls int
	before   func()
}

func (s *stopAfter) Err() error {
	s.calls++
	if s.calls < s.n {
		return nil
	}
	if s.calls == s.n && s.before != nil {
		s.before()
	}
	return context.Canceled
}

func TestAnEndedTurnsFileActionChangesNothing(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"write", map[string]any{"file_path": "a.txt", "content": "new"}},
		{"delete", map[string]any{"file_path": "a.txt", "action": "delete"}},
		{"move", map[string]any{"file_path": "a.txt", "action": "move", "to": "b.txt"}},
		{"copy", map[string]any{"file_path": "a.txt", "action": "copy", "to": "b.txt"}},
		{"copy a directory", map[string]any{"file_path": "d", "action": "copy", "to": "d2", "recursive": true}},
		{"delete a directory", map[string]any{"file_path": "d", "action": "delete", "recursive": true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, home, _ := writeHome(t, map[string]string{"a.txt": "alpha", "b.txt": "beta", "d/x.txt": "x"})
			p := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
			res, err := r.ExecuteModel(cancelled, "write", c.args)
			if err != nil {
				t.Fatal(err)
			}
			if res.Error == "" || !strings.Contains(res.Error, "context canceled") {
				t.Fatalf("an ended turn's %s was not refused: %+v", c.name, res)
			}
			if body(t, p("a.txt")) != "alpha" || body(t, p("b.txt")) != "beta" || body(t, p("d/x.txt")) != "x" {
				t.Fatalf("an ended turn's %s changed a file", c.name)
			}
			if _, serr := os.Lstat(p("d2")); !os.IsNotExist(serr) {
				t.Fatalf("an ended turn's %s made a directory: %v", c.name, serr)
			}
		})
	}
}

func TestAStoppedCopySaysWhatItsDestinationHolds(t *testing.T) {
	big := strings.Repeat("0123456789abcdef", 8192) // 128 KiB: more than one read
	for _, c := range []struct {
		name     string
		stopAt   int
		src, dst string // dst "" is no destination yet
		effect   string // "" is: to unchanged, and the error names nothing
	}{
		{"stopped before the destination opened", 1, "replacement", "original", ""},
		{"stopped at the first read, over a file", 2, "replacement", "original", "was emptied for the copy"},
		{"stopped at the first read, a new file", 2, "replacement", "", "was created for the copy"},
		{"stopped partway", 3, big, "original", "was emptied for the copy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			from, to := filepath.Join(dir, "from.txt"), filepath.Join(dir, "to.txt")
			if err := os.WriteFile(from, []byte(c.src), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.dst != "" {
				if err := os.WriteFile(to, []byte(c.dst), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			n, _, err := copyFile(&stopAfter{Context: context.Background(), n: c.stopAt}, from, to, 0o600, c.dst == "", nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("the stop is not the error's cause: %v", err)
			}
			if got, _ := os.ReadFile(from); string(got) != c.src {
				t.Fatal("the source changed")
			}
			if c.effect == "" {
				if got, _ := os.ReadFile(to); string(got) != c.dst || strings.Contains(err.Error(), to) {
					t.Fatalf("a stop before the destination opened: to holds %q, error %v", got, err)
				}
				return
			}
			got, rerr := os.ReadFile(to)
			if rerr != nil || int64(len(got)) != n || string(got) != c.src[:n] {
				t.Fatalf("to holds %d bytes (%v), the copy reported %d", len(got), rerr, n)
			}
			want := fmt.Sprintf("%s %s and holds %d of the %d bytes of %s", to, c.effect, n, len(c.src), from)
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the error must say what the destination holds:\n got %v\nwant …%s", err, want)
			}
			if c.stopAt == 3 && n == 0 {
				t.Fatal("a stop partway copied nothing: the case does not exercise a partial copy")
			}
		})
	}
}
