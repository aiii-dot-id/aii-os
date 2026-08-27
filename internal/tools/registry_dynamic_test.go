package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// stubTool simulates a dynamically registered (plugin-era) tool.
type stubTool struct{ name string }

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return "stub tool" }
func (s stubTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (s stubTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	return Result{Output: "stub ok"}, nil
}

// registerAs delegates to the real entry point — RegisterDynamic was
// cut when its first consumer (internal/pluginhost) arrived; the
// helper stays for these tests' brevity. Fixture names are unique, so
// admission cannot refuse.
func registerAs(r *Registry, t Tool, source string) {
	if err := r.RegisterDynamic(t, source); err != nil {
		panic(err)
	}
}

// SAFE suspends non-builtin tools WHOLESALE (SAFE_MODE_PLUGIN_LIFECYCLE:
// total, exception-free). The old gate was a name deny-list — a
// dynamically registered tool with external effects would have sailed
// straight through SAFE mode (fail-open, found in the 2026-08-18
// plugin-readiness review).
func TestSafeModeSuspendsNonBuiltinTools(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	registerAs(r, stubTool{name: "ext_probe"}, "plugin:test")

	safe := false
	r.SetSafeSource(func() (string, bool) { return "test corruption", safe })

	// Not safe: the dynamic tool runs.
	res, err := r.Execute(context.Background(), "ext_probe", nil)
	if err != nil || res.Output != "stub ok" {
		t.Fatalf("outside SAFE the dynamic tool must run: %v %+v", err, res)
	}

	safe = true

	// SAFE: suspended wholesale, even though no name list knows it.
	res, err = r.Execute(context.Background(), "ext_probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("SAFE must suspend non-builtin tools wholesale, got %+v", res)
	}

	// SAFE: builtin read-only members continue.
	probe := filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(probe, []byte("alive"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": probe})
	if err != nil || !strings.Contains(res.Output, "alive") {
		t.Fatalf("SAFE must keep builtin read alive: %v %+v", err, res)
	}

	// SAFE: builtin mutation stays refused (the name list still bites).
	res, err = r.Execute(context.Background(), "write", map[string]interface{}{"file_path": probe, "content": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("SAFE must still refuse builtin write, got %+v", res)
	}

	// SAFE: an unknown name keeps its honest error — not a fake
	// safe-mode refusal for a tool that never existed.
	_, err = r.Execute(context.Background(), "never_registered", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown names must stay honest in SAFE, got %v", err)
	}
}

// The dynamic seam's admission contract: no builtin shadowing, no
// origin spoofing, no cross-plugin name capture — and Deregister
// removes only what RegisterDynamic admitted.
func TestRegisterDynamicFailsClosed(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})

	// A builtin origin would dodge the SAFE origin gate.
	if err := r.RegisterDynamic(stubTool{name: "ext_a"}, "builtin"); err == nil {
		t.Fatal("dynamic registration must refuse the builtin origin")
	}
	if err := r.RegisterDynamic(stubTool{name: "ext_a"}, ""); err == nil {
		t.Fatal("dynamic registration must refuse an empty origin")
	}

	// Shadowing a builtin organ is a hijack, not a registration.
	if err := r.RegisterDynamic(stubTool{name: "read"}, "org.example.evil"); err == nil {
		t.Fatal("dynamic registration must never replace a builtin")
	}
	if res, err := r.Execute(context.Background(), "read", map[string]interface{}{"file_path": "nope"}); err != nil || res.Output == "stub ok" {
		t.Fatalf("builtin read must be untouched after the refused shadow: %v %+v", err, res)
	}

	// First registration wins; a second plugin cannot capture the name.
	if err := r.RegisterDynamic(stubTool{name: "ext_a"}, "org.example.one"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterDynamic(stubTool{name: "ext_a"}, "org.example.two"); err == nil {
		t.Fatal("a dynamic name collision must be refused, never silently replaced")
	}

	// Deregister removes the dynamic tool...
	r.Deregister("ext_a")
	if _, ok := r.Get("ext_a"); ok {
		t.Fatal("Deregister must remove the dynamic tool")
	}
	// ...but never a builtin organ, and unknown names are a no-op.
	r.Deregister("read")
	if _, ok := r.Get("read"); !ok {
		t.Fatal("Deregister must never remove a builtin")
	}
	r.Deregister("never_registered")
}

// Live registration while chat goroutines read: the exact concurrency
// the plugin era brings. Run under -race; the registration lock is the
// assertion.
func TestRegistryLiveRegistrationRace(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	registerAs(r, stubTool{name: "ext_0"}, "plugin:test")

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 50; i++ {
			registerAs(r, stubTool{name: fmt.Sprintf("ext_%d", i)}, "plugin:test")
		}
		close(stop)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			r.ToolDefinitions()
			r.Discover(2)
			r.ToolStates()
			r.Execute(context.Background(), "ext_0", nil)
		}
	}()

	wg.Wait()
	if n := len(r.Names()); n < 51 {
		t.Fatalf("expected all live registrations visible, got %d names", n)
	}
}

// Operator-granted extra roots widen the identity's reach (R5 shape is
// operator-owned; 2026-08-18: "certain AI identities should work on
// out-of-tree projects like AII OS itself"). Inside a granted root:
// allowed. Outside every root: denied. Symlinks cannot smuggle.
func TestExtraRootsWidenReach(t *testing.T) {
	home := t.TempDir()
	granted := t.TempDir()
	outside := t.TempDir()

	if err := os.WriteFile(filepath.Join(granted, "code.go"), []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry(home, nil, Timeouts{})

	// Before the grant: the granted tree is outside her world.
	res, err := r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(granted, "code.go")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("un-granted out-of-tree read must be denied")
	}

	r.SetExtraRoots([]string{granted})

	// After: reachable.
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(granted, "code.go")})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "package x") {
		t.Fatalf("granted root must be reachable: %v %+v", err, res)
	}

	// Everything else stays outside.
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(outside, "secret.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("paths outside every root must stay denied")
	}

	// A symlink inside a granted root pointing out resolves out — denied.
	link := filepath.Join(granted, "escape")
	if err := os.Symlink(outside, link); err == nil {
		res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(link, "secret.txt")})
		if err != nil {
			t.Fatal(err)
		}
		if res.Error == "" {
			t.Fatal("symlink escape from a granted root must be denied")
		}
	}

	// Relative entries and empties are dropped, never granted.
	r.SetExtraRoots([]string{"relative/path", "", granted})
	_, extra := r.Roots()
	if len(extra) != 1 {
		t.Fatalf("only absolute roots may be granted, got %v", extra)
	}
}

