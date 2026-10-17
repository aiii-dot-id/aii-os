package sections

import (
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// A DECLARATION THE HOST REFUSES WAITS ON THE PACKAGE. A malformed
// section.json refuses the same way on every attempt, but DeclError did not
// say so: the facility retried it on its clock (30 s to 10 min, forever)
// and the page promised a retry that could never succeed. Now it waits for
// different bytes, as every other declaration the host refuses does.
func TestAMalformedSectionDeclarationWaitsOnThePackage(t *testing.T) {
	for _, raw := range []string{`{`, `{"id":"Not An Id","slot":"rail","entry":"index.html"}`, `{"id":"ok","slot":"nowhere","entry":"index.html"}`} {
		_, err := ParseDecl([]byte(raw))
		var bound pluginfacility.PackageBound
		if !errors.As(err, &bound) || !bound.WaitsOnPackage() {
			t.Errorf("%s: %v does not wait on the package", raw, err)
		}
	}
}
