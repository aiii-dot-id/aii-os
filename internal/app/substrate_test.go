package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func TestPluginStorageVerificationKeepsItsOptionsSnapshot(t *testing.T) {
	first, next := &packagefmt.RevocationStatusSet{}, &packagefmt.RevocationStatusSet{}
	a := &App{pluginOpts: &pluginhost.Options{Roots: packagefmt.TrustRoots{Revocation: first}}}
	held := a.pluginOptions()
	a.publishPluginRevocations(next)
	if held.Roots.Revocation != first {
		t.Fatal("verification's held options changed beneath it")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			a.publishPluginRevocations(first)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = a.pluginOptions().Roots.Revocation
		}
	}()
	wg.Wait()
}

func TestNativeSubstrateInventoryUsesConfiguredOwners(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	cfg := defaultConfig()
	cfg.Tools.CWD = filepath.Join(root, "home")
	cfg.SourcePath = filepath.Join(root, "config", "config.json")
	cfg.Identity.LedgerPath = filepath.Join(root, "data", "ledger.jsonl")
	cfg.Identity.KeyPath = filepath.Join(root, "keys", "identity.sec")
	cfg.Identity.DBPath = filepath.Join(root, "data", "aii.db")
	nested := filepath.Join(root, "data", "plugins-models", "selected", "secret")
	cfg.Plugins.AuthProfiles = map[string]broker.AuthProfile{"nested": {SecretFile: nested}}
	a := &App{cfg: cfg}
	opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.NativePlaces == nil {
		t.Fatal("native activation received no substrate owner")
	}
	if opts.LegacyStorageOwners == nil {
		t.Fatal("plugin activation has no verified legacy ownership source")
	}
	data := identityDataDir(cfg.Identity.LedgerPath)
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	// This is the historical updater file contract, not a new plugin marker.
	if err := os.WriteFile(filepath.Join(data, "aii.previous"), []byte("recovery reader"), 0o700); err != nil {
		t.Fatal(err)
	}
	if opts.StorageUpgradeReady == nil {
		t.Fatal("directory upgrade has no recovery boundary")
	}
	if err := opts.StorageUpgradeReady(); !errors.Is(err, updates.ErrBootRecoveryPending) {
		t.Fatalf("legacy rename bypasses boot-recovery owner: %v", err)
	}
	updates.WriteBootMarker(data)
	if err := opts.StorageUpgradeReady(); err != nil {
		t.Fatal(err)
	}
	if _, err := opts.LegacyStorageOwners(t.Context()); err != nil {
		t.Fatalf("settled boot cannot query verified owners: %v", err)
	}
	assertNamed := func(paths ...string) {
		t.Helper()
		found := make(map[string]bool)
		places, err := opts.NativePlaces()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range places {
			found[p.Path] = true
		}
		for _, p := range paths {
			if !found[realPath(p)] {
				t.Errorf("native inventory omits %s", p)
			}
		}
	}
	assertNamed(identityDataDir(cfg.Identity.LedgerPath), identityCredentials(cfg.Identity.LedgerPath),
		cfg.Identity.KeyPath, cfg.SourcePath, providerFilePath(cfg.SourcePath),
		continuity.SnapshotKeyPath(cfg.Identity.KeyPath), nested)
	floor := firewall.DefaultPolicy()
	floor.SetPlaces(substratePlaces(*cfg, cfg.Tools.CWD))
	if got := strings.Count(floor.LocalFloor(), "**providers.json**"); got != 1 {
		t.Fatalf("provider name/place must share one reason in the resident's walls, got %d entries", got)
	}
	// A later activation gets the current app snapshot, not boot's profile list.
	later := filepath.Join(root, "another-profile", "key")
	a.cfgMu.Lock()
	a.cfg.Plugins.AuthProfiles = map[string]broker.AuthProfile{"later": {SecretFile: later}}
	a.cfgMu.Unlock()
	assertNamed(later)
}

func stockSlotApp(t *testing.T, files map[string]string) (*App, string) {
	t.Helper()
	return stockSlotAppAt(t, t.TempDir(), files)
}

