package app

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

func TestExpiredAdoptedCredentialIsNotReportedAsSignedOut(t *testing.T) {
	// The shape providers.go classifies: ErrOwnerRefreshRequired wrapped
	// by errCredentialUnavailable, exactly as discoverMetaForEntry wraps
	// it before probeEntry sees it.
	wrapped := wrapUnavailable(oauth.ErrOwnerRefreshRequired)

	if !errors.Is(wrapped, errCredentialUnavailable) {
		t.Fatal("the wrap must still satisfy errCredentialUnavailable, or the old branch stops matching")
	}
	if !errors.Is(wrapped, oauth.ErrOwnerRefreshRequired) {
		t.Fatal("the wrap must still satisfy ErrOwnerRefreshRequired, or expiry cannot be distinguished at all")
	}

	// Order matters: the expired branch must be tested BEFORE the
	// generic unavailable branch, or expiry is swallowed by it.
	got := classifyCredentialErr(wrapped)
	if got != "credential_expired" {
		t.Fatalf("an expired adopted credential classified as %q, want %q", got, "credential_expired")
	}

	// A genuinely absent credential still reports as absent.
	absent := wrapUnavailable(errors.New("no credential file"))
	if got := classifyCredentialErr(absent); got != "no_credential" {
		t.Fatalf("an absent credential classified as %q, want %q", got, "no_credential")
	}
}

// wrapUnavailable mirrors how discoverMetaForEntry reports a credential
// failure, so classification can be exercised without a live provider.
func wrapUnavailable(err error) error {
	return fmt.Errorf("%w: %w", errCredentialUnavailable, err)
}
