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
	// The build's builtins are the same list on every host, whatever this
	// one registered: tools.disabled is read against it. AllBuiltins asks a
	// table built for a host with no shell and no writes, so a name that
	// followed the host would go missing here, on this host too.
	if all := AllBuiltins(); strings.Join(all, ",") != strings.Join(expected, ",") {
		t.Errorf("AllBuiltins() = %v, want %v", all, expected)
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

	result, err := r.Execute(context.Background(), "read", map[string]any{
		"file_path": path,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("tool error: %s", result.Error)
	}
	if result.Output != "     1\thello world" {
		t.Errorf("output = %q", result.Output)
	}
}

func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	path := filepath.Join(dir, "output.txt")

	result, err := r.Execute(context.Background(), "write", map[string]any{
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

// THE RECEIPT SAYS WHAT THE WRITE REPLACED. A whole-file write that drops a
// call the rest of the package depended on reads exactly like one that adds
// a line; the caller's only witness is what the tool answers, so the answer
// carries the previous size and the line delta — and says plainly when there
// was nothing there before.
func TestWriteToolReceiptNamesWhatItReplaced(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	path := filepath.Join(dir, "pkg.go")

	write := func(t *testing.T, content string) string {
		t.Helper()
		result, err := r.Execute(context.Background(), "write", map[string]any{
			"file_path": path, "content": content,
		})
		if err != nil || result.Error != "" {
			t.Fatalf("write failed: %v %s", err, result.Error)
		}
		return result.Output
	}

	if out := write(t, "one\ntwo\nthree\n"); !strings.Contains(out, "(new file)") {
		t.Fatalf("a first write must say it made the file, got %q", out)
	}

	// The shrink that matters: two lines gone, and the receipt says so.
	if out := write(t, "one\n"); !strings.Contains(out, "(replaced 14 bytes; −2 lines)") {
		t.Fatalf("a write that deleted two lines reported %q", out)
	}
	if out := write(t, "one\ntwo\nthree\nfour\n"); !strings.Contains(out, "(replaced 4 bytes; +3 lines)") {
		t.Fatalf("a write that added three lines reported %q", out)
	}
	// Same line count, different bytes — the size still moved, and the
	// delta must not invent a change it cannot see.
	if out := write(t, "1\n2\n3\n4\n"); !strings.Contains(out, "(replaced 19 bytes; ±0 lines)") {
		t.Fatalf("a same-line-count rewrite reported %q", out)
	}
	if out := write(t, "1\n2\n3"); !strings.Contains(out, "(replaced 8 bytes; −1 lines)") {
		t.Fatalf("dropping the last line reported %q", out)
	}
	// An unterminated last line is a line on both sides: terminating it
	// changes no line count, and the delta must not report one.
	if out := write(t, "1\n2\n3\n"); !strings.Contains(out, "(replaced 5 bytes; ±0 lines)") {
		t.Fatalf("an unterminated last line was not counted: %q", out)
	}
}

func TestEditTool(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir, nil, Timeouts{})
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("old content here"), 0644)

	result, err := r.Execute(context.Background(), "edit", map[string]any{
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

	result, err := r.Execute(context.Background(), "shell", map[string]any{
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

	result, err := r.Execute(context.Background(), "ls", map[string]any{
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

	result, err := r.Execute(context.Background(), "grep", map[string]any{
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
	result, _ := r.Execute(context.Background(), "read", map[string]any{
		"file_path": "data/ledger.jsonl",
	})
	if !strings.HasPrefix(result.Error, "access denied: protected path") {
		t.Errorf("expected access denied, got: %s", result.Error)
	}

	// Should deny reading the identity key
	result, _ = r.Execute(context.Background(), "read", map[string]any{
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
	result, _ := r.Execute(context.Background(), "write", map[string]any{
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
	result, _ := r.Execute(context.Background(), "shell", map[string]any{
		"command": "cat data/ledger.jsonl",
	})
	if result.Error == "" || !strings.Contains(result.Error, "access denied") {
		t.Errorf("expected access denied, got: %s", result.Error)
	}

	// Should deny bash commands targeting the key
	result, _ = r.Execute(context.Background(), "shell", map[string]any{
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
	result, _ := r.Execute(context.Background(), "write", map[string]any{
		"file_path": path,
		"content":   "safe content",
	})
	if result.Error != "" {
		t.Errorf("normal path should be allowed, got: %s", result.Error)
	}
}

func TestSandboxConfinementFirstBirthLessons(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "own"), 0755)
	os.WriteFile(filepath.Join(dir, "own", "note.txt"), []byte("mine"), 0644)
	outside := t.TempDir() // stand-in for the predecessor workspace
	os.WriteFile(filepath.Join(outside, "NOTES.md"), []byte("predecessor notes"), 0644)

	t.Setenv("SANDBOX_TEST_SECRET", "marker") // must NOT reach the bash env
	r := NewRegistry(dir, nil, Timeouts{})

	if res, _ := r.Execute(context.Background(), "read", map[string]any{
		"file_path": filepath.Join(outside, "NOTES.md")}); res.Error == "" {
		t.Error("read outside sandbox allowed")
	}
	if res, _ := r.Execute(context.Background(), "read", map[string]any{
		"file_path": "~/example/NOTES.md"}); res.Error == "" {
		t.Error("read tilde path allowed")
	}
	if res, _ := r.Execute(context.Background(), "read", map[string]any{
		"file_path": "../../etc/passwd"}); res.Error == "" {
		t.Error("read via dotdot-walk allowed")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]any{
		"command": "cat '" + filepath.Join(outside, "NOTES.md") + "'"}); res.Error == "" {
		t.Error("bash cat outside sandbox allowed")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]any{
		"command": "cat ~/notes/COMMITMENT.md"}); res.Error == "" {
		t.Error("bash tilde reference allowed")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]any{
		"command": "ls $HOME"}); res.Error == "" {
		t.Error("bash HOME-var reference allowed")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]any{
		"command": "ls /srv/example/"}); res.Error == "" {
		t.Error("bash absolute path outside sandbox allowed")
	}
	os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte("[]"), 0644)
	if res, _ := r.Execute(context.Background(), "shell", map[string]any{
		"command": "head ledger.jsonl"}); res.Error == "" {
		t.Error("bash substrate floor lost")
	}
	if res, _ := r.Execute(context.Background(), "shell", map[string]any{
		"command": "echo VAR=$SANDBOX_TEST_SECRET"}); strings.Contains(res.Output, "marker") {
		t.Error("bash environment carries host secrets")
	}
	// TrimSpace, because the claim is that the sandbox can READ ITS OWN
	// FILE, not that two shells agree on trailing whitespace: PowerShell's
	// Get-Content appends a newline that bash's cat does not. Line ENDINGS
	// are already one shape by here (normalizeShellOutput); this is the
	// residual trailing newline, and it is not what the test is about.
	if res, _ := r.Execute(context.Background(), "shell", map[string]any{
		"command": "cat own/note.txt"}); strings.TrimSpace(res.Output) != "mine" {
		t.Errorf("bash inside sandbox broken: out=%q err=%q", res.Output, res.Error)
	}

	r2 := NewRegistry(dir, nil, Timeouts{})
	r2.SetToolEnabled("shell", false)
	if res, _ := r2.Execute(context.Background(), "shell", map[string]any{
		"command": "ls"}); res.Error == "" || res.ReasonCode != ReasonOperatorDisabled {
		t.Errorf("a disabled shell must refuse with %s: %+v", ReasonOperatorDisabled, res)
	}
	if res, _ := r.Execute(context.Background(), "ls", map[string]any{
		"path": outside}); res.Error == "" {
		t.Error("ls outside sandbox allowed")
	}
	if res, _ := r.Execute(context.Background(), "ls", map[string]any{
		"path": "own"}); !strings.Contains(res.Output, "note.txt") {
		t.Errorf("ls inside sandbox broken: %q", res.Output)
	}
}

// TestPolicyIsTheEnforcer proves the Ring 5 policy is WIRED: a custom rule
// given to the policy changes enforcement. Before the wiring fix, the
// policy engine was unwired dead code — enforcement used a parallel
// hardcoded copy.
func TestPolicyIsTheEnforcer(t *testing.T) {
	dir := t.TempDir()
	pol := firewall.DefaultPolicy()
	// A custom place that exists ONLY here — if the registry still used a
	// hardcoded parallel list, this rule would change nothing.
	pol.SetPlaces([]*firewall.Rule{{
		ID: "sub.test-secret", Kind: firewall.KindSubstrate, Path: filepath.Join(dir, "notes", "operator_secrets.txt"),
		Reason: "test rule — must be enforced", Enforced: true,
	}})

	r := NewRegistry(dir, pol, Timeouts{})

	result, _ := r.Execute(context.Background(), "read", map[string]any{
		"file_path": "notes/operator_secrets.txt",
	})
	if !strings.Contains(result.Error, "sub.test-secret") {
		t.Fatalf("custom policy rule not enforced — policy is unwired. got: %s", result.Error)
	}

	// And the default substrate floor still holds through the policy path
	result, _ = r.Execute(context.Background(), "read", map[string]any{
		"file_path": "data/ledger.jsonl",
	})
	if !strings.Contains(result.Error, "sub.ledger") {
		t.Fatalf("default substrate rule not enforced via policy. got: %s", result.Error)
	}
}

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

	if r.shellRefusal(cmd) == "" {
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
		res, err := wf.Execute(context.Background(), map[string]any{"url": u})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", u, err)
		}
		if res.Error == "" {
			t.Errorf("web_fetch %q was NOT blocked — SSRF guard hole", u)
		}
	}

	if err := firewall.FetchGuard(context.Background(), "https://93.184.215.14/"); err != nil {
		t.Errorf("public https URL should pass the guard: %v", err)
	}
}

func TestParallelSafeIsRegistryMetadataOnly(t *testing.T) {
	r := NewRegistry("", nil, Timeouts{})
	for name, want := range map[string]bool{
		"read": true, "grep": true, "ls": true,
		"shell": false, "write": false, "edit": false,
		"no_such_tool": false,
	} {
		if got := r.ParallelSafe(name); got != want {
			t.Errorf("ParallelSafe(%q) = %v, want %v", name, got, want)
		}
	}
}
