//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func TestRuntimeDeclarationRefusesTypedWhereItCannotBeMet(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fx := newRuntimeFixture(t)
	const id = "org.example.rtdecl"
	pkg := nativePackage(t, id, child, role, runtimesJSON(t, fx))

	// This host has no plugin store: refused before anything runs.
	var missing *RuntimeMissingError
	if _, err := Activate(ctx, pkg, newRegistry(t), supervisedOpts(Options{Roots: roots})); !errors.As(err, &missing) || !strings.Contains(err.Error(), "has no plugin store") {
		t.Fatalf("a host without a plugin store refuses typed: %v", err)
	}
	// A store whose writer has no download path, and nothing in it: the
	// honest offline refusal, and nothing published.
	offline := supervisedOpts(Options{Roots: roots})
	w := useStore(t, offline, nil)
	if _, err := Activate(ctx, pkg, newRegistry(t), offline); !errors.As(err, &missing) || !errors.Is(err, errNoDownloadPath) {
		t.Fatalf("offline refuses typed: %v", err)
	}
	if n := storeRoots(t, w); n != 0 {
		t.Fatalf("a refusal publishes nothing: %d roots", n)
	}
	// A declaration the host does not honor is a typed RuntimeError.
	bad := nativePackage(t, "org.example.rtbad", child, role, []byte(`{"runtimes":[]}`))
	var rerr *RuntimeError
	if _, err := Activate(ctx, bad, newRegistry(t), offline); !errors.As(err, &rerr) {
		t.Fatalf("an empty declaration refuses typed: %v", err)
	}
}

