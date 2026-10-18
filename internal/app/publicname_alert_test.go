package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
)

// THE PUBLIC NAME'S ALERT REACHES THE OPERATOR. publicname reports a
// certificate it could not obtain through its Alert wire, which is
// continuity's outbox notice. New builds both runtimes, and a wire bound
// as a method value captures a.continuity as it was when the wires were
// built: built in the other order, every certificate alert went to a nil
// runtime. This drives the real path, a Settings retry whose issuance
// fails, and reads the notice from the outbox.
func TestACertificateThatCannotBeObtainedReachesTheOperatorsOutbox(t *testing.T) {
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	app.pn.ManageForTest(&publicnametest.Manager{Fail: errors.New("the CA refused the order")})
	if _, err := app.pn.Retry(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		id := "maint_certificate_" + time.Now().UTC().Format("20060102")
		got, err := app.store.OutboxContent(id)
		if err == nil && strings.Contains(got, "[maintenance] certificate:") && strings.Contains(got, "the CA refused the order") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no certificate alert in the operator's outbox after 10 s: %q (%v)", got, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
