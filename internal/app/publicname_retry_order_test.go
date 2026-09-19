package app

import (
	"context"
	"testing"
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
func TestARetryThatFailsAtOnceIsNotOverwrittenByItsOwnCaller(t *testing.T) {
	dir := t.TempDir()
	pub := &stubNamePublisher{zone: "example.test"}
	var managers []*stubManager
	app := publicNameApp(t, dir, pub, &managers)
	defer app.Stop()
	if _, err := app.claimPublicName(); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, app, publicNameIssued)
	managers[0].mu.Lock()
	managers[0].fail = context.DeadlineExceeded
	managers[0].mu.Unlock()

	prior := retryIssueStarted
	defer func() { retryIssueStarted = prior }()
	// .
	// .
	retryIssueStarted = func() { waitStatus(t, app, publicNameFailing) }
	if _, err := app.retryPublicCertificate(); err != nil {
		t.Fatal(err)
	}
	st := app.publicNameState()
	if st.Status != publicNameFailing || st.LastError == "" {
		t.Fatalf("the caller overwrote the failure its own goroutine had recorded: %+v", st)
	}
}
