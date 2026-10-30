package app

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
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

type listsHost struct {
	app      *App
	plat     *packagetest.Role
	reviewer *packagetest.Role
	trust    string
	served   map[string][]byte
	asked    []string
}

func newListsHost(t *testing.T, epochs map[string]int64) *listsHost {
	t.Helper()
	dir := t.TempDir()
	h := &listsHost{served: map[string][]byte{}, trust: filepath.Join(dir, "trust")}
	var err error
	if h.plat, err = packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease); err != nil {
		t.Fatal(err)
	}
	if h.reviewer, err = packagetest.NewRole("test-reviewer", packagetest.KeyTypeReviewer); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.trust, 0o700); err != nil {
		t.Fatal(err)
	}
	h.write(t, packagetest.StatusFilePlatform, h.list(t, h.plat, 1))
	h.write(t, packagetest.StatusFileReviewer, h.list(t, h.reviewer, 1))

	catDir := filepath.Join(dir, "catalog")
	announcingCatalog(t, catDir, h.plat, epochs)

	h.app = New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")},
		Plugins: PluginsConfig{CatalogURL: "https://catalog.example.test/aiios-plugins.md"}})
	h.app.trustDir = h.trust
	h.reload()
	h.app.catalog.Open(catDir, "", h.plat.Env, h.app.effectiveCatalogURL)
	if h.app.catalog.Current() == nil {
		t.Fatal("the announcing catalog was not held")
	}
	h.app.listFetch = func(ctx context.Context, name string, max int64) ([]byte, error) {
		h.asked = append(h.asked, name)
		if b, ok := h.served[name]; ok {
			return b, nil
		}
		return nil, fmt.Errorf("the catalog host answered 404")
	}
	return h
}

