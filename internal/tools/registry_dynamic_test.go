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

// .
type stubTool struct{ name string }

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return "stub tool" }
func (s stubTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (s stubTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	return Result{Output: "stub ok"}, nil
}

// .
// .
// .
// .
func registerAs(r *Registry, t Tool, source string) {
	if err := r.RegisterDynamic(t, source); err != nil {
		panic(err)
	}
}

// .
// .
// .
// .
// .
func TestSafeModeSuspendsNonBuiltinTools(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	registerAs(r, stubTool{name: "ext_probe"}, "plugin:test")

	safe := false
	r.SetSafeSource(func() (string, bool) { return "test corruption", safe })

	// .
	res, err := r.Execute(context.Background(), "ext_probe", nil)
	if err != nil || res.Output != "stub ok" {
		t.Fatalf("outside SAFE the dynamic tool must run: %v %+v", err, res)
	}

	safe = true

	// .
	res, err = r.Execute(context.Background(), "ext_probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("SAFE must suspend non-builtin tools wholesale, got %+v", res)
	}

	// .
	probe := filepath.Join(dir, "probe.txt")
	if err := os.WriteFile(probe, []byte("alive"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": probe})
	if err != nil || !strings.Contains(res.Output, "alive") {
		t.Fatalf("SAFE must keep builtin read alive: %v %+v", err, res)
	}

	// .
	res, err = r.Execute(context.Background(), "write", map[string]interface{}{"file_path": probe, "content": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "safe mode") {
		t.Fatalf("SAFE must still refuse builtin write, got %+v", res)
	}

	// .
	// .
	_, err = r.Execute(context.Background(), "never_registered", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown names must stay honest in SAFE, got %v", err)
	}
}

// .
// .
// .
func TestRegisterDynamicFailsClosed(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})

	// .
	if err := r.RegisterDynamic(stubTool{name: "ext_a"}, "builtin"); err == nil {
		t.Fatal("dynamic registration must refuse the builtin origin")
	}
	if err := r.RegisterDynamic(stubTool{name: "ext_a"}, ""); err == nil {
		t.Fatal("dynamic registration must refuse an empty origin")
	}

	// .
	if err := r.RegisterDynamic(stubTool{name: "read"}, "org.example.evil"); err == nil {
		t.Fatal("dynamic registration must never replace a builtin")
	}
	if res, err := r.Execute(context.Background(), "read", map[string]interface{}{"file_path": "nope"}); err != nil || res.Output == "stub ok" {
		t.Fatalf("builtin read must be untouched after the refused shadow: %v %+v", err, res)
	}

	// .
	if err := r.RegisterDynamic(stubTool{name: "ext_a"}, "org.example.one"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterDynamic(stubTool{name: "ext_a"}, "org.example.two"); err == nil {
		t.Fatal("a dynamic name collision must be refused, never silently replaced")
	}

	// .
	r.Deregister("ext_a")
	if _, ok := r.Get("ext_a"); ok {
		t.Fatal("Deregister must remove the dynamic tool")
	}
	// .
	r.Deregister("read")
	if _, ok := r.Get("read"); !ok {
		t.Fatal("Deregister must never remove a builtin")
	}
	r.Deregister("never_registered")
}

// .
// .
// .
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

// .
// .
// .
// .
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

	// .
	res, err := r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(granted, "code.go")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("un-granted out-of-tree read must be denied")
	}

	r.SetExtraRoots([]string{granted})

	// .
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(granted, "code.go")})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "package x") {
		t.Fatalf("granted root must be reachable: %v %+v", err, res)
	}

	// .
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(outside, "secret.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("paths outside every root must stay denied")
	}

	// .
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

	// .
	r.SetExtraRoots([]string{"relative/path", "", granted})
	_, extra := r.Roots()
	if len(extra) != 1 {
		t.Fatalf("only absolute roots may be granted, got %v", extra)
	}
}

// .
// .
// .
// .
func TestGrantedRootsEscapeHomeSubstratePatterns(t *testing.T) {
	home := t.TempDir()
	granted := t.TempDir()

	// .
	// .
	deep := filepath.Join(granted, "aii-os", "config")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "config.go"), []byte("package config"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "ledger.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry(home, nil, Timeouts{})
	r.SetExtraRoots([]string{granted})

	// .
	// .
	res, err := r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(deep, "config.go")})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "package config") {
		t.Fatalf("granted-root read must not trip home patterns: %v %+v", err, res)
	}

	// .
	if r.shellCommandEscapes("cat " + filepath.Join(deep, "config.go")) {
		t.Fatal("bash into a granted root must not trip home-substrate patterns")
	}

	// .
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

// .
// .
// .
// .
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

	// .
	res, err := r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(deep, "config.go")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("un-granted nested checkout must trip home substrate patterns")
	}

	r.SetExtraRoots([]string{work})

	// .
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

	// .
	res, err = r.Execute(context.Background(), "read", map[string]interface{}{"file_path": filepath.Join(home, "data", "ledger.jsonl")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatal("substrate outside the granted opening must stay denied")
	}
}

// .
// .
// .
// .
// .
// .
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

	// .
	for _, bad := range []string{"/", home, parent, dataDir} {
		if reason := r.RootRejectionReason(bad); reason == "" {
			t.Fatalf("grant %q must be refused (it exposes substrate)", bad)
		}
		r.SetExtraRoots([]string{bad})
		if _, extra := r.Roots(); len(extra) != 0 {
			t.Fatalf("grant %q reached the registry despite being unsafe: %v", bad, extra)
		}
	}

	// .
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

	// .
	// .
	// .
}
