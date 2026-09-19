package pluginfacility

import (
	"errors"
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestARefusalSaysWhetherWaitingWillHelp(t *testing.T) {
	for _, stage := range []Stage{StageVerify, StagePolicy, StageContain, StageRegister, StageCancelled} {
		if class, _ := Classify(stage); class != ClassPermanent {
			t.Errorf("%s must not be retried on a timer", stage)
		}
	}
	for _, stage := range []Stage{StageMaterial, StageStart, StageReadiness, StageHealth, StageCrashed, StagePanic} {
		if class, _ := Classify(stage); class != ClassTransient {
			t.Errorf("%s recovers on its own and must be retried", stage)
		}
	}
	// .
	// .
	verify := NewRefusal("id.example.p", "1.0.0", 3, StageVerify, errors.New("the signature does not verify"))
	if !verify.WaitsOn(InputPackage) || !verify.WaitsOn(InputTrust) {
		t.Fatalf("a verification refusal reconsiders on new bytes or a new snapshot: %+v", verify.WakeOn)
	}
	if verify.WaitsOn(InputConfig) {
		t.Fatal("a signature does not become valid because a setting changed")
	}
	// .
	// .
	readiness := NewRefusal("id.example.p", "1.0.0", 4, StageReadiness, errors.New("no readiness mark within 30s"))
	for _, in := range []Input{InputPackage, InputTrust, InputPolicy, InputConfig, InputHost} {
		if !readiness.WaitsOn(in) {
			t.Fatalf("a transient refusal waits on everything, missed %s", in)
		}
	}
}

func TestARefusalReadsAsOneSentenceAndKeepsBothFailures(t *testing.T) {
	cause := errors.New("no readiness mark within 30s; killed")
	r := NewRefusal("id.aiii.voice", "0.1.0-beta.1", 2, StageReadiness, cause)
	r.Remedy = "Raise plugins.resources.id.aiii.voice.startup_timeout_ms, or fix the engine."
	r.Evidence = "last phase: worker-started-awaiting-warm-inference at 23 ms"
	got := r.Error()
	for _, want := range []string{"id.aiii.voice", "0.1.0-beta.1", "readiness refused",
		"no readiness mark", "Raise plugins.resources", "worker-started-awaiting-warm-inference"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the sentence must carry %q: %s", want, got)
		}
	}
	if !errors.Is(r, cause) {
		t.Fatalf("the runtime's own error must stay reachable: %v", r)
	}

	// .
	// .
	r.Cleanup = errors.New("child 48211 not yet reaped")
	got = r.Error()
	if !strings.Contains(got, "no readiness mark") || !strings.Contains(got, "not yet reaped") {
		t.Fatalf("both failures must survive: %s", got)
	}
	if !errors.Is(r, cause) {
		t.Fatal("a cleanup failure must not displace the primary cause")
	}
}