// stockSlotAppAt is stockSlotApp with the home named by the caller, which
// may reach it through a symlink.
func stockSlotAppAt(t *testing.T, home string, files map[string]string) (*App, string) {
	t.Helper()
	data := filepath.Join(home, "data")
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SubstrateTest",
		KeyPath:    filepath.Join(data, "identity.sec"),
		LedgerPath: filepath.Join(data, "ledger.jsonl"),
		DBPath:     filepath.Join(data, "aii.db"),
	}).Ledger.Close()
	for rel, body := range files {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(home)
	cfg := defaultConfig()
	cfg.Identity.KeyPath, cfg.Identity.LedgerPath, cfg.Identity.DBPath = "data/identity.sec", "data/ledger.jsonl", "data/aii.db"
	cfg.LLM = withTestProvider(t, home, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.SourcePath = filepath.Join(home, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = "."
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })
	return app, home
}

// toolRead is what the identity's own read tool answers for a path, its
// refusal included.
func toolRead(app *App, path string) string {
	res, err := app.toolReg.Execute(context.Background(), "read", map[string]any{"file_path": path})
	if err != nil {
		return err.Error()
	}
	return res.Output + res.Error
}

// toolWrite is what the identity's own write tool answers, its refusal
// included.
func toolWrite(app *App, path, content string) string {
	res, err := app.toolReg.Execute(context.Background(), "write", map[string]any{"file_path": path, "content": content})
	if err != nil {
		return err.Error()
	}
	return res.Output + res.Error
}

func TestTheDataDirectoryIsSubstrate(t *testing.T) {
	secrets := []string{
		"credentials/profiles/id.aiii.codequality.api_key.key",
		"credentials/anthropic.json",
		"tls/server.key",
		"trust/platform.pub",
		"plugins-models/weights.bin",
		"backups/snap-1/aii.db",
		"plugins-data/org.example/state.json",
		"snapshot.key",
		"aii.db.bak-harvested-20260829T220551Z",
	}
	files := map[string]string{"notes.md": "mine"}
	for _, rel := range append(secrets, "ui-layout.json") {
		files["data/"+rel] = "SECRET " + rel
	}
	app, home := stockSlotApp(t, files)
	data := filepath.Join(home, "data")
	read := func(p string) string { return toolRead(app, p) }
	for _, rel := range secrets {
		for _, spelled := range []string{"data/" + rel, filepath.Join(data, rel), "DATA/" + rel} {
			if out := read(spelled); strings.Contains(out, "SECRET") || !strings.Contains(out, "access denied: protected path") {
				t.Fatalf("the identity's read of %s must be refused as substrate, got %q", spelled, out)
			}
		}
	}
	if out := read("data/ui-layout.json"); !strings.Contains(out, "SECRET ui-layout.json") {
		t.Fatalf("ui-layout.json is the identity's to read and edit: %q", out)
	}
	if out := read("notes.md"); !strings.Contains(out, "mine") {
		t.Fatalf("the home stays the identity's: %q", out)
	}

	ls := func(p string) tools.Result {
		res, _ := app.toolReg.Execute(context.Background(), "ls", map[string]any{"path": p})
		return res
	}
	for _, p := range []string{"data", "data/credentials", "data/credentials/profiles"} {
		if res := ls(p); !res.Refused || !strings.Contains(res.Error, "access denied: protected path") || res.Output != "" {
			t.Fatalf("ls %s must be refused as substrate: %+v", p, res)
		}
	}
	if res := ls("data/ui-layout.json"); res.Error != "" || !strings.Contains(res.Output, "ui-layout.json") {
		t.Fatalf("ls of the UI surface lists it: %+v", res)
	}
	res, _ := app.toolReg.Execute(context.Background(), "shell", map[string]any{"command": "cat data/credentials/profiles/id.aiii.codequality.api_key.key"})
	if strings.Contains(res.Output, "SECRET") || !strings.Contains(res.Error, "access denied") {
		t.Fatalf("the shell's best-effort check refuses the same place: %+v", res)
	}
	if !strings.Contains(app.ensureRing5Policy().LocalFloor(), "- **data** — ") {
		t.Fatal("the floor names the data directory")
	}
}

