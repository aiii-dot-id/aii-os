package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestANegativeSearchSaysWhatItCouldNotRead(t *testing.T) {
	r, home := relRegistry(t)
	for p, body := range map[string]string{"notes.txt": "alpha\n", "sub/a.txt": "beta\n"} {
		full := filepath.Join(home, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	search := func(args map[string]any) Result {
		t.Helper()
		res, err := r.ExecuteModel(context.Background(), "grep", args)
		if err != nil {
			t.Fatal(err)
		}
		if res.Error != "" || res.Refused {
			t.Fatalf("grep %v: a search that found nothing is an answer, not an error: %+v", args, res)
		}
		return res
	}

	// Everything read, nothing found: a negative, and whole.
	whole := search(map[string]any{"pattern": "zzz"})
	if !strings.HasPrefix(whole.Output, "no matches for zzz under ") || whole.Truncated || strings.Contains(whole.Output, "could not be read") {
		t.Fatalf("a search that read everything and found nothing is a whole negative: %+v", whole)
	}

	// The place it was sent does not exist: nothing was searched, and the
	// answer is not the whole negative above.
	missing := search(map[string]any{"pattern": "zzz", "path": "missing.txt"})
	if !missing.Truncated || !strings.Contains(missing.Output, "1 path(s) could not be read") {
		t.Fatalf("a search of a path that is not there must say so and be marked incomplete: %+v", missing)
	}

	// A directory it may not open: the rest is searched, and the negative
	// says what it did not see. (Root opens it anyway; Windows keeps no
	// such mode.)
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	locked := filepath.Join(home, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "inside.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	partial := search(map[string]any{"pattern": "needle"})
	if !partial.Truncated || !strings.Contains(partial.Output, "could not be read") {
		t.Fatalf("the needle is in a directory the search could not open; its negative must say so and be marked incomplete: %+v", partial)
	}
	// A match elsewhere does not hide what was not read.
	found := search(map[string]any{"pattern": "beta"})
	if !strings.Contains(found.Output, "sub/a.txt") || !found.Truncated || !strings.Contains(found.Output, "could not be read") {
		t.Fatalf("a search that matched and also skipped a directory must say both: %+v", found)
	}
}
