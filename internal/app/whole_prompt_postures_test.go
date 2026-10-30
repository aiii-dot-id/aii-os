package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

func postureShapes() []requestShape {
	var shapes []requestShape
	for _, p := range safeGoldenPostures() {
		about := map[string]string{
			"safe_runtime": "the operator's typed words to an identity that entered SAFE while running: the SAFE section first in the block",
			"safe_boot":    "the operator's typed words to a boot that stopped in SAFE: the SAFE posture in Ring 0's place",
		}[p.name]
		shapes = append(shapes, requestShape{
			name: p.name, about: about, tools: "safe",
			send: func(t *testing.T) sentRequest {
				a, dir := p.enter(t)
				obs := fixtureModel(t, a, answerNoted)
				operatorWords(t, a, "GOLDEN_SAFE_OPERATOR the typed words")
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		})
	}
	return append(shapes,
		requestShape{
			name:  "degraded_ring5_wait",
			about: "the operator's typed words to a start that waits for Ring 5 (DEGRADED): the waiting posture in Ring 0's place",
			tools: "safe",
			send: func(t *testing.T) sentRequest {
				// No Ring 5 server answers, and the start holds its first
				// ask off for an hour: it waits for the whole test
				// (TestAStartNoRing5ServerAnsweredIsDegradedAndCompletesWhenOneDoes's rig).
				prev := ring5RetryFirst
				t.Cleanup(func() { ring5RetryFirst = prev })
				ring5RetryFirst = time.Hour
				gate, _, _ := ring5Gate(t)
				dir, keyPath, ledgerPath, dbPath := recordedBy(t, "SafeGolden", nil)
				cfg := safebootConfig(t, dir, "SafeGolden", keyPath, ledgerPath, dbPath)
				cfg.Genesis.FirewallURL, cfg.Genesis.FetchAttempts, cfg.Genesis.Ring5RetrySeconds = gate, 1, 1
				a := New(cfg)
				if err := a.startLive(); err != nil {
					t.Fatalf("a start no Ring 5 server answered must hold its page: %v", err)
				}
				t.Cleanup(func() { stopApp(t, a) })
				if trigger, _, _ := a.safeCause(); trigger != safeRing5Unreached {
					t.Fatalf("the start is not waiting for Ring 5 (trigger %d)", trigger)
				}
				obs := fixtureModel(t, a, answerNoted)
				operatorWords(t, a, "GOLDEN_WAITING_OPERATOR the typed words")
				// The address of the test's Ring 5 server, where the posture
				// says what it asked: a port the system chose.
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir, more: func(s string) string {
					return strings.ReplaceAll(s, gate, "http://<ring5 server>")
				}}
			},
		},
		requestShape{
			name:   "degraded_witness",
			about:  "the operator's typed words while the witness is unreachable (DEGRADED): the request is turn_operator's, byte for byte",
			tools:  "resident",
			sameAs: "turn_operator",
			send: func(t *testing.T) sentRequest {
				a, dir := goldenTurnApp(t)
				for range 3 {
					a.witnessAttempt(errors.New("GOLDEN_WITNESS the witness did not answer"))
				}
				if a.currentMode() != ModeDegradedWitness {
					t.Fatalf("three failed witness probes left the mode %s", a.currentMode())
				}
				obs := fixtureModel(t, a, answerNoted)
				turnPathNamed(t, "operator").run(t, a) // as turn_operator runs it
				waitUntil(t, func() bool { return !a.TurnActive() }, "the turn to end")
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
		requestShape{
			name:  "pressured_operator",
			about: "the operator's typed words to the realistic identity in a window too small for its whole prompt: folds, receipts, Ring 2's evidence elided",
			tools: "resident_with_plugin",
			send: func(t *testing.T) sentRequest {
				a, dir := realisticApp(t)
				obs := fixtureModel(t, a, answerNoted)
				words := "GOLDEN_PRESSED what is the state of the build?"
				reserve, err := a.promptReserve(llm.Message{Role: "user", Content: words}, 0)
				if err != nil {
					t.Fatal(err)
				}
				workState, err := a.buildWorkState()
				if err != nil {
					t.Fatal(err)
				}
				facts, err := a.buildTurnFacts(t.Context(), true)
				if err != nil {
					t.Fatal(err)
				}
				unpressed, err := a.composer.ComposeTurn(workState, facts, "", reserve)
				if err != nil {
					t.Fatal(err)
				}
				budget := reserve + (tokenestimate.EstimateWire(unpressed.Text)+tokenestimate.EstimateWire(unpressed.Turn))*82/100
				a.composer.SetMaxTokens(budget)
				a.promptGate.SetMaxTokens(budget)
				a.conv.SetModelLimits(budget, 0)
				operatorWords(t, a, words)
				return sentRequest{body: onlyRequest(t, obs.snapshot()), dir: dir}
			},
		},
	)
}
