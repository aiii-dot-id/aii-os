package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
)

// A PLUGIN CALL'S FAILURE CARRIES ITS CODE: a timeout, a trap or a
// resource limit, a reply off the response contract, and otherwise a fault.
func TestAPluginFailureIsCoded(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&pluginworker.TimeoutError{}, ReasonPluginTimeout},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), ReasonPluginTimeout},
		{&pluginworker.TrapError{Reason: "unreachable"}, ReasonPluginTrap},
		{&pluginworker.ResourceLimitError{}, ReasonPluginTrap},
		{&pluginhost.ResponseContractError{Requirement: "reply must be a JSON object"}, ReasonPluginResponseContract},
		{errors.New("something else"), ReasonPluginFault},
		// A STOPPED CALL KEEPS NO CODE (the SDK contract; the host,
		// 2026-11-07): the operator's or the turn's own cancel.
		{context.Canceled, ""},
		{fmt.Errorf("plugin: %w", context.Canceled), ""},
	} {
		if got := pluginFailureCode(c.err); got != c.want {
			t.Errorf("%T: %s, want %s", c.err, got, c.want)
		}
	}
}

// A REFUSAL AN ORGAN SAYS IS ONE IS RECORDED REFUSED (the host,
// 2026-11-07): an error carrying RefusedBeforeEffect and its own code.
type codedRefusal struct{ code string }

func (e *codedRefusal) Error() string             { return "refused: " + e.code }
func (e *codedRefusal) ReasonCode() string        { return e.code }
func (e *codedRefusal) RefusedBeforeEffect() bool { return true }

func TestAnOrgansRefusalIsRecordedRefusedUnderItsCode(t *testing.T) {
	var refusal interface{ RefusedBeforeEffect() bool }
	err := fmt.Errorf("work: %w", &codedRefusal{code: "backup.spaced"})
	if !errors.As(err, &refusal) || !refusal.RefusedBeforeEffect() {
		t.Fatal("the refusal is not seen through the wrap")
	}
}
