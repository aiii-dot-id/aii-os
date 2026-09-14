package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func fsParams(op, root, path, args string) string {
	if args == "" {
		args = "{}"
	}
	return fmt.Sprintf(`{"operation":%q,"target":{"root":%q,"path":%q},"arguments":%s}`, op, root, path, args)
}

func opResult(t *testing.T, m map[string]json.RawMessage) map[string]interface{} {
	t.Helper()
	var or map[string]interface{}
	_ = json.Unmarshal(m["operation_result"], &or)
	return or
}

// .
// .
// .
// .
func TestThePrivateDirectoryNeedsNoGrantAndFollowsTheTierRule(t *testing.T) {
	data := t.TempDir()
	st := newStore(t)
	h := newHost(t, st, Config{MaxFilesBytes: 4096})
	b := h.Bind("p", packagefmt.TierT0, []string{"fs.private"})
	private := filepath.Join(data, "p")
	b.SetFiles(private, 0)

	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "notes/a.txt", `{"data":"hello"}`)), statusSucceeded, "")
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "notes/a.txt", `{"data":" world","append":true}`)), statusSucceeded, "")
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(private, "notes", "a.txt"))
		if err != nil || fi.Mode().Perm()&0o111 != 0 {
			t.Fatalf("never executable: %v %v", fi, err)
		}
	}
	m := dispatch(t, b, fsParams("fs.read", "private", "notes/a.txt", `{"offset":6,"length":3}`))
	wantResult(t, m, statusSucceeded, "")
	or := opResult(t, m)
	got, _ := base64.StdEncoding.DecodeString(or["data_b64"].(string))
	if string(got) != "wor" || or["size"] != 11.0 || or["eof"] != false {
		t.Fatalf("read in pieces: %v %q", or, got)
	}
	m = dispatch(t, b, fsParams("fs.list", "private", "notes", ""))
	wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(string(m["operation_result"]), `"name":"a.txt"`) {
		t.Fatalf("list: %s", m["operation_result"])
	}
	wantResult(t, dispatch(t, b, fsParams("fs.list", "private", "", "")), statusSucceeded, "")
	wantResult(t, dispatch(t, b, fsParams("fs.read", "private", "nothing.txt", "")), statusFailed, reasonFSNotFound)
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "big.bin", `{"data":"`+strings.Repeat("x", 4090)+`"}`)), statusFailed, reasonFSQuotaExceeded)
	wantResult(t, dispatch(t, b, fsParams("fs.delete", "private", "notes/a.txt", "")), statusSucceeded, "")
	m = dispatch(t, b, fsParams("fs.delete", "private", "notes/a.txt", ""))
	wantResult(t, m, statusSucceeded, "")
	if opResult(t, m)["deleted"] != false {
		t.Fatal("deleting twice is idempotent")
	}
	for name, params := range map[string]string{
		"traversal":     fsParams("fs.read", "private", "../secret", ""),
		"absolute":      fsParams("fs.read", "private", "/etc/passwd", ""),
		"dot component": fsParams("fs.read", "private", "a/./b", ""),
		"no path":       fsParams("fs.read", "private", "", ""),
		"deep":          fsParams("fs.write", "private", strings.Repeat("d/", 17)+"f", `{"data":"x"}`),
	} {
		wantResult(t, dispatch(t, b, params), statusDenied, reasonFSPathInvalid)
		_ = name
	}
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "x", `{"data":"x","mode":"0755"}`)), statusDenied, reasonNetUnknownArgument)
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "x", `{}`)), statusDenied, reasonArgumentInvalid)

	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "keep.txt", `{"data":"t0"}`)), statusSucceeded, "")
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Fatal("at T0 the private directory dies with the activation")
	}
	b1 := h.Bind("p", packagefmt.TierT1, []string{"fs.private"})
	b1.SetFiles(private, 0)
	wantResult(t, dispatch(t, b1, fsParams("fs.write", "private", "keep.txt", `{"data":"t1"}`)), statusSucceeded, "")
	_ = b1.Close()
	if _, err := os.Stat(filepath.Join(private, "keep.txt")); err != nil {
		t.Fatal("at T1 the private directory persists")
	}
	unset := h.Bind("q", packagefmt.TierT1, []string{"fs.private"})
	wantResult(t, dispatch(t, unset, fsParams("fs.read", "private", "x", "")), statusDenied, reasonFSRootRefused)
	undeclared := h.Bind("r", packagefmt.TierT1, nil)
	undeclared.SetFiles(filepath.Join(data, "r"), 0)
	wantResult(t, dispatch(t, undeclared, fsParams("fs.read", "private", "x", "")), statusDenied, reasonNotInEnvelope)
}

