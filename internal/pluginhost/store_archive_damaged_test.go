//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// A CACHED ARCHIVE THAT IS NO LONGER ITS DIGEST IS REPLACED. The store takes
// an archive as present by its name and size. One damaged where it lay (a
// write torn by a power loss, a sector that rotted) failed its extraction,
// was kept because it had not verified, and was found present again at
// every attempt: Try again repeated the same failure until someone deleted
// a file the message never named, and the plugin could not be installed.
func TestACachedArchiveThatIsNotItsDigestIsFetchedAgain(t *testing.T) {
	w := openStore(t)
	fx := newRuntimeFixture(t)
	fx.decl.VariantID = hostVariantID()
	work := rootWork(t, &fx.decl, fx.entry, packagefmt.TreeLimits{})

	// The archive under its name, the declared size, one byte wrong.
	path, err := w.Archive(fx.decl.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	damaged := bytes.Clone(fx.archive)
	damaged[len(damaged)/2] ^= 0x01
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, damaged, 0o400); err != nil {
		t.Fatal(err)
	}

	if err := EnsureEntry(context.Background(), w, work, fx.fetch, Adoption{}, nil); err != nil {
		t.Fatalf("A DAMAGED CACHED ARCHIVE WAS KEPT: the root could not be built, and every later attempt meets the same file: %v", err)
	}
	if fx.fetches != 1 {
		t.Fatalf("the archive was fetched %d times; want once, to replace the damaged copy", fx.fetches)
	}
	if !EntryPresent(w, work) {
		t.Fatal("the root is not published")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, fx.archive) {
		t.Fatalf("the archive under its name is not the declared bytes after the repair: %v", err)
	}
}

// A DOWNLOAD IS DURABLE BEFORE IT TAKES ITS NAME. The store takes a file
// under its name and size as present, so a rename that reached the disk
// ahead of the bytes would leave, after a power loss, an archive of the
// right name and size that is not its digest. The verified partial is
// synced while it is still a partial, and a sync that fails publishes
// nothing.
func TestADownloadedArchiveIsSyncedBeforeItTakesItsName(t *testing.T) {
	w := openStore(t)
	fx := newRuntimeFixture(t)
	fx.decl.VariantID = hostVariantID()
	work := rootWork(t, &fx.decl, fx.entry, packagefmt.TreeLimits{})
	path, err := w.Archive(fx.decl.SHA256)
	if err != nil {
		t.Fatal(err)
	}

	real := syncDownload
	t.Cleanup(func() { syncDownload = real })
	var synced []string
	named := false
	var fail error
	syncDownload = func(p string) error {
		synced = append(synced, p)
		if _, err := os.Lstat(path); err == nil {
			named = true
		}
		if fail != nil {
			return fail
		}
		return real(p)
	}

	fail = errors.New("the disk said no")
	if err := EnsureEntry(context.Background(), w, work, fx.fetch, Adoption{}, nil); err == nil {
		t.Fatal("a download whose sync failed was published")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a download whose sync failed took its name: %v", err)
	}

	fail = nil
	synced, named = nil, false
	if err := EnsureEntry(context.Background(), w, work, fx.fetch, Adoption{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(synced) != 1 || synced[0] != path+plugstore.PartialSuffix {
		t.Fatalf("the downloaded archive must be synced once, as its partial: %v", synced)
	}
	if named {
		t.Fatal("the archive had its name before its bytes were synced")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, fx.archive) {
		t.Fatalf("the archive under its name is not the declared bytes: %v", err)
	}
}