func TestTheIdentityKeepsItsUISurfaceInTheDataDirectory(t *testing.T) {
	app, home := stockSlotApp(t, map[string]string{
		"data/credentials/anthropic.json": "SECRET credential",
		"data/ui/README.md":               "seeded",
		"data/ui/deeper/still/.keep":      "", // the write tool does not make directories
		"data/theme.json":                 `{"v":1,"tokens":{}}`,
	})
	data := filepath.Join(home, "data")
	// A sealed segment appears after boot (boot verifies the ones it finds).
	if err := os.WriteFile(filepath.Join(data, "segment-1-9.jsonl.gz"), []byte("SECRET segment"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The surface is the identity's, at any depth, in and out.
	for _, rel := range []string{"data/ui/name", "data/ui/custom.css", "data/ui/deeper/still/custom.js", "data/theme.json", "data/ui-layout.json"} {
		if out := toolWrite(app, rel, "mine "+rel); !strings.Contains(out, "Wrote") {
			t.Fatalf("the identity's write of %s must land: %q", rel, out)
		}
		if out := toolRead(app, rel); !strings.Contains(out, "mine "+rel) {
			t.Fatalf("the identity's read of %s must return what it wrote: %q", rel, out)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(data, "ui", "deeper", "still", "custom.js")); err != nil || string(raw) != "mine data/ui/deeper/still/custom.js" {
		t.Fatalf("the write reached the disk: %q %v", raw, err)
	}

	// The name it is told to write is the name the dashboard reads, and
	// the wall lets it write that file. One path in three places.
	if got := path.Join("data", uiOverlayDirName, uiNameFileName); got != prompt.NameFile {
		t.Fatalf("the prompt names %s, the reader reads %s", prompt.NameFile, got)
	}
	if _, _, err := app.toolReg.Admit("write", prompt.NameFile); err != nil {
		t.Fatalf("the prompt tells the identity to write %s and the wall refuses it: %v", prompt.NameFile, err)
	}
	if out := toolWrite(app, prompt.NameFile, "Aria\n"); !strings.Contains(out, "Wrote") {
		t.Fatalf("recording a name: %q", out)
	}
	if got := app.resolveDisplayName(); got != "Aria" {
		t.Fatalf("the name the identity wrote is not the name the dashboard reads: %q", got)
	}

	// Everything else stays the host's — a sibling that shares a prefix,
	// a dot-dot out, a symlink out, the record, the credentials.
	if err := os.Symlink(filepath.Join(data, "credentials", "anthropic.json"), filepath.Join(data, "ui", "link")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	for _, rel := range []string{
		"data/ui2/x", "data/uifoo", "data/ui-notes/x", "data/theme.json.bak", "data/ui-layout.json.bak",
		"data/ui/../credentials/anthropic.json", "data/ui/link", "data/credentials/anthropic.json",
		"data/segment-1-9.jsonl.gz", "data/ui/../segment-1-9.jsonl.gz", "data/ledger.jsonl", "data/newfile.txt",
	} {
		for what, out := range map[string]string{"read": toolRead(app, rel), "write": toolWrite(app, rel, "SPRAY")} {
			if !strings.Contains(out, "access denied: protected path") || strings.Contains(out, "SECRET") {
				t.Errorf("the identity's %s of %s must be refused as substrate, got %q", what, rel, out)
			}
		}
	}
	for _, rel := range []string{"data/credentials/anthropic.json", "data/segment-1-9.jsonl.gz"} {
		if raw, _ := os.ReadFile(filepath.Join(home, rel)); !strings.HasPrefix(string(raw), "SECRET") {
			t.Errorf("%s changed under a refused write: %q", rel, raw)
		}
	}
	for _, rel := range []string{"data/ui2", "data/uifoo", "data/ui-notes", "data/theme.json.bak", "data/newfile.txt"} {
		if _, err := os.Stat(filepath.Join(home, rel)); err == nil {
			t.Errorf("%s exists: a refused write landed", rel)
		}
	}

	// The floor says what the identity may edit, and it is true.
	floor := app.ensureRing5Policy().LocalFloor()
	want := "What you edit in it: ui/ (your name and the dashboard's frame), theme.json and ui-layout.json."
	if !strings.Contains(floor, want) || strings.Contains(floor, "the one file in it you edit") {
		t.Fatalf("the floor must name the three things the identity edits, and only them:\n%s", floor)
	}
}

// THE SURFACE HOLDS WHEN THE SLOT IS REACHED THROUGH A SYMLINK — a macOS
// temp directory, a home on a linked volume. The data place is kept under
// the resolved path, so the surface it excepts must be named under the
// resolved path too, or a leaf that does not exist yet (theme.json on an
// identity that has never had one) would be named where the wall never
// looks and the exception would silently not apply.
func TestTheUISurfaceHoldsWhenTheSlotIsReachedThroughASymlink(t *testing.T) {
	real := t.TempDir()
	slot := filepath.Join(t.TempDir(), "slot")
	if err := os.Symlink(real, slot); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	app, home := stockSlotAppAt(t, slot, map[string]string{"data/credentials/anthropic.json": "SECRET credential"})
	if home != slot {
		t.Fatalf("the slot is reached through the link: %s", home)
	}
	if _, err := os.Stat(filepath.Join(real, "data", "theme.json")); err == nil {
		t.Fatal("the leaf must not exist yet: that is the case under test")
	}
	for _, rel := range []string{"data/theme.json", "data/ui/name", "data/ui/custom.css"} {
		if out := toolWrite(app, rel, "mine"); !strings.Contains(out, "Wrote") {
			t.Fatalf("the identity's write of %s must land through the link: %q", rel, out)
		}
	}
	for _, rel := range []string{"data/credentials/anthropic.json", "data/uifoo", "data/newfile.txt"} {
		if out := toolWrite(app, rel, "SPRAY"); !strings.Contains(out, "access denied: protected path") {
			t.Errorf("the identity's write of %s must be refused as substrate through the link, got %q", rel, out)
		}
	}
	// The plugin door names the surface the same way, so it holds through the link too.
	door := newPluginSandbox(app.toolReg, app.configSnapshot().Identity.LedgerPath)
	for _, rel := range []string{"data/theme.json", "data/ui/name", "data/ui/custom.css"} {
		_, _, err := door.Admit("org.example.probe fs.write", rel)
		var denied *tools.SubstrateError
		if !errors.As(err, &denied) || denied.Rule.ID != "sub.ui" {
			t.Errorf("a plugin's %s must be refused as the UI surface through the link, got %v", rel, err)
		}
	}
}

// THE INVENTORY NAMES A PLACE NOT YET MADE AS IT NAMES ITS DIRECTORY. A
// fresh identity has no credentials and no backups. Under a slot reached
// through a link each was named with the link in it, because only a place
// that existed was resolved, while the data directory, which exists, was
// named by its real path. So the place was not found inside its own
// directory: the inventory listed it beside the directory that already
// holds it, and handed the native wall a name no file would ever have.
// (The floor resolved its places again and held; the whole suite's run on
// a Mac, whose temporary directory is such a link, found the helper.)
func TestTheInventoryNamesAPlaceNotYetMadeByItsDirectorysRealPath(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	slot := filepath.Join(t.TempDir(), "slot")
	if err := os.Symlink(real, slot); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(real, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, want := realPath(filepath.Join(slot, "data", "credentials", "new.json")), filepath.Join(real, "data", "credentials", "new.json"); got != want {
		t.Fatalf("a path not yet made is named %s; its directory's real path makes it %s", got, want)
	}
	cfg := defaultConfig()
	cfg.Tools.CWD = slot
	cfg.SourcePath = filepath.Join(slot, "config.json")
	cfg.Identity = IdentityConfig{
		KeyPath: filepath.Join(slot, "data", "identity.sec"), LedgerPath: filepath.Join(slot, "data", "ledger.jsonl"), DBPath: filepath.Join(slot, "data", "aii.db"),
	}
	data := filepath.Join(real, "data")
	named := map[string]string{}
	for _, o := range substrateObjects(*cfg, slot) {
		named[o.ID] = o.Path
		if inside(slot, o.Path) {
			t.Errorf("%s is named through the link: %s", o.ID, o.Path)
		}
	}
	for _, id := range []string{"sub.data.credentials", "sub.data.backups", "sub.data.trust", "sub.ledger", "sub.key"} {
		if named[id] == "" || !inside(data, named[id]) {
			t.Errorf("%s is not named inside the data directory's real path %s: %q", id, data, named[id])
		}
	}
	for _, p := range substratePlaces(*cfg, slot) {
		if p.Path != data && inside(data, p.Path) {
			t.Errorf("%s (%s) is listed beside the data directory that already holds it", p.ID, p.Path)
		}
		if strings.HasPrefix(p.ID, "sub.data.") {
			t.Errorf("%s (%s) was not found inside its own directory", p.ID, p.Path)
		}
	}
}

func TestAPluginNeverReachesTheIdentitysUISurface(t *testing.T) {
	app, _ := stockSlotApp(t, map[string]string{
		"notes.md":                        "mine",
		"data/credentials/anthropic.json": "SECRET credential",
		"data/ui/README.md":               "seeded",
	})
	door := newPluginSandbox(app.toolReg, app.configSnapshot().Identity.LedgerPath)
	const plugin = "org.example.probe fs.write"

	for _, rel := range []string{
		"data/ui", "data/ui/name", "data/ui/custom.js", "data/ui/deeper/still/app.js", "DATA/UI/custom.js",
		"data/theme.json", "data/ui-layout.json",
	} {
		// The identity's own door lets the surface through — the contrast that
		// makes the plugin's refusal the door's and not the harness's.
		if _, _, err := app.toolReg.Admit("write", rel); err != nil {
			t.Fatalf("the identity's own door must admit %s: %v", rel, err)
		}
		_, _, err := door.Admit(plugin, rel)
		var denied *tools.SubstrateError
		if !errors.As(err, &denied) || denied.Rule.ID != "sub.ui" {
			t.Errorf("a plugin's %s must be refused as the UI surface, got %v", rel, err)
			continue
		}
	}

	// Everything else is the one rule's answer, unchanged: the home is
	// open, a sibling that shares a prefix and the credentials are refused
	// by the rule itself, not by the surface.
	if _, _, err := door.Admit(plugin, "notes.md"); err != nil {
		t.Errorf("the home stays a plugin's to work in: %v", err)
	}
	for _, rel := range []string{"data/uifoo", "data/credentials/anthropic.json", "data/newfile.txt"} {
		_, _, err := door.Admit(plugin, rel)
		var denied *tools.SubstrateError
		if !errors.As(err, &denied) || denied.Rule.ID == "sub.ui" {
			t.Errorf("%s must be refused by the data place, not the surface: %v", rel, err)
		}
	}
}

// THE PLUGIN DOOR IS WIRED INTO THE REAL BROKER: a plugin granted files
// (not read only), bound on the app's own broker, is refused the identity's
// UI surface — FS_PROTECTED, nothing on disk — while it works in the home
// and the identity's own tools write the same surface. The adapter alone
// (above) would pass if the broker were handed the bare sandbox.
func TestThePluginDoorIsWiredIntoTheRealBroker(t *testing.T) {
	app, home := stockSlotApp(t, map[string]string{"data/ui/README.md": "seeded"})
	if app.pluginOpts == nil || app.pluginOpts.Broker == nil {
		t.Fatal("the app has a broker to bind a plugin on")
	}
	const id = "org.example.probe"
	host := app.pluginOpts.Broker
	host.ReplacePolicy(map[string]broker.Grant{id: {Files: true}}, nil)
	b := host.Bind(id, packagefmt.TierT1, []string{"fs.sandbox"})
	write := func(path string) string {
		reply, err := b.Dispatch(context.Background(), "invoke-call",
			[]byte(fmt.Sprintf(`{"operation":"fs.write","target":{"root":"sandbox","path":%q},"arguments":{"data":"PROBE"}}`, path)))
		if err != nil {
			t.Fatal(err)
		}
		return string(reply)
	}

	if r := write("plugin-note.txt"); !strings.Contains(r, `"succeeded"`) {
		t.Fatalf("the plugin must work in the home (the harness writes): %s", r)
	}
	if got, _ := os.ReadFile(filepath.Join(home, "plugin-note.txt")); string(got) != "PROBE" {
		t.Fatalf("the control write reached the disk: %q", got)
	}
	for _, rel := range []string{"data/ui/custom.js", "data/ui/name", "data/theme.json", "data/ui-layout.json"} {
		if r := write(rel); !strings.Contains(r, "FS_PROTECTED") || strings.Contains(r, `"succeeded"`) {
			t.Errorf("a plugin's write to %s must be refused FS_PROTECTED, got %s", rel, r)
		}
		if _, err := os.Stat(filepath.Join(home, rel)); err == nil {
			t.Errorf("%s exists: a refused plugin write landed", rel)
		}
	}
	// The same path is the identity's to write.
	if out := toolWrite(app, "data/ui/custom.js", "mine"); !strings.Contains(out, "Wrote") {
		t.Errorf("the identity's own write of the surface must land: %q", out)
	}
}

func TestThePluginDoorKnowsTheInterfaceSurfaceByWhatItIs(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	theme := filepath.Join(home, "data", "theme.json")
	if err := os.MkdirAll(filepath.Dir(theme), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{theme, filepath.Join(home, "notes.md")} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	second := filepath.Join(home, "second.json")
	linked := os.Link(theme, second) == nil
	door := pluginSandbox{
		reg: tools.NewRegistry(home, firewall.DefaultPolicy(), tools.Timeouts{}),
		surface: []*firewall.Rule{{ID: "sub.ui", Kind: firewall.KindSubstrate, Path: theme, Enforced: true,
			Reason: "The dashboard's frame, colours and layout are your operator's and the identity's to re-form, not a plugin's."}},
	}
	held := func(path string) error {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return door.Held(info, nil)
	}
	var denied *tools.SubstrateError
	if err := held(theme); !errors.As(err, &denied) || denied.Rule.ID != "sub.ui" {
		t.Errorf("a surface file is held as the surface: %v", err)
	}
	if linked {
		if err := held(second); !errors.As(err, &denied) || denied.Rule.ID != "sub.ui" {
			t.Errorf("the same file under another name is held as the surface: %v", err)
		}
	} else {
		t.Log("hard links unavailable here: the second name is not asked")
	}
	if err := held(filepath.Join(home, "notes.md")); err != nil {
		t.Errorf("an ordinary file is not held: %v", err)
	}
}

// THE PLUGIN DOOR ASKS ONE LOOK FOR AN OPERATION. Its Look is the door
// for one operation: Admit, Held and Admit again of a file with two names
// walk the kept directories once, where the door as it stands walks for
// each question.
func TestThePluginDoorAsksOneLookForAnOperation(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, "data")
	if err := os.MkdirAll(filepath.Join(home, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	pair := filepath.Join(home, "notes", "pair-a.txt")
	if err := os.WriteFile(pair, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(pair, filepath.Join(home, "notes", "pair-b.txt")); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data, Reason: "kept", Enforced: true}})
	door := pluginSandbox{reg: tools.NewRegistry(home, policy, tools.Timeouts{})}
	ask := func(gate tools.Gate) int64 {
		t.Helper()
		before := firewall.WalksForTest()
		if _, _, err := gate.Admit("p fs.read", "notes/pair-b.txt"); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(filepath.Join(home, "notes", "pair-b.txt"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		info, _ := f.Stat()
		if err := gate.Held(info, f); err != nil {
			t.Fatal(err)
		}
		if _, _, err := gate.Admit("p fs.read", "notes/pair-b.txt"); err != nil {
			t.Fatal(err)
		}
		return firewall.WalksForTest() - before
	}
	if n := ask(door.Look()); n != 1 {
		t.Errorf("one operation's look walked the kept directories %d times, want once", n)
	}
	if n := ask(door); n != 3 {
		t.Errorf("the door as it stands walks for each question: %d walks, want 3", n)
	}
}

// linkTo stands a link at at that leads to target: a symbolic link, or on
// Windows a junction, which a process there makes without a privilege.
func linkTo(t *testing.T, target, at string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", at, target).CombinedOutput(); err != nil {
			t.Fatalf("a junction %s -> %s: %v: %s", at, target, err, out)
		}
		return
	}
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

func TestTheDashboardSurfaceIsOpenedByItsOwnNameNeverThroughALink(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	sibling := filepath.Join(root, "identity-1")
	for _, d := range []string{data, sibling} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	top := filepath.VolumeName(root) + string(filepath.Separator)
	for _, target := range []string{top, sibling} {
		ui := identityUISurface(realPath(data))[0]
		os.Remove(ui)
		linkTo(t, target, ui)
		got := uiSurfacePaths(filepath.Join(data, "ledger.jsonl"))
		if len(got) != 3 || got[0] != ui {
			t.Fatalf("data/ui a link to %s: the surface is %q, want data/ui by its own name first", target, got)
		}
		p := firewall.DefaultPolicy()
		p.SetPlaces([]*firewall.Rule{{ID: "sub.data", Kind: firewall.KindSubstrate, Path: data, Except: got, Enforced: true}})
		for _, r := range p.PlaceRules() {
			for _, e := range r.Except {
				if e == realPath(target) || !inside(realPath(data), e) {
					t.Errorf("data/ui a link to %s: the policy's exception %s is not inside the data directory", target, e)
				}
			}
		}
		surface := newPluginSandbox(nil, filepath.Join(data, "ledger.jsonl")).surface
		leads := false
		for _, r := range surface {
			leads = leads || r.Covers(filepath.Join(realPath(target), "x"))
		}
		if len(surface) < 3 || surface[0].Path != ui || !surface[0].Covers(ui) || !leads {
			t.Errorf("data/ui a link to %s: a plugin's refusal is not data/ui by its own name and where it leads: %+v", target, surface)
		}
		os.Remove(ui)
	}
}
