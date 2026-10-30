package plugincatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// lineIndex is an index of entries generated at generated, naming
// mustUnderstand, and its signature by plat; hostLine is the payload's
// host_line, absent when "".
func lineIndex(t *testing.T, plat *packagetest.Role, generated, hostLine string, mustUnderstand []string, entries ...CatalogEntry) (md, sig []byte) {
	t.Helper()
	block, err := json.MarshalIndent(Index{Version: 1, Generated: generated, Plugins: entries, MustUnderstand: mustUnderstand}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	md = []byte("# Catalog\n\n```json\n" + string(block) + "\n```\n")
	payload := map[string]any{"catalog_version": 1, "generated": generated, "catalog_sha256": sigenvelope.SHA256Prefixed(md)}
	if hostLine != "" {
		payload["host_line"] = hostLine
	}
	if sig, err = plat.Sign(ArtifactKindPluginCatalog, payload); err != nil {
		t.Fatal(err)
	}
	return md, sig
}

func entry(id, version, min string, packages ...CatalogPackage) CatalogEntry {
	if packages == nil {
		packages = []CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/" + id + ".aiiospkg", SHA256: "sha256:aa", Size: 1}}
	}
	return CatalogEntry{ID: id, Version: version, Tier: "T1", Summary: id, Packages: packages, AiiosMinVersion: min}
}

func platformRole(t *testing.T) *packagetest.Role {
	t.Helper()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	return plat
}

func TestEachHostLineReadsItsOwnCatalogFile(t *testing.T) {
	for v, line := range map[string]string{"0.1.23": "0.1", "0.2.0": "0.2", "0.2.0-rc.1": "0.2", "v0.3.4": "0.3", "1.0.0+build": "1.0", "0.2": "", "dev": ""} {
		if got := HostLine(v); got != line {
			t.Errorf("the line of %q is %q, want %q", v, got, line)
		}
	}
	for line, name := range map[string]string{"0.1": "aiios-plugins.md", "": "aiios-plugins.md", "0.2": "aiios-plugins-0.2.md", "0.3": "aiios-plugins-0.3.md"} {
		if got := CatalogFileFor(line); got != name {
			t.Errorf("line %q reads %q, want %q", line, got, name)
		}
	}
	for name, line := range map[string]string{"aiios-plugins.md": "0.1", "aiios-plugins-0.2.md": "0.2", "aiios-plugins-1.0.md": "1.0",
		"aiios-plugins-0.1.md": "", "aiios-plugins-0.2.1.md": "", "aiios-plugins-02.md": "", "plugins.md": "", "aiios-plugins-0.2.md.sig": ""} {
		if got, ok := LineOfCatalogFile(name); got != line || ok != (line != "") {
			t.Errorf("%q serves line %q (%v), want %q", name, got, ok, line)
		}
	}
	const published = "https://raw.githubusercontent.com/aiii-dot-id/plugin-catalog/main/aiios-plugins.md"
	if got := LineURL(published, "0.3"); got != "https://raw.githubusercontent.com/aiii-dot-id/plugin-catalog/main/aiios-plugins-0.3.md" {
		t.Errorf("the 0.3 file beside the published one is %q", got)
	}
	if got := LineURL("https://mirror.example.test/c/mine.md", "0.2"); got != "https://mirror.example.test/c/aiios-plugins-0.2.md" {
		t.Errorf("the 0.2 file beside an operator's mirror is %q", got)
	}
	if got, want := DefaultCatalogURL, LineURL(published, OwnLine()); got != want {
		t.Fatalf("this program (line %s) reads %q by default, want %q", OwnLine(), got, want)
	}
	if OwnLine() == LegacyLine && DefaultCatalogURL != published {
		t.Fatalf("a 0.1 program reads %q, not the address its line was released reading", DefaultCatalogURL)
	}
}

// THE SIGNATURE BINDS THE FILE TO ITS LINE. A host after the first line
// reads only an index whose signature names its own line: an index of the
// 0.1 file (no line) or of another line served at its address is refused,
// naming both. The first line reads its published file, which names none,
// and refuses one signed for another line. must_understand may name
// host_line.
func TestAnIndexIsReadOnlyByTheLineItIsSignedFor(t *testing.T) {
	plat := platformRole(t)
	const at = "2026-10-22T00:00:00Z"
	for _, c := range []struct {
		host, signed string
		refusal      string // "" = read
	}{
		{"0.2", "0.2", ""},
		{"0.2", "", "signature names no host line, and an AII OS 0.2 host reads only an index signed for its line"},
		{"0.2", "0.1", "signed for AII OS 0.1 hosts, and this host is AII OS 0.2"},
		{"0.2", "0.3", "signed for AII OS 0.3 hosts, and this host is AII OS 0.2"},
		{"0.1", "", ""},
		{"0.1", "0.1", ""},
		{"0.1", "0.2", "signed for AII OS 0.2 hosts, and this host is AII OS 0.1"},
	} {
		md, sig := lineIndex(t, plat, at, c.signed, []string{"compat", "host_line"}, entry("org.example.a", "1.0.0", ""))
		cat, err := ParseCatalogForLine(md, sig, plat.Env, c.host)
		switch {
		case c.refusal == "" && (err != nil || len(cat.Plugins) != 1):
			t.Errorf("a %s host refused an index signed for %q: %v", c.host, c.signed, err)
		case c.refusal != "" && (err == nil || !strings.Contains(err.Error(), c.refusal)):
			t.Errorf("a %s host read an index signed for %q: %v, want a refusal saying %q", c.host, c.signed, err, c.refusal)
		}
	}
}

// A HOST OF A LINE REFRESHES FROM ITS LINE'S FILE AND KEEPS IT UNDER THAT
// NAME. The kept copy of the line before (the file the 0.1 programs read
// from the same data directory, which the rollback to 0.1.23 starts from)
// is left byte for byte, and a 0.1 program opened on that directory after
// the 0.2 one has kept its own still holds its own. The 0.1 file served at
// the 0.2 address is refused and the index held stays; an address with no
// file is said as not published, and the index held stays.
func TestAHostOfALineFetchesAndKeepsOnlyItsLinesFile(t *testing.T) {
	plat := platformRole(t)
	cache := filepath.Join(t.TempDir(), "plugins-catalog")
	base := "https://catalog.example.test/"
	served := map[string][]byte{}
	fetch := func(_ context.Context, u string, _ int64) ([]byte, error) {
		if b, ok := served[u]; ok {
			return b, nil
		}
		return nil, &StatusError{Code: 404}
	}

	// The 0.1 program kept its file first.
	old := &Catalog{line: "0.1", fetch: fetch}
	old.Open("", cache, plat.Env, func() string { return base + "aiios-plugins.md" })
	served[base+"aiios-plugins.md"], served[base+"aiios-plugins.md.sig"] = lineIndex(t, plat, "2026-10-01T00:00:00Z", "", []string{"compat"}, entry("org.example.a", "0.9.0", ""))
	if err := old.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	keptOld, err := os.ReadFile(filepath.Join(cache, "aiios-plugins.md"))
	if err != nil {
		t.Fatal(err)
	}

	// Then the 0.2 program, on the same data.
	url := LineURL(base+"aiios-plugins.md", "0.2")
	c := &Catalog{line: "0.2", fetch: fetch}
	c.Open("", cache, plat.Env, func() string { return url })
	if c.Current() != nil {
		t.Fatal("a 0.2 program holds the 0.1 program's kept copy")
	}
	if err := c.Refresh(context.Background()); !errors.As(err, new(*NotPublishedError)) || !strings.Contains(err.Error(), "no catalog for AII OS 0.2 is published at "+url+" yet") {
		t.Fatalf("an address with no 0.2 file is said as %v", err)
	}
	served[url], served[url+".sig"] = lineIndex(t, plat, "2026-10-22T00:00:00Z", "0.2", []string{"compat", "host_line"}, entry("org.example.a", "1.0.0", "0.2.0"))
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.Current(); got == nil || got.Plugins[0].Version != "1.0.0" {
		t.Fatalf("the 0.2 file is not held: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(cache, "aiios-plugins-0.2.md.sig")); err != nil {
		t.Fatalf("the 0.2 file is not kept under its own name: %v", err)
	}
	if now, err := os.ReadFile(filepath.Join(cache, "aiios-plugins.md")); err != nil || !bytes.Equal(now, keptOld) {
		t.Fatalf("the 0.1 program's kept copy changed under the 0.2 program (%v)", err)
	}

	// The 0.1 file served at the 0.2 address is refused; 1.0.0 stays.
	served[url], served[url+".sig"] = served[base+"aiios-plugins.md"], served[base+"aiios-plugins.md.sig"]
	if err := c.Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "names no host line") {
		t.Fatalf("the 0.1 file at the 0.2 address: %v", err)
	}
	if got := c.Current(); got == nil || got.Plugins[0].Version != "1.0.0" {
		t.Fatalf("a refused index replaced the one held: %+v", got)
	}

	// Going back: a 0.1 program started on this data holds its own file.
	back := &Catalog{line: "0.1"}
	back.Open("", cache, plat.Env, func() string { return base + "aiios-plugins.md" })
	if got := back.Current(); got == nil || got.Plugins[0].Version != "0.9.0" {
		t.Fatalf("a 0.1 program started after the 0.2 one holds %+v", got)
	}
	if _, refusal := back.State(); refusal != "" {
		t.Fatalf("a 0.1 program started after the 0.2 one is told %q", refusal)
	}
}

// THE UPDATES CARD NAMES WHAT AN UPDATE TO ANOTHER LINE WOULD STRAND. The
// offered line's catalog is fetched beside this one (or read from the
// operator's checkout) and verified for that line; each installed plugin
// it does not carry, carries only for later releases of the line, or
// carries with no build for this system is named with why. An address with
// no file for that line is a NotPublishedError.
func TestTheOfferedLinesCatalogNamesThePluginsAnUpdateWouldStrand(t *testing.T) {
	plat := platformRole(t)
	md, sig := lineIndex(t, plat, "2026-12-01T00:00:00Z", "0.3", []string{"compat", "host_line"},
		entry("org.example.carried", "2.0.0", "0.3.0"),
		entry("org.example.later", "2.0.0", "0.3.1"),
		entry("org.example.linux", "2.0.0", "0.3.0", CatalogPackage{Platform: "linux", Arch: "amd64", URL: "https://x/l", SHA256: "sha256:aa", Size: 1}))
	url := "https://catalog.example.test/aiios-plugins-0.2.md"
	asked := []string{}
	c := &Catalog{line: "0.2", fetch: func(_ context.Context, u string, _ int64) ([]byte, error) {
		asked = append(asked, u)
		switch u {
		case "https://catalog.example.test/aiios-plugins-0.3.md":
			return md, nil
		case "https://catalog.example.test/aiios-plugins-0.3.md.sig":
			return sig, nil
		}
		return nil, &StatusError{Code: 404}
	}}
	c.Open("", "", plat.Env, func() string { return url })
	next, err := c.FetchLine(context.Background(), "0.3")
	if err != nil {
		t.Fatal(err)
	}
	installed := map[string]string{"org.example.carried": "1.0.0", "org.example.later": "1.0.0", "org.example.linux": "1.0.0", "org.example.gone": "1.4.0"}
	got := next.StrandedBy(installed, "0.3.0", "darwin", "arm64")
	want := []Stranded{
		{ID: "org.example.gone", Version: "1.4.0", Reason: "the AII OS 0.3 catalog does not carry it"},
		{ID: "org.example.later", Version: "1.0.0", Reason: "the AII OS 0.3 catalog's 2.0.0 needs AII OS 0.3.1 or newer"},
		{ID: "org.example.linux", Version: "1.0.0", Reason: "the AII OS 0.3 catalog has no build of it for darwin/arm64"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("an update to 0.3.0 strands\n%v\nwant\n%v", got, want)
	}
	if len(asked) != 2 || asked[0] != "https://catalog.example.test/aiios-plugins-0.3.md" {
		t.Fatalf("the 0.3 catalog was asked at %v", asked)
	}

	// The 0.3 file signed for 0.2 is not the 0.3 catalog.
	md2, sig2 := lineIndex(t, plat, "2026-12-01T00:00:00Z", "0.2", []string{"compat", "host_line"}, entry("org.example.carried", "2.0.0", ""))
	md, sig = md2, sig2
	if _, err := c.FetchLine(context.Background(), "0.3"); err == nil || !strings.Contains(err.Error(), "signed for AII OS 0.2 hosts, and this host is AII OS 0.3") {
		t.Fatalf("a 0.2 index at the 0.3 address: %v", err)
	}
	var none *NotPublishedError
	if _, err := c.FetchLine(context.Background(), "0.4"); !errors.As(err, &none) || none.Line != "0.4" {
		t.Fatalf("no 0.4 file: %v", err)
	}

	// From the operator's checkout, the line's file in it.
	dir := t.TempDir()
	md, sig = lineIndex(t, plat, "2026-12-01T00:00:00Z", "0.3", []string{"compat", "host_line"}, entry("org.example.carried", "2.0.0", ""))
	if err := os.WriteFile(filepath.Join(dir, "aiios-plugins-0.3.md"), md, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aiios-plugins-0.3.md.sig"), sig, 0o600); err != nil {
		t.Fatal(err)
	}
	checkout := &Catalog{line: "0.2"}
	checkout.Open(dir, "", plat.Env, func() string { return url })
	if got, err := checkout.FetchLine(context.Background(), "0.3"); err != nil || len(got.StrandedBy(map[string]string{"org.example.carried": "1.0.0"}, "0.3.0", "darwin", "arm64")) != 0 {
		t.Fatalf("the checkout's 0.3 file: %v", err)
	}
	if _, err := checkout.FetchLine(context.Background(), "0.4"); !errors.As(err, &none) {
		t.Fatalf("no 0.4 file in the checkout: %v", err)
	}
}

// THE RELEASE TOOL'S PAYLOAD BINDS THE LINE OF THE RELEASE IT ANNOUNCES,
// and refuses an index for a line after the first that does not name
// host_line in must_understand (a host of an earlier line pointed at it
// would half-read it). What it renders, once signed, a host of that line
// reads.
func TestThePayloadBindsTheLineOfTheReleaseItAnnounces(t *testing.T) {
	plat := platformRole(t)
	md, _ := lineIndex(t, plat, "2026-10-22T00:00:00Z", "", []string{"compat"}, entry("org.example.a", "1.0.0", ""))
	if _, err := SignedPayload(md, "0.2.0", "2026-10-22T00:00:00Z", nil); err == nil || !strings.Contains(err.Error(), "names host_line in must_understand") {
		t.Fatalf("a 0.2 index without host_line in must_understand: %v", err)
	}
	legacy, err := SignedPayload(md, "0.1.23", "2026-10-22T00:00:00Z", nil)
	if err != nil || !strings.Contains(string(legacy), `"host_line": "0.1"`) {
		t.Fatalf("the 0.1 payload: %s %v", legacy, err)
	}
	md, _ = lineIndex(t, plat, "2026-10-22T00:00:00Z", "", []string{"compat", "host_line"}, entry("org.example.a", "1.0.0", ""))
	payload, err := SignedPayload(md, "0.2.1", "2026-10-22T00:00:00Z", nil)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil || p["host_line"] != "0.2" {
		t.Fatalf("the 0.2 payload: %s %v", payload, err)
	}
	sig, err := plat.Sign(ArtifactKindPluginCatalog, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCatalogForLine(md, sig, plat.Env, "0.2"); err != nil {
		t.Fatalf("a 0.2 host refuses the payload the tool rendered for it: %v", err)
	}
	if _, err := ParseCatalogForLine(md, sig, plat.Env, "0.3"); err == nil {
		t.Fatal("a 0.3 host reads the payload rendered for 0.2")
	}
}
