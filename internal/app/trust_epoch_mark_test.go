package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestTheEpochMarksReplayAsWrittenAndAreThenPerRoot(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	st := newAdmissionStore(t, dir)
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st}
	door.onIntegrity = func(_ safeTrigger, err error) { t.Fatalf("an append entered SAFE: %v", err) }
	guard := trustEpochGuard{door: door, st: st}

	oldSHA := "sha256:" + strings.Repeat("d", 64)
	if _, err := door.Append(ledger.EventTrustEpochAccepted, 0,
		store.TrustEpochPayload{Root: packagefmt.KeyTypePublisherCertifier, TrustEpoch: 2, PayloadSHA256: oldSHA}, ""); err != nil {
		t.Fatal(err)
	}
	real, err := packagetest.NewRole("aiii_plugin_publisher_certifier_replaytest", packagetest.KeyTypePublisherCertifier)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := packagetest.NewRole("dev.certifier.replaytest", packagetest.KeyTypePublisherCertifier)
	if err != nil {
		t.Fatal(err)
	}
	lists := map[string][]byte{}
	list := func(r *packagetest.Role, epoch int64) []byte {
		key := fmt.Sprintf("%s@%d", r.KeyID, epoch)
		if lists[key] == nil {
			raw, err := r.SignRevocationStatus(epoch, nil)
			if err != nil {
				t.Fatal(err)
			}
			lists[key] = raw
		}
		return lists[key]
	}
	load := func(g packagefmt.EpochGuard, r *packagetest.Role, epoch int64) string {
		t.Helper()
		trust := t.TempDir()
		if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFileCertifier), list(r, epoch), 0o644); err != nil {
			t.Fatal(err)
		}
		set := packagefmt.LoadRevocationStatus(trust, packagefmt.TrustRoots{PublisherCertifier: r.Env}, g)
		if _, ok := set.Epoch(packagefmt.KeyTypePublisherCertifier); ok {
			return "held"
		}
		return strings.Join(set.Describe(), "; ")
	}
	steps := []struct {
		root  *packagetest.Role
		epoch int64
		want  string
	}{
		{real, 1, "ROLLBACK refused"},
		{real, 3, "held"},
		{dev, 4, "held"},
		{real, 3, "held"},
		{dev, 3, "ROLLBACK refused"},
		{real, 2, "ROLLBACK refused"},
	}
	for i, s := range steps {
		if got := load(guard, s.root, s.epoch); !strings.Contains(got, s.want) {
			t.Fatalf("step %d, %s at epoch %d: %q, want %q", i+1, s.root.KeyID, s.epoch, got, s.want)
		}
	}
	marks := []string{packagefmt.KeyTypePublisherCertifier, packagefmt.EpochMark(real.Env), packagefmt.EpochMark(dev.Env)}
	type mark struct {
		epoch int64
		sha   string
		ok    bool
	}
	read := func(s *store.Store) []mark {
		t.Helper()
		var out []mark
		for _, name := range marks {
			e, sha, ok, err := s.TrustEpochHighWater(name)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, mark{e, sha, ok})
		}
		return out
	}
	before := read(st)
	if want := []int64{2, 3, 4}; before[0].epoch != want[0] || before[0].sha != oldSHA || before[1].epoch != want[1] || before[2].epoch != want[2] {
		t.Fatalf("the marks recorded are %+v, want epochs %v under %v", before, want, marks)
	}

	replayed, err := store.NewForTest(filepath.Join(dir, "replayed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer replayed.Close()
	if err := replayed.ReplayFromFile(filepath.Join(dir, "ledger.jsonl")); err != nil {
		t.Fatal(err)
	}
	if after := read(replayed); len(after) != len(before) || after[0] != before[0] || after[1] != before[1] || after[2] != before[2] {
		t.Fatalf("replay changed the marks: %+v, before %+v", after, before)
	}
	frozen := frozenGuard{replayed}
	for i, s := range steps[3:] {
		if got := load(frozen, s.root, s.epoch); !strings.Contains(got, s.want) {
			t.Fatalf("after replay, step %d, %s at epoch %d: %q, want %q", i+4, s.root.KeyID, s.epoch, got, s.want)
		}
	}
	if got := load(frozen, real, 1); !strings.Contains(got, "ROLLBACK refused") {
		t.Fatalf("after replay the old mark no longer judges: %q", got)
	}
}

// frozenGuard reads a projection and records nothing: what a replayed
// record says, asked again.
type frozenGuard struct{ st *store.Store }

func (g frozenGuard) TrustEpochHighWater(mark string) (int64, string, bool, error) {
	return g.st.TrustEpochHighWater(mark)
}

func (frozenGuard) AcceptTrustEpoch(string, int64, string) error {
	return os.ErrPermission
}
