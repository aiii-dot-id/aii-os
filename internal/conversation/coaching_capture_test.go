package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
// .
// .
// .
// .
func TestCoachingIsFixedWhenTheLoopIsBuilt(t *testing.T) {
	cfg := Config{HeuristicNudges: true}
	cap := &captureLLM{script: []llm.Response{textResp(multiStepReadOnlyAnnouncement()), textResp("Done — proceeding.")}}
	loop := New(cap, nil, nil, nil, nil, cfg)
	cfg.HeuristicNudges = false
	if _, err := loop.Run(context.Background(), "s", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatalf("loop: %v", err)
	}
	nudged := false
	for _, msgs := range cap.seen {
		for _, m := range msgs {
			if strings.Contains(m.Content, "Continue — take the step") {
				nudged = true
			}
		}
	}
	if !nudged {
		t.Fatal("the loop was built with coaching ON; a later change to the caller's config must not switch it off — only a rebuilt loop (a restart) does")
	}
}
