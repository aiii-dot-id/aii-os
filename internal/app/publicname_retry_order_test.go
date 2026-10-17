package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/publicname/publicnametest"
)

// A RETRY THAT FAILS AT ONCE STAYS FAILED. the retry used to
// write "claiming" AFTER starting the issuance goroutine — and the
// goroutine owns every transition from there. When it failed before the
// caller resumed, the caller's late "claiming" overwrote the "failing" it
// had just recorded: the name sat in claiming beside the very error that
// ended it, with no writer left to correct it, and Settings offered
// nothing to press. The public runner's race job hit it; on a developer's
// machine the goroutine almost never wins, so this holds the caller at
// the one instant that matters instead of hoping for the schedule.
func TestARetryThatFailsAtOnceIsNotOverwrittenByItsOwnCaller(t *testing.T) {
	dir := t.TempDir()
	pub := &publicnametest.Publisher{Zone: "example.test"}
	var managers []*publicnametest.Manager
	app := publicNameApp(t, dir, pub, &managers)
	defer stopApp(t, app)
	if _, err := app.pn.Claim(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicname.StatusIssued)
	managers[0].Mu.Lock()
	managers[0].Fail = context.DeadlineExceeded
	managers[0].Mu.Unlock()

	// The goroutine wins: it has failed and said so before the caller
	// takes another step.
	app.pn.RetryIssueStarted = func() { waitStatus(t, app, publicname.StatusFailing) }
	if _, err := app.pn.Retry(); err != nil {
		t.Fatal(err)
	}
	st := app.pn.State()
	if st.Status != publicname.StatusFailing || st.LastError == "" {
		t.Fatalf("the caller overwrote the failure its own goroutine had recorded: %+v", st)
	}
}
