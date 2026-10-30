package pluginfacility

import (
	"errors"
	"testing"
)

// REFUSED FOR GOOD IS THE FACILITY'S OWN WORD, ON THE QUESTION ASKED NOW.
// A host that keeps an earlier release to fall back to (the application's
// kept release, h-plugin-admit-04) reads it from the view: a permanent
// refusal of the package present, under the policy in force. A transient
// refusal is tried again on its backoff, a cancellation is no answer, a
// policy refusal is the operator's word and not the release's, and a
// refusal under an earlier policy no longer answers.
func TestRefusedForGoodIsAPermanentRefusalOfThePackagePresentUnderThePolicyNow(t *testing.T) {
	const id = "id.example.kept"
	view := func(f *Facility) InstanceView {
		for _, v := range f.Snapshot().Instances {
			if v.ID == id {
				return v
			}
		}
		t.Fatal("no view of the instance")
		return InstanceView{}
	}
	pkg := Observed{ID: id, Dir: "plugins/" + id, Package: "plugins/" + id + "/" + id + "-2.aiiospkg", Hash: "h2", ManifestHash: "m2"}
	for gen, tc := range []struct {
		name  string
		stage Stage
		class Class
		want  bool
	}{
		{"permanent", StageStart, ClassPermanent, true},
		{"transient", StageHealth, ClassTransient, false},
		{"cancelled", StageCancelled, ClassPermanent, false},
		{"policy", StagePolicy, ClassPermanent, false},
	} {
		f := New(Config{})
		f.Observe([]Observed{pkg}, Policy{Revision: 1})
		g := Generation(gen + 1)
		f.record(Event{PluginID: id, Gen: g, Kind: EventStarted, Version: "2"})
		f.record(Event{PluginID: id, Gen: g, Kind: EventRefused, Version: "2",
			Refusal: &Refusal{PluginID: id, Version: "2", Gen: g, Stage: tc.stage, Class: tc.class, Cause: errors.New(tc.name)}})
		v := view(f)
		if v.RefusedForGood != tc.want || v.Package != pkg.Package {
			t.Fatalf("%s: refused for good = %v of %q, want %v of %q", tc.name, v.RefusedForGood, v.Package, tc.want, pkg.Package)
		}
		if tc.want {
			// The policy moved: the old answer no longer stands in the way.
			f.Observe([]Observed{pkg}, Policy{Revision: 2})
			if view(f).RefusedForGood {
				t.Fatalf("%s: a refusal under an earlier policy still reads as refused for good", tc.name)
			}
		}
	}
}
