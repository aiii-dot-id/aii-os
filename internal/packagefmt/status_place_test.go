package packagefmt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func TestPlaceStatusKeepsTheNewestVerifiedList(t *testing.T) {
	f := fixtures(t)
	dir := filepath.Join(t.TempDir(), "trust")
	onDisk := func() []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, statusFilePlatform))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	if epoch, placed, err := PlaceStatus(dir, f.roots, statusFilePlatform, f.statusPlatform); err != nil || !placed || epoch != 1 {
		t.Fatalf("a list where none is: epoch %d, placed %v, %v", epoch, placed, err)
	}
	if epoch, placed, err := PlaceStatus(dir, f.roots, statusFilePlatform, f.statusRevokePlatform); err != nil || !placed || epoch != 2 {
		t.Fatalf("a newer list over an older one: epoch %d, placed %v, %v", epoch, placed, err)
	}
	if !bytes.Equal(onDisk(), f.statusRevokePlatform) {
		t.Fatal("the newer list is not what is on disk")
	}
	for _, raw := range [][]byte{f.statusPlatform, f.statusRevokePlatform} {
		if _, placed, err := PlaceStatus(dir, f.roots, statusFilePlatform, raw); err != nil || placed {
			t.Fatalf("a list of a lower or the same epoch was written: placed %v, %v", placed, err)
		}
	}
	if !bytes.Equal(onDisk(), f.statusRevokePlatform) {
		t.Fatal("an older list took the file backwards")
	}
	if set := LoadRevocationStatus(dir, f.roots, newFakeGuard()); func() bool { e, ok := set.Epoch(keyTypePlatformRelease); return !ok || e != 2 }() {
		t.Fatalf("what was placed does not load: %v", set.Describe())
	}

	// Verified as at load: another root's list under this name, and a list
	// of this root signed by another key, are refused and write nothing.
	if _, placed, err := PlaceStatus(dir, f.roots, statusFileReviewer, f.statusPlatform); err == nil || placed {
		t.Fatalf("a list under another root's name was placed: %v, %v", placed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, statusFileReviewer)); !os.IsNotExist(err) {
		t.Fatalf("a refused list left a file: %v", err)
	}
	if _, _, err := PlaceStatus(dir, f.roots, "elsewhere.json", f.statusPlatform); err == nil {
		t.Fatal("a name that is no status file was accepted")
	}

	// Present and broken stays the operator's to mend, and is said.
	if err := writeFileForTest(dir, statusFileCertifier, "{ not a snapshot"); err != nil {
		t.Fatal(err)
	}
	if _, placed, err := PlaceStatus(dir, f.roots, statusFileCertifier, f.statusEpoch3); placed || err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("a broken file was replaced, or silently left: placed %v, %v", placed, err)
	}
}

func TestANewerShippedListReplacesAnOlderFile(t *testing.T) {
	f := fixtures(t)
	shipped := func(d RevocationDomain) (*sigenvelope.PublicKeyEnvelope, []byte) {
		if d.RootKeyType == keyTypePlatformRelease {
			return f.platform, f.statusRevokePlatform
		}
		return nil, nil
	}
	dir := t.TempDir()
	if err := writeFileForTest(dir, statusFilePlatform, string(f.statusPlatform)); err != nil {
		t.Fatal(err)
	}
	if wrote, err := placeFrom(dir, f.roots, false, shipped); err != nil || len(wrote) != 0 {
		t.Fatalf("at start a file that is there was replaced: %v, %v", wrote, err)
	}
	wrote, err := placeFrom(dir, f.roots, true, shipped)
	if err != nil || len(wrote) != 1 || wrote[0] != statusFilePlatform {
		t.Fatalf("a settled start did not place the newer shipped list: %v, %v", wrote, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, statusFilePlatform)); !bytes.Equal(raw, f.statusRevokePlatform) {
		t.Fatal("the newer shipped list is not on disk")
	}
	// On the real shipped roots there is nothing older to replace: a
	// directory the start provisioned is left as it is.
	provisioned := filepath.Join(t.TempDir(), "trust")
	if _, err := ProvisionShippedStatus(provisioned, ShippedRoots()); err != nil {
		t.Fatal(err)
	}
	if again, err := PlaceShippedStatus(provisioned, ShippedRoots()); err != nil || len(again) != 0 {
		t.Fatalf("the shipped lists replaced themselves: %v, %v", again, err)
	}
}
