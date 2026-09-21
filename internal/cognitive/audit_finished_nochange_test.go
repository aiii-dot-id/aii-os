package cognitive

import (
	"context"
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestAuditUnfinishedNoChangeIsNotSuccess(t *testing.T) {
	for _, reason := range []string{"stop", "length", "refusal", "pause_turn"} {
		t.Run(reason, func(t *testing.T) {
			s := &SelfModelFacility{}
			err := s.applyResponse(context.Background(), &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "NO_CHANGE"}, FinishReason: reason}}}, nil)
			if reason == "stop" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var incomplete *llm.IncompleteResponseError
			if !errors.As(err, &incomplete) {
				t.Fatalf("unfinished NO_CHANGE accepted or misclassified: %v", err)
			}
		})
	}
}
