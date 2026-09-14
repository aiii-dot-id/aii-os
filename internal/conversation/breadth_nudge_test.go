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
// .
func multiStepReadOnlyAnnouncement() string {
	return "I'll survey the seams now. 1. Let me read the store layer. 2. Let me check the verb surface. Next let me list the render sites."
}

// .
// .
func coupledAnnouncement() string {
	return "Two things first. 1. Let me read the config source. Then, based on what I find, let me verify the defaults."
}

func runBreadthLoop(t *testing.T, reply string, cfg Config) ([]llm.Message, int) {
	t.Helper()
	cap := &captureLLM{script: []llm.Response{
		textResp(reply),
		textResp("Done — proceeding."),
	}}
	loop := New(cap, nil, nil, nil, nil, cfg)
	if _, err := loop.Run(context.Background(), "s", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatalf("loop: %v", err)
	}
	nudges := 0
	var last []llm.Message
	for _, msgs := range cap.seen {
		for _, m := range msgs {
			if strings.Contains(m.Content, "Continue — take the step") {
				nudges++
				last = msgs
			}
		}
	}
	return last, nudges
}

func TestBreadthNudgeFiresOnIndependentReadOnlyAnnouncement(t *testing.T) {
	msgs, nudges := runBreadthLoop(t, multiStepReadOnlyAnnouncement(), Config{HeuristicNudges: true})
	if nudges == 0 {
		t.Fatal("breadth announcement drew no nudge")
	}
	var breadth bool
	for _, m := range msgs {
		if strings.Contains(m.Content, "work spawn") {
			breadth = true
		}
	}
	if !breadth {
		t.Fatal("nudge fired but taught the serial step, not the fan-out")
	}
}

func TestBreadthNudgeSuppressedOnCoupling(t *testing.T) {
	msgs, nudges := runBreadthLoop(t, coupledAnnouncement(), Config{HeuristicNudges: true})
	if nudges == 0 {
		t.Fatal("announced intent drew no nudge at all — the serial nudge must still fire")
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "work spawn") {
			t.Fatal("coupled steps drew the FAN-OUT nudge — coupling markers must suppress it")
		}
	}
}

func TestBreadthNudgeSilentOnGoodbyeClosers(t *testing.T) {
	closer := "Let me know if you need anything else. 1. I will read your notes. 2. I will check them twice."
	_, nudges := runBreadthLoop(t, closer, Config{HeuristicNudges: true})
	if nudges != 0 {
		t.Fatalf("goodbye-class closer drew %d nudges — endsInAnnouncedIntent must reject it first", nudges)
	}
}

func TestBreadthNudgeAtMostOncePerTurn(t *testing.T) {
	c := &captureLLM{script: []llm.Response{
		textResp(multiStepReadOnlyAnnouncement()),
		textResp("Wait — 1. Let me read A. 2. Let me check B."),
		textResp("Done."),
	}}
	loop := New(c, nil, nil, nil, nil, Config{HeuristicNudges: true})
	if _, err := loop.Run(context.Background(), "s", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatalf("loop: %v", err)
	}
	nudges := 0
	for _, msgs := range c.seen {
		for _, m := range msgs {
			nudges += strings.Count(m.Content, "Continue — take the step")
		}
	}
	if nudges > 1 {
		t.Fatalf("nudged %d times in one turn — one is the cap", nudges)
	}
}

func TestBreadthNudgeGateOffKeepsSerial(t *testing.T) {
	off := false
	msgs, nudges := runBreadthLoop(t, multiStepReadOnlyAnnouncement(), Config{HeuristicNudges: true, BreadthNudge: &off})
	if nudges == 0 {
		t.Fatal("gate off must still allow the SERIAL nudge")
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "work spawn") {
			t.Fatal("gate off yet the fan-out nudge fired")
		}
	}
}
