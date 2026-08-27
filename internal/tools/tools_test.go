package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestRegistry(t *testing.T) {
	r := NewRegistry("", nil, Timeouts{})

	names := r.Names()
	if len(names) != 7 {
		t.Errorf("expected 7 tools, got %d: %v", len(names), names)
	}

	expected := []string{"edit", "grep", "ls", "read", "shell", "web_fetch", "write"}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("names[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestDiscover(t *testing.T) {
	r := NewRegistry("", nil, Timeouts{})

	// Depth 1: names only
	infos := r.Discover(1)
	if len(infos) != 7 {
		t.Fatalf("expected 7 infos, got %d", len(infos))
	}
	if infos[0].Description != "" {
		t.Error("depth 1 should have empty description")
	}

	// Depth 2: name + description
	infos = r.Discover(2)
	if infos[0].Description == "" {
		t.Error("depth 2 should have description")
	}
}

func TestReadTool(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	result, err := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": path,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("tool error: %s", result.Error)
	}
	if result.Output != "hello world" {
		t.Errorf("output = %q", result.Output)
	}
}

func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	path := filepath.Join(dir, "output.txt")

	result, err := r.Execute(context.Background(), "write", map[string]interface{}{
		"file_path": path,
		"content":   "written content",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("tool error: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "written content" {
		t.Errorf("file content = %q", string(data))
	}
}

func TestEditTool(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("old content here"), 0644)

	result, err := r.Execute(context.Background(), "edit", map[string]interface{}{
		"file_path":  path,
		"old_string": "old content",
		"new_string": "new content",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("tool error: %s", result.Error)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "new content here" {
		t.Errorf("file content = %q", string(data))
	}
}

func TestShellTool(t *testing.T) {
	r := NewRegistry("", nil, Timeouts{})

	result, err := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "echo 'hello from bash'",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "hello from bash") {
		t.Errorf("output = %q", result.Output)
	}
}

func TestLsTool(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	result, err := r.Execute(context.Background(), "ls", map[string]interface{}{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "a.txt") || !strings.Contains(result.Output, "subdir") {
		t.Errorf("ls output missing entries: %q", result.Output)
	}
}

func TestGrepTool(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	os.WriteFile(filepath.Join(dir, "search.txt"), []byte("contains target word"), 0644)

	result, err := r.Execute(context.Background(), "grep", map[string]interface{}{
		"pattern": "target",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != "" && result.Output == "" {
		t.Fatalf("tool error: %s", result.Error)
	}
	if !strings.Contains(result.Output, "target") {
		t.Errorf("grep output missing match: %q", result.Output)
	}
}

func TestUnknownTool(t *testing.T) {
	r := NewRegistry("", nil, Timeouts{})

	_, err := r.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestDenyListRead(t *testing.T) {
	r := NewRegistry("", nil, Timeouts{})

	// Should deny reading the ledger
	result, _ := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": "data/ledger.jsonl",
	})
	if !strings.HasPrefix(result.Error, "access denied: protected path") {
		t.Errorf("expected access denied, got: %s", result.Error)
	}

	// Should deny reading the identity key
	result, _ = r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": "data/identity.sec",
	})
	if !strings.HasPrefix(result.Error, "access denied: protected path") {
		t.Errorf("expected access denied for key file, got: %s", result.Error)
	}
}

func TestDenyListWrite(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})

	// Should deny writing to the database
	result, _ := r.Execute(context.Background(), "write", map[string]interface{}{
		"file_path": dir + "/aii.db",
		"content":   "malicious",
	})
	if !strings.HasPrefix(result.Error, "access denied: protected path") {
		t.Errorf("expected access denied, got: %s", result.Error)
	}
}

func TestDenyListBash(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})

	// Should deny bash commands targeting the ledger
	result, _ := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "cat data/ledger.jsonl",
	})
	if result.Error == "" || !strings.Contains(result.Error, "access denied") {
		t.Errorf("expected access denied, got: %s", result.Error)
	}

	// Should deny bash commands targeting the key
	result, _ = r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "cp data/identity.sec /tmp/stolen",
	})
	if result.Error == "" || !strings.Contains(result.Error, "access denied") {
		t.Errorf("expected access denied for key exfil, got: %s", result.Error)
	}
}

func TestDenyListAllowsNormalPaths(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	path := filepath.Join(dir, "safe_file.txt")

	// Normal file should be allowed
	result, _ := r.Execute(context.Background(), "write", map[string]interface{}{
		"file_path": path,
		"content":   "safe content",
	})
	if result.Error != "" {
		t.Errorf("normal path should be allowed, got: %s", result.Error)
	}
}

