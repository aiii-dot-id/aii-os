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

// The grep walk's per-file refusal must be Admit's refusal. It was not: the
// walk applied the home-relative name patterns alone, so under a granted
// checkout (every absolute path of which contains "aii-os") each file was
// counted "protected by the substrate floor" and the search came back
// empty, while read and Admit opened the very same files (live, v0.1.12).
// The home's own kept names and kept places must stay refused.
func TestGrepWalkReachesAdmitsVerdictUnderAGrantedRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "vega")
	data := filepath.Join(home, "data")
	granted := filepath.Join(base, "grants", "aii-os")

	files := map[string]string{
		filepath.Join(granted, "internal", "plain.go"): "needle-granted-plain\n",
		filepath.Join(granted, "config.json"):          "needle-granted-config\n", // a kept NAME at home, the grant's to open
		filepath.Join(home, "notes.md"):                "needle-home-notes\n",
		filepath.Join(home, "ledger.jsonl"):            "needle-home-ledger\n", // kept name in the home
		filepath.Join(data, "credentials", "k.key"):    "needle-home-place\n",  // kept place
		filepath.Join(data, "ui-layout.json"):          "needle-home-except\n", // the place's exception
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data,
		Except: []string{filepath.Join(data, "ui-layout.json")}, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{})
	r.SetExtraRoots([]string{granted})

	grep, ok := r.Get("grep")
	if !ok {
		t.Fatal("registry has no grep tool")
	}
	found := map[string]bool{}
	for _, root := range []string{home, granted} {
		res, execErr := grep.Execute(context.Background(), map[string]any{"pattern": "needle-", "path": root})
		if execErr != nil {
			t.Fatal(execErr)
		}
		for p, body := range files {
			if strings.Contains(res.Output, strings.TrimSpace(body)) {
				found[p] = true
			}
		}
	}

	// Agreement: the walk finds a file exactly when Admit admits it.
	for p, body := range files {
		_, _, admitErr := r.Admit("grep", p)
		var sub *SubstrateError
		if admitErr != nil && !errors.As(admitErr, &sub) {
			t.Fatalf("Admit(%q): unexpected error %v", p, admitErr)
		}
		if want := admitErr == nil; found[p] != want {
			t.Errorf("%s: grep found=%v but Admit allowed=%v (%q)", strings.TrimPrefix(p, base), found[p], want, strings.TrimSpace(body))
		}
	}
	// Pinned verdicts, so a change that moves Admit and the walk together
	// cannot pass as agreement.
	for p, want := range map[string]bool{
		filepath.Join(granted, "internal", "plain.go"): true,
		filepath.Join(granted, "config.json"):          true,
		filepath.Join(home, "notes.md"):                true,
		filepath.Join(data, "ui-layout.json"):          true,
		filepath.Join(home, "ledger.jsonl"):            false,
		filepath.Join(data, "credentials", "k.key"):    false,
	} {
		if found[p] != want {
			t.Errorf("%s: grep found=%v, want %v", strings.TrimPrefix(p, base), found[p], want)
		}
	}
}
