package firewall

import (
	"strings"
	"testing"
)

// The floor must be GENERATED from enforced rules, never contain
// command-form access language, and stay within budget.
func TestFloorGeneratedFromPolicy(t *testing.T) {
	p := DefaultPolicy()
	floor := p.LocalFloor()

	if len(floor) > 1200 {
		t.Errorf("floor is %d bytes — budget 1200. Bloat is protection decay.", len(floor))
	}

	for _, banned := range []string{"you must not", "never read", "do not read", "you cannot read", "do not write"} {
		if strings.Contains(floor, banned) {
			t.Errorf("floor contains command-form rule %q — walls are facts", banned)
		}
	}

	// Every enforced substrate pattern appears in the floor
	for _, r := range p.rules {
		if r.Enforced && r.Kind == KindSubstrate && r.Pattern != "" {
			if !strings.Contains(floor, r.Pattern) {
				t.Errorf("enforced rule %s (pattern %q) missing from floor — floor drifts from enforcement", r.ID, r.Pattern)
			}
		}
	}

	// Every enforced rule's REASON appears — the identity knows WHY
	for _, r := range p.rules {
		if r.Enforced && r.Kind == KindSubstrate {
			if !strings.Contains(floor, r.Reason) {
				t.Errorf("rule %s reason missing from floor — enforcement without explanation", r.ID)
			}
		}
	}
}

// Policy engine: substrate patterns are denied, others allowed
func TestPolicyCheck(t *testing.T) {
	p := DefaultPolicy()

	denied := []string{
		"data/ledger.jsonl",
		"config/config.json",
		"data/identity.sec",
		"data/aii.db",
		"data/LEDGER.JSONL", // case-insensitive
	}
	for _, path := range denied {
		v := p.Check("read", path)
		if v.Allowed {
			t.Errorf("Check(%q) allowed — substrate floor breached", path)
		}
		if v.Rule == nil {
			t.Errorf("Check(%q) denied without a rule", path)
		}
	}

	allowed := []string{
		"notes.txt",
		"projects/main.go",
		"work/output.md",

		"my-aii-os-notes.md",
		"/home/operator/aii-os-example/note.md",
		"/work/aii-os/README.md",
	}
	for _, path := range allowed {
		v := p.Check("read", path)
		if !v.Allowed {
			t.Errorf("Check(%q) denied unexpectedly by rule %s", path, v.Rule.ID)
		}
	}
}

func TestFloorTextMatchesEnforcement(t *testing.T) {
	p := DefaultPolicy()
	patterns := enforcedNames(p)
	if len(patterns) == 0 {
		t.Fatal("no enforced substrate patterns")
	}
	floor := p.LocalFloor()
	for _, pat := range patterns {
		if !strings.Contains(floor, pat) {
			t.Errorf("pattern %q is enforced but missing from LocalFloor — docs drifted from enforcement", pat)
		}
	}
	// The load-bearing substrate files must be in the default policy
	critical := []string{"ledger.jsonl", "identity.sec"}
	patSet := map[string]bool{}
	for _, pat := range patterns {
		patSet[pat] = true
	}
	for _, c := range critical {
		if !patSet[c] {
			t.Errorf("critical substrate file %q missing from default policy", c)
		}
	}
}

func TestRegistryMatchesPolicy(t *testing.T) {

	registryPatterns := []string{
		"ledger.jsonl", "identity.sec", "aii.db",
		"config.json", "providers.json",
	}
	p := DefaultPolicy()
	policyPatterns := map[string]bool{}
	for _, r := range p.rules {
		if r.Kind == KindSubstrate && r.Enforced {
			policyPatterns[r.Pattern] = true
		}
	}
	for _, rp := range registryPatterns {
		if !policyPatterns[rp] {
			t.Errorf("registry denies %q but policy doesn't declare it — drift. Add the rule or remove the deny entry.", rp)
		}
	}
	for pp := range policyPatterns {
		found := false
		for _, rp := range registryPatterns {
			if rp == pp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("policy declares %q but registry doesn't enforce it — declared-but-unenforced rules are theater.", pp)
		}
	}
}

// A PLACE'S EXCEPTION MAY NAME A DIRECTORY, and a directory is open whole:
// the directory itself, everything beneath it at any depth, in any case —
// and not a sibling that merely shares its prefix. A file exception still
// opens exactly that file. The rest of the place stays the host's.
func TestAPlaceRuleLeavesADirectoryOpenWhole(t *testing.T) {
	p := DefaultPolicy()
	data := "/home/vega/data"
	p.SetPlaces([]*Rule{{ID: "sub.data", Kind: KindSubstrate, Path: data,
		Except: []string{data + "/ui", data + "/theme.json", data + "/ui-layout.json/"},
		Reason: "The host keeps this directory.", Enforced: true}})
	for _, path := range []string{
		data + "/ui", data + "/ui/name", data + "/ui/deeper/still/app.js", "/HOME/Vega/Data/UI/custom.css",
		data + "/theme.json", data + "/ui-layout.json",
	} {
		if v := p.Check("write", path); !v.Allowed {
			t.Fatalf("%s must be open, refused by %s", path, v.Rule.ID)
		}
		if p.Covered(path) != nil {
			t.Fatalf("%s must not be covered", path)
		}
	}
	for _, path := range []string{
		data + "/ui2/x", data + "/uifoo", data + "/ui-notes/x", data + "/theme.json.bak",
		data + "/credentials/x.key", data + "/segment-1-2.jsonl.gz", data,
	} {
		if v := p.Check("write", path); v.Allowed {
			t.Fatalf("%s must stay the host's: a directory exception opens what is beneath it, not what shares its prefix", path)
		}
	}
}

