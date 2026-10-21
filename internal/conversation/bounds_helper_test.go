package conversation

import "github.com/aiii-dot-id/aii-os/internal/limits"

// testBounds names, for a test, each limit its configuration leaves unset,
// at the numbers the loop carried as its own defaults until it stopped
// holding any: ten rounds, a 600,000-token fence, no call
// ceiling, the 32/4/2 checkpoint, three refused tool schemas and 32,000
// characters of a tool result. The tests that predate that were written
// against those numbers; the loop itself now refuses a turn whose limits
// were not named (TestTheLoopHasNoDefaultOfItsOwn).
func testBounds(c Config) Config {
	c.MaxIterations = c.MaxIterations.Or(limits.Of(10))
	c.TurnTokenBudget = c.TurnTokenBudget.Or(limits.Of(600_000))
	c.MaxToolCalls = c.MaxToolCalls.Or(limits.Unlimited)
	c.Checkpoint.Calls = c.Checkpoint.Calls.Or(limits.Of(32))
	if c.Checkpoint.GraceRounds <= 0 {
		c.Checkpoint.GraceRounds = 4
	}
	if c.Checkpoint.Slack <= 0 {
		c.Checkpoint.Slack = 2
	}
	if c.ToolSchemaRefusals <= 0 {
		c.ToolSchemaRefusals = 3
	}
	if c.MaxToolResultChars <= 0 {
		c.MaxToolResultChars = 32_000
	}
	return c
}
