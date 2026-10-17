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

// A hard link has the same file identity as the spelling that the floor
// protects. On a case-folding filesystem the alternate spelling itself
// already has that identity; on a case-sensitive host the hard link gives
// the test the same boundary without pretending the host is APFS or NTFS.
func protectedFileAlias(t *testing.T, home, original, alias string) string {
	t.Helper()
	protected := filepath.Join(home, original)
	spelled := filepath.Join(home, alias)
	if err := os.MkdirAll(filepath.Dir(protected), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protected, []byte("fixture-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spelled); errors.Is(err, os.ErrNotExist) {
		t.Logf("alias proof %s: hard-link fixture (filesystem distinguishes spelling)", alias)
		if err := os.Link(protected, spelled); err != nil {
			t.Fatalf("make an alternate spelling of the protected file: %v", err)
		}
	} else if err != nil {
		t.Fatal(err)
	} else {
		t.Logf("alias proof %s: native filesystem spelling", alias)
	}
	a, err := os.Stat(protected)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(spelled)
	if err != nil || !os.SameFile(a, b) {
		t.Fatalf("alternate spelling must reach the same file: %v", err)
	}
	return spelled
}

func TestSubstrateFloorFollowsTheFileThroughUnicodeCaseAliases(t *testing.T) {
	for _, tc := range []struct{ original, alias, rule string }{
		{"providers.json", "providerſ.json", "sub.providers"},
		{"config.json", "config.jſon", "sub.config"},
		{"data/ledger.jsonl", "data/ledger.jſonl", "sub.ledger"},
		{"data/identity.sec", "data/identity.ſec", "sub.key"},
		{"providers.json", "PROVIDERS.JSON", "sub.providers"},
		{"data/ledger.jsonl", "data/LEDGER.JSONL", "sub.ledger"},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			home := t.TempDir()
			alias := protectedFileAlias(t, home, tc.original, tc.alias)
			policy := firewall.DefaultPolicy()
			if v := policy.Check("read", alias); v.Allowed || v.Rule == nil || v.Rule.ID != tc.rule {
				t.Fatalf("floor let an alias reach %s: %+v", tc.original, v)
			}
			r := NewRegistry(home, policy, Timeouts{})
			_, _, admissionErr := r.Admit("read", alias)
			var denied *SubstrateError
			if !errors.As(admissionErr, &denied) || denied.Rule.ID != tc.rule {
				t.Fatalf("admission must name the protected rule: %v", admissionErr)
			}
			read, err := r.Execute(t.Context(), "read", map[string]any{"file_path": tc.alias})
			if err != nil || !strings.Contains(read.Error, "protected path") || strings.Contains(read.Text(), "fixture-secret") {
				t.Fatalf("read reached a protected file: %+v %v", read, err)
			}
			write, err := r.Execute(t.Context(), "write", map[string]any{"file_path": tc.alias, "content": "tampered"})
			if err != nil || !strings.Contains(write.Error, "protected path") {
				t.Fatalf("write reached a protected file: %+v %v", write, err)
			}
			edit, err := r.Execute(t.Context(), "edit", map[string]any{"file_path": tc.alias, "old_string": "fixture-secret", "new_string": "tampered"})
			if err != nil || !strings.Contains(edit.Error, "protected path") {
				t.Fatalf("edit reached a protected file: %+v %v", edit, err)
			}
			contents, err := os.ReadFile(filepath.Join(home, tc.original))
			if err != nil || string(contents) != "fixture-secret" {
				t.Fatalf("protected file changed: %q %v", contents, err)
			}
			grep, err := r.Execute(t.Context(), "grep", map[string]any{"pattern": "fixture-secret", "path": tc.alias})
			if err != nil || !strings.Contains(grep.Error, "protected path") || strings.Contains(grep.Text(), "fixture-secret") {
				t.Fatalf("grep reached a protected file: %+v %v", grep, err)
			}
			walk, err := r.Execute(t.Context(), "grep", map[string]any{"pattern": "secret", "path": "."})
			if err != nil || strings.Contains(walk.Text(), "fixture-secret") || strings.Contains(walk.Text(), tc.alias) {
				t.Fatalf("recursive grep exposed a protected alias: %+v %v", walk, err)
			}
		})
	}
}

