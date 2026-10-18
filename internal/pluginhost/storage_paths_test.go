package pluginhost

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestPluginStorageLinkedRootsKeepTheirTargetsAndCannotBeAdoptedTwice(t *testing.T) {
	const a, b = "id.example.linked", "id_example.linked"
	base := t.TempDir()
	external := filepath.Join(t.TempDir(), "models-é space")
	before := storageFixture(t, external, "external model or private files")
	old := filepath.Join(base, sanitizeToken(a))
	if err := atomicfile.LinkDir(external, old); err != nil {
		t.Fatal(err)
	}
	first, err := upgradePluginDir(t.Context(), base, a, func(context.Context) ([]string, error) { return []string{a}, nil })
	if err != nil {
		t.Fatal(err)
	}
	assertStorage(t, first, "external model or private files")
	after, err := os.Stat(filepath.Join(first, "state"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("external tree moved or changed: %v", err)
	}

	second, err := upgradePluginDir(t.Context(), base, b, func(context.Context) ([]string, error) { return []string{b}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(second, "state")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("new package adopted orphaned private state: %v", err)
	}
	assertStorage(t, external, "external model or private files")
}

func TestPluginStorageNativeWallKeepsOnlyTheOwnersCompatibilityPath(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("argv containment platforms")
	}
	skipWhereNativeWallCannotBeEstablished(t)
	const a, b = "id.example.native", "id_example.native"
	data := t.TempDir()
	base := filepath.Join(data, "private")
	legacy := filepath.Join(base, sanitizeToken(a))
	storageFixture(t, legacy, "incumbent")
	first, err := upgradePluginDir(t.Context(), base, a, func(context.Context) ([]string, error) { return []string{a}, nil })
	if err != nil {
		t.Fatal(err)
	}
	second, err := upgradePluginDir(t.Context(), base, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	storageFixture(t, second, "newcomer")
	for _, tc := range []struct{ id, path, want, old string }{{a, first, "incumbent", "incumbent"}, {b, second, "newcomer", ""}} {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		files := nativeFiles{places: []*firewall.Rule{nativePlace(data)}, private: tc.path, read: []string{pluginLegacyReadPath(base, tc.id, tc.path)}, own: standInTools}
		argv, wall, err := containArgv(ctx, []string{"/bin/sh", "-c", `test "$(cat "$1/state")" = "$3" || exit 20; value=$(cat "$2/state") || value=; test "$value" = "$4"`, "probe", tc.path, legacy, tc.want, tc.old}, nil, files)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		out, err := walled(t, exec.CommandContext(ctx, argv[0], argv[1:]...), wall).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("native directory upgrade for %s: %v %s", tc.id, err, out)
		}
	}
}

func storageFixture(t *testing.T, dir, value string) os.FileInfo {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func assertStorage(t *testing.T, dir, value string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "state"))
	if err != nil || string(got) != value {
		t.Fatalf("private state at %s: %q, %v", dir, got, err)
	}
}

func TestPluginStorageNamesDoNotCollapseVerifiedIDs(t *testing.T) {
	base := t.TempDir()
	seen := map[string]bool{}
	for _, id := range []string{"id.example.reader", "id_example.reader", "id.example_reader", "id_example_reader", "con", "con.example"} {
		path := pluginStorageDir(base, id)
		if seen[strings.ToLower(path)] {
			t.Fatalf("collapsed plugin ID %q", id)
		}
		seen[strings.ToLower(path)] = true
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("nonportable path for %s: %v", id, err)
		}
	}
}

func TestPluginStorageWaitsBeforeBothNewAllocationAndLegacyRename(t *testing.T) {
	const id = "id.example.boot"
	for _, legacy := range []bool{false, true} {
		base := t.TempDir()
		if legacy {
			storageFixture(t, filepath.Join(base, sanitizeToken(id)), "old reader still works")
		}
		pending := errors.New("recovery reader is still armed")
		opts := &Options{StorageUpgradeReady: func() error { return pending }, LegacyStorageOwners: func(context.Context) ([]string, error) { return []string{id}, nil }}
		if _, err := opts.upgradeStorageDir(t.Context(), base, id); !errors.Is(err, pending) {
			t.Fatalf("crossed unsettled recovery boundary: %v", err)
		}
		if _, err := os.Stat(pluginStorageDir(base, id)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("created new-format data before boot settled: %v", err)
		}
		if legacy {
			assertStorage(t, filepath.Join(base, sanitizeToken(id)), "old reader still works")
		}
		opts.StorageUpgradeReady = func() error { return nil }
		if _, err := opts.upgradeStorageDir(t.Context(), base, id); err != nil {
			t.Fatalf("normal retry did not recover: %v", err)
		}
	}
}

