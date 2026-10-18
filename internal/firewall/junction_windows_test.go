//go:build windows

package firewall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestThePolicyFollowsAJunction(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := base + `\identity-0`
	data := home + `\data`
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data+`\identity.sec`, []byte("KEPT\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	junction := func(link, target string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("a junction %s -> %s: %v: %s", link, target, err, out)
		}
	}
	junction(home+`\j`, data)
	junction(home+`\a\b`, data)
	junction(home+`\jl`, data+`\later`) // its target is not made yet

	p := DefaultPolicy()
	p.SetPlaces([]*Rule{
		{ID: "sub.data", Kind: KindSubstrate, Path: data, Except: []string{data + `\ui`}, Reason: "kept", Enforced: true},
		{ID: "sub.later", Kind: KindSubstrate, Path: data + `\later`, Reason: "kept", Enforced: true},
	})
	for _, c := range []struct{ through, want string }{
		{home + `\j\identity.sec`, data + `\identity.sec`},
		{home + `\a\b\identity.sec`, data + `\identity.sec`},
		{home + `\j\credentials\new.json`, data + `\credentials\new.json`},
		{home + `\jl\forged.json`, data + `\later\forged.json`},
	} {
		if got := ResolvePath(c.through); !strings.EqualFold(got, c.want) {
			t.Errorf("%s resolves to %s, want %s", c.through, got, c.want)
		}
		if rule := p.Covered(c.through); rule == nil {
			t.Errorf("%s, through a junction, is not covered by a kept place", c.through)
		}
	}
	if rule := p.Covered(home + `\notes.md`); rule != nil {
		t.Errorf("the home is covered: %+v", rule)
	}
}
