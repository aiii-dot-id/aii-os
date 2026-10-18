package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// A kept place and a grant may arrive under different symlink spellings.
// Exercise the actual read door, not just a corrected fixture expectation.
func TestProtectedPlaceAliasesRefuseGrantsAndReads(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	data := filepath.Join(home, "kept")
	open := filepath.Join(data, "surface")
	safe := filepath.Join(base, "work")
	for _, dir := range []string{data, open, safe} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(data, "proof.txt")
	if err := os.WriteFile(secret, []byte("synthetic-private-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "kept-link")
	if err := os.Symlink(data, alias); err != nil {
		t.Fatal(err)
	}
	place := &firewall.Rule{ID: "sub.fixture", Kind: firewall.KindSubstrate,
		Path: alias, Except: []string{filepath.Join(alias, "surface")}, Reason: "kept", Enforced: true}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{place})
	if place.Path != alias || place.Except[0] != filepath.Join(alias, "surface") {
		t.Fatal("publishing the policy changed the operator's input")
	}
	r := NewRegistry(home, policy, Timeouts{})
	for _, bad := range []string{alias, data, filepath.Join(alias, "credentials"), filepath.Join(data, "credentials")} {
		if why := r.RootRejectionReason(bad); why == "" {
			t.Errorf("protected grant %q was accepted", bad)
		}
		r.SetExtraRoots([]string{bad})
		if _, roots := r.Roots(); len(roots) != 0 {
			t.Fatalf("protected grant %q reached the registry: %v", bad, roots)
		}
	}
	for _, bad := range []string{secret, filepath.Join(alias, "proof.txt"), filepath.Join(data, "credentials", "missing")} {
		var sub *SubstrateError
		if _, _, err := r.Admit("read", bad); !errors.As(err, &sub) {
			t.Errorf("protected path %q was not refused by its place: %v", bad, err)
		}
		result, err := r.Execute(context.Background(), "read", map[string]any{"file_path": bad})
		if err != nil || !strings.HasPrefix(result.Error, "access denied: protected path") || result.Output != "" {
			t.Fatalf("protected read %q: %+v %v", bad, result, err)
		}
	}
	// Preserve the deliberate surface exception and a genuinely safe sibling.
	if _, _, err := r.Admit("write", filepath.Join(open, "new.txt")); err != nil {
		t.Fatalf("surface exception lost: %v", err)
	}
	// Re-publishing an exception after its entry becomes a link must not
	// open the protected target. The exception belongs to the surface slot.
	if err := os.Remove(open); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(data, open); err != nil {
		t.Fatal(err)
	}
	policy.SetPlaces([]*firewall.Rule{place})
	linkedSecret := filepath.Join(open, "proof.txt")
	result, err := r.Execute(context.Background(), "read", map[string]any{"file_path": linkedSecret})
	if err != nil || !strings.HasPrefix(result.Error, "access denied: protected path") || result.Output != "" {
		t.Fatalf("surface symlink opened its protected target: %+v %v", result, err)
	}
	if why := r.RootRejectionReason(safe); why != "" {
		t.Fatalf("safe sibling refused: %s", why)
	}
	r.SetExtraRoots([]string{safe})
	if _, _, err := r.Admit("write", filepath.Join(safe, "new.txt")); err != nil {
		t.Fatalf("safe sibling is unreachable: %v", err)
	}
}

func TestShellGlobsKeepTheirMixedCaseLiteralParent(t *testing.T) {
	r, home := relRegistry(t)
	dir := filepath.Join(home, "MixedParent")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"providers.json", "ordinary.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pattern := filepath.Join(dir, "Provi*.json")
	matches := r.expandGlobs([]string{pattern})
	found := false
	for _, match := range matches {
		found = found || match == filepath.Join(dir, "providers.json")
	}
	if !found {
		t.Fatalf("protected pattern lost its literal parent: %v", matches)
	}
	if why := r.shellRefusal("cat " + strconv.Quote(pattern)); why == "" {
		t.Fatal("protected-name glob was admitted")
	}
	if why := r.shellRefusal("cat " + strconv.Quote(filepath.Join(dir, "ordinary*.txt"))); why != "" {
		t.Fatalf("ordinary glob refused: %s", why)
	}
}

func TestShellGrantedCheckoutSurvivesUnrelatedGlobs(t *testing.T) {
	if shellDialect != "bash" {
		t.Skip("reproduces the Unix shell checkout incident")
	}
	r, home := relRegistry(t)
	checkout := filepath.Join(t.TempDir(), "Granted", "aii-os")
	if err := os.MkdirAll(checkout, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(checkout, "proof.go")
	if err := os.WriteFile(file, []byte("fixture-project"), 0600); err != nil {
		t.Fatal(err)
	}
	r.SetExtraRoots([]string{checkout})
	spellings := []string{checkout}
	if lower := strings.ToLower(checkout); lower != checkout {
		if a, err := os.Stat(checkout); err == nil {
			if b, err := os.Stat(lower); err == nil && os.SameFile(a, b) {
				spellings = append(spellings, lower)
			}
		}
	}
	for _, path := range spellings {
		file := filepath.Join(path, "proof.go")
		_, rel, err := r.Admit("read", file)
		if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("granted alias did not yield a contained relative path: %q %v", rel, err)
		}
		read, err := r.Execute(context.Background(), "read", map[string]any{"file_path": file})
		if err != nil || read.Error != "" || !strings.Contains(read.Output, "fixture-project") {
			t.Fatalf("granted alias read failed: %+v %v", read, err)
		}
		cmd := "cd " + strconv.Quote(path) + " && find . -name '*.go' && cat " + strconv.Quote(filepath.Join(path, "proof.go"))
		result, err := r.Execute(context.Background(), "shell", map[string]any{"command": cmd})
		if err != nil || result.Error != "" || !strings.Contains(result.Output, "fixture-project") {
			t.Fatalf("granted checkout %q failed: %+v %v", path, result, err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "providers.json"), []byte("synthetic-private-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := "cat " + strconv.Quote(filepath.Join(home, "Provi*.json"))
	if why := r.shellRefusal(cmd); why == "" {
		t.Fatal("fixing granted globs opened the home substrate")
	}
}
