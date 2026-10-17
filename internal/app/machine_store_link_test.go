//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// THE IDENTITY ASKS THE SERVICE ON ITS LINK. These tests run the account's
// machine service in the test's process (machine.StartServiceForTest) and
// link each identity to it over a pipe: the identity's own Acquirer, its
// production materializer (the link's need), and the service's real store,
// fetcher and collector.

// linkFetches is the service's download path in these tests.
type linkFetches struct {
	mu     sync.Mutex
	bodies map[string][]byte
	calls  int
	// before, when set, runs before each call's bytes; an error ends the
	// call with it.
	before func(ctx context.Context) error
}

func (f *linkFetches) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f.mu.Lock()
	body, ok := f.bodies[url]
	f.calls++
	before := f.before
	f.mu.Unlock()
	if before != nil {
		if err := before(ctx); err != nil {
			return 0, err
		}
	}
	if !ok {
		return 0, &pluginhost.FetchStatusError{Status: 404}
	}
	n, err := w.Write(body[offset:])
	return int64(n), err
}

func (f *linkFetches) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *linkFetches) model(name string, body []byte) pluginhost.ModelDecl {
	sum := sha256.Sum256(body)
	url := "https://models.example/" + name
	f.mu.Lock()
	if f.bodies == nil {
		f.bodies = map[string][]byte{}
	}
	f.bodies[url] = body
	f.mu.Unlock()
	return pluginhost.ModelDecl{Name: name, URL: url, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
}

// linkedService is the service and its directory.
type linkedService struct {
	svc *machine.TestService
	dir string
	f   *linkFetches
}

func startLinkedService(t *testing.T, messages ...string) *linkedService {
	t.Helper()
	f := &linkFetches{}
	dir := filepath.Join(t.TempDir(), "machine")
	svc, err := machine.StartServiceForTest(dir, f.fetch, messages...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)
	return &linkedService{svc: svc, dir: dir, f: f}
}

// linkedIdentity is one identity of the account: its slot, its link, and
// an Acquirer that asks through the identity's production materializer.
type linkedIdentity struct {
	a     *App
	slot  string
	acq   *pluginhost.Acquirer
	ready chan string
}

func linkIdentity(t *testing.T, s *linkedService, name string) *linkedIdentity {
	t.Helper()
	slot := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(slot, 0o700); err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(slot); err == nil {
		slot = real
	}
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Machine.Dir = s.dir
	a := &App{cfg: cfg, pluginOpts: &pluginhost.Options{StoreDir: s.svc.Store()}}
	c := s.svc.ClientForTest(machine.ClientOptions{Dir: s.dir, Slot: slot, Key: kp, PollEvery: 20 * time.Millisecond})
	a.machine.client.Store(c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	ready := make(chan string, 8)
	acq := pluginhost.NewAcquirer(pluginhost.AcquirerConfig{Materialize: a.materializer(), Ready: func(id string) { ready <- id }, Logf: func(string, ...any) {}})
	acq.Attach(ctx)
	t.Cleanup(func() { cancel(); <-done })
	voiceWait(t, name+"'s link", 10*time.Second, func() bool { return c.Snapshot().State == machine.StateLinked })
	return &linkedIdentity{a: a, slot: slot, acq: acq, ready: ready}
}

func (li *linkedIdentity) awaitReady(t *testing.T, id string) {
	t.Helper()
	select {
	case got := <-li.ready:
		if got != id {
			t.Fatalf("ready for %s, want %s", got, id)
		}
	case <-time.After(15 * time.Second):
		st, _ := li.acq.Status(id)
		t.Fatalf("%s never became ready: %+v", id, st)
	}
}

// A SECOND IDENTITY RUNS FROM WHAT THE FIRST BROUGHT, WITH NO FETCH. Each
// asks the service on its own link through its production materializer;
// the first need fetches into the store, the second finds it present;
// both slots come to refer to it; and neither identity carries a fetcher.
func TestASecondIdentityFetchesNothingTheFirstBrought(t *testing.T) {
	s := startLinkedService(t)
	d := s.f.model("voice.bin", []byte("one voice for the whole account"))
	m := pluginhost.ModelMaterialForTest(s.svc.Store(), "org.example.voice", "0.1.0", d)
	a := linkIdentity(t, s, "identity-a")
	a.acq.Want(m)
	a.awaitReady(t, m.PluginID)
	if n := s.f.count(); n != 1 {
		t.Fatalf("the first identity's need: %d fetches, want 1", n)
	}
	if !strings.HasPrefix(m.ModelsDir, s.svc.Store()+string(filepath.Separator)) {
		t.Fatalf("the material's view %s is not in the store %s", m.ModelsDir, s.svc.Store())
	}
	if _, err := os.Stat(filepath.Join(m.ModelsDir, d.Name)); err != nil {
		t.Fatalf("the view holds no model: %v", err)
	}
	b := linkIdentity(t, s, "identity-b")
	b.acq.Want(m)
	b.awaitReady(t, m.PluginID)
	if n := s.f.count(); n != 1 {
		t.Fatalf("the second identity fetched: %d fetches in all, want 1", n)
	}
	refs := s.svc.Refs()
	for _, slot := range []string{a.slot, b.slot} {
		if !slices.ContainsFunc(refs, func(r machine.Ref) bool { return r.Slot == slot && r.Plugin == m.PluginID }) {
			t.Errorf("the service does not refer %s to the plugin's material: %+v", slot, refs)
		}
	}
}

// UNINSTALLED IN ONE IDENTITY, KEPT FOR THE OTHER: the reconciliation lets
// go of the uninstalled plugin's references, and the collector spares what
// the other identity still refers to — it is live, never noted as
// unreferenced.
func TestAnUninstallLetsGoOfOneIdentitysReferencesOnly(t *testing.T) {
	s := startLinkedService(t)
	m := pluginhost.ModelMaterialForTest(s.svc.Store(), "org.example.shared", "0.1.0", s.f.model("shared.bin", []byte("shared by two identities")))
	a, b := linkIdentity(t, s, "identity-a"), linkIdentity(t, s, "identity-b")
	for _, li := range []*linkedIdentity{a, b} {
		li.acq.Want(m)
		li.awaitReady(t, m.PluginID)
	}
	c := a.a.machine.client.Load()
	voiceWait(t, "a's status to name its references", 10*time.Second, func() bool { return len(c.Snapshot().Refs) == 1 })
	a.a.reconcileStoreRefs(t.Context(), map[string]bool{}, false, 2)
	refs := s.svc.Refs()
	if slices.ContainsFunc(refs, func(r machine.Ref) bool { return r.Slot == a.slot }) {
		t.Fatalf("the uninstalling identity still refers to the material: %+v", refs)
	}
	if !slices.ContainsFunc(refs, func(r machine.Ref) bool { return r.Slot == b.slot }) {
		t.Fatalf("the other identity's reference went with the uninstall: %+v", refs)
	}
	if err := s.svc.Collect(); err != nil {
		t.Fatal(err)
	}
	if u := s.svc.Unreferenced(); len(u) != 0 {
		t.Fatalf("the collector found what another identity refers to unreferenced: %v", u)
	}
	if _, err := os.Stat(m.ModelsDir); err != nil {
		t.Fatalf("the other identity's view: %v", err)
	}
}

func TestWithTheServiceStoppedMissingMaterialSaysItIsNotRunning(t *testing.T) {
	s := startLinkedService(t)
	li := linkIdentity(t, s, "identity-a")
	m := pluginhost.ModelMaterialForTest(s.svc.Store(), "org.example.later", "0.1.0", s.f.model("later.bin", []byte("brought later")))
	s.svc.Stop()
	c := li.a.machine.client.Load()
	voiceWait(t, "the link to see the service gone", 10*time.Second, func() bool { return c.Snapshot().State == machine.StateDown })
	err := li.a.needOverLink(t.Context(), m, nil)
	var wait *pluginhost.MaterialWait
	if !errors.As(err, &wait) || !strings.Contains(wait.Cause, "machine service is not running") || wait.Remedy == "" {
		t.Fatalf("a need with the service stopped: %v", err)
	}
}

// A SERVICE THAT DOES NOT SPEAK NEED REFUSES EACH NATIVE PLUGIN ALONE,
// naming the message and both builds; the page's line about the service
// is still served.
func TestAServiceWithoutNeedRefusesOnlyNativePlugins(t *testing.T) {
	s := startLinkedService(t, machine.MsgStatus, machine.MsgUpdateForce)
	li := linkIdentity(t, s, "identity-a")
	m := pluginhost.ModelMaterialForTest(s.svc.Store(), "org.example.native", "0.1.0", s.f.model("n.bin", []byte("n")))
	err := li.a.needOverLink(t.Context(), m, nil)
	var ended *pluginhost.MaterialEnded
	if !errors.As(err, &ended) || !strings.Contains(ended.Cause, "does not speak need") || !strings.Contains(ended.Cause, "plugin org.example.native") || ended.Final() == "" {
		t.Fatalf("a need of a service without it: %v", err)
	}
	if v := li.a.machineView(); v == nil || v.State != machine.StateLinked {
		t.Fatalf("the service's line is refused with the plugin: %+v", v)
	}
}

func TestInSAFENoNeedIsSent(t *testing.T) {
	s := startLinkedService(t)
	li := linkIdentity(t, s, "identity-a")
	li.a.mode.mode = ModeSafe
	m := pluginhost.ModelMaterialForTest(s.svc.Store(), "org.example.safe", "0.1.0", s.f.model("s.bin", []byte("s")))
	err := li.a.needOverLink(t.Context(), m, nil)
	var wait *pluginhost.MaterialWait
	if !errors.As(err, &wait) || !strings.Contains(wait.Cause, "SAFE") {
		t.Fatalf("a need in SAFE: %v", err)
	}
	if refs := s.svc.Refs(); len(refs) != 0 || s.f.count() != 0 {
		t.Fatalf("SAFE sent a need: refs %+v, fetches %d", refs, s.f.count())
	}
}

// THE STORE MOVES UNDER A RUNNING IDENTITY: a need prepared against the old
// place is answered with the new one, and every activation from then on
// reads there.
func TestAMovedStoreIsWhereTheNextActivationReads(t *testing.T) {
	s := startLinkedService(t)
	li := linkIdentity(t, s, "identity-a")
	old := s.svc.Store()
	m := pluginhost.ModelMaterialForTest(old, "org.example.moving", "0.1.0", s.f.model("m.bin", []byte("m")))
	next := filepath.Join(t.TempDir(), "plugins")
	if _, err := li.a.machine.client.Load().PlaceStore(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	err := li.a.needOverLink(t.Context(), m, nil)
	var wait *pluginhost.MaterialWait
	if !errors.As(err, &wait) || !strings.Contains(wait.Cause, "moved to "+next) {
		t.Fatalf("a need prepared against the old place: %v", err)
	}
	if got := li.a.pluginOptions().StoreDir; got != next {
		t.Fatalf("the next activation reads the store at %s, want %s", got, next)
	}
}

func TestAnIdentityRunningPresentMaterialComesToReferToIt(t *testing.T) {
	s := startLinkedService(t)
	m := pluginhost.ModelMaterialForTest(s.svc.Store(), "org.example.present", "0.1.0", s.f.model("p.bin", []byte("present already")))
	a := linkIdentity(t, s, "identity-a")
	a.acq.Want(m)
	a.awaitReady(t, m.PluginID)
	b := linkIdentity(t, s, "identity-b")
	b.a.noteActivated(m.PluginID, m)
	b.a.reconcileStoreRefs(t.Context(), map[string]bool{m.PluginID: true}, false, 2)
	if !slices.ContainsFunc(s.svc.Refs(), func(r machine.Ref) bool { return r.Slot == b.slot && r.Plugin == m.PluginID }) {
		t.Fatalf("an identity running present material does not refer to it: %+v", s.svc.Refs())
	}
	if n := s.f.count(); n != 1 {
		t.Fatalf("the reconciliation fetched: %d fetches in all, want 1", n)
	}
}

func TestInSAFEOnlyWhatTheStoreDoesNotHoldIsLetGo(t *testing.T) {
	s := startLinkedService(t)
	li := linkIdentity(t, s, "identity-a")
	present := pluginhost.ModelMaterialForTest(s.svc.Store(), "org.example.present", "0.1.0", s.f.model("here.bin", []byte("here")))
	gone := s.f.model("gone.bin", []byte("never served"))
	s.f.mu.Lock()
	delete(s.f.bodies, gone.URL)
	s.f.mu.Unlock()
	absent := pluginhost.ModelMaterialForTest(s.svc.Store(), "org.example.absent", "0.1.0", gone)
	li.acq.Want(present)
	li.awaitReady(t, present.PluginID)
	_ = li.a.needOverLink(t.Context(), absent, nil) // recorded, then its download ends
	c := li.a.machine.client.Load()
	voiceWait(t, "both references in the link's status", 10*time.Second, func() bool { return len(c.Snapshot().Refs) == 2 })
	li.a.mode.mode = ModeSafe
	li.a.reconcileStoreRefs(t.Context(), map[string]bool{present.PluginID: true, absent.PluginID: true}, true, 2)
	refs := s.svc.Refs()
	if !slices.ContainsFunc(refs, func(r machine.Ref) bool { return r.Plugin == present.PluginID }) {
		t.Errorf("SAFE let go of material that is present: %+v", refs)
	}
	if slices.ContainsFunc(refs, func(r machine.Ref) bool { return r.Plugin == absent.PluginID }) {
		t.Errorf("SAFE kept a reference to material the store does not hold: %+v", refs)
	}
}

func TestALiveDownloadStopsOnceNoIdentityRefersToIt(t *testing.T) {
	s := startLinkedService(t)
	began, gaveUp := make(chan struct{}, 4), make(chan struct{}, 4)
	s.f.mu.Lock()
	s.f.before = func(ctx context.Context) error {
		select {
		case began <- struct{}{}:
		default:
		}
		<-ctx.Done()
		select {
		case gaveUp <- struct{}{}:
		default:
		}
		return ctx.Err()
	}
	s.f.mu.Unlock()
	a, b := linkIdentity(t, s, "identity-a"), linkIdentity(t, s, "identity-b")
	m := pluginhost.ModelMaterialForTest(s.svc.Store(), "org.example.slow", "0.1.0", s.f.model("slow.bin", []byte("held in flight")))
	for _, li := range []*linkedIdentity{a, b} {
		_ = li.a.needOverLink(t.Context(), m, nil) // pending: recorded, its job started
		c := li.a.machine.client.Load()
		voiceWait(t, "the reference in the link's status", 10*time.Second, func() bool { return len(c.Snapshot().Refs) == 1 })
	}
	select {
	case <-began:
	case <-time.After(10 * time.Second):
		t.Fatal("the download never began")
	}
	a.a.reconcileStoreRefs(t.Context(), map[string]bool{}, false, 2)
	if refs := s.svc.Refs(); len(refs) != 1 || refs[0].Slot != b.slot {
		t.Fatalf("the uninstall in one identity: want the other's reference alone, got %+v", refs)
	}
	select {
	case <-gaveUp:
		t.Fatal("a download another identity still refers to was stopped by an uninstall")
	case <-time.After(300 * time.Millisecond):
	}
	b.a.mode.mode = ModeSafe
	b.a.reconcileStoreRefs(t.Context(), map[string]bool{m.PluginID: true}, true, 2)
	if refs := s.svc.Refs(); len(refs) != 0 {
		t.Fatalf("SAFE kept a reference to material the store does not hold: %+v", refs)
	}
	select {
	case <-gaveUp:
	case <-time.After(10 * time.Second):
		t.Fatal("a download no identity refers to any longer was not stopped")
	}
}

func TestSupersededEntriesKeepTheNewestRoots(t *testing.T) {
	store := plugstore.Store{Dir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(store.Dir, plugstore.DirRoots), 0o700); err != nil {
		t.Fatal(err)
	}
	hex64 := func(c string) string { return strings.Repeat(c, 64) }
	root := func(key, archive string, age time.Duration) string {
		raw, err := json.Marshal(plugstore.RootRecord{VariantID: "v", InventorySHA256: hex64("e"), ArchiveSHA256: archive,
			Entrypoint: "carrier", EntrypointSHA256: "sha256:" + hex64("f"), Files: 1, InstalledBytes: 1, Inventory: []byte("{}")})
		if err != nil {
			t.Fatal(err)
		}
		path, _ := store.RootRecord(key)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
		return "r/" + key
	}
	current := root("cccccccccccccccc", hex64("3"), 0)
	newer := root("bbbbbbbbbbbbbbbb", hex64("2"), time.Hour)
	older := root("aaaaaaaaaaaaaaaa", hex64("1"), 2*time.Hour)
	recorded := map[string]bool{current: true, newer: true, older: true, "a/" + hex64("3"): true, "a/" + hex64("2"): true, "a/" + hex64("1"): true, "v/0123456789abcdef": true}
	named := map[string]pluginhost.Material{current: {}, "a/" + hex64("3"): {}}
	got := superseded(store, recorded, named, []string{current}, 2)
	slices.Sort(got)
	want := []string{"a/" + hex64("1"), older, "v/0123456789abcdef"}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("superseded: %v, want %v", got, want)
	}
}

func TestAPluginActivatedAgainLetsGoOfWhatItsEarlierActivationRanFrom(t *testing.T) {
	store := t.TempDir()
	const id = "org.example.voice"
	before := pluginhost.ModelMaterialForTest(store, id, "0.1.0", pluginhost.ModelDecl{Name: "before.bin"})
	arriving := pluginhost.ModelMaterialForTest(store, id, "0.3.0", pluginhost.ModelDecl{Name: "arriving.bin"})
	after := pluginhost.ModelMaterialForTest(store, id, "0.2.0", pluginhost.ModelDecl{Name: "after.bin"})
	a := &App{}
	a.noteActivated(id, before)
	a.noteMaterial(arriving)
	a.noteActivated(id, after)
	a.noteActivated(id, after)
	recorded := map[string]bool{}
	for _, m := range []pluginhost.Material{before, arriving, after} {
		for _, e := range m.Entries() {
			recorded[e] = true
		}
	}
	a.machine.mu.Lock()
	named, serving := a.machine.needs[id], a.machine.serving[id]
	a.machine.mu.Unlock()
	got := superseded(plugstore.Store{Dir: store}, recorded, named, serving, 1)
	if want := before.Entries(); !slices.Equal(got, want) {
		t.Fatalf("after an activation replaced another, superseded is %v; want the earlier activation's %v alone", got, want)
	}
}

func TestThePluginStoreIsAPlaceTheHostKeeps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	// The account's service lies under this test's home (TestMain points
	// every other test at a directory of the binary's own).
	saved := accountMachineDir
	accountMachineDir = func() (string, error) { return filepath.Join(home, ".aii", "machine"), nil }
	t.Cleanup(func() { accountMachineDir = saved })
	slot := filepath.Join(home, ".aii", "identity-0")
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(slot, "data", "identity.sec"), LedgerPath: filepath.Join(slot, "data", "ledger.jsonl"), DBPath: filepath.Join(slot, "data", "aii.db")}
	find := func(c Config) *firewall.Rule {
		for _, o := range substrateObjects(c, slot) {
			if o.ID == "sub.store" {
				return o
			}
		}
		return nil
	}
	o := find(*cfg)
	if o == nil || o.Path != realPath(filepath.Join(home, ".aii", "plugins")) || !o.Enforced || !strings.Contains(o.Reason, "plugin store") {
		t.Fatalf("the account's plugin store is not a kept place: %+v", o)
	}
	s := startLinkedService(t)
	li := linkIdentity(t, s, "identity-a")
	placed := filepath.Join(t.TempDir(), "plugins")
	if _, err := li.a.machine.client.Load().PlaceStore(t.Context(), placed); err != nil {
		t.Fatal(err)
	}
	cfg.Machine.Dir = s.dir
	if o := find(*cfg); o == nil || o.Path != realPath(placed) {
		t.Fatalf("the placed store is not a kept place: %+v", o)
	}
}

