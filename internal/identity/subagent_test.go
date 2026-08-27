package identity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSubagentRequestRoundTripAndValidation(t *testing.T) {
	want := SubagentRequest{
		Goal: "verify routing", Depth: 2, SessionID: "ws_test",
		ThinkingBudget: 100, Context: "folded", ParentBudget: 200,
		Role: "review",
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got SubagentRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("valid request refused: %v", err)
	}

	for name, request := range map[string]SubagentRequest{
		"goal":    {Depth: 1, SessionID: "ws", Context: "folded"},
		"session": {Goal: "x", Depth: 1, Context: "folded"},
		"depth":   {Goal: "x", SessionID: "ws", Context: "folded"},
		"context": {Goal: "x", Depth: 1, SessionID: "ws", Context: "summary"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := request.Validate(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("invalid %s request returned %v", name, err)
			}
		})
	}
}
