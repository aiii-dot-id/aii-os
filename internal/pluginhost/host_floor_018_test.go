package pluginhost

import (
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/version"
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
func TestAFloorOf018AdmitsThisHostAndRefusesEvery017(t *testing.T) {
	if got := version.Authored(); !packagefmt.ValidHostBound(got) || packagefmt.CompareHostBounds(got, "0.1.8") < 0 {
		t.Fatalf("this host is authored as %q: a release that needs 0.1.8 would refuse it", got)
	}
	voice := &packagefmt.Manifest{ID: "id.aiii.voice", Version: "0.2.0", AiiosMinVersion: "0.1.8"}
	if err := checkHostWindow(voice, version.Authored()); err != nil {
		t.Fatalf("this host refuses a release authored for it: %v", err)
	}
	if err := checkHostWindow(voice, hostVersionFor(nil)); err != nil {
		t.Fatalf("the version the production path judges with refuses it: %v", err)
	}
	for _, older := range []string{"0.1.7", "0.1.6", "00.01.007"} {
		err := checkHostWindow(voice, older)
		var hv *HostVersionError
		if !errors.As(err, &hv) || !hv.HostTooOld || hv.Unknown {
			t.Fatalf("a host calling itself %s must be refused as too old, got: %v", older, err)
		}
		if msg := err.Error(); !strings.Contains(msg, "0.1.8 or newer") || !strings.Contains(msg, "update the host") {
			t.Errorf("the refusal does not say what is needed and what to do: %s", msg)
		}
	}
	// .
	// .
	entry := CatalogEntry{ID: "id.aiii.voice", Version: "0.2.0", AiiosMinVersion: "0.1.8"}
	if ok, why := entry.SupportedBy(version.Authored()); !ok {
		t.Errorf("the catalog withholds it from this host: %s", why)
	}
	if ok, why := entry.SupportedBy("0.1.7"); ok || !strings.Contains(why, "0.1.8") {
		t.Errorf("the catalog offers it to a 0.1.7 host (or does not say why not): ok=%v %q", ok, why)
	}
}
