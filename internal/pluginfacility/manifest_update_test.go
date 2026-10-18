package pluginfacility

import (
	"context"
	"testing"
	"time"
)

func TestManifestOnlyReplacementUpdatesAtTheSamePath(t *testing.T) {
	for _, changed := range []bool{false, true} {
		got := planFor(t, func(i *Instance) {
			i.ManifestHash, i.Desired.ManifestHash = "manifest-new", "manifest-new"
			a := NewActivation(1, i.ID, "1.0.0", i.Package, i.PackageHash, time.Now())
			a.Role, a.ManifestHash = RoleActive, "manifest-new"
			if changed {
				a.ManifestHash = "manifest-old"
			}
			a.Intent = intentOf(i.ID, i.Package, i.PackageHash, a.ManifestHash, Policy{Revision: 1})
			i.Activations = append(i.Activations, a)
		})
		if changed {
			if len(got) != 1 || got[0] != string(cmdUpdate) {
				t.Fatalf("manifest changed at same path and payload: %v; want update", got)
			}
		} else if len(got) != 0 {
			t.Fatalf("unchanged manifest restarted: %v", got)
		}
	}
}

func TestManifestChangeFencesOldAttemptAndPreservesHeldUpdate(t *testing.T) {
	f := New(Config{Runtime: newSharedRuntime()})
	old := Observed{ID: "id.example.one", Package: "one.aiiospkg", Hash: "payload", ManifestHash: "old"}
	pol := Policy{Revision: 1}
	f.Observe([]Observed{old}, pol)
	i := f.instances[old.ID]
	a := NewActivation(1, i.ID, "1.0.0", i.Package, i.PackageHash, time.Now())
	a.Role, a.ManifestHash = RoleActive, old.ManifestHash
	a.Intent = intentOf(i.ID, i.Package, i.PackageHash, a.ManifestHash, pol)
	i.Activations = append(i.Activations, a)
	if !f.commit(i.ID, a.Gen) {
		t.Fatal("unchanged release is not current")
	}
	next := old
	next.ManifestHash = "new"
	f.Observe([]Observed{next}, pol)
	if f.commit(i.ID, a.Gen) {
		t.Fatal("old manifest attempt can still publish")
	}
	pol.Hold = true
	f.Observe([]Observed{next}, pol)
	if got := f.Snapshot().Instances[0].Held; got == "" {
		t.Fatal("a held manifest-only update disappeared from status")
	}
}

func TestManifestReplacedAfterDiscoveryNeverStartsUnderOldEvidence(t *testing.T) {
	rt := manifestRuntime{newFakeRuntime(), "new"}
	col := &collector{}
	e, _ := newTestExecutor(t, rt, col)
	c := newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
	c.manifestHash = "old"
	e.Send(c)
	waitDone(t, c, "superseded manifest")
	ref := col.last(EventRefused)
	if ref == nil || ref.Refusal.Stage != StageCancelled {
		t.Fatalf("changed manifest was not cancelled: %+v", ref)
	}
	if got := rt.seen(); len(got) != 1 || got[0] != "verify:p.aiiospkg" {
		t.Fatalf("a replacement ran under the old observation: %v", got)
	}
	// Re-observation supplies the new manifest; the same healthy bytes work.
	c = newCommand(cmdActivate, "p.aiiospkg", "sha256:aa")
	c.manifestHash = "new"
	e.Send(c)
	waitDone(t, c, "current manifest")
	if c.outcome != "active" {
		t.Fatalf("current release did not serve: %s", c.outcome)
	}
}

type manifestRuntime struct {
	*fakeRuntime
	manifest string
}

func (r manifestRuntime) Verify(ctx context.Context, path string) (Evidence, error) {
	ev, err := r.fakeRuntime.Verify(ctx, path)
	ev.ManifestHash = r.manifest
	return ev, err
}

func TestRescanKeepsTheVerifiedManifestWithTheObservation(t *testing.T) {
	rt := manifestRuntime{newFakeRuntime(), "manifest"}
	f := New(Config{Runtime: rt, Discover: func() Discovery {
		return Discovery{Found: []Found{{Dir: "plugins/p", Package: "p.aiiospkg", Size: 10, MTime: 1}}}
	}})
	f.Rescan(Policy{Revision: 1})
	i := f.instances["id.example.p"]
	if i == nil || i.ManifestHash != "manifest" || i.Desired.ManifestHash != "manifest" {
		t.Fatalf("verified manifest lost at observation: %+v", i)
	}
	// Policy-only rescans must carry the same release evidence too.
	f.cfg.Discover = nil
	f.Rescan(Policy{Revision: 2})
	if i.ManifestHash != "manifest" || i.Desired.ManifestHash != "manifest" {
		t.Fatalf("policy-only rescan lost the manifest: %+v", i)
	}
}
