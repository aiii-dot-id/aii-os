package certs

import (
	"testing"
	"time"
)

// .
// .
// .
// .
func TestLifetimesLeaveClockSkewMargin(t *testing.T) {
	const serviceClaimMax, servicePublishMax = 10 * time.Minute, 15 * time.Minute
	const margin = time.Minute
	if claimLifetime > serviceClaimMax-margin {
		t.Fatalf("claim lifetime %v leaves less than %v under the service's %v", claimLifetime, margin, serviceClaimMax)
	}
	if publishLifetime > servicePublishMax-margin {
		t.Fatalf("publish lifetime %v leaves less than %v under the service's %v", publishLifetime, margin, servicePublishMax)
	}
	if claimLifetime < 5*time.Minute || publishLifetime < 10*time.Minute {
		t.Fatalf("lifetimes too short for a validation round: claim %v publish %v", claimLifetime, publishLifetime)
	}
}
