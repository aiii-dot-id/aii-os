package broker

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func TestSameReleaseActivationsOwnSeparateTemporaryFiles(t *testing.T) {
	data := t.TempDir()
	persistent := filepath.Join(data, "p")
	if err := os.MkdirAll(persistent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(persistent, "legacy"), []byte("persistent"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHost(t, newStore(t), Config{})
	release := Release{Version: "1.0.0", PackageHash: "same-release"}
	old := h.BindRelease("p", packagefmt.TierT0, []string{capFSPrivate}, release)
	next := h.BindRelease("p", packagefmt.TierT0, []string{capFSPrivate}, release)
	old.SetFiles(persistent, 0)
	next.SetFiles(persistent, 0)
	if old.activation == next.activation || old.privateDir == next.privateDir {
		t.Fatal("same release reused activation ownership")
	}
	for _, b := range []*Binding{old, next} {
		wantResult(t, dispatch(t, b, fsParams(opFSRead, PrivateRoot, "legacy", "")), statusFailed, reasonFSNotFound)
		wantResult(t, dispatch(t, b, fsParams(opFSWrite, PrivateRoot, "state", `{"data":"temporary"}`)), statusSucceeded, "")
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	wantResult(t, dispatch(t, old, fsParams(opFSRead, PrivateRoot, "state", "")), statusSucceeded, "")
	if err := old.ClearTempScope(); err != nil {
		t.Fatal(err)
	}
	wantResult(t, dispatch(t, old, fsParams(opFSRead, PrivateRoot, "state", "")), statusFailed, reasonFSNotFound)
	// A publisher-proven successor retains its existing plugin directory.
	proven := h.BindRelease("p", packagefmt.TierT1, []string{capFSPrivate}, release)
	proven.SetFiles(persistent, 0)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	wantResult(t, dispatch(t, proven, fsParams(opFSRead, PrivateRoot, "legacy", "")), statusSucceeded, "")
	if err := proven.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(persistent, "legacy")); err != nil || string(got) != "persistent" {
		t.Fatalf("persistent files changed: %q %v", got, err)
	}
}

func TestCloseDrainsAdmittedStorageAndRefusesLateDispatch(t *testing.T) {
	st := newStore(t)
	entered, release := make(chan struct{}), make(chan struct{})
	h := newHost(t, st, Config{InSAFE: func() bool {
		// Files check SAFE after storage admission and before writing.
		close(entered)
		<-release
		return false
	}})
	b := h.Bind("p", packagefmt.TierT0, []string{capFSPrivate})
	b.SetFiles(filepath.Join(t.TempDir(), "p"), 0)
	if err := st.PluginKVPut(b.storageScope(), "temp", "value", true, 10, 100); err != nil {
		t.Fatal(err)
	}
	callDone := make(chan error, 1)
	go func() {
		_, err := b.Dispatch(context.Background(), methodInvokeCall, []byte(fsParams(opFSWrite, PrivateRoot, "state", `{"data":"finished"}`)))
		callDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("storage dispatch never reached admission")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	select {
	case <-b.lifetime.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("close did not cancel activation storage")
	}
	select {
	case err := <-closeDone:
		t.Fatalf("close finished before admitted storage: %v", err)
	default:
	}
	wantErrorReason(t, dispatch(t, b, fsParams(opFSWrite, PrivateRoot, "late", `{"data":"forbidden"}`)), reasonPolicyDeny)
	if _, found, err := st.PluginKVGet(b.storageScope(), "temp"); err != nil || !found {
		t.Fatalf("cleanup ran while storage was in flight: %v %v", found, err)
	}
	close(release)
	for _, done := range []chan error{callDone, closeDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("storage retirement did not finish")
		}
	}
	if _, found, err := st.PluginKVGet(b.storageScope(), "temp"); err != nil || found {
		t.Fatalf("retired scope retained temporary data: %v %v", found, err)
	}
	if _, err := os.Stat(b.privateDir); !os.IsNotExist(err) {
		t.Fatalf("in-flight storage resurrected temporary files after cleanup: %v", err)
	}
}

func TestCanceledCloseRetainsCleanupForRetry(t *testing.T) {
	st := newStore(t)
	b := newHost(t, st, Config{}).Bind("p", packagefmt.TierT0, nil)
	ctx, finish, admitted := b.admitStorage(context.Background())
	if !admitted {
		t.Fatal("fixture was not admitted")
	}
	if err := st.PluginKVPut(b.storageScope(), "state", "pending", true, 10, 100); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.CloseContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("close cancellation: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("close did not propagate storage cancellation")
	}
	if _, found, err := st.PluginKVGet(b.storageScope(), "state"); err != nil || !found {
		t.Fatalf("canceled drain cleaned in-flight state: %v %v", found, err)
	}
	finish()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.PluginKVGet(b.storageScope(), "state"); err != nil || found {
		t.Fatalf("retry missed state: %v %v", found, err)
	}
}

func TestTemporaryFileCleanupFailureCanBeRetried(t *testing.T) {
	data := t.TempDir()
	b := newHost(t, newStore(t), Config{}).Bind("p", packagefmt.TierT0, []string{capFSPrivate})
	b.SetFiles(filepath.Join(data, "p"), 0)
	wantResult(t, dispatch(t, b, fsParams(opFSWrite, PrivateRoot, "state", `{"data":"temporary"}`)), statusSucceeded, "")
	parent := filepath.Join(data, tempFilesParent)
	if err := os.Rename(parent, parent+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(parent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err == nil {
		t.Fatal("cleanup failure was swallowed")
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent+"-saved", parent); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.privateDir); !os.IsNotExist(err) {
		t.Fatalf("retry retained temporary directory: %v", err)
	}
}

func TestColdFileCleanupOnlyRemovesLabelledActivations(t *testing.T) {
	data := t.TempDir()
	legacy := filepath.Join(data, "p")
	orphan := filepath.Join(data, tempFilesParent, rand.Text()+rand.Text())
	for _, dir := range []string{legacy, orphan} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := ClearOrphanTempFiles(data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "state")); err != nil {
		t.Fatalf("legacy data lost: %v", err)
	}
}

func TestTemporaryParentRejectsSymlinkAndIdentitySwap(t *testing.T) {
	data := t.TempDir()
	persistent := filepath.Join(data, "p")
	if err := os.Mkdir(persistent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(persistent, "state"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(data, tempFilesParent)
	if err := os.Symlink(persistent, parent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := ClearOrphanTempFiles(data); err == nil {
		t.Fatal("symlinked temporary parent accepted")
	}
	b := newHost(t, newStore(t), Config{}).Bind("p", packagefmt.TierT0, []string{capFSPrivate})
	b.SetFiles(persistent, 0)
	wantResult(t, dispatch(t, b, fsParams(opFSWrite, PrivateRoot, "state", `{"data":"forbidden"}`)), statusFailed, reasonFSIOFailed)
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(data)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	checked, err := root.Lstat(tempFilesParent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, parent+"-saved"); err != nil {
		t.Fatal(err)
	}
	// A relative target stays inside os.Root; only identity validation can
	// reject this swap, rather than the separate root escape protection.
	if err := os.Symlink("p", parent); err != nil {
		t.Fatal(err)
	}
	opened, err := openCheckedDirectory(root, tempFilesParent, checked)
	if err == nil {
		opened.Close()
		t.Fatal("directory swap redirected the anchored root")
	}
	if got, err := os.ReadFile(filepath.Join(persistent, "state")); err != nil || string(got) != "keep" {
		t.Fatalf("persistent data changed: %q %v", got, err)
	}
}