// EVERY OTHER IDENTITY'S SLOT IS HIDDEN FROM THIS IDENTITY'S NATIVE PLUGINS
// (H6): read from the machine service's state whether it runs or not, each
// recorded slot but this identity's own is a masked place; a state that
// does not read refuses the native child instead of leaving one unmasked.
func TestEveryOtherRecordedIdentitysSlotIsHiddenFromNativeChildren(t *testing.T) {
	root := t.TempDir()
	own, other := filepath.Join(root, "identity-a"), filepath.Join(root, "identity-b")
	for _, d := range []string{own, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(t.TempDir(), "machine")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	state, _ := json.Marshal(map[string]any{"identities": []map[string]string{
		{"slot": own, "identity": "aa", "public_key": "k", "how": "registered", "at": "2026-10-09T00:00:00Z"},
		{"slot": other, "identity": "bb", "public_key": "k", "how": "registered", "at": "2026-10-09T00:00:00Z"},
	}})
	if err := os.WriteFile(filepath.Join(dir, "state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Machine.Dir = dir
	places, err := otherIdentitiesPlaces(*cfg, own)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 || places[0].Path != realPath(other) || places[0].ID != "sub.identity" || !places[0].Enforced {
		t.Fatalf("the places: %+v, want the other identity's slot alone", places)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := otherIdentitiesPlaces(*cfg, own); err == nil {
		t.Fatal("a state that does not read named no refusal: another identity's slot would be left unmasked")
	}
}

func TestASiblingSlotThatIsNotRecordedIsHiddenFromNativeChildren(t *testing.T) {
	root := t.TempDir()
	own, other := filepath.Join(root, "identity-0"), filepath.Join(root, "identity-1")
	for _, d := range []string{own, other, filepath.Join(root, "by-name"), filepath.Join(root, "notes")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "identity-7"), []byte("a file, not a slot"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine") // nothing recorded: no state at all
	places, err := otherIdentitiesPlaces(*cfg, own)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 || places[0].Path != realPath(other) || places[0].ID != "sub.identity" || !places[0].Enforced {
		t.Fatalf("the places: %+v, want the unrecorded sibling %s alone", places, other)
	}
}

// A SIBLING ON ANOTHER VOLUME, REACHED THROUGH A LINK, IS HIDDEN BY ITS
// REAL PATH: the wall compares physical paths, and a mask named by the
// link would hide nothing a child opens by the target.
func TestASymlinkedSiblingIsHiddenByItsRealPath(t *testing.T) {
	root := t.TempDir()
	own := filepath.Join(root, "identity-0")
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatal(err)
	}
	volume := filepath.Join(t.TempDir(), "volume", "slot")
	if err := os.MkdirAll(filepath.Join(volume, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(volume, filepath.Join(root, "identity-4")); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Machine.Dir = filepath.Join(t.TempDir(), "machine")
	places, err := otherIdentitiesPlaces(*cfg, own)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 || places[0].Path != realPath(volume) {
		t.Fatalf("the places: %+v, want the linked sibling by its real path %s", places, realPath(volume))
	}
	// The linked slot's own identity runs in it through the link: its own
	// slot is never one of its places, and its sibling is.
	places, err = otherIdentitiesPlaces(*cfg, filepath.Join(root, "identity-4"))
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 || places[0].Path != realPath(own) {
		t.Fatalf("from the linked slot the places are %+v, want its sibling %s alone", places, realPath(own))
	}
}