func TestRuntimeRootIsPublishedPinnedLaunchedFromAndReleased(t *testing.T) {
	skipWhereNativeIsRefused(t)
	// This proof LAUNCHES the child, so it needs a host that can build
	// the namespaces — not merely one that can describe them. The
	// refusal path below catches a host whose containment fails while
	// Activate is still running; a host whose bwrap dies on spawn fails
	// after Activate has returned, and only this asks about that.
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const id = "org.example.rtlaunch"
	fx := newRuntimeFixtureWith(t, "# one\n")
	pkg := nativePackage(t, id, child, role, runtimesJSON(t, fx))
	capture := logsink.CaptureForTest(t)
	// The identity's own archive from before the store, under the old
	// spelling of its directory: the store's writer adopts it by digest,
	// offline, and the tree is published from it with no download. The
	// later release below exercises fetching.
	slot := t.TempDir()
	cache := filepath.Join(slot, sanitizeToken(id), archivesDir)
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, fx.decl.SHA256+archiveSuffix), fx.archive, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := supervisedOpts(Options{Roots: roots, AdoptRuntimeDir: slot})
	fetch := ModelFetcher(nil)
	store := useStore(t, opts, func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		if fetch == nil {
			return 0, &FetchStatusError{Status: 404}
		}
		return fetch(ctx, url, offset, w)
	})
	opts.NativePlaces = func() ([]*firewall.Rule, error) { return []*firewall.Rule{nativePlace(store.Dir)}, nil }
	root, _ := store.Root(plugstore.RootKey(hostVariantID(), fx.decl.InventorySHA256, "child", digestOf(child)))
	pinned := func(root string) bool {
		h, err := store.HoldExclusive(plugstore.Entry{Kind: plugstore.DirRoots, ID: filepath.Base(root)})
		if err == nil {
			h.Close()
		}
		return errors.Is(err, plugstore.ErrPinned)
	}

	ap, err := Activate(ctx, pkg, newRegistry(t), opts)
	if err != nil {
		// A host that cannot contain a native child refuses the launch
		// AFTER the tree is published: the root stays for the next
		// attempt, and the pin the activation took is released.
		if !strings.Contains(err.Error(), "native lane unavailable") && !strings.Contains(err.Error(), "cannot contain") {
			t.Fatalf("activation: %v", err)
		}
		if _, serr := os.Stat(filepath.Join(root, "child")); serr != nil {
			t.Fatalf("the tree is published before the launch is attempted: %v", serr)
		}
		if pinned(root) {
			t.Fatal("a failed launch releases its pin")
		}
		t.Skipf("the launch itself needs a host that can contain a native child: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = ap.Deactivate(ctx)
		}
	}()
	if ap.RuntimeRoot != root {
		t.Fatalf("the activation runs from %q, want %q", ap.RuntimeRoot, root)
	}
	if ap.artifactDir != "" {
		t.Fatalf("no extracted artifact exists beside a runtime root: %q", ap.artifactDir)
	}
	if !pinned(root) || fx.fetches != 0 {
		t.Fatalf("pinned, and adopted with no download: pinned %v fetches %d", pinned(root), fx.fetches)
	}
	// The child runs from the tree and sees it. It reports the root %q-quoted,
	// so a Windows path arrives with its backslashes doubled.
	if !waitUntil(func() bool {
		return regexp.MustCompile(`plugins\.session: plugin org\.example\.rtlaunch: fakechild: child-ready .*runtime_root=` + regexp.QuoteMeta(strconv.Quote(root))).MatchString(capture.String())
	}) {
		t.Fatalf("timed out waiting for the child to report runtime root %q; the child's own log:\n%s", root, capture.String())
	}
	if !regexp.MustCompile(`plugins\.start: plugin org\.example\.rtlaunch: native child .*; runtime root ` + regexp.QuoteMeta(root) + `; image `).MatchString(capture.String()) {
		t.Fatalf("the containment line names the root:\n%s", capture.String())
	}
	resp, err := ap.sup.Invoke(ctx, []byte(`{"jsonrpc":"2.0","id":"h1","method":"invoke.call","params":{}}`))
	if err != nil || !strings.Contains(string(resp), `"answered":true`) {
		t.Fatalf("the child answers from its root: %v %s", err, resp)
	}
	// Tampering the tree after publication: the next spawn is refused
	// typed by the per-spawn check, never re-executed.
	if err := os.WriteFile(filepath.Join(root, "engine", "session.py"), []byte("print('evil')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = ap.sup.KillChild()
	pollUntil(t, "tamper stop", func() bool {
		state, _ := ap.sup.State()
		return state == supervisor.StateStopped
	})
	_, reason := ap.sup.State()
	var refused *supervisor.SpawnRefusedError
	if !errors.As(reason, &refused) || !strings.Contains(reason.Error(), "session.py") {
		t.Fatalf("a tampered tree stops the supervisor typed, naming the file: %v", reason)
	}
	// Deactivation releases the pin; the root outlives the activation.
	if err := ap.Deactivate(ctx); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	released = true
	if pinned(root) {
		t.Fatal("deactivated, the root is still pinned")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the root stays for the next activation: %v", err)
	}
	// A release with a new tree, fetched: the new root pinned; the older
	// one is not the activation's to remove — the store's collector judges
	// it by every identity's references (the machine service).
	fx2 := newRuntimeFixtureWith(t, "# two\n")
	pkg2 := nativePackage(t, id, child, role, runtimesJSON(t, fx2))
	fetch = fx2.fetch
	ap2, err := Activate(ctx, pkg2, newRegistry(t), opts)
	if err != nil {
		t.Fatalf("the second release activates: %v", err)
	}
	defer ap2.Deactivate(ctx)
	root2, _ := store.Root(plugstore.RootKey(hostVariantID(), fx2.decl.InventorySHA256, "child", digestOf(child)))
	if ap2.RuntimeRoot != root2 || !pinned(root2) || fx2.fetches != 1 {
		t.Fatalf("the second activation runs pinned from %q: %q pinned %v fetches %d", root2, ap2.RuntimeRoot, pinned(root2), fx2.fetches)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("an activation removed the older root from the store: %v", err)
	}
}