func announcingCatalog(t *testing.T, catDir string, plat *packagetest.Role, epochs map[string]int64) {
	t.Helper()
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		t.Fatal(err)
	}
	block, _ := json.MarshalIndent(plugincatalog.Index{Version: 1, Generated: "2026-10-01T00:00:00Z"}, "", "  ")
	md := []byte("# Catalog\n\n```json\n" + string(block) + "\n```\n")
	sig, err := plat.Sign(plugincatalog.ArtifactKindPluginCatalog, map[string]any{
		"catalog_version": 1, "generated": "2026-10-14T00:00:00Z", "catalog_sha256": sigenvelope.SHA256Prefixed(md),
		"host_release": "0.2.0", "status_epochs": epochs, "host_line": plugincatalog.OwnLine(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catDir, plugincatalog.CatalogFileFor(plugincatalog.OwnLine())), md, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catDir, plugincatalog.CatalogFileFor(plugincatalog.OwnLine())+".sig"), sig, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (h *listsHost) list(t *testing.T, role *packagetest.Role, epoch int64) []byte {
	t.Helper()
	raw, err := role.SignRevocationStatus(epoch, nil)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (h *listsHost) write(t *testing.T, name string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.trust, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// reload is the identity's load, as the sweep makes it after a change.
func (h *listsHost) reload() {
	roots := packagefmt.TrustRoots{PlatformRelease: h.plat.Env, Reviewer: h.reviewer.Env}
	roots.Revocation = packagefmt.LoadRevocationStatus(h.trust, roots, nil)
	h.app.pluginOpts = &pluginhost.Options{Roots: roots}
}

func (h *listsHost) onDisk(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.trust, name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAnAnnouncedListIsBroughtHereOnlyWhenNewer(t *testing.T) {
	h := newListsHost(t, map[string]int64{"platform_release": 2, "plugin_reviewer": 1})
	newer := h.list(t, h.plat, 2)
	h.served[packagetest.StatusFilePlatform] = newer
	if _, err := h.app.bringTrustLists(context.Background()); err != nil {
		t.Fatalf("an announced list that is served was not brought: %v", err)
	}
	if len(h.asked) != 1 || h.asked[0] != packagetest.StatusFilePlatform {
		t.Fatalf("fetched %v; want only the platform list, the one announced newer", h.asked)
	}
	if !bytes.Equal(h.onDisk(t, packagetest.StatusFilePlatform), newer) {
		t.Fatal("the announced list is not in the trust directory")
	}
	select {
	case <-h.app.sweepPoke:
	default:
		t.Fatal("the sweep was not asked to load the list written")
	}

	// Loaded, the identity holds what is announced: nothing is fetched.
	h.reload()
	h.asked = nil
	if _, err := h.app.bringTrustLists(context.Background()); err != nil || len(h.asked) != 0 {
		t.Fatalf("with the announced lists held, the check fetched %v (%v)", h.asked, err)
	}
}

// A LIST ANNOUNCED AND NOT BROUGHT HERE IS SAID, and for the platform
// release key's list it holds an automatic update. A list that does not
// verify is not written; one behind its announcement is kept when it is
// newer than the file, and the announcement is still said to be missing.
func TestAnAnnouncedListThatCannotBeBroughtIsSaid(t *testing.T) {
	h := newListsHost(t, map[string]int64{"platform_release": 3, "plugin_reviewer": 2})
	before := h.onDisk(t, packagetest.StatusFilePlatform)
	_, err := h.app.bringTrustLists(context.Background())
	if !errors.Is(err, updates.ErrReleaseListBehind) {
		t.Fatalf("a missing platform list must hold an automatic update: %v", err)
	}
	if !bytes.Equal(h.onDisk(t, packagetest.StatusFilePlatform), before) {
		t.Fatal("a list that could not be fetched changed the file")
	}
	for _, want := range []string{
		"The platform release key's withdrawal list announced at epoch 3 could not be brought here",
		"this machine holds epoch 1",
		"no update is installed automatically",
		"The plugin reviewer's withdrawal list announced at epoch 2",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the card is not told %q:\n%v", want, err)
		}
	}

	// Served behind its announcement, and signed by another key: neither
	// is written, and each is said.
	h.served[packagetest.StatusFilePlatform] = h.list(t, h.plat, 2)
	h.served[packagetest.StatusFileReviewer] = h.list(t, h.plat, 2)
	_, err = h.app.bringTrustLists(context.Background())
	if !errors.Is(err, updates.ErrReleaseListBehind) || !strings.Contains(err.Error(), "the list published is at epoch 2") {
		t.Fatalf("a list behind its announcement was taken as current: %v", err)
	}
	// The epoch-2 list is newer than the file, so it is kept; what still
	// holds the update is that the one announced is not here.
	if !bytes.Equal(h.onDisk(t, packagetest.StatusFilePlatform), h.served[packagetest.StatusFilePlatform]) {
		t.Fatal("a verified list newer than the one held was not kept")
	}
	if !strings.Contains(err.Error(), "reviewer") || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("a list signed by another key was not refused by name: %v", err)
	}

	// Only another root's list missing: said, holding nothing.
	h2 := newListsHost(t, map[string]int64{"plugin_reviewer": 2})
	if _, err := h2.app.bringTrustLists(context.Background()); err == nil || errors.Is(err, updates.ErrReleaseListBehind) {
		t.Fatalf("a reviewer list that is not here: %v", err)
	}
}

// THE UPDATES CARD NAMES THE NEWEST RELEASE ANNOUNCED AND ITS DATE (row
// 9.32), read from the catalog held, with the catalog's address when the
// operator set one, and what the last check said of the lists.
func TestTheUpdatesCardNamesTheNewestAnnouncedRelease(t *testing.T) {
	h := newListsHost(t, map[string]int64{"platform_release": 1})
	h.app.updateChecker = updates.NewChecker(
		func() *sigenvelope.PublicKeyEnvelope { return h.plat.Env },
		func() string { return "0.1.22" },
		func() bool { return false }, func() string { return "" },
		nil, t.TempDir(), func() bool { return false }, func() bool { return false })
	view := h.app.updateStateView()
	if view.AnnouncedVersion != "0.2.0" || view.AnnouncedOn != "2026-10-14" || view.AnnouncedFrom != "https://catalog.example.test/aiios-plugins.md" {
		t.Fatalf("the card is told %q on %q from %q", view.AnnouncedVersion, view.AnnouncedOn, view.AnnouncedFrom)
	}
	h.app.cfg.Plugins.CatalogURL = ""
	if view := h.app.updateStateView(); view.AnnouncedFrom != "" || view.AnnouncedVersion != "0.2.0" {
		t.Fatalf("the platform's own address is named, or the announcement lost: %+v", view)
	}
}

// THE BOOT WIRES THE LISTS TO THE UPDATE CHECK, and what a check brings
// is loaded by the identity under its guard. A started identity pins a
// platform root of the test's whose list the binary does not ship, and
// its catalog announces that root's list at epoch 2; the checker the boot
// built brings it, and the identity's next load accepts it and records
// the acceptance in its record.
func TestBootWiresTheListsToTheUpdateCheck(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "ListsTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	rootBytes, _ := json.Marshal(plat.Env)
	rootPath := filepath.Join(dir, "platform-root.json")
	if err := os.WriteFile(rootPath, rootBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	catDir := filepath.Join(dir, "catalog")
	announcingCatalog(t, catDir, plat, map[string]int64{"platform_release": 2})
	t.Chdir(dir)
	app := New(&Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0", PlatformRoot: rootPath, CatalogDir: catDir},
		Agency:     defaultConfig().Agency,
	})
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })
	list, err := plat.SignRevocationStatus(2, nil)
	if err != nil {
		t.Fatal(err)
	}
	app.listFetch = func(ctx context.Context, name string, max int64) ([]byte, error) {
		if name == packagetest.StatusFilePlatform {
			return list, nil
		}
		return nil, fmt.Errorf("the catalog host answered 404")
	}
	if err := app.updateChecker.BringTrustLists(context.Background()); err != nil {
		t.Fatalf("the boot's checker did not bring the announced list: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(app.trustDir, packagetest.StatusFilePlatform)); !bytes.Equal(got, list) {
		t.Fatal("the announced list is not in the trust directory")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		recorded, _, ok, err := app.trustGuard.TrustEpochHighWater(packagefmt.EpochMark(plat.Env))
		held, have := app.pluginOptions().Roots.Revocation.Epoch(packagefmt.KeyTypePlatformRelease)
		if err == nil && ok && recorded == 2 && have && held == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the identity did not load and record the list the check brought: recorded %d (%v, %v), held %d (%v)", recorded, ok, err, held, have)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestAListOfAnotherHandIsToldNotRefused(t *testing.T) {
	h := newListsHost(t, map[string]int64{"platform_release": 1, "plugin_reviewer": 5})
	roots := h.app.pluginOptions().Roots
	roots.Reviewer = packagefmt.ShippedRoots().Reviewer
	roots.Revocation = packagefmt.LoadRevocationStatus(h.trust, roots, nil)
	h.app.pluginOpts = &pluginhost.Options{Roots: roots}
	for i := 0; i < 2; i++ {
		note, err := h.app.bringTrustLists(context.Background())
		if err != nil || len(h.asked) != 0 {
			t.Fatalf("a list the announcement does not speak for was fetched or refused: %v, fetched %v", err, h.asked)
		}
		if !strings.Contains(note, "The plugin reviewer's withdrawal list is announced in the catalog signed with this machine's own platform release key (plugins.platform_root)") ||
			!strings.Contains(note, "it is not fetched") {
			t.Fatalf("the note does not say which list and why: %q", note)
		}
	}
}

// WHAT IS ANNOUNCED AGAINST WHAT THIS MACHINE SEES: a newer release
// announced than the source offers, said only after a check that
// succeeded; a program newer than anything announced.
func TestTheAnnouncementIsHeldAgainstWhatIsOffered(t *testing.T) {
	for _, c := range []struct {
		announced, running, available string
		checked, notOffered, behind   bool
	}{
		{"0.2.1", "0.2.0", "", true, true, false},
		{"0.2.1", "0.2.0", "0.2.1", true, false, false},
		{"0.2.2", "0.2.0", "0.2.1", true, true, false},
		{"0.2.1", "0.2.0", "", false, false, false},
		{"0.2.0", "0.2.0", "", true, false, false},
		{"0.2.0", "0.2.1", "", true, false, true},
		{"latest", "0.2.0", "", true, false, false},
	} {
		n, b := announcedAgainst(c.announced, c.running, c.available, c.checked)
		if n != c.notOffered || b != c.behind {
			t.Errorf("%+v: not offered %v, behind %v", c, n, b)
		}
	}
}