func TestShellNamesAProtectedUnicodeAliasAndLeavesOrdinaryWork(t *testing.T) {
	home := filepath.Join(t.TempDir(), "aii-os-project")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	protectedFileAlias(t, home, "providers.json", "providerſ.json")
	r := NewRegistry(home, nil, Timeouts{})
	if why := r.shellRefusal("echo ready"); why != "" {
		t.Fatalf("a shell command from an aii-os checkout was refused: %q", why)
	}
	if why := r.shellRefusal("cat providerſ.json"); !strings.Contains(why, "providerſ.json") {
		t.Fatalf("direct shell spelling was not refused by name: %q", why)
	}
	if _, ok := r.lookup("shell"); ok {
		result, err := r.Execute(t.Context(), "shell", map[string]any{"command": "cat providerſ.json"})
		if err != nil || !strings.Contains(result.Error, "access denied") || strings.Contains(result.Text(), "fixture-secret") {
			t.Fatalf("shell executed a protected spelling: %+v %v", result, err)
		}
	}
	if why := r.shellRefusal("cat notes.md"); why != "" {
		t.Fatalf("ordinary shell command refused: %q", why)
	}
	ordinaryHome := t.TempDir()
	ordinary := NewRegistry(ordinaryHome, nil, Timeouts{})
	if err := os.WriteFile(filepath.Join(ordinaryHome, "notes.md"), []byte("ordinary work"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ordinary.Execute(context.Background(), "read", map[string]any{"file_path": "notes.md"})
	if err != nil || read.Output != "     1\tordinary work" {
		t.Fatalf("ordinary file read changed: %+v %v", read, err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "work.md"), []byte("granted project"), 0o600); err != nil {
		t.Fatal(err)
	}
	ordinary.SetExtraRoots([]string{project})
	read, err = ordinary.Execute(t.Context(), "read", map[string]any{"file_path": filepath.Join(project, "work.md")})
	if err != nil || read.Output != "     1\tgranted project" {
		t.Fatalf("granted project access changed: %+v %v", read, err)
	}
}

func TestDistinctUnicodeFileRemainsUsableWhereTheFilesystemDistinguishesIt(t *testing.T) {
	home := t.TempDir()
	protected := filepath.Join(home, "providers.json")
	alias := filepath.Join(home, "providerſ.json")
	if err := os.WriteFile(protected, []byte("fixture-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(alias); err == nil {
		// APFS or NTFS: this is the protected file, not an ordinary control.
		t.Log("distinct-file control not applicable: native alias is the protected file")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.WriteFile(alias, []byte("separate user file"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(home, nil, Timeouts{})
	read, err := r.Execute(t.Context(), "read", map[string]any{"file_path": "providerſ.json"})
	if err != nil || read.Output != "     1\tseparate user file" {
		t.Fatalf("separate user file must stay readable: %+v %v", read, err)
	}
	write, err := r.Execute(t.Context(), "write", map[string]any{"file_path": "providerſ.json", "content": "still usable"})
	if err != nil || write.Error != "" {
		t.Fatalf("separate user file must stay writable: %+v %v", write, err)
	}
	if b, err := os.ReadFile(protected); err != nil || string(b) != "fixture-secret" {
		t.Fatalf("writing distinct file changed protected contents: %q %v", b, err)
	}
	if b, err := os.ReadFile(alias); err != nil || string(b) != "still usable" {
		t.Fatalf("distinct-file write did not persist: %q %v", b, err)
	}
}

func TestAliasFloorPreservesGrantedProjectNamesAndOrdinaryShell(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	project := t.TempDir()
	r.SetExtraRoots([]string{project})
	alias := protectedFileAlias(t, project, "providers.json", "providerſ.json")
	for _, name := range []string{filepath.Join(project, "providers.json"), alias} {
		read, err := r.Execute(t.Context(), "read", map[string]any{"file_path": name})
		if err != nil || read.Error != "" || read.Output != "     1\tfixture-secret" {
			t.Fatalf("granted project's own file must remain readable: %+v %v", read, err)
		}
		if why := r.shellRefusal("cat '" + name + "'"); why != "" {
			t.Fatalf("granted project's own file must remain usable by shell: %s", why)
		}
	}
	if _, ok := r.lookup("shell"); ok {
		result, err := r.Execute(t.Context(), "shell", map[string]any{"command": "echo ordinary-work"})
		if err != nil || result.Error != "" || !strings.Contains(result.Output, "ordinary-work") {
			t.Fatalf("ordinary shell must execute: %+v %v", result, err)
		}
	}
}
