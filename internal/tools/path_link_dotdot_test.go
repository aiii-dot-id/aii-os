package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WHAT IS JUDGED IS WHAT IS OPENED. Admission reads an absolute path with
// "x/.." taken out before any link is followed. The file tools were then
// handed the path as written, and the kernel follows a directory link
// before it applies the "..". So a path through a link in the home and
// back up was judged as an innocent path inside the home and opened
// beside the link's target: a read, or a write, outside the sandbox.
func TestAPathThroughALinkAndBackUpOpensWhatWasJudged(t *testing.T) {
	r, sandbox := relRegistry(t)
	elsewhere := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(elsewhere); err == nil {
		elsewhere = resolved
	}
	if err := os.Mkdir(filepath.Join(elsewhere, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "not the identity's to read"
	if err := os.WriteFile(filepath.Join(elsewhere, "secret.txt"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	// A link in the home to a directory outside it: a checkout's own, or
	// one a copied tree brought with it.
	if err := os.Symlink(filepath.Join(elsewhere, "sub"), filepath.Join(sandbox, "link")); err != nil {
		t.Skipf("no directory links here: %v", err)
	}
	sep := string(filepath.Separator)
	through := func(name string) string { return sandbox + sep + "link" + sep + ".." + sep + name }

	args := map[string]any{"file_path": through("secret.txt")}
	res, err := r.Execute(context.Background(), "read", args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, secret) {
		t.Fatalf("A READ LEFT THE SANDBOX: %s was judged as %s and opened beside the link's target", through("secret.txt"), filepath.Join(sandbox, "secret.txt"))
	}
	if got, want := args["file_path"], filepath.Join(sandbox, "secret.txt"); got != want {
		t.Errorf("the tool was handed %q, want the path as it was judged, %q", got, want)
	}

	if _, err := r.Execute(context.Background(), "write", map[string]any{"file_path": through("planted.txt"), "content": "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "planted.txt")); err == nil {
		t.Fatal("A WRITE LEFT THE SANDBOX: a file was created beside the link's target")
	}
	if _, err := os.Stat(filepath.Join(sandbox, "planted.txt")); err != nil {
		t.Errorf("the write did not land where it was judged, in the home: %v", err)
	}

	for _, tool := range []string{"grep", "ls"} {
		args := map[string]any{"path": through("."), "pattern": "identity"}
		if _, err := r.Execute(context.Background(), tool, args); err != nil {
			t.Fatal(err)
		}
		if got := args["path"]; got != sandbox {
			t.Errorf("%s was handed %q, want the path as it was judged, %q", tool, got, sandbox)
		}
	}
}
