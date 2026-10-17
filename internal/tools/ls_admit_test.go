package tools

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestLsIsAdmittedLikeTheOtherFileTools(t *testing.T) {
	r, home, _ := writeHome(t, map[string]string{
		"data/credentials/openai-key.txt": "sk-SECRET",
		"data/notes.txt":                  "mine",
		"ledger.jsonl":                    "{}",
	})
	cred := filepath.Join(home, "data", "credentials")
	r.policy.SetPlaces([]*firewall.Rule{{ID: "sub.data.credentials", Kind: firewall.KindSubstrate, Path: cred,
		Reason: "kept", Enforced: true}})
	for _, c := range []struct{ path, rule string }{
		{"data/credentials", "sub.data.credentials"},
		{cred, "sub.data.credentials"},
		{"Data/Credentials", "sub.data.credentials"},
		{"data/credentials/openai-key.txt", "sub.data.credentials"},
		{"ledger.jsonl", "sub.ledger"},
	} {
		for _, recursive := range []bool{false, true} {
			res := modelCall(t, r, "ls", map[string]any{"path": c.path, "recursive": recursive})
			if !res.Refused || res.ReasonCode != ReasonPathRefused || !strings.Contains(res.Error, c.rule) || res.Output != "" {
				t.Errorf("ls %s (recursive=%v) must be refused by %s and say nothing of it: %+v", c.path, recursive, c.rule, res)
			}
		}
		if res := modelCall(t, r, "grep", map[string]any{"pattern": "sk", "path": c.path}); !res.Refused || !strings.Contains(res.Error, c.rule) {
			t.Errorf("grep %s answers as ls now does: %+v", c.path, res)
		}
	}
	// The control: a listing of the directory that holds the place lists
	// what is the identity's, and counts the kept entry without naming it.
	res := modelCall(t, r, "ls", map[string]any{"path": "data"})
	if res.Refused || res.Error != "" || !strings.Contains(res.Output, "notes.txt") || strings.Contains(res.Output, "credentials") ||
		!strings.Contains(res.Output, "1 path(s) protected by the substrate floor were not listed") {
		t.Fatalf("ls data lists the identity's entries and counts the kept one: %+v", res)
	}
}
