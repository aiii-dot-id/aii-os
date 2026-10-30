package app

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// whole_prompt_facilities_test.go — what each facility of the
// unconscious sends, made by the facility the boot wired (wireCognitive)
// on the realistic identity, through the facility door and its preamble
// (ringAuthority), to the fake provider.

func facilityShapes() []requestShape {
	return []requestShape{
		{
			name: "facility_dream", about: "a DREAM pass over the realistic identity's raw experiences and its conversation",
			send: func(t *testing.T) sentRequest {
				a, dir := realisticApp(t)
				obs := fixtureModel(t, a, replyingWith(textReply("GOLDEN_DREAM you may be noticing a pattern")))
				if err := a.cognition.rhythm.Load().dream.Execute(t.Context()); err != nil {
					t.Fatal(err)
				}
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
		{
			name: "facility_consolidate", about: "a CONSOLIDATE metabolism pass, offered emit_consolidation for its envelope",
			tools: "consolidation",
			send: func(t *testing.T) sentRequest {
				a, dir := realisticApp(t)
				obs := fixtureModel(t, a, replyingWith(consolidationEnvelope))
				if err := a.cognition.consolidateFacility.Load().Execute(t.Context()); err != nil {
					t.Fatal(err)
				}
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
		{
			name: "facility_consolidate_view", about: "a CONSOLIDATE render-only pass: no raw experience waits, so the working truth is rendered again and nothing is minted",
			send: func(t *testing.T) sentRequest {
				a, dir := realisticApp(t)
				fixtureModel(t, a, replyingWith(consolidationEnvelope))
				consolidate := a.cognition.consolidateFacility.Load()
				// The metabolism pass consumes what waits; the next pass
				// finds nothing due and renders.
				if err := consolidate.Execute(t.Context()); err != nil {
					t.Fatal(err)
				}
				obs := fixtureModel(t, a, replyingWith(textReply("GOLDEN_VIEW you believe the build is close to green")))
				if err := consolidate.Execute(t.Context()); err != nil {
					t.Fatal(err)
				}
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
		{
			name: "facility_self_model", about: "a SELF_MODEL pass, offered commit narrowed to self_model.synthesize",
			tools: "self_model_commit",
			send: func(t *testing.T) sentRequest {
				a, dir := realisticApp(t)
				obs := fixtureModel(t, a, replyingWith(textReply("NO_CHANGE")))
				if err := a.cognition.rhythm.Load().selfModel.Execute(t.Context()); err != nil {
					t.Fatal(err)
				}
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
		{
			name: "facility_morning_brief", about: "a MORNING_BRIEF pass over the realistic identity's day",
			send: func(t *testing.T) sentRequest {
				a, dir := realisticApp(t)
				obs := fixtureModel(t, a, replyingWith(textReply("GOLDEN_BRIEF the build is patched and not yet proven")))
				if err := a.briefFacility.Execute(t.Context()); err != nil {
					t.Fatal(err)
				}
				// The brief states the time it is written at
				// (morning_brief.go: "Current time: …"): the clock.
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir, more: func(s string) string {
					return briefNowPattern.ReplaceAllString(s, "Current time: <now>")
				}}
			},
		},
		{
			name: "facility_outcomes", about: "the outcome intake: what became of an intention the identity completed",
			send: func(t *testing.T) sentRequest {
				a, dir := realisticApp(t)
				obs := fixtureModel(t, a, replyingWith(textReply("NOTHING SURFACED")))
				if err := a.cognition.consolidateFacility.Load().ObserveOutcomes(t.Context()); err != nil {
					t.Fatal(err)
				}
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
		{
			name: "facility_tool_outcomes", about: "the tool-outcome intake: the identity's own tool calls over a window, and what worked next",
			send: func(t *testing.T) sentRequest {
				a, dir := realisticApp(t)
				goldenToolSession(t, a)
				// The store reads only completed milliseconds. Advance
				// this fixture's clock past its just-delivered session.
				now := time.Now().Add(time.Millisecond)
				facility := a.cognition.consolidateFacility.Load()
				facility.SetClock(func() time.Time { return now })
				obs := fixtureModel(t, a, replyingWith(textReply("NOTHING SURFACED")))
				if err := facility.ObserveToolOutcomes(t.Context()); err != nil {
					t.Fatal(err)
				}
				// The window's bounds are the clock's.
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir, more: func(s string) string {
					return toolWindowPattern.ReplaceAllString(s, "recorded from <from> to <through> (UTC)")
				}}
			},
		},
	}
}

var briefNowPattern = regexp.MustCompile(`Current time: [^\n]*`)

var toolWindowPattern = regexp.MustCompile(`recorded from [^\n]+ to [^\n]+ \(UTC\)`)

// goldenToolSession is a delivered work session whose calls make a tool
// intake due: three calls the wall refused, then the call that worked.
func goldenToolSession(t *testing.T, a *App) {
	t.Helper()
	ctx := t.Context()
	const session = "ws_golden_tools"
	if err := a.store.StartWorkSession(ctx, session, "read the build log"); err != nil {
		t.Fatal(err)
	}
	call := func(n int, args, result string, outcome interaction.Outcome, reason string) {
		t.Helper()
		turn := fmt.Sprintf("turn_golden_tools_%d", n)
		if err := a.store.RecordToolStart(turn, 1, "main", "m", "", "shell", args); err != nil {
			t.Fatal(err)
		}
		if err := a.store.RecordToolDoneMeasured(turn, 1, "shell", args, result, outcome, false, store.ToolMeasurement{ReasonCode: reason}); err != nil {
			t.Fatal(err)
		}
	}
	refused := "Error: access denied: ../data/build.log leaves the home by a relative path (best-effort check; run under a container for a hard boundary)"
	for n := 1; n <= 3; n++ {
		call(n, `{"command":"cat ../data/build.log"}`, refused, interaction.Refused, "SHELL_WALL")
	}
	call(4, `{"command":"cat build.log"}`, "build ok\n[exit status 0]", interaction.Succeeded, "")
	if err := a.store.DeliverWorkSession(ctx, session, "served: read the build log", "", ""); err != nil {
		t.Fatal(err)
	}
}

// consolidationEnvelope is a metabolism pass's answer through its tool: no
// operation, and a view.
var consolidationEnvelope = callReply("", toolCall("call_consolidate_1", "emit_consolidation",
	`{"operations":[],"ring3_view":"GOLDEN_VIEW you believe the build is close to green"}`))
