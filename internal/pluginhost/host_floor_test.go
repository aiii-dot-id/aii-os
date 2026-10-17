package pluginhost

import (
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

func TestAFloorAdmitsThisHostAndRefusesEveryOlderOne(t *testing.T) {
	for _, f := range []struct {
		floor string
		// A release authored for the floor.
		id, release string
		// Hosts below it, each refused as too old.
		older []string
		// The host just below, which the catalog must not offer it to.
		below string
	}{
		// 0.1.8 IS THE VERSION THAT HAS THESE CAPABILITIES. Two different
		// hosts both called themselves 0.1.7: one that ends a microphone
		// when the ENGINE ends its input — the completion descriptor, the
		// status carrier, the reason the operator reads — and one that
		// does not. A voice release that needs the first could not
		// exclude the second. So the capability has a number of its own.
		// "00.01.007" is 0.1.7 too: the comparison reads numbers.
		{floor: "0.1.8", id: "id.aiii.voice", release: "0.2.0", older: []string{"0.1.7", "0.1.6", "00.01.007"}, below: "0.1.7"},
		// 0.1.9 IS THE VERSION THAT READS choices_from AND TAKES A PASTED
		// KEY. A 0.1.8 host refuses a settings declaration that names
		// choices_from (the declaration is read strictly), so a release
		// that looks its choices up must say it needs 0.1.9.
		{floor: "0.1.9", id: "id.aiii.codequality", release: "0.1.1", older: []string{"0.1.8", "0.1.7"}, below: "0.1.8"},

		{floor: "0.1.10", id: "id.aiii.codequality", release: "0.1.2", older: []string{"0.1.9", "0.1.1"}, below: "0.1.9"},

		{floor: "0.1.11", id: "com.aiii.examples.gmail", release: "0.1.0", older: []string{"0.1.10", "0.1.9"}, below: "0.1.10"},
	} {
		t.Run(f.floor, func(t *testing.T) {
			if got := version.Authored(); !packagefmt.ValidHostBound(got) || packagefmt.CompareHostBounds(got, f.floor) < 0 {
				t.Fatalf("this host is authored as %q: a release that needs %s would refuse it", got, f.floor)
			}
			m := &packagefmt.Manifest{ID: f.id, Version: f.release, AiiosMinVersion: f.floor}
			if err := checkHostWindow(m, version.Authored()); err != nil {
				t.Fatalf("this host refuses a release authored for it: %v", err)
			}
			if err := checkHostWindow(m, hostVersionFor(nil)); err != nil {
				t.Fatalf("the version the production path judges with refuses it: %v", err)
			}
			for _, older := range f.older {
				err := checkHostWindow(m, older)
				var hv *HostVersionError
				if !errors.As(err, &hv) || !hv.HostTooOld || hv.Unknown {
					t.Fatalf("a host calling itself %s must be refused as too old, got: %v", older, err)
				}
				if msg := err.Error(); !strings.Contains(msg, f.floor+" or newer") || !strings.Contains(msg, "update the host") {
					t.Errorf("the refusal does not say what is needed and what to do: %s", msg)
				}
			}
			entry := plugincatalog.CatalogEntry{ID: f.id, Version: f.release, AiiosMinVersion: f.floor}
			if ok, why := entry.SupportedBy(version.Authored()); !ok {
				t.Errorf("the catalog withholds it from this host: %s", why)
			}
			if ok, why := entry.SupportedBy(f.below); ok || !strings.Contains(why, f.floor) {
				t.Errorf("the catalog offers it to a %s host (or does not say why not): ok=%v %q", f.below, ok, why)
			}
		})
	}
}
