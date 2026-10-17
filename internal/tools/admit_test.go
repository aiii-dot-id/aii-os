package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// ADMIT IS THE ONE RULE for a path in the identity's world, whichever
// door it comes through: taken from the home when relative; inside the
// home or a granted root, symlinks resolved; not a name the floor keeps
// in the home, and never a place the floor keeps, wherever it lies. It
// answers with the directory that holds the path — the innermost granted
// root, else the home — and the resolved path relative to it.
func TestAdmitIsTheOneRuleForTheIdentitysWorld(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "vega")
	data := filepath.Join(home, "data")
	work := filepath.Join(home, "work")
	outside := filepath.Join(base, "elsewhere")
	for _, d := range []string{filepath.Join(data, "credentials"), filepath.Join(work, "proj"), outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(data, "credentials", "k.key"), filepath.Join(data, "ui-layout.json"), filepath.Join(work, "proj", "main.go"), filepath.Join(home, "notes.md"), filepath.Join(outside, "x")} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(home, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(data, "credentials"), filepath.Join(work, "creds")); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	dataPlace := &firewall.Rule{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data,
		Except: []string{filepath.Join(data, "ui-layout.json")}, Reason: "kept", Enforced: true}
	policy.SetPlaces([]*firewall.Rule{dataPlace})
	r := NewRegistry(home, policy, Timeouts{})
	r.SetExtraRoots([]string{work})

	for _, c := range []struct{ path, dir, rel string }{
		{"notes.md", home, "notes.md"},
		{filepath.Join(home, "notes.md"), home, "notes.md"},
		{".", home, "."},
		{"work/proj/main.go", work, "proj/main.go"},
		{filepath.Join(work, "proj", "new.go"), work, "proj/new.go"},
		{"data/ui-layout.json", home, "data/ui-layout.json"},
		{filepath.Join(work, "aii-os", "config.json"), work, "aii-os/config.json"},
	} {
		dir, rel, err := r.Admit("read", c.path)
		wantDir, resolveErr := filepath.EvalSymlinks(c.dir)
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		if err != nil || dir != wantDir || filepath.ToSlash(rel) != c.rel {
			t.Fatalf("Admit(%q) = %q %q %v, want %q %q", c.path, dir, rel, err, wantDir, c.rel)
		}
	}
	for _, path := range []string{"../elsewhere/x", filepath.Join(outside, "x"), "out/x", "/"} {
		if _, _, err := r.Admit("read", path); !errors.Is(err, ErrOutsideSandbox) {
			t.Fatalf("Admit(%q) must be outside the sandbox, got %v", path, err)
		}
	}
	for _, path := range []string{"data/credentials/k.key", "data", "Data/Credentials/k.key", "work/creds/k.key", "data/ledger.jsonl", "notes/ledger.jsonl"} {
		var sub *SubstrateError
		if _, _, err := r.Admit("read", path); !errors.As(err, &sub) {
			t.Fatalf("Admit(%q) must be refused as substrate, got %v", path, err)
		}
	}
	// A place named after a root was granted — a profile's credential file
	// the operator keeps inside a granted folder — is kept there too: the
	// names are the home's, a place is the host's wherever it lies.
	token := filepath.Join(work, "proj", "token.json")
	if err := os.WriteFile(token, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy.SetPlaces([]*firewall.Rule{dataPlace, {ID: "sub.profile", Kind: firewall.KindSubstrate, Path: token, Reason: "a profile's", Enforced: true}})
	var sub *SubstrateError
	if _, _, err := r.Admit("read", token); !errors.As(err, &sub) || sub.Rule.ID != "sub.profile" {
		t.Fatalf("a place inside a granted root is kept: %v", err)
	}
	if _, _, err := r.Admit("read", filepath.Join(work, "proj", "main.go")); err != nil {
		t.Fatalf("the rest of the granted root stays open: %v", err)
	}

	// The identity's own read answers from the same rule, in the same words.
	res, _ := r.Execute(context.Background(), "read", map[string]any{"file_path": "work/creds/k.key"})
	if !strings.HasPrefix(res.Error, "access denied: protected path (sub.data: kept)") {
		t.Fatalf("read through a granted root's symlink into a kept place: %+v", res)
	}
	res, _ = r.Execute(context.Background(), "grep", map[string]any{"pattern": "x", "path": "data/credentials"})
	if !strings.HasPrefix(res.Error, "access denied: protected path") {
		t.Fatalf("grep into a kept place: %+v", res)
	}
	for _, cmd := range []string{"cat data/credentials/k.key", "cat " + filepath.Join(data, "credentials", "k.key"), "cat d*/cred*/k.key", "cd data", "cat work/creds/k.key"} {
		if why := r.shellRefusal(cmd); why == "" {
			t.Fatalf("the shell's check must refuse %q", cmd)
		}
	}
	if why := r.shellRefusal("cat data/ui-layout.json"); why != "" {
		t.Fatalf("the open file stays open to the shell: %s", why)
	}
}
