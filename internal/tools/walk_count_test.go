package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestTheShellsCheckWalksTheKeptDirectoriesOnce(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(body string, parts ...string) string {
		t.Helper()
		p := filepath.Join(append([]string{home}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	key := put("INSIDE\n", "data", "identity.sec")
	for i := 0; i < 40; i++ {
		put("x\n", "data", "store", fmt.Sprintf("f%d", i))
	}
	var named []string
	for i := 0; i < 20; i++ {
		a := put("a note twice\n", "notes", "twin", fmt.Sprintf("t%02d.txt", i))
		if err := os.MkdirAll(filepath.Join(home, "notes", "other"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(a, filepath.Join(home, "notes", "other", fmt.Sprintf("t%02d.txt", i))); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		if i < 10 {
			named = append(named, "notes/twin/"+filepath.Base(a))
		}
		put("a note once\n", "notes", "once", fmt.Sprintf("o%02d.txt", i))
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(stockPlaces(home, filepath.Join(home, "conf", "settings")))
	r := NewRegistry(home, policy, Timeouts{})
	walked := func(what string, do func()) int64 {
		t.Helper()
		before := firewall.WalksForTest()
		do()
		return firewall.WalksForTest() - before
	}

	cmd := "wc -l " + strings.Join(named, " ") + " notes/twin/*.txt"
	if n := walked("the check", func() {
		if why := r.shellRefusal(cmd); why != "" {
			t.Fatalf("a command over the home's own files with two names each passes the check: %q", why)
		}
	}); n != 1 {
		t.Errorf("the shell's check of a command over %d files with two names, and a glob over 20 more, walked the kept directories %d times, want once", len(named), n)
	}
	second := filepath.Join(home, "notes", "k.txt")
	if err := os.Link(key, second); err != nil {
		t.Fatal(err)
	}
	if n := walked("a call", func() {
		res := modelCall(t, r, "shell", map[string]any{"command": cmd + " notes/k.txt"})
		if !res.Refused || !strings.Contains(res.Error, "which the host keeps") {
			t.Errorf("a command that names a second name for the key is refused: %+v", res)
		}
	}); n != 1 {
		t.Errorf("a shell call that names a second name for the key among %d files with two names walked the kept directories %d times, want once", len(named), n)
	}
	if n := walked("files with one name", func() {
		if why := r.shellRefusal("wc -l notes/once/o00.txt notes/once/*.txt"); why != "" {
			t.Fatalf("a command over files with one name passes the check: %q", why)
		}
	}); n != 0 {
		t.Errorf("a command over files with one name walked the kept directories %d times, want never", n)
	}
}