// The first-birth firewall test: a newborn with bash must not be able to
// rummage the operator's home, other identities' workspaces, or the shell
// environment. Every case below is a route the real newborn took on 2026-08-15.
func TestSandboxConfinementFirstBirthLessons(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "own"), 0755)
	os.WriteFile(filepath.Join(dir, "own", "note.txt"), []byte("mine"), 0644)
	outside := t.TempDir() // stand-in for the predecessor workspace
	os.WriteFile(filepath.Join(outside, "AGENTS.md"), []byte("predecessor notes"), 0644)

	t.Setenv("SANDBOX_TEST_SECRET", "marker") // must NOT reach the bash env
	r := NewRegistry(dir, nil, Timeouts{})

	if res, _ := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": filepath.Join(outside, "AGENTS.md")}); res.Error == "" {
		t.Error("read outside sandbox allowed")
	}
	if res, _ := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": "~/clawd/AGENTS.md"}); res.Error == "" {
		t.Error("read tilde path allowed")
	}
	if res, _ := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": "../../etc/passwd"}); res.Error == "" {
		t.Error("read via dotdot-walk allowed")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "cat " + filepath.Join(outside, "AGENTS.md")}); res.Error == "" {
		t.Error("bash cat outside sandbox allowed")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "cat ~/clawd/COMMITMENT.md"}); res.Error == "" {
		t.Error("bash tilde reference allowed")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "ls $HOME"}); res.Error == "" {
		t.Error("bash HOME-var reference allowed")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "ls /work/teledatics/"}); res.Error == "" {
		t.Error("bash absolute path outside sandbox allowed")
	}
	os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte("[]"), 0644)
	if res, _ := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "head ledger.jsonl"}); res.Error == "" {
		t.Error("bash substrate floor lost")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "echo VAR=$SANDBOX_TEST_SECRET"}); strings.Contains(res.Output, "marker") {
		t.Error("bash environment carries host secrets")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]interface{}{
		"command": "cat own/note.txt"}); res.Output != "mine" {
		t.Errorf("bash inside sandbox broken: out=%q err=%q", res.Output, res.Error)
	}
	r2 := NewRegistry(dir, nil, Timeouts{})
	r2.SetToolEnabled("bash", false)
	if res, _ := r2.Execute(context.Background(), "bash", map[string]interface{}{
		"command": "ls"}); res.Error == "" {
		t.Error("disabled bash still executes")
	}
	if res, _ := r.Execute(context.Background(), "ls", map[string]interface{}{
		"path": outside}); res.Error == "" {
		t.Error("ls outside sandbox allowed")
	}
	if res, _ := r.Execute(context.Background(), "ls", map[string]interface{}{
		"path": "own"}); !strings.Contains(res.Output, "note.txt") {
		t.Errorf("ls inside sandbox broken: %q", res.Output)
	}
}

// TestPolicyIsTheEnforcer proves the Ring 5 policy is WIRED: a custom rule
// added to the policy changes enforcement, and the denial lands in the
// policy's audit trail. Before the wiring fix, the policy engine was
// unwired dead code — enforcement used a parallel hardcoded copy, and the
// audit trail was permanently empty.
func TestPolicyIsTheEnforcer(t *testing.T) {
	dir := t.TempDir()
	pol := firewall.DefaultPolicy()
	// A custom rule that exists ONLY here — if the registry still used a
	// hardcoded parallel list, this rule would change nothing.
	custom := &firewall.Rule{
		ID: "sub.test-secret", Kind: firewall.KindSubstrate, Pattern: "operator_secrets.txt",
		Reason: "test rule — must be enforced and audited", Enforced: true,
	}
	pol.AddRule(custom)

	r := NewRegistry(dir, pol, Timeouts{})

	result, _ := r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": "notes/operator_secrets.txt",
	})
	if !strings.Contains(result.Error, "sub.test-secret") {
		t.Fatalf("custom policy rule not enforced — policy is unwired. got: %s", result.Error)
	}

	audit := pol.Audit()
	found := false
	for _, d := range audit {
		if d.RuleID == "sub.test-secret" {
			found = true
		}
	}
	if !found {
		t.Fatalf("denial not recorded in policy audit — audit trail is theater. audit=%v", audit)
	}

	// And the default substrate floor still holds through the policy path
	result, _ = r.Execute(context.Background(), "read", map[string]interface{}{
		"file_path": "data/ledger.jsonl",
	})
	if !strings.Contains(result.Error, "sub.ledger") {
		t.Fatalf("default substrate rule not enforced via policy. got: %s", result.Error)
	}
}

// Finding 3 (2026-08-17 review): expandGlobs used to interpolate the raw
// model command UNQUOTED into `bash -c` — command substitution in the
// token list (`$(touch …)`) executed DURING the denial check, before the
// command was refused. The check must never execute what it denies.
func TestExpandGlobsNoCommandExecution(t *testing.T) {
	dir := t.TempDir()
	sandbox := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sandbox, "data"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandbox, "data", "ledger.jsonl"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(sandbox, nil, Timeouts{})

	marker := filepath.Join(dir, "pwned-marker")
	// A glob triggers the expansion path; the $(...) would have executed
	// in the old string-interpolated form.
	cmd := "cat data/led$(touch " + marker + ")*"

	if !r.shellCommandEscapes(cmd) {
		t.Fatal("command should be denied (matches the ledger deny pattern after expansion)")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("expandGlobs EXECUTED command substitution from the command being checked — the denial check must never run what it denies")
	}
}

// Finding 10: web_fetch reached localhost and cloud-metadata endpoints.
func TestWebFetchSSRFGuard(t *testing.T) {
	wf := &WebFetchTool{maxBytes: 1000}
	// Vectors the guard MUST refuse: loopback (name and IP), link-local
	// (incl. the cloud-metadata address), RFC1918/ULA space, non-http
	// schemes, and embedded credentials.
	blocked := []string{
		"http://127.0.0.1:8080/ws",
		"http://localhost/secret",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/admin",
		"http://192.168.1.1/",
		"http://[fd00::1]/",
		"file://" + "/etc/hostname", // assembled: non-http scheme is the assertion
		"http://user:pass@example.com/",
	}
	for _, u := range blocked {
		res, err := wf.Execute(context.Background(), map[string]interface{}{"url": u})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", u, err)
		}
		if res.Error == "" {
			t.Errorf("web_fetch %q was NOT blocked — SSRF guard hole", u)
		}
	}
	// A public host by name passes the guard (the network fetch itself
	// may fail in a sandboxed CI — the GUARD's verdict is the assertion).
	if err := FetchGuard("https://example.com/"); err != nil {
		t.Errorf("public https URL should pass the guard: %v", err)
	}
}
