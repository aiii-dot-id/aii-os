package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func TestThePageIsToldOfThePackageTheUpdaterVerified(t *testing.T) {
	verified := updates.PackageUpdate{Version: "1.1.0", Path: "/data/updates/aii-os_1.1.0_amd64.deb"}
	if got := packageView(updates.StateSnapshot{AvailableVersion: "1.1.0", Package: verified}); got == nil || got.Path != verified.Path || got.Version != "1.1.0" || got.Refusal != "" {
		t.Fatalf("the verified package did not reach the page: %+v", got)
	}
	refused := updates.PackageUpdate{Version: "1.1.0", Refusal: "its signature did not verify"}
	if got := packageView(updates.StateSnapshot{AvailableVersion: "1.1.0", Package: refused}); got == nil || got.Path != "" || got.Refusal != refused.Refusal {
		t.Fatalf("the refusal did not reach the page: %+v", got)
	}
	if got := packageView(updates.StateSnapshot{AvailableVersion: "1.2.0", Package: verified}); got != nil {
		t.Fatalf("a package of another version was offered for the one announced: %+v", got)
	}
	if got := packageView(updates.StateSnapshot{AvailableVersion: "1.1.0"}); got != nil {
		t.Fatalf("an install the package manager did not make was told of a package: %+v", got)
	}
}
