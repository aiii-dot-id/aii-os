package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// ceremonySign stands in for ai3-bundle: it signs phase 1's payload with
// a fresh platform root and writes the signature, the root and the
// root's empty snapshot where phase 2 reads them.
func ceremonySign(t *testing.T, payloadPath string) (sig, root, status string) {
	t.Helper()
	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	role, snapshot, err := packagetest.NewPlatformRelease("aiii_ceremony_stand_in")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := role.Sign(packagetest.ArtifactKindPlatformSig, payload)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(role.Env)
	dir := t.TempDir()
	sig, root, status = filepath.Join(dir, "platform.sig.json"), filepath.Join(dir, "root.pub.json"), filepath.Join(dir, "status.json")
	for path, data := range map[string][]byte{sig: signed, root: env, status: snapshot} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return sig, root, status
}

func phase1(t *testing.T, dir string) string {
	t.Helper()
	pair := filepath.Join(t.TempDir(), "pair.json")
	var stdout, stderr bytes.Buffer
	if rc := runDevsign([]string{"-staging", dir, "-payload-out", pair}, &stdout, &stderr); rc != 0 {
		t.Fatalf("phase 1 rc=%d: %s", rc, stderr.String())
	}
	return pair
}

// A PACKAGE THE VERIFIER REFUSES NEVER REACHES DISK. Phase 2 against a
// root that did not sign, or over staged bytes that changed after phase
// 1, must refuse and leave nothing at -o.
func TestDevsignPhaseTwoRefusesWhatItCannotVerify(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(t *testing.T, dir, root string) string // returns the -root to use
	}{
		{"a root that did not sign", func(t *testing.T, dir, root string) string {
			_, other, _ := ceremonySign(t, phase1(t, dir))
			return other
		}},
		{"staging changed after phase 1", func(t *testing.T, dir, root string) string {
			p := filepath.Join(dir, "install-root", "variants", "native", "memoryd")
			if err := os.WriteFile(p, []byte("\x7fELFother-bytes"), 0o644); err != nil {
				t.Fatal(err)
			}
			return root
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := stage(t)
			sig, root, status := ceremonySign(t, phase1(t, dir))
			root = c.mutate(t, dir, root)
			out := filepath.Join(t.TempDir(), "memoryd.aiiospkg")
			var stdout, stderr bytes.Buffer
			if rc := runDevsign([]string{"-staging", dir, "-attach-sig", sig, "-root", root, "-status", status, "-o", out}, &stdout, &stderr); rc != 1 {
				t.Fatalf("rc=%d, want a refusal: %s%s", rc, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "does NOT verify") {
				t.Fatalf("refused for another reason: %s", stderr.String())
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("a refused package was written to %s (%v)", out, err)
			}
		})
	}
}

// EACH MODE TAKES ITS OWN FLAGS. A flag the mode would ignore is refused,
// and so is a run that could only produce a package no host verifies.
func TestDevsignRefusesFlagsItWouldIgnore(t *testing.T) {
	dir := stage(t)
	tmp := t.TempDir()
	at := func(name string) string { return filepath.Join(tmp, name) }
	for _, c := range []struct {
		name string
		args []string
		says string
	}{
		{"phase 1 with an output", []string{"-payload-out", at("p.json"), "-o", at("x.aiiospkg")}, "takes only -staging"},
		{"phase 1 with phase 2's signature", []string{"-payload-out", at("p.json"), "-attach-sig", at("s.json")}, "takes only -staging"},
		{"a ceremony root without phase 2", []string{"-o", at("x.aiiospkg"), "-root-out", at("r.json"), "-root", at("root.json")}, "belong to ceremony phase 2"},
		{"phase 2 with ephemeral outputs", []string{"-attach-sig", at("s.json"), "-root", at("r.json"), "-status", at("st.json"), "-o", at("x.aiiospkg"), "-root-out", at("r2.json")}, "belong to ephemeral mode"},
		{"ephemeral without its root", []string{"-o", at("x.aiiospkg")}, "needs -root-out"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if rc := runDevsign(append([]string{"-staging", dir}, c.args...), &stdout, &stderr); rc != 2 {
				t.Fatalf("rc=%d, want the usage refusal: %s%s", rc, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), c.says) {
				t.Fatalf("refused for another reason (want %q): %s", c.says, stderr.String())
			}
			if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
				t.Fatalf("a refused run wrote %d files", len(entries))
			}
		})
	}
}

// THE STAGING DIR IS READ AS WRITTEN. A field devsign.json does not name
// is a typo that would drop what it meant (a host window, say), a link
// would sign bytes from outside the staging dir, and a path the format
// cannot carry is a refusal, not a crash.
func TestDevsignRefusesAStagingDirItCannotPackageAsWritten(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(t *testing.T, dir string)
		says   string
	}{
		{"an unknown field", func(t *testing.T, dir string) {
			raw, err := os.ReadFile(filepath.Join(dir, "devsign.json"))
			if err != nil {
				t.Fatal(err)
			}
			raw = bytes.Replace(raw, []byte(`"id":`), []byte(`"aiios_min_versoin":"0.1.0","id":`), 1)
			if err := os.WriteFile(filepath.Join(dir, "devsign.json"), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "aiios_min_versoin"},
		{"a link", func(t *testing.T, dir string) {
			outside := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(outside, []byte("not staged"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, "install-root", "linked")); err != nil {
				t.Skipf("this host cannot create a symlink: %v", err)
			}
		}, "not a regular file"},
		{"a path over the format's bound", func(t *testing.T, dir string) {
			deep := filepath.Join(dir, "install-root", strings.Repeat("d", 200), strings.Repeat("e", 200), strings.Repeat("f", 120))
			if err := os.MkdirAll(filepath.Dir(deep), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(deep, []byte("x"), 0o644); err != nil {
				t.Skipf("this host cannot hold the path: %v", err)
			}
		}, "cannot be packaged"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := stage(t)
			c.mutate(t, dir)
			out := filepath.Join(t.TempDir(), "x.aiiospkg")
			var stdout, stderr bytes.Buffer
			if rc := runDevsign([]string{"-staging", dir, "-o", out, "-root-out", filepath.Join(t.TempDir(), "r.json")}, &stdout, &stderr); rc != 1 {
				t.Fatalf("rc=%d, want a refusal: %s%s", rc, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), c.says) {
				t.Fatalf("refused for another reason (want %q): %s", c.says, stderr.String())
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("a refused run wrote the package (%v)", err)
			}
		})
	}
}

// The dev root expires a day after minting, and a host that pins an
// expired root quarantines every plugin; the pin line says when.
func TestDevsignSaysWhenTheDevRootExpires(t *testing.T) {
	dir := stage(t)
	var stdout, stderr bytes.Buffer
	if rc := runDevsign([]string{"-staging", dir, "-o", filepath.Join(t.TempDir(), "x.aiiospkg"), "-root-out", filepath.Join(t.TempDir(), "r.json")}, &stdout, &stderr); rc != 0 {
		t.Fatalf("rc=%d: %s", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "expires 20") {
		t.Fatalf("the pin line does not say when the dev root expires:\n%s", stdout.String())
	}
}
