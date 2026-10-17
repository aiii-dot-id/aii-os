package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// writeHome is a home with files, the registry over it, and a directory
// outside it.
func writeHome(t *testing.T, files map[string]string) (r *Registry, home, outside string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, outside = filepath.Join(base, "home"), filepath.Join(base, "outside")
	for _, d := range []string{home, outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		p := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return NewRegistry(home, firewall.DefaultPolicy(), Timeouts{}), home, outside
}

func write(t *testing.T, r *Registry, args map[string]any) Result {
	t.Helper()
	return modelCall(t, r, "write", args)
}

func body(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func gone(path string) bool {
	_, err := os.Lstat(path)
	return os.IsNotExist(err)
}

func TestWriteMakesParentsAndRefusesAMissingContent(t *testing.T) {
	r, home, _ := writeHome(t, map[string]string{"keep.txt": "kept"})
	res := write(t, r, map[string]any{"file_path": "a/b/c.txt", "content": "hi"})
	if res.Error != "" || body(t, filepath.Join(home, "a", "b", "c.txt")) != "hi" ||
		!strings.HasSuffix(res.Output, "(created "+filepath.Join(home, "a")+")") {
		t.Fatalf("write did not make the missing parents and say so: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": "keep.txt"}); res.ReasonCode != ReasonArgumentsRequired ||
		!strings.Contains(res.Error, "missing required content") || r.MalformedCallCount() != 1 {
		t.Errorf("a write without content must be refused as malformed and counted: %+v (count %d)", res, r.MalformedCallCount())
	}
	if got := body(t, filepath.Join(home, "keep.txt")); got != "kept" {
		t.Errorf("a write without content emptied the file: %q", got)
	}
	if res := write(t, r, map[string]any{"file_path": "keep.txt", "action": "rename", "to": "x"}); !strings.Contains(res.Error, "action must be") {
		t.Errorf("an unknown action: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": "keep.txt", "content": "x", "to": "y.txt"}); !strings.Contains(res.Error, "pass action=move or action=copy") ||
		body(t, filepath.Join(home, "keep.txt")) != "kept" {
		t.Errorf("to without move or copy must be refused, not ignored: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": "keep.txt", "action": "move"}); res.ReasonCode != ReasonArgumentsRequired {
		t.Errorf("a move without to: %+v", res)
	}
}

func TestWriteDeletes(t *testing.T) {
	r, home, _ := writeHome(t, map[string]string{
		"f.txt": "x", "tree/a.txt": "a", "tree/b.txt": "b", "tree/sub/c.txt": "c", "kept/ledger.jsonl": "{}", "kept/notes.md": "n",
	})
	if err := os.Mkdir(filepath.Join(home, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }

	if res := write(t, r, map[string]any{"file_path": "f.txt", "action": "delete"}); res.Output != "deleted the file "+p("f.txt") || !gone(p("f.txt")) {
		t.Errorf("delete a file: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": "empty", "action": "delete"}); res.Output != "deleted the empty directory "+p("empty") || !gone(p("empty")) {
		t.Errorf("an empty directory is deleted without recursive: %+v", res)
	}
	res := write(t, r, map[string]any{"file_path": "tree", "action": "delete"})
	if !strings.Contains(res.Error, "holding 3 files and 1 directory; pass recursive=true") || gone(p("tree/sub/c.txt")) {
		t.Errorf("a directory with something in it needs recursive: %+v", res)
	}
	res = write(t, r, map[string]any{"file_path": "tree", "action": "delete", "recursive": true})
	if res.Output != "deleted "+p("tree")+" and the 3 files and 1 directory under it" || !gone(p("tree")) {
		t.Errorf("a recursive delete says exactly what went: %+v", res)
	}
	res = write(t, r, map[string]any{"file_path": "kept", "action": "delete", "recursive": true})
	if !strings.Contains(res.Error, "1 path(s) inside "+p("kept")+" are protected by the substrate floor; nothing was deleted") || gone(p("kept/notes.md")) {
		t.Errorf("a tree holding a kept name must not be deleted, any of it: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": ".", "action": "delete", "recursive": true}); !strings.Contains(res.Error, "is the home or a granted root itself") {
		t.Errorf("the home itself: %+v", res)
	}
	granted := t.TempDir()
	r.SetExtraRoots([]string{granted})
	if res := write(t, r, map[string]any{"file_path": granted, "action": "delete", "recursive": true}); !strings.Contains(res.Error, "is the home or a granted root itself") || gone(granted) {
		t.Errorf("a granted root itself: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": granted, "action": "move", "to": "moved-grant"}); !strings.Contains(res.Error, "is the home or a granted root itself") || gone(granted) {
		t.Errorf("a granted root moved away: %+v", res)
	}
}

func TestWriteMovesAndCopies(t *testing.T) {
	r, home, _ := writeHome(t, map[string]string{"a.txt": "alpha", "d/x.txt": "x", "d/sub/y.txt": "y", "other.txt": "other"})
	p := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	if err := os.Chmod(p("a.txt"), 0o750); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("x.txt", p("d/ln")); err != nil {
			t.Fatal(err)
		}
	}

	res := write(t, r, map[string]any{"file_path": "a.txt", "action": "copy", "to": "new/b.txt"})
	if res.Output != "copied "+p("a.txt")+" to "+p("new/b.txt")+" (5 bytes; new file) (created "+p("new")+")" || body(t, p("new/b.txt")) != "alpha" || body(t, p("a.txt")) != "alpha" {
		t.Errorf("copy a file into a new directory: %+v", res)
	}
	if st, err := os.Stat(p("new/b.txt")); runtime.GOOS != "windows" && (err != nil || st.Mode().Perm() != 0o750) {
		t.Errorf("a copy keeps the file's permissions: %v %v", st.Mode(), err)
	}
	res = write(t, r, map[string]any{"file_path": "a.txt", "action": "copy", "to": "other.txt"})
	if !strings.Contains(res.Output, "(5 bytes; replaced a file)") || body(t, p("other.txt")) != "alpha" {
		t.Errorf("copy over a file: %+v", res)
	}
	// Another name for the same file: a copy would empty the file it
	// reads from.
	if err := os.Link(p("a.txt"), p("hard.txt")); err != nil {
		t.Fatal(err)
	}
	if res := write(t, r, map[string]any{"file_path": "a.txt", "action": "copy", "to": "hard.txt"}); !strings.Contains(res.Error, "the same file") || body(t, p("a.txt")) != "alpha" {
		t.Errorf("a copy onto its own file: %+v", res)
	}
	res = write(t, r, map[string]any{"file_path": "a.txt", "action": "move", "to": "m/a2.txt"})
	if res.Output != "moved "+p("a.txt")+" to "+p("m/a2.txt")+" (created "+p("m")+")" || !gone(p("a.txt")) || body(t, p("m/a2.txt")) != "alpha" {
		t.Errorf("move a file: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": "m/a2.txt", "action": "move", "to": "d"}); !strings.Contains(res.Error, "to names the new path itself, for example "+p("d/a2.txt")) {
		t.Errorf("a file onto a directory: %+v", res)
	}

	held := func(files string) string { // d/x.txt, d/sub/y.txt, d/ln
		if runtime.GOOS == "windows" {
			return files + " and 1 directory"
		}
		return files + ", 1 link and 1 directory"
	}
	if res := write(t, r, map[string]any{"file_path": "d", "action": "copy", "to": "d2"}); !strings.Contains(res.Error, "pass recursive=true to copy it") || !gone(p("d2")) {
		t.Errorf("a directory is copied only with recursive: %+v", res)
	}
	res = write(t, r, map[string]any{"file_path": "d", "action": "copy", "to": "d2", "recursive": true})
	if res.Output != "copied "+p("d")+" to "+p("d2")+" with the "+held("2 files")+" under it" ||
		body(t, p("d2/sub/y.txt")) != "y" || body(t, p("d/sub/y.txt")) != "y" {
		t.Errorf("copy a directory: %+v", res)
	}
	if target, err := os.Readlink(p("d2/ln")); runtime.GOOS != "windows" && (err != nil || target != "x.txt") {
		t.Errorf("a link is copied as a link: %q %v", target, err)
	}
	for _, bad := range []map[string]any{
		{"file_path": "d", "action": "copy", "to": "d/inner", "recursive": true},
		{"file_path": "d", "action": "move", "to": "d/inner"},
	} {
		if res := write(t, r, bad); !strings.Contains(res.Error, "into itself") || !gone(p("d/inner")) {
			t.Errorf("%v: a directory into itself: %+v", bad, res)
		}
	}
	if res := write(t, r, map[string]any{"file_path": "d", "action": "move", "to": "d2"}); !strings.Contains(res.Error, "already exists; a directory moves to a new path") {
		t.Errorf("a directory onto an existing path: %+v", res)
	}
	if res := write(t, r, map[string]any{"file_path": "d", "action": "move", "to": "d"}); !strings.Contains(res.Error, "same path") {
		t.Errorf("a move onto itself: %+v", res)
	}
	if err := os.WriteFile(p("d/z.txt"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	res = write(t, r, map[string]any{"file_path": "d", "action": "move", "to": "e/d3"})
	if res.Output != "moved "+p("d")+" to "+p("e/d3")+" with the "+held("3 files")+" under it (created "+p("e")+")" || !gone(p("d")) || body(t, p("e/d3/sub/y.txt")) != "y" {
		t.Errorf("move a directory: %+v", res)
	}
}

// to is held to the gate file_path is: outside the sandbox, a protected
// name or place, or a link out of the sandbox, a move or a copy is
// refused and nothing moves. A source outside the sandbox is refused the
// same way, and a tree is surveyed for kept names before anything moves.
func TestMoveAndCopyDestinationsPassTheGate(t *testing.T) {
	r, home, outside := writeHome(t, map[string]string{"a.txt": "alpha", "proj/providers.json": "{}", "proj/main.go": "package main"})
	p := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := runtime.GOOS != "windows"
	if links {
		if err := os.Symlink(outside, p("out")); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []string{"move", "copy"} {
		for _, to := range []string{filepath.Join(outside, "x.txt"), "../outside/x.txt", "ledger.jsonl", "sub/providers.json", "out/x.txt"} {
			if to == "out/x.txt" && !links {
				continue
			}
			res := write(t, r, map[string]any{"file_path": "a.txt", "action": action, "to": to})
			if !strings.HasPrefix(res.Error, "access denied: to ") {
				t.Errorf("%s to %s was not refused at the gate: %+v", action, to, res)
			}
			if body(t, p("a.txt")) != "alpha" || !gone(filepath.Join(outside, "x.txt")) || !gone(p("ledger.jsonl")) || !gone(p("sub")) {
				t.Fatalf("%s to %s changed something", action, to)
			}
		}
		res := write(t, r, map[string]any{"file_path": filepath.Join(outside, "secret.txt"), "action": action, "to": "stolen.txt"})
		if !strings.HasPrefix(res.Error, "access denied: ") || !gone(p("stolen.txt")) || body(t, filepath.Join(outside, "secret.txt")) != "secret" {
			t.Errorf("%s from outside the sandbox: %+v", action, res)
		}
		res = write(t, r, map[string]any{"file_path": "proj", "action": action, "to": "proj2", "recursive": true})
		if !strings.Contains(res.Error, "1 path(s) inside "+p("proj")+" are protected by the substrate floor; nothing was ") || !gone(p("proj2")) || gone(p("proj/providers.json")) {
			t.Errorf("%s of a tree holding a kept name: %+v", action, res)
		}
	}
	// A kept place under a destination that is not kept itself: the
	// survey holds each landing path to the floor too.
	place := filepath.Join(home, "data", "credentials")
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.cred", Kind: firewall.KindSubstrate, Path: place, Reason: "kept", Enforced: true}})
	r2 := NewRegistry(home, policy, Timeouts{})
	if err := os.MkdirAll(p("keys/credentials"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p("keys/credentials/k"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := modelCall(t, r2, "write", map[string]any{"file_path": "keys", "action": "copy", "to": "data", "recursive": true})
	if !strings.Contains(res.Error, "protected by the substrate floor; nothing was copied") || !gone(p("data")) {
		t.Errorf("a copy landing in a kept place: %+v", res)
	}
}

// Across filesystems a rename fails; the move is then a copy and a
// removal, said in the receipt.
func TestMoveCrossesFilesystemsByCopying(t *testing.T) {
	r, home, _ := writeHome(t, map[string]string{"a.txt": "alpha", "d/x.txt": "x", "d/sub/y.txt": "y"})
	p := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	wt, _ := r.Get("write")
	wt.(*WriteTool).rename = func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errCrossDevice}
	}
	res := write(t, r, map[string]any{"file_path": "a.txt", "action": "move", "to": "b.txt"})
	if res.Output != "moved "+p("a.txt")+" to "+p("b.txt")+" (across filesystems: copied, then removed)" || !gone(p("a.txt")) || body(t, p("b.txt")) != "alpha" {
		t.Errorf("a file across filesystems: %+v", res)
	}
	res = write(t, r, map[string]any{"file_path": "d", "action": "move", "to": "d2"})
	if !strings.Contains(res.Output, "(across filesystems: copied, then removed)") || !gone(p("d")) || body(t, p("d2/sub/y.txt")) != "y" {
		t.Errorf("a directory across filesystems: %+v", res)
	}
	wt.(*WriteTool).rename = func(from, to string) error {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: os.ErrPermission}
	}
	if res := write(t, r, map[string]any{"file_path": "b.txt", "action": "move", "to": "c.txt"}); res.Error == "" || gone(p("b.txt")) || !gone(p("c.txt")) {
		t.Errorf("any other rename failure is not copied around: %+v", res)
	}
}