// A RULE HOLDS A PLACE OF ITS OWN, compared as the floor compares: the
// place and everything beneath it, in any case, and not a sibling that
// merely shares its prefix.
func TestARuleCoversItsOwnPlaceAsTheFloorDoes(t *testing.T) {
	r := &Rule{ID: "sub.ui", Kind: KindSubstrate, Path: "/home/vega/data/ui", Enforced: true}
	for _, path := range []string{"/home/vega/data/ui", "/home/vega/data/ui/name", "/HOME/Vega/Data/UI/deeper/x.js"} {
		if !r.Covers(path) {
			t.Errorf("%s lies in the place", path)
		}
	}
	for _, path := range []string{"/home/vega/data", "/home/vega/data/ui2/x", "/home/vega/data/uifoo", "/home/vega/data2/ui/x"} {
		if r.Covers(path) {
			t.Errorf("%s does not lie in the place", path)
		}
	}
}

// A PLACE RULE covers a path whole — the place and everything inside it,
// in any case — leaves open exactly the files it names, not a sibling
// that merely shares its prefix; SetPlaces replaces the places and keeps
// the names; and the floor names each place once, with its reason, with
// no empty entry for the boundary.
func TestAPlaceRuleCoversAPathWhole(t *testing.T) {
	p := DefaultPolicy()
	data := "/home/vega/data"
	p.SetPlaces([]*Rule{{ID: "sub.data", Kind: KindSubstrate, Path: data, Except: []string{data + "/ui-layout.json"},
		Reason: "The host keeps this directory.", Enforced: true}})
	for _, path := range []string{data, data + "/credentials/profiles/x.key", "/HOME/Vega/Data/tls/key.pem", data + "/segment-1-2.jsonl.gz"} {
		if v := p.Check("read", path); v.Allowed || v.Rule.ID != "sub.data" {
			t.Fatalf("%s must be refused by the place rule, got %+v", path, v)
		}
		if p.Covered(path) == nil {
			t.Fatalf("%s must be covered", path)
		}
	}
	for _, path := range []string{data + "/ui-layout.json", "/home/vega/data2/x", "/home/vega/work/data.txt", "/home/vega"} {
		if v := p.Check("read", path); !v.Allowed {
			t.Fatalf("%s must be open, refused by %s", path, v.Rule.ID)
		}
	}
	if v := p.Check("read", "/home/vega/work/ledger.jsonl"); v.Allowed {
		t.Fatal("the names still hold beside the places")
	}
	if p.Covered("/home/vega/work/ledger.jsonl") != nil {
		t.Fatal("Covered is the places only")
	}
	floor := p.LocalFloor()
	if !strings.Contains(floor, "- **data** — The host keeps this directory.") || strings.Contains(floor, "****") {
		t.Fatalf("the floor names the place once and nothing empty:\n%s", floor)
	}
	p.SetPlaces([]*Rule{{ID: "sub.data", Kind: KindSubstrate, Path: data, Reason: "The host keeps this directory.", Enforced: true},
		{ID: "sub.config", Kind: KindSubstrate, Path: "/home/vega/config/config.json", Reason: DefaultPolicy().Rule("sub.config").Reason, Enforced: true},
		{ID: "sub.db", Kind: KindSubstrate, Path: "/srv/db/aii.db", Reason: DefaultPolicy().Rule("sub.db").Reason, Enforced: true},
		{ID: "sub.db", Kind: KindSubstrate, Path: "/srv/db/aii.db-wal", Reason: DefaultPolicy().Rule("sub.db").Reason, Enforced: true}})
	floor = p.LocalFloor()
	if strings.Count(floor, "config.json") != 1 || !strings.Contains(floor, "- **aii.db**, **aii.db-wal** — ") || strings.Count(floor, "The host keeps this directory.") != 1 {
		t.Fatalf("a reason is said once, with every name and place it covers:\n%s", floor)
	}
	p.SetPlaces([]*Rule{{ID: "sub.plugins", Kind: KindSubstrate, Path: "/home/vega/plugins", Reason: "r", Enforced: true}})
	if got := p.Places(); len(got) != 1 || got[0] != ResolvePath("/home/vega/plugins") {
		t.Fatalf("SetPlaces replaces the places: %v", got)
	}
	if v := p.Check("read", data+"/x"); !v.Allowed {
		t.Fatal("a replaced place no longer holds")
	}
	if len(enforcedNames(p)) != len(enforcedNames(DefaultPolicy())) {
		t.Fatal("SetPlaces keeps every name")
	}
}

// enforcedNames are the enforced substrate names — the patterns the
// tools layer refuses wherever a path carries them.
func enforcedNames(p *Policy) []string {
	var out []string
	for _, r := range p.rules {
		if r.Enforced && r.Kind == KindSubstrate && r.Pattern != "" {
			out = append(out, r.Pattern)
		}
	}
	return out
}
