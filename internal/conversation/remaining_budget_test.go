package conversation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

type remainingBudgetTools struct {
	predicted int
	ordinal   int
}

func (d *remainingBudgetTools) Execute(_ context.Context, call llm.ToolCall) Observation {
	if call.Function.Name == "work" {
		var args struct {
			Steps int  `json:"steps"`
			Deny  bool `json:"deny"`
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return Observation{Text: err.Error(), Failed: true}
		}
		if args.Deny {
			return Observation{Text: "declaration refused", Failed: true, Refused: true}
		}
		if args.Steps > 0 {
			d.predicted, d.ordinal = args.Steps, call.EmissionOrdinal
		}
	}
	return Observation{Text: "ok"}
}

func TestRemainingBudgetCountsCallsAfterTheDeclaration(t *testing.T) {
	read := func() llm.ToolCall { return toolCall("r", "read", `{}`) }
	declare := func(args string) llm.ToolCall { return toolCall("w", "work", args) }
	for _, tt := range []struct {
		name        string
		script      []llm.Response
		ordinal     int
		checkpoints int
		calls       int
	}{
		{
			name: "same batch before and after declaration",
			script: []llm.Response{
				resp("", read(), read(), read(), declare(`{"steps":2}`), read()),
				resp("", read()),
				textResp("done"),
			},
			ordinal: 4, checkpoints: 1, calls: 6,
		},
		{
			name: "same estimate at a later position revises remaining work",
			script: []llm.Response{
				resp("", declare(`{"steps":3}`)),
				resp("", read(), read(), declare(`{"steps":3}`)),
				resp("", read(), read()),
				textResp("done"),
			},
			ordinal: 4, checkpoints: 0, calls: 6,
		},
		{
			name: "refused revision spends budget without resetting it",
			script: []llm.Response{
				resp("", declare(`{"steps":2}`)),
				resp("", read(), declare(`{"steps":100,"deny":true}`)),
				textResp("done"),
			},
			ordinal: 1, checkpoints: 1, calls: 3,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &captureLLM{script: tt.script}
			meter := &remainingBudgetTools{}
			loop := New(client, meter, &fakeDefs{}, nil, nil, testBounds(Config{
				MaxIterations:     limits.Of(60),
				PredictedThisTurn: func() (int, int) { return meter.predicted, meter.ordinal },
				Checkpoint:        Checkpoint{Calls: limits.Of(100), Slack: 1},
			}))
			result, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
			if err != nil {
				t.Fatal(err)
			}
			if meter.ordinal != tt.ordinal || result.ToolCallsUsed != tt.calls || checkpointCount(client) != tt.checkpoints {
				t.Fatalf("declaration=%d calls=%d checkpoints=%d; want %d, %d, %d", meter.ordinal, result.ToolCallsUsed, checkpointCount(client), tt.ordinal, tt.calls, tt.checkpoints)
			}
			if tt.checkpoints > 0 && !strings.Contains(joined(client.seen[len(client.seen)-1]), "have spent 2 since that declaration") {
				t.Fatal("checkpoint did not name the remaining-work window")
			}
		})
	}
}

func TestRemainingBudgetRevisionCannotExtendAnArmedGrace(t *testing.T) {
	client := &captureLLM{script: []llm.Response{
		resp("", toolCall("w", "work", `{"steps":1}`)),
		resp("", toolCall("r", "read", `{}`)),
		resp("", toolCall("w2", "work", `{"steps":100}`)),
		textResp("checkpointed"),
	}}
	meter := &remainingBudgetTools{}
	loop := New(client, meter, &fakeDefs{}, nil, nil, testBounds(Config{
		MaxIterations:     limits.Of(60),
		PredictedThisTurn: func() (int, int) { return meter.predicted, meter.ordinal },
		Checkpoint:        Checkpoint{Calls: limits.Of(100), Slack: 1, GraceRounds: 1},
	}))
	result, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ContinuedAtCap || checkpointCount(client) != 1 || len(client.seen) != 4 || result.ToolCallsUsed != 3 {
		t.Fatalf("revision extended the armed grace: %+v, requests=%d, checkpoints=%d", result, len(client.seen), checkpointCount(client))
	}
}

func TestInvalidRemainingBudgetPositionKeepsTheUndeclaredFloor(t *testing.T) {
	for _, ordinal := range []int{-1, 100} {
		client := &captureLLM{script: []llm.Response{
			resp("", toolCall("r1", "read", `{}`)),
			resp("", toolCall("r2", "read", `{}`)),
			textResp("checkpointed"),
		}}
		loop := New(client, &remainingBudgetTools{}, &fakeDefs{}, nil, nil, testBounds(Config{
			MaxIterations:     limits.Of(60),
			PredictedThisTurn: func() (int, int) { return 100, ordinal },
			Checkpoint:        Checkpoint{Calls: limits.Of(1), GraceRounds: 1},
		}))
		result, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
		if err != nil {
			t.Fatal(err)
		}
		if !result.ContinuedAtCap || checkpointCount(client) != 1 {
			t.Fatalf("invalid declaration position %d bypassed floor", ordinal)
		}
	}
}

// Five reads followed by steps=3 used to trigger a checkpoint immediately:
// all six calls were charged against an estimate explicitly for the rest of
// the turn. An estimate must travel with the position it measures from.
func TestRemainingBudgetDoesNotSpendEarlierCalls(t *testing.T) {
	var script []llm.Response
	for i := 0; i < 5; i++ {
		script = append(script, resp("", toolCall("read", "read", `{}`)))
	}
	script = append(script, resp("", toolCall("plan", "work", `{"action":"update","steps":3}`)))
	for i := 0; i < 3; i++ {
		script = append(script, resp("", toolCall("next", "shell", `{}`)))
	}
	script = append(script, textResp("done"))
	client := &captureLLM{script: script}
	meter := &remainingBudgetTools{}
	loop := New(client, meter, &fakeDefs{}, nil, nil, testBounds(Config{
		MaxIterations:     limits.Of(60),
		PredictedThisTurn: func() (int, int) { return meter.predicted, meter.ordinal },
	}))
	result, err := loop.Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if meter.ordinal != 6 || result.ToolCallsUsed != 9 {
		t.Fatalf("wrong fixture: declaration at %d, calls %d", meter.ordinal, result.ToolCallsUsed)
	}
	if checkpointCount(client) != 0 || result.ContinuedAtCap {
		t.Fatal("earlier reads spent the newly declared remaining budget")
	}
}
