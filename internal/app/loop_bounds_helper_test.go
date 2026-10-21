package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/limits"
)

// testLoop names, for a test that builds a loop of its own, each limit its
// configuration leaves unset, at the numbers the loop carried as its own
// defaults until it stopped holding any. The loop refuses a
// turn whose limits were not named; the app's own loops are given theirs
// from the settings table.
func testLoop(c conversation.Config) conversation.Config {
	c.MaxIterations = c.MaxIterations.Or(limits.Of(10))
	c.TurnTokenBudget = c.TurnTokenBudget.Or(limits.Of(600_000))
	c.MaxToolCalls = c.MaxToolCalls.Or(limits.Unlimited)
	c.Checkpoint.Calls = c.Checkpoint.Calls.Or(limits.Of(limits.DefaultCheckpointCalls))
	if c.Checkpoint.GraceRounds <= 0 {
		c.Checkpoint.GraceRounds = limits.DefaultGraceRounds
	}
	if c.Checkpoint.Slack <= 0 {
		c.Checkpoint.Slack = limits.DefaultSlack
	}
	if c.ToolSchemaRefusals <= 0 {
		c.ToolSchemaRefusals = limits.DefaultToolSchemaRefusals
	}
	if c.MaxToolResultChars <= 0 {
		c.MaxToolResultChars = limits.DefaultToolResultChars
	}
	return c
}

// fenceTurnAt holds the resident's next turns to a token fence of tokens,
// every other limit as the configuration has it.
func fenceTurnAt(t testing.TB, a *App, tokens int) {
	t.Helper()
	b := a.residentLimitsFor(a.configSnapshot()).residentBounds()
	b.TurnTokens = limits.Of(tokens)
	if err := a.conv.SetTurnBounds(b); err != nil {
		t.Fatal(err)
	}
}
