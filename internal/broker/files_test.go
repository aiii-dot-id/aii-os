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

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func fsParams(op, root, path, args string) string {
	if args == "" {
		args = "{}"
	}
	return fmt.Sprintf(`{"operation":%q,"target":{"root":%q,"path":%q},"arguments":%s}`, op, root, path, args)
}

func opResult(t *testing.T, m map[string]json.RawMessage) map[string]any {
	t.Helper()
	var or map[string]any
	_ = json.Unmarshal(m["operation_result"], &or)
	return or
}

// The private directory: no grant, any tier; write, list, read in
// pieces, delete; parents made on the way; nothing executable; the
// quota holds by bytes and by entries; at T0 the directory dies with
// the activation and at T1 it stays.
func TestThePrivateDirectoryNeedsNoGrantAndFollowsTheTierRule(t *testing.T) {
	data := t.TempDir()
	st := newStore(t)
	h := newHost(t, st, Config{MaxFilesBytes: 4096})
	b := h.Bind("p", packagefmt.TierT0, []string{"fs.private"})
	private := filepath.Join(data, "p")
	b.SetFiles(private, 0)
	temporary := b.privateDir

	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "notes/a.txt", `{"data":"hello"}`)), statusSucceeded, "")
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "notes/a.txt", `{"data":" world","append":true}`)), statusSucceeded, "")
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(temporary, "notes", "a.txt"))
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
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
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

// A PLUGIN WORKS IN THE IDENTITY'S SANDBOX, under the one rule the
// identity's own tools obey (a real tools.Registry): a path relative to
// the home or absolute in a root the operator granted the identity, the
// reply echoing it as given; the floor's places refused wherever the
// path is spelled, and a symlink judged where it leads; nothing without
// the files grant, the envelope and a publisher-proven tier; read only
// refusing every write; what an activation writes budgeted; and the
// refusal written to the floor's audit, naming the plugin.
func TestAPluginWorksInTheIdentitysSandbox(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "vega")
	data := filepath.Join(home, "data")
	work := filepath.Join(base, "work")
	outside := filepath.Join(base, "elsewhere")
	for _, d := range []string{filepath.Join(data, "credentials"), filepath.Join(work, "proj"), outside} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for f, body := range map[string]string{
		filepath.Join(home, "notes.md"):               "# notes",
		filepath.Join(work, "proj", "main.go"):        "package main",
		filepath.Join(data, "credentials", "jev.key"): "SECRET",
		filepath.Join(data, "ui-layout.json"):         "{}",
		filepath.Join(outside, "secret.txt"):          "SECRET",
	} {
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(home, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if err := os.Symlink(filepath.Join(work, "proj"), filepath.Join(home, "proj")); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data,
		Except: []string{filepath.Join(data, "ui-layout.json")}, Reason: "the host keeps it", Enforced: true}})
	world := tools.NewRegistry(home, policy, tools.Timeouts{})
	world.SetExtraRoots([]string{work})
	grants := map[string]Grant{"p": {Files: true}, "ro": {Files: true, ReadOnly: true}, "none": {KV: true}}
	h := newHost(t, newStore(t), Config{Grants: grants, Sandbox: world, MaxFilesBytes: 64})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})

	read := func(b *Binding, path string) map[string]json.RawMessage {
		return dispatch(t, b, fsParams("fs.read", "sandbox", path, ""))
	}
	for path, want := range map[string]string{"notes.md": "# notes", filepath.Join(work, "proj", "main.go"): "package main", "proj/main.go": "package main", "data/ui-layout.json": "{}"} {
		m := read(b, path)
		wantResult(t, m, statusSucceeded, "")
		or := opResult(t, m)
		got, _ := base64.StdEncoding.DecodeString(or["data_b64"].(string))
		if string(got) != want || or["path"] != path || or["root"] != "sandbox" {
			t.Fatalf("read %s = %q at %v, want %q echoed as given", path, got, or["path"], want)
		}
	}
	m := dispatch(t, b, fsParams("fs.list", "sandbox", "", ""))
	wantResult(t, m, statusSucceeded, "")
	if !strings.Contains(string(m["operation_result"]), `"notes.md"`) {
		t.Fatalf("the home lists: %s", m["operation_result"])
	}
	for _, path := range []string{"data/credentials/jev.key", filepath.Join(data, "credentials", "jev.key"), "Data/Credentials/jev.key", "data", "notes/ledger.jsonl"} {
		wantResult(t, read(b, path), statusDenied, reasonFSProtected)
	}
	for _, path := range []string{"link.txt", "../elsewhere/secret.txt", filepath.Join(outside, "secret.txt"), "/"} {
		wantResult(t, read(b, path), statusDenied, reasonFSOutside)
	}

	// Writes: in the world, never into a kept place, budgeted per activation.
	wantResult(t, dispatch(t, b, fsParams("fs.write", "sandbox", "out.txt", `{"data":"`+strings.Repeat("y", 40)+`"}`)), statusSucceeded, "")
	if got, _ := os.ReadFile(filepath.Join(home, "out.txt")); len(got) != 40 {
		t.Fatalf("the write landed in the home: %q", got)
	}
	wantResult(t, dispatch(t, b, fsParams("fs.write", "sandbox", filepath.Join(work, "b.txt"), `{"data":"`+strings.Repeat("y", 40)+`"}`)), statusFailed, reasonFSQuotaExceeded)
	wantResult(t, dispatch(t, b, fsParams("fs.write", "sandbox", "data/credentials/jev.key", `{"data":"x"}`)), statusDenied, reasonFSProtected)
	wantResult(t, dispatch(t, b, fsParams("fs.publish", "sandbox", filepath.Join(work, "proj", "main.go"), `{"from":"out.txt","sha256":"`+strings.Repeat("0", 64)+`"}`)), statusDenied, reasonFSPathInvalid)
	wantResult(t, dispatch(t, b, fsParams("fs.delete", "sandbox", "out.txt", "")), statusSucceeded, "")

	// Nothing without every one of: the tier, the envelope, the grant, a
	// sandbox; read only refuses the write before dispatch; a root that
	// is neither is refused by name.
	t0 := h.Bind("p", packagefmt.TierT0, []string{"fs.sandbox"})
	wantResult(t, read(t0, "notes.md"), statusDenied, reasonTierDenied)
	wantResult(t, read(h.Bind("p", packagefmt.TierT1, nil), "notes.md"), statusDenied, reasonNotInEnvelope)
	wantResult(t, read(h.Bind("none", packagefmt.TierT1, []string{"fs.sandbox"}), "notes.md"), statusDenied, reasonPolicyDeny)
	ro := h.Bind("ro", packagefmt.TierT1, []string{"fs.sandbox"})
	wantResult(t, read(ro, "notes.md"), statusSucceeded, "")
	if reply, _ := ro.Dispatch(context.Background(), "invoke-call", []byte(fsParams("fs.write", "sandbox", "x.txt", `{"data":"x"}`))); !strings.Contains(string(reply), reasonPolicyDeny) {
		t.Fatalf("read only refuses a write: %s", reply)
	}
	bare := newHost(t, newStore(t), Config{Grants: grants}).Bind("p", packagefmt.TierT1, []string{"fs.sandbox"})
	wantResult(t, read(bare, "notes.md"), statusDenied, reasonFSOutside)
	if reply, _ := b.Dispatch(context.Background(), "invoke-call", []byte(fsParams("fs.read", "docs", "x", ""))); !strings.Contains(string(reply), reasonTargetInvalid) || !strings.Contains(string(reply), `\"sandbox\"`) {
		t.Fatalf("a root that is neither is refused, naming the two: %s", reply)
	}
}

