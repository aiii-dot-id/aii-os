package app

import (
	"context"
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestTheSettleHelperAsksAgainOnlyForWaits(t *testing.T) {
	cases := []struct {
		name  string
		view  pluginfacility.InstanceView
		retry bool
	}{{
		name:  "an active instance is not a refusal",
		view:  pluginfacility.InstanceView{ID: "a", State: pluginfacility.StateActive},
		retry: false,
	}, {
		name: "a transient refusal is the facility saying not yet",
		view: pluginfacility.InstanceView{ID: "b", State: pluginfacility.StateRefused,
			Refusal: &pluginfacility.Refusal{Stage: pluginfacility.StageReadiness, Class: pluginfacility.ClassTransient}},
		retry: true,
	}, {
		// .
		// .
		// .
		name: "a deadline cancellation is the machine, not a verdict",
		view: pluginfacility.InstanceView{ID: "c", State: pluginfacility.StateRefused,
			Refusal: &pluginfacility.Refusal{Stage: pluginfacility.StageCancelled,
				Class: pluginfacility.ClassPermanent, Cause: context.DeadlineExceeded}},
		retry: true,
	}, {
		name: "a withdrawal has nothing to ask again for",
		view: pluginfacility.InstanceView{ID: "d", State: pluginfacility.StateRefused,
			Refusal: &pluginfacility.Refusal{Stage: pluginfacility.StageCancelled,
				Class: pluginfacility.ClassPermanent, Cause: context.Canceled}},
		retry: false,
	}, {
		name: "a package that does not verify is an answer, not a wait",
		view: pluginfacility.InstanceView{ID: "e", State: pluginfacility.StateRefused,
			Refusal: &pluginfacility.Refusal{Stage: pluginfacility.StageVerify,
				Class: pluginfacility.ClassPermanent, Cause: errors.New("signature does not verify")}},
		retry: false,
	}, {
		name:  "a refusal with no detail is not retried on a guess",
		view:  pluginfacility.InstanceView{ID: "f", State: pluginfacility.StateRefused},
		retry: false,
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := retryableRefusal(c.view) != ""
			if got != c.retry {
				t.Fatalf("retryableRefusal = %v, want %v (%q)", got, c.retry, retryableRefusal(c.view))
			}
		})
	}
}

// .
// .
func TestARefusalIsReportedWithItsReason(t *testing.T) {
	a := &App{}
	if got := refusedNow(a); got != "" {
		t.Fatalf("an app with no facility reports %q, want nothing", got)
	}
}
