package app

import (
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// .
// .
// .
// .
// .
func TestExpiredAdoptedCredentialIsNotReportedAsSignedOut(t *testing.T) {
	// .
	// .
	// .
	wrapped := wrapUnavailable(oauth.ErrOwnerRefreshRequired)

	if !errors.Is(wrapped, errCredentialUnavailable) {
		t.Fatal("the wrap must still satisfy errCredentialUnavailable, or the old branch stops matching")
	}
	if !errors.Is(wrapped, oauth.ErrOwnerRefreshRequired) {
		t.Fatal("the wrap must still satisfy ErrOwnerRefreshRequired, or expiry cannot be distinguished at all")
	}

	// .
	// .
	got := classifyCredentialErr(wrapped)
	if got != "credential_expired" {
		t.Fatalf("an expired adopted credential classified as %q, want %q", got, "credential_expired")
	}

	// .
	absent := wrapUnavailable(errors.New("no credential file"))
	if got := classifyCredentialErr(absent); got != "no_credential" {
		t.Fatalf("an absent credential classified as %q, want %q", got, "no_credential")
	}
}