// Ring 0 and SAFE for files: a read-only operation cannot write; under
// SAFE the private directory stays readable and everything else waits.
func TestFilesUnderTheOperationScopeAndSAFE(t *testing.T) {
	docs := t.TempDir()
	if err := os.WriteFile(filepath.Join(docs, "r.txt"), []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	safe := false
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Files: true}}, Sandbox: tools.NewRegistry(docs, nil, tools.Timeouts{}), InSAFE: func() bool { return safe }})
	b := h.Bind("p", packagefmt.TierT1, []string{"fs.private", "fs.sandbox"})
	b.SetFiles(filepath.Join(t.TempDir(), "p"), 0)
	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "own.txt", `{"data":"mine"}`)), statusSucceeded, "")

	b.BeginOperation(OperationScope{Operation: "look", Effects: EffectsReadInternal, Capabilities: []string{"fs.private"}, Declared: true})
	reply, _ := b.Dispatch(context.Background(), "invoke-call", []byte(fsParams("fs.write", "private", "own.txt", `{"data":"no"}`)))
	if !strings.Contains(string(reply), "declares effects read.internal") {
		t.Fatalf("a read-only operation cannot write: %s", reply)
	}
	reply, _ = b.Dispatch(context.Background(), "invoke-call", []byte(fsParams("fs.read", "sandbox", "r.txt", "")))
	if !strings.Contains(string(reply), "did not declare fs.sandbox") {
		t.Fatalf("an operation that declared fs.private alone cannot reach the sandbox: %s", reply)
	}
	wantResult(t, dispatch(t, b, fsParams("fs.read", "private", "own.txt", "")), statusSucceeded, "")
	b.EndOperation()

	safe = true
	wantResult(t, dispatch(t, b, fsParams("fs.read", "private", "own.txt", "")), statusSucceeded, "")
	for _, params := range []string{fsParams("fs.write", "private", "own.txt", `{"data":"x"}`), fsParams("fs.read", "sandbox", "r.txt", ""), fsParams("fs.delete", "private", "own.txt", "")} {
		reply, _ := b.Dispatch(context.Background(), "invoke-call", []byte(params))
		if !strings.Contains(string(reply), "SAFE") {
			t.Fatalf("SAFE refuses: %s", reply)
		}
	}
}
