package packagefmt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// markGuard is an identity's record of accepted epochs, reduced to the
// guard's contract: the newest acceptance under each name, and every name
// an acceptance was recorded under, in order.
type markGuard struct {
	epochs   map[string]int64
	shas     map[string]string
	recorded []string
}

func newMarkGuard() *markGuard {
	return &markGuard{epochs: map[string]int64{}, shas: map[string]string{}}
}

func (g *markGuard) TrustEpochHighWater(mark string) (int64, string, bool, error) {
	e, ok := g.epochs[mark]
	return e, g.shas[mark], ok, nil
}

func (g *markGuard) AcceptTrustEpoch(mark string, epoch int64, sha string) error {
	g.epochs[mark], g.shas[mark] = epoch, sha
	g.recorded = append(g.recorded, mark)
	return nil
}

// certifiers are two publisher certifier roots: the one an identity
// trusts, and a plugin author's development root.
func certifiers(t *testing.T) (real, dev *packagetest.Role) {
	t.Helper()
	real, err := packagetest.NewRole("aiii_plugin_publisher_certifier_marktest", packagetest.KeyTypePublisherCertifier)
	if err != nil {
		t.Fatal(err)
	}
	dev, err = packagetest.NewRole("dev.certifier.marktest", packagetest.KeyTypePublisherCertifier)
	if err != nil {
		t.Fatal(err)
	}
	return real, dev
}

// loadCertifierList loads root's list at epoch under guard, with root
// pinned as the certifier, and returns the epoch the identity holds, or
// the reason it holds none.
func loadCertifierList(t *testing.T, root *packagetest.Role, epoch int64, guard packagefmt.EpochGuard) (int64, string) {
	t.Helper()
	list, err := root.SignRevocationStatus(epoch, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, packagetest.StatusFileCertifier), list, 0o644); err != nil {
		t.Fatal(err)
	}
	set := packagefmt.LoadRevocationStatus(dir, packagefmt.TrustRoots{PublisherCertifier: root.Env}, guard)
	if held, ok := set.Epoch(packagetest.KeyTypePublisherCertifier); ok {
		return held, ""
	}
	return 0, strings.Join(set.Describe(), "; ")
}

func TestOneRootsEpochsNeverJudgeAnothers(t *testing.T) {
	real, dev := certifiers(t)
	guard := newMarkGuard()
	if held, why := loadCertifierList(t, dev, 2, guard); held != 2 {
		t.Fatalf("the development root's list was not accepted: %s", why)
	}
	if want := []string{packagefmt.EpochMark(dev.Env)}; len(guard.recorded) != 1 || guard.recorded[0] != want[0] {
		t.Fatalf("the acceptance was recorded as %v, not under the root's own mark %v", guard.recorded, want)
	}
	if !strings.HasPrefix(guard.recorded[0], "plugin_publisher_certifier:sha256:") {
		t.Fatalf("the mark does not name the key type and the root's fingerprint: %q", guard.recorded[0])
	}
	if held, why := loadCertifierList(t, real, 1, guard); held != 1 {
		t.Fatalf("the development root's epoch judged the trusted certifier's list: %s", why)
	}
	if held, why := loadCertifierList(t, real, 3, guard); held != 3 {
		t.Fatalf("the trusted certifier's newer list was not accepted: %s", why)
	}
	if _, why := loadCertifierList(t, real, 1, guard); !strings.Contains(why, "ROLLBACK refused") {
		t.Fatalf("a root's own older list is not refused: %q", why)
	}
	if _, why := loadCertifierList(t, dev, 1, guard); !strings.Contains(why, "ROLLBACK refused") {
		t.Fatalf("the development root's own older list is not refused: %q", why)
	}
}

// A MARK RECORDED BEFORE THE MARK WAS PER ROOT IS READ AS IT WAS WRITTEN.
// Its event names the key type, and it was the mark of every root of that
// type: it still is, beside a root's own, the higher standing. A root's
// first newer list is recorded under its own mark, and the old one is
// left as it was. After a downgrade and an upgrade, an old mark above a
// root's own still stands.
func TestAMarkRecordedByTheKeyTypeJudgesAsItDid(t *testing.T) {
	real, dev := certifiers(t)
	guard := newMarkGuard()
	old := packagetest.KeyTypePublisherCertifier
	guard.epochs[old], guard.shas[old] = 2, "sha256:"+strings.Repeat("d", 64)

	if _, why := loadCertifierList(t, real, 1, guard); !strings.Contains(why, "ROLLBACK refused") {
		t.Fatalf("a mark an older host recorded no longer judges a root of its type: %q", why)
	}
	if held, why := loadCertifierList(t, real, 3, guard); held != 3 {
		t.Fatalf("a list above the old mark was not accepted: %s", why)
	}
	if len(guard.recorded) != 1 || guard.recorded[0] != packagefmt.EpochMark(real.Env) || guard.epochs[old] != 2 {
		t.Fatalf("the acceptance was not recorded under the root's own mark alone: %v, old mark %d", guard.recorded, guard.epochs[old])
	}
	if _, why := loadCertifierList(t, dev, 1, guard); !strings.Contains(why, "ROLLBACK refused") {
		t.Fatalf("the old mark stopped judging a root with no mark of its own: %q", why)
	}

	guard.epochs[old] = 5 // written by an older host after a downgrade
	if _, why := loadCertifierList(t, real, 4, guard); !strings.Contains(why, "below the ledgered high-water mark 5") {
		t.Fatalf("an old mark above the root's own did not stand: %q", why)
	}
}
