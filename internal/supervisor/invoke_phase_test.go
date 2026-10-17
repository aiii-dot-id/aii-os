package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A DEATH NOTICED WHILE INVOKING IS REPORTED IN THE INVOKE PHASE (T37
// follow-up). The child takes the request and exits without answering;
// the invocation's error is the typed exit, with its code and the phase
// it was read in — "invoke", never the start's word.
func TestAChildThatDiesWhileInvokedIsReportedInTheInvokePhase(t *testing.T) {
	s := mustStart(t, childSpec("die-on-request"), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := s.Invoke(ctx, []byte(testReq))
	var exit *ChildExitError
	if !errors.As(err, &exit) {
		t.Fatalf("a child that died with the request unanswered is a typed exit: %v", err)
	}
	if exit.Phase != "invoke" || exit.Code != 9 {
		t.Fatalf("the exit is read in the invoke phase with the child's code 9: phase %q code %d (%v)", exit.Phase, exit.Code, err)
	}
}