func TestPluginStorageRenamePreservesBytesAndResumes(t *testing.T) {
	const id = "id.example.reader"
	for _, boundary := range []string{"normal", "rename_refused", "after_rename", "destination_sync", "parent_sync"} {
		t.Run(boundary, func(t *testing.T) {
			base := t.TempDir()
			legacy := filepath.Join(base, "id_example_reader")
			before := storageFixture(t, legacy, "irreplaceable private state")
			injected := errors.New("injected storage interruption")
			rename := func(from, to string) error {
				if boundary == "rename_refused" {
					return injected
				}
				if err := atomicfile.Rename(from, to); err != nil {
					return err
				}
				if boundary == "after_rename" {
					return injected
				}
				return nil
			}
			syncDir := func(path string) error {
				if boundary == "destination_sync" && path != base || boundary == "parent_sync" && path == base {
					return injected
				}
				return atomicfile.SyncDir(path)
			}
			owners := func(context.Context) ([]string, error) { return []string{id}, nil }
			_, err := upgradePluginDirWith(t.Context(), base, id, owners, rename, syncDir)
			if boundary == "normal" && err != nil {
				t.Fatal(err)
			}
			if boundary != "normal" && !errors.Is(err, injected) {
				t.Fatalf("lost interruption: %v", err)
			}

			path, err := upgradePluginDir(t.Context(), base, id, owners)
			if err != nil {
				t.Fatal(err)
			}
			assertStorage(t, path, "irreplaceable private state")
			after, err := os.Stat(filepath.Join(path, "state"))
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("upgrade copied or recreated private bytes: %v", err)
			}
			oldReader, err := os.Stat(filepath.Join(legacy, "state"))
			if err != nil || !os.SameFile(before, oldReader) {
				t.Fatalf("legacy reader lost access to the same bytes: %v", err)
			}
			if err := os.WriteFile(filepath.Join(path, "next"), []byte("next operation"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := upgradePluginDir(t.Context(), base, id, nil); err != nil {
				t.Fatalf("already upgraded data needs no historical adoption again: %v", err)
			}
		})
	}
}

func TestPluginStorageNeverGuessesOrOverwritesLegacyOwnership(t *testing.T) {
	const a, b = "id.example.reader", "id_example.reader"
	for _, boundary := range []string{"no_inventory", "absent_id", "ambiguous", "both_locations"} {
		t.Run(boundary, func(t *testing.T) {
			base := t.TempDir()
			legacy := filepath.Join(base, "id_example_reader")
			storageFixture(t, legacy, "keep legacy state")
			var owners func(context.Context) ([]string, error)
			switch boundary {
			case "absent_id":
				owners = func(context.Context) ([]string, error) { return []string{b}, nil }
			case "ambiguous":
				owners = func(context.Context) ([]string, error) { return []string{a, b}, nil }
			case "both_locations":
				owners = func(context.Context) ([]string, error) { return []string{a}, nil }
				storageFixture(t, pluginStorageDir(base, a), "keep separate state")
			}
			_, err := upgradePluginDir(t.Context(), base, a, owners)
			var refused *StorageUpgradeError
			if !errors.As(err, &refused) {
				t.Fatalf("wanted typed refusal, got %v", err)
			}
			assertStorage(t, legacy, "keep legacy state")
			if boundary == "both_locations" {
				assertStorage(t, pluginStorageDir(base, a), "keep separate state")
			}
		})
	}
}

func TestPluginStorageCollidingNewcomerCannotTouchEstablishedData(t *testing.T) {
	const a, b = "id.example.reader", "id_example.reader"
	base := t.TempDir()
	storageFixture(t, filepath.Join(base, "id_example_reader"), "incumbent")
	first, err := upgradePluginDir(t.Context(), base, a, func(context.Context) ([]string, error) { return []string{a}, nil })
	if err != nil {
		t.Fatal(err)
	}
	second, err := upgradePluginDir(t.Context(), base, b, func(context.Context) ([]string, error) { return []string{a, b}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("newcomer shares the incumbent's directory")
	}
	if got := pluginLegacyReadPath(base, b, second); got != "" {
		t.Fatalf("newcomer was offered the incumbent's legacy path: %s", got)
	}
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := pluginLegacyReadPath(base, a, first); got != filepath.Join(realBase, "id_example_reader") {
		t.Fatalf("incumbent lost its old native path: %s", got)
	}
	if _, err := os.Stat(filepath.Join(second, "state")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("newcomer inherited another plugin's files: %v", err)
	}
	storageFixture(t, second, "newcomer")
	assertStorage(t, first, "incumbent")
	for _, id := range []string{a, b} {
		if _, err := upgradePluginDir(t.Context(), base, id, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginStorageConcurrentUpgradeAndCancellation(t *testing.T) {
	const id = "id.example.concurrent"
	base := t.TempDir()
	storageFixture(t, filepath.Join(base, sanitizeToken(id)), "keep")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := upgradePluginDir(ctx, base, id, func(context.Context) ([]string, error) { return []string{id}, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := upgradePluginDir(t.Context(), base, id, func(context.Context) ([]string, error) { return []string{id}, nil })
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertStorage(t, pluginStorageDir(base, id), "keep")
	for _, id := range []string{"../escape", "", "absolute/id", "id~fake"} {
		if _, err := upgradePluginDir(t.Context(), base, id, nil); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("invalid ID %q: %v", id, err)
		}
	}
}