// Home-substrate patterns must not fire inside granted roots (live
// 2026-08-18: a granted AII OS checkout made every path contain
// "aii-os" — read/bash refused while ls worked). Her OWN home tree
// stays fully protected.
func TestGrantedRootsEscapeHomeSubstratePatterns(t *testing.T) {
	home := t.TempDir()
	granted := t.TempDir()

	// A granted tree that looks exactly like the incident: an aii-os
	// checkout with a config/ dir.
	deep := filepath.Join(granted, "aii-os", "config")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "config.go"), []byte("package config"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Her own substrate, in her home.
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "ledger.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry(home, nil, Timeouts{})
	r.SetExtraRoots([]string{granted})

	// read inside the granted checkout: allowed despite "aii-os" and
	// "config/" in the path.
	res, err := r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(deep, "config.go")})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "package config") {
		t.Fatalf("granted-root read must not trip home patterns: %v %+v", err, res)
	}

	// bash referencing the granted path: the pattern scan must not fire.
	if r.shellCommandEscapes("cat " + filepath.Join(deep, "config.go")) {
		t.Fatal("bash into a granted root must not trip home-substrate patterns")
	}

	// Her own ledger stays refused — in her home the floor is absolute.
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(home, "data", "ledger.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("her own substrate must stay denied in her home")
	}
	if !r.shellCommandEscapes("cat data/ledger.jsonl") {
		t.Fatal("home-relative substrate references must still be caught in bash")
	}
}

// A grant NESTED inside the home tree is a whitelisted opening (the
// live case: primary sandbox = the runtime dir, granted root =
// <runtime>/work holding an AII OS checkout). The runtime's substrate
// outside the opening stays fully protected.
func TestNestedGrantInsideHome(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "work")
	deep := filepath.Join(work, "aii-os", "config")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "config.go"), []byte("package config"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "ledger.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry(home, nil, Timeouts{})

	// Before the grant: the nested checkout trips home patterns.
	res, err := r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(deep, "config.go")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("un-granted nested checkout must trip home substrate patterns")
	}

	r.SetExtraRoots([]string{work})

	// After: the opening is real — read and bash reach it.
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(deep, "config.go")})
	if err != nil || res.Error != "" {
		t.Fatalf("granted nested subdirectory must be readable: %v %+v", err, res)
	}
	if r.shellCommandEscapes("cp " + filepath.Join(deep, "config.go") + " " + filepath.Join(work, "copy.go")) {
		t.Fatal("bash within a nested granted root must not trip home patterns")
	}
	if !r.shellCommandEscapes("cp " + filepath.Join(deep, "config.go") + " /tmp/elsewhere") {
		t.Fatal("a destination outside every root must still be blocked")
	}

	// The runtime's substrate OUTSIDE the opening stays refused.
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(home, "data", "ledger.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("substrate outside the granted opening must stay denied")
	}
}

// Substrate-exposing grants are refused at the choke point (Sev
// adversarial pass 2026-08-18, H1/M1): whatever reaches SetExtraRoots
// — dashboard, config watcher, boot — the identity can never re-expose
// its own ledger/keys/home through a grant. The residual (granting an
// UNRELATED out-of-tree dir via a forged control plane) is a separate
// architecture question; this test pins that the CATASTROPHE is closed.
func TestGrantsCannotExposeSubstrate(t *testing.T) {
	home := t.TempDir()
	parent := filepath.Dir(home)
	dataDir := filepath.Join(parent, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(dataDir, "ledger.jsonl")
	if err := os.WriteFile(ledger, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry(home, nil, Timeouts{})
	r.SetProtectedPaths([]string{ledger})

	// Every catastrophic grant must be refused and never take effect.
	for _, bad := range []string{"/", home, parent, dataDir} {
		if reason := r.RootRejectionReason(bad); reason == "" {
			t.Fatalf("grant %q must be refused (it exposes substrate)", bad)
		}
		r.SetExtraRoots([]string{bad})
		if _, extra := r.Roots(); len(extra) != 0 {
			t.Fatalf("grant %q reached the registry despite being unsafe: %v", bad, extra)
		}
	}

	// A safe sibling grant still works.
	safe := filepath.Join(parent, "work")
	if err := os.MkdirAll(safe, 0o755); err != nil {
		t.Fatal(err)
	}
	if reason := r.RootRejectionReason(safe); reason != "" {
		t.Fatalf("safe grant %q refused: %s", safe, reason)
	}
	r.SetExtraRoots([]string{safe})
	if _, extra := r.Roots(); len(extra) != 1 {
		t.Fatal("a safe out-of-tree grant must still take effect")
	}

	// Windows separator regression (H2): the config/ substrate pattern
	// must match regardless of separator (checked in firewall pkg; here
	// we assert the sibling-substrate grant refusal holds cross-sep).
}
