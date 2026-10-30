package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// verb runs one aii-release verb and returns its rc, stdout and stderr.
func verb(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	out, _ := os.Create(filepath.Join(dir, "out"))
	errf, _ := os.Create(filepath.Join(dir, "err"))
	rc := run(args, out, errf)
	out.Close()
	errf.Close()
	o, _ := os.ReadFile(filepath.Join(dir, "out"))
	e, _ := os.ReadFile(filepath.Join(dir, "err"))
	return rc, string(o), string(e)
}

// signed has role sign the payload a verb printed, as ai3-bundle does.
func signed(t *testing.T, role *packagetest.Role, kind, payload string) []byte {
	t.Helper()
	var obj any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		t.Fatalf("the verb printed no JSON object: %v\n%s", err, payload)
	}
	sig, err := role.Sign(kind, obj)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func writeRoot(t *testing.T, dir, name string, role *packagetest.Role) string {
	t.Helper()
	raw, _ := json.Marshal(role.Env)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWhatTheReleaseVerbsRenderTheHostAccepts(t *testing.T) {
	dir := t.TempDir()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := packagetest.NewRole("test-reviewer", packagetest.KeyTypeReviewer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := packagetest.NewRole("test-certifier", packagetest.KeyTypePublisherCertifier)
	if err != nil {
		t.Fatal(err)
	}
	rootArgs := []string{"-platform-root", writeRoot(t, dir, "p.json", plat), "-reviewer-root", writeRoot(t, dir, "r.json", rev),
		"-certifier-root", writeRoot(t, dir, "c.json", cert)}
	roots := packagefmt.TrustRoots{PlatformRelease: plat.Env, Reviewer: rev.Env, PublisherCertifier: cert.Env}
	write := func(name string, b []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// The release being withdrawn: one asset's signature.
	asset := signed(t, plat, packagefmt.ArtifactKindHostRelease, `{"archive_hash":"`+strings.Repeat("a", 64)+`","version":"0.2.0","platform":"linux","arch":"amd64","source_rev":"abc"}`)
	current, err := plat.SignRevocationStatus(1, nil)
	if err != nil {
		t.Fatal(err)
	}
	rc, payload, stderr := verb(t, append([]string{"status-payload", "-file", packagetest.StatusFilePlatform,
		"-current", write("current.json", current), "-withdraw", write("asset.sig", asset)}, rootArgs...)...)
	if rc != 0 {
		t.Fatalf("status-payload: rc %d: %s", rc, stderr)
	}
	next := signed(t, plat, packagefmt.ArtifactKindRevocationStatus, payload)
	trust := filepath.Join(dir, "trust")
	if epoch, placed, err := packagefmt.PlaceStatus(trust, roots, packagetest.StatusFilePlatform, next); err != nil || !placed || epoch != 2 {
		t.Fatalf("the host does not place the list the verb rendered: epoch %d, placed %v, %v", epoch, placed, err)
	}
	set := packagefmt.LoadRevocationStatus(trust, roots, nil)
	var signedPayload struct {
		Payload json.RawMessage `json:"payload"`
	}
	_ = json.Unmarshal(asset, &signedPayload)
	if err := packagefmt.CheckReleaseRevocation(packagefmt.TrustRoots{PlatformRelease: plat.Env, Revocation: set}, packagefmt.ArtifactKindHostRelease, signedPayload.Payload); err == nil {
		t.Fatal("the list the verb rendered does not revoke the release it was given")
	}
	// A signature of another root's kind is not this root's to withdraw.
	att := signed(t, rev, "plugin.attestation", `{"x":1}`)
	if rc, _, _ := verb(t, append([]string{"status-payload", "-file", packagetest.StatusFilePlatform,
		"-current", write("current.json", current), "-withdraw", write("att.sig", att)}, rootArgs...)...); rc == 0 {
		t.Fatal("status-payload took another root's artifact")
	}

	// The catalog, the list beside it, the announcement: the 0.2 line's
	// file, which names host_line in must_understand.
	bare, _ := json.MarshalIndent(plugincatalog.Index{Version: 1, Generated: "2026-10-01T00:00:00Z"}, "", "  ")
	if rc, _, stderr := verb(t, append([]string{"catalog-payload", "-catalog", write("aiios-plugins-0.2.md", []byte("# Catalog\n\n```json\n"+string(bare)+"\n```\n")),
		"-host-release", "0.2.1"}, rootArgs...)...); rc == 0 || !strings.Contains(stderr, "names host_line in must_understand") {
		t.Fatalf("catalog-payload rendered a 0.2 index that a 0.1 host would half-read: rc %d %s", rc, stderr)
	}
	block, _ := json.MarshalIndent(plugincatalog.Index{Version: 1, Generated: "2026-10-01T00:00:00Z", MustUnderstand: []string{"compat", "host_line"}}, "", "  ")
	catalog := write("aiios-plugins-0.2.md", []byte("# Catalog\n\n```json\n"+string(block)+"\n```\n"))
	for _, wrong := range [][]string{{"-catalog", write("aiios-plugins.md", []byte("x")), "-host-release", "0.2.1"}, {"-catalog", catalog, "-host-release", "0.3.0"}} {
		if rc, _, stderr := verb(t, append(append([]string{"catalog-payload"}, wrong...), rootArgs...)...); rc == 0 || !strings.Contains(stderr, "line's file, and -host-release") {
			t.Fatalf("catalog-payload rendered %v for another line's file: rc %d %s", wrong, rc, stderr)
		}
	}
	write(filepath.Base(plugincatalog.PublishedBeside(catalog, packagetest.StatusFilePlatform)), next)
	rc, payload, stderr = verb(t, append([]string{"catalog-payload", "-catalog", catalog, "-host-release", "0.2.1",
		"-generated", "2026-10-20T12:00:00Z"}, rootArgs...)...)
	if rc != 0 {
		t.Fatalf("catalog-payload: rc %d: %s", rc, stderr)
	}
	if !strings.Contains(stderr, "plugin_reviewer list is not announced") {
		t.Errorf("a list that is not beside the catalog is not said: %q", stderr)
	}
	sig := signed(t, plat, plugincatalog.ArtifactKindPluginCatalog, payload)
	md, _ := os.ReadFile(catalog)
	cat, err := plugincatalog.ParseCatalogForLine(md, sig, plat.Env, "0.2")
	if err != nil {
		t.Fatalf("the host refuses the catalog signed over the verb's payload: %v", err)
	}
	if a := cat.Announced; a.Release != "0.2.1" || a.At != "2026-10-20T12:00:00Z" || len(a.StatusEpochs) != 1 || a.StatusEpochs["platform_release"] != 2 {
		t.Fatalf("the host reads %+v", a)
	}
	write("aiios-plugins-0.2.md.sig", sig)
	if rc, out, stderr := verb(t, append([]string{"catalog-verify", "-catalog", catalog}, rootArgs...)...); rc != 0 || !strings.Contains(out, "VERIFIED") {
		t.Fatalf("catalog-verify refused what the host accepts: rc %d %s %s", rc, out, stderr)
	}
	// A 0.2 index signed by hand without host_line in must_understand is
	// refused before it goes out: a 0.1 host pointed at it would read half.
	bareCatalog := write("aiios-plugins-0.2.md", []byte("# Catalog\n\n```json\n"+string(bare)+"\n```\n"))
	bareMD, _ := os.ReadFile(bareCatalog)
	write("aiios-plugins-0.2.md.sig", signed(t, plat, plugincatalog.ArtifactKindPluginCatalog, `{"catalog_version":1,"generated":"2026-10-20T12:00:00Z","catalog_sha256":"`+sigenvelope.SHA256Prefixed(bareMD)+`","host_release":"0.2.1","host_line":"0.2"}`))
	if rc, _, stderr := verb(t, append([]string{"catalog-verify", "-catalog", bareCatalog}, rootArgs...)...); rc == 0 || !strings.Contains(stderr, "does not name host_line in must_understand") {
		t.Fatalf("catalog-verify passed a 0.2 index without host_line in must_understand: rc %d %s", rc, stderr)
	}
	write("aiios-plugins-0.2.md", md)
	write("aiios-plugins-0.2.md.sig", sig)

	// The same pair under the 0.1 line's name is another line's catalog.
	write("aiios-plugins.md", md)
	write("aiios-plugins.md.sig", sig)
	if rc, _, stderr := verb(t, append([]string{"catalog-verify", "-catalog", filepath.Join(filepath.Dir(catalog), "aiios-plugins.md")}, rootArgs...)...); rc == 0 || !strings.Contains(stderr, "signed for AII OS 0.2 hosts, and this host is AII OS 0.1") {
		t.Fatalf("catalog-verify passed a 0.2 index under the 0.1 name: rc %d %s", rc, stderr)
	}

	// Published behind its announcement: refused before it goes out.
	write(filepath.Base(plugincatalog.PublishedBeside(catalog, packagetest.StatusFilePlatform)), current)
	if rc, _, stderr := verb(t, append([]string{"catalog-verify", "-catalog", catalog}, rootArgs...)...); rc == 0 || !strings.Contains(stderr, "announced at epoch 2 and is epoch 1") {
		t.Fatalf("catalog-verify passed a list behind its announcement: rc %d %s", rc, stderr)
	}
	os.Remove(plugincatalog.PublishedBeside(catalog, packagetest.StatusFilePlatform))
	if rc, _, stderr := verb(t, append([]string{"catalog-verify", "-catalog", catalog}, rootArgs...)...); rc == 0 || !strings.Contains(stderr, "is not beside the catalog") {
		t.Fatalf("catalog-verify passed an announcement with no list: rc %d %s", rc, stderr)
	}
}