// .
// .
// .
// .
func TestGrantedRootsHoldTheSubstrateInvariant(t *testing.T) {
	docs := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "readme.md"), []byte("# hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s3cr3t"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(docs, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(docs, "outdir")); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	dataDir := filepath.Join(home, "aii")
	if err := os.MkdirAll(filepath.Join(dataDir, "trust"), 0o700); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(dataDir, "ledger.jsonl")
	if err := os.WriteFile(ledger, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newStore(t)
	grants := map[string]Grant{"p": {Hosts: []string{"api.example.test:443"}, Roots: []RootGrant{
		{Name: "docs", Path: docs},
		{Name: "out", Path: outside, Write: true},
		{Name: "home", Path: home},
		{Name: "trust", Path: filepath.Join(dataDir, "trust")},
		{Name: "gone", Path: filepath.Join(home, "missing")},
	}}}
	h := newHost(t, st, Config{Grants: grants, ProtectedPaths: []string{ledger, filepath.Join(dataDir, "trust")}, MaxFilesBytes: 64})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.roots"})

	m := dispatch(t, b, fsParams("fs.read", "docs", "readme.md", ""))
	wantResult(t, m, statusSucceeded, "")
	got, _ := base64.StdEncoding.DecodeString(opResult(t, m)["data_b64"].(string))
	if string(got) != "# hello" {
		t.Fatalf("read a granted file: %q", got)
	}
	wantResult(t, dispatch(t, b, fsParams("fs.write", "docs", "new.md", `{"data":"x"}`)), statusDenied, reasonFSReadOnly)
	wantResult(t, dispatch(t, b, fsParams("fs.delete", "docs", "readme.md", "")), statusDenied, reasonFSReadOnly)
	wantResult(t, dispatch(t, b, fsParams("fs.read", "docs", "link.txt", "")), statusDenied, reasonFSSymlinkRefused)
	wantResult(t, dispatch(t, b, fsParams("fs.read", "docs", "outdir/secret.txt", "")), statusDenied, reasonFSSymlinkRefused)
	m = dispatch(t, b, fsParams("fs.list", "docs", "", ""))
	wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(string(m["operation_result"]), `"symlink":true`) {
		t.Fatalf("a symlink is shown as one, never followed: %s", m["operation_result"])
	}
	wantResult(t, dispatch(t, b, fsParams("fs.read", "home", "aii/ledger.jsonl", "")), statusDenied, reasonFSRootRefused)
	wantResult(t, dispatch(t, b, fsParams("fs.list", "trust", "", "")), statusDenied, reasonFSRootRefused)
	wantResult(t, dispatch(t, b, fsParams("fs.list", "gone", "", "")), statusDenied, reasonFSRootRefused)
	wantResult(t, dispatch(t, b, fsParams("fs.read", "elsewhere", "x", "")), statusDenied, reasonPolicyDeny)

	wantResult(t, dispatch(t, b, fsParams("fs.write", "out", "a.txt", `{"data":"`+strings.Repeat("y", 40)+`"}`)), statusSucceeded, "")
	wantResult(t, dispatch(t, b, fsParams("fs.write", "out", "b.txt", `{"data":"`+strings.Repeat("y", 40)+`"}`)), statusFailed, reasonFSQuotaExceeded)
	if _, err := os.Stat(filepath.Join(outside, "a.txt")); err != nil {
		t.Fatal("the granted write landed")
	}

	t0 := h.Bind("p", packagefmt.TierT0, []string{"fs.roots"})
	wantResult(t, dispatch(t, t0, fsParams("fs.read", "docs", "readme.md", "")), statusDenied, reasonTierDenied)
	noEnvelope := h.Bind("p", packagefmt.TierT1, nil)
	wantResult(t, dispatch(t, noEnvelope, fsParams("fs.read", "docs", "readme.md", "")), statusDenied, reasonNotInEnvelope)
}

// .
// .
func TestFilesUnderTheOperationScopeAndSAFE(t *testing.T) {
	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "r.txt"), []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	safe := false
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Roots: []RootGrant{{Name: "docs", Path: docs}}}}, InSAFE: func() bool { return safe }})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.private", "fs.roots"})
	b.SetFiles(filepath.Join(t.TempDir(), "p"), 0)
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "own.txt", `{"data":"mine"}`)), statusSucceeded, "")

	b.BeginOperation(OperationScope{Operation: "look", Effects: EffectsReadInternal, Capabilities: []string{"fs.private"}, Declared: true})
	reply, _ := b.Dispatch(context.Background(), "invoke-call", []byte(fsParams("fs.write", "private", "own.txt", `{"data":"no"}`)))
	if !strings.Contains(string(reply), "declares effects read.internal") {
		t.Fatalf("a read-only operation cannot write: %s", reply)
	}
	reply, _ = b.Dispatch(context.Background(), "invoke-call", []byte(fsParams("fs.read", "docs", "r.txt", "")))
	if !strings.Contains(string(reply), "did not declare fs.roots") {
		t.Fatalf("an operation that declared fs.private alone cannot reach a root: %s", reply)
	}
	wantResult(t, dispatch(t, b, fsParams("fs.read", "private", "own.txt", "")), statusSucceeded, "")
	b.EndOperation()

	safe = true
	wantResult(t, dispatch(t, b, fsParams("fs.read", "private", "own.txt", "")), statusSucceeded, "")
	for _, params := range []string{fsParams("fs.write", "private", "own.txt", `{"data":"x"}`), fsParams("fs.read", "docs", "r.txt", ""), fsParams("fs.delete", "private", "own.txt", "")} {
		reply, _ := b.Dispatch(context.Background(), "invoke-call", []byte(params))
		if !strings.Contains(string(reply), "SAFE") {
			t.Fatalf("SAFE refuses: %s", reply)
		}
	}
}
