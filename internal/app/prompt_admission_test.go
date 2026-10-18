package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

func TestAPromptComposedToItsBudgetIsAdmitted(t *testing.T) {
	// A large elastic Ring 4, shaped like a real one: short lines.
	standing := strings.Repeat("- the port is next; the reader waits on the review; the survey is parked\n", 260)
	// A long current message with the shape of a pasted document.
	doc := strings.Repeat("The lighthouse keeper logged the tide, the wind and the lamp oil, line after line.\n", 300)
	current := llm.Message{Role: "user", Content: doc}
	for _, tc := range []struct {
		name   string
		budget func(reserve, raw, wire int) int
	}{

		{"the wire's escaping", func(reserve, raw, wire int) int { return reserve + raw + 60 }},
		// The composed prompt fits with five tokens to spare on the wire
		// count: nothing folds, and every unreserved byte — the block's
		// frame — is a refusal.
		{"the block's frame", func(reserve, raw, wire int) int { return reserve + wire + 5 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh identity each time: with history the fit could yield
			// a turn instead of refusing, and the live case had none.
			app, _ := promptCacheApp(t)
			if err := app.store.SetStandingState(standing); err != nil {
				t.Fatal(err)
			}
			// The composed prompt with no pressure at all, to size the budget from.
			app.composer.SetMaxTokens(10_000_000)
			workState, err := app.buildWorkState()
			if err != nil {
				t.Fatal(err)
			}
			facts, err := app.buildTurnFacts(t.Context(), true)
			if err != nil {
				t.Fatal(err)
			}
			reserve, err := app.promptReserve(current, 0)
			if err != nil {
				t.Fatal(err)
			}
			p0, err := app.composer.ComposeTurn(workState, facts, "", reserve)
			if err != nil {
				t.Fatal(err)
			}
			raw := tokenestimate.Estimate(p0.Text) + tokenestimate.Estimate(p0.Turn)
			wire := tokenestimate.EstimateWire(p0.Text + "\n\n" + p0.Turn)
			budget := tc.budget(reserve, raw, wire)
			app.composer.SetMaxTokens(budget)
			app.promptGate.SetMaxTokens(budget)
			app.conv.SetModelLimits(budget, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := app.acquireTurn(ctx); err != nil {
				t.Fatal(err)
			}
			_, err = app.observeChat(ctx, doc, func(kind, name, args string) {}) // releases the turn itself
			var limit *llm.ContextLimitError
			if errors.As(err, &limit) {
				t.Fatalf("a prompt the composer fitted to %d tokens was refused at admission: %v", budget, err)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The turn contract and the platform note are composed into every system
// prompt, last, by the composer (SetClosing): priced with the rest, and the
// loop appends nothing to a composed prompt. A sub-agent's compose ends
// the same way.
func TestTheContractIsComposedIntoTheSystemText(t *testing.T) {
	app, _ := promptCacheApp(t)
	closing := conversation.SystemAdditions()
	workState, err := app.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	p, err := app.composer.ComposeTurn(workState, "### Rhythm — 1 turn", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(app.gatedSystem(p).Content, closing) {
		t.Fatalf("the turn's system text does not end with the contract: %.120q", app.gatedSystem(p).Content[len(app.gatedSystem(p).Content)-120:])
	}
	sub, err := app.composer.ComposeFolded(workState, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(sub.Text, closing) {
		t.Fatal("a sub-agent's composed system text does not end with the contract")
	}
}

// What the request carries beyond the composed prompt — the words, the
// block's frame with its history note, the tools — is the reserve.
func TestTheReserveCarriesTheBlockFrame(t *testing.T) {
	app, _ := promptCacheApp(t)
	current := llm.Message{Role: "user", Content: "hi"}
	reserve, err := app.promptReserve(current, 3)
	if err != nil {
		t.Fatal(err)
	}
	words, err := llm.EstimateInputTokens([]llm.Message{current}, app.buildToolDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	if frame := tokenestimate.EstimateWire(conversation.TurnBlockFrame(3, false)); reserve-words < frame {
		t.Fatalf("the reserve leaves %d tokens for a %d-token frame", reserve-words, frame)
	}

	seq, err := app.store.AddConversationTurnSeq("resident", "noted")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.AddReasoning(seq, []store.ReasoningCall{{Model: "m", Text: "a thought"}}); err != nil {
		t.Fatal(err)
	}
	with, err := app.promptReserve(current, 3)
	if err != nil {
		t.Fatal(err)
	}
	longer := tokenestimate.EstimateWire(conversation.TurnBlockFrame(3, true))
	if with-words < longer {
		t.Fatalf("with thinking recorded the reserve leaves %d tokens for a %d-token frame", with-words, longer)
	}
	if with <= reserve {
		t.Fatalf("the reserve did not grow with the block: %d, then %d", reserve, with)
	}
}
