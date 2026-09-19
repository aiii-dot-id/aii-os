package app

import (
	"errors"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
// .
// .
// .
func TestTheCardIsRenderedFromTheFacilitySnapshot(t *testing.T) {
	at := time.Date(2030, 1, 1, 10, 20, 30, 0, time.UTC)
	ref := pluginfacility.NewRefusal("org.example.eng", "1.2.0", 7, pluginfacility.StageHealth, errors.New("the engine never answered"))
	ref.Class, ref.Remedy, ref.Evidence = pluginfacility.ClassTransient, "It is tried again on its own.", "probe: no reply in 10s"
	v := pluginfacility.InstanceView{ID: "org.example.eng", Version: "1.2.0", State: pluginfacility.StateUpdating, Since: at,
		Admission: "admitted (host 2.9 GB; 5.1 GB free after it)", Residue: []string{"child: child 48211 not yet reaped"}, RetryAt: at.Add(30 * time.Second),
		Activations: []pluginfacility.ActivationView{
			{Gen: 6, Role: pluginfacility.RoleActive, Version: "1.1.0", Since: at.Add(-time.Hour),
				Timings: map[pluginfacility.Stage]time.Duration{pluginfacility.StageVerify: 22 * time.Millisecond, pluginfacility.StageStart: 25500 * time.Millisecond}},
			{Gen: 7, Role: pluginfacility.RoleCandidate, Version: "1.2.0", Since: at, Refusal: ref},
		}}
	l := lifecycleView(v)
	if l.State != "updating" || l.Since != "2030-01-01T10:20:30Z" || l.Admission == "" || len(l.Residue) != 1 || l.RetryAt != "2030-01-01T10:21:00Z" {
		t.Fatalf("the instance's own facts: %+v", l)
	}
	if len(l.Activations) != 2 || l.Activations[0].Gen != 6 || l.Activations[0].Role != "active" || l.Activations[1].Role != "candidate" {
		t.Fatalf("every activation, oldest first: %+v", l.Activations)
	}
	if got := l.Activations[0].Timings; got["verify"] != 22 || got["start"] != 25500 {
		t.Fatalf("stage timings in milliseconds: %v", got)
	}
	// .
	rv := refusalView(ref)
	if rv == nil || rv.Stage != "health" || rv.Class != "transient" || rv.Cause != "the engine never answered" || rv.Remedy == "" || rv.Evidence == "" {
		t.Fatalf("class, remedy and evidence reach the card: %+v", rv)
	}
	a := &App{}
	a.pluginLife = map[string]pluginLifecycle{"org.example.eng": {version: "1.2.0", phase: lifeRefused, reason: refusalReason(ref), since: at,
		view: pluginfacility.InstanceView{Refusal: ref, Residue: v.Residue, RetryAt: v.RetryAt}}}
	views := a.pluginPendingViews()
	if len(views) != 1 || views[0].Refusal == nil || views[0].Refusal.Class != "transient" || len(views[0].Residue) != 1 || views[0].RetryAt != "2030-01-01T10:21:00Z" {
		t.Fatalf("the pending card carries the refusal, the residue and the retry: %+v", views)
	}
}
