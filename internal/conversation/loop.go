// Package conversation implements the resident's voice path: the loop
// that carries one turn from composed prompt through LLM, tool calls,
// and spoken reply.
//
// This is where every lived moment flows. It was extracted from app.go
// (the god-file had the system's most intricate logic in its only
// untested package — structure review 2026-08-16, finding: test
// placement inversion). The loop is pure conversation logic: no store,
// no engine, no dashboard — everything else arrives through narrow,
// consumer-side ports.
//
// Honesty properties this package owns (each pinned by a test):
//   - Everything the model says aloud across the loop reaches the
//     operator (spoken accumulation — the 1108-char/32-char bug).
//   - The truncation banner tells the model the result lives in the
//     transcript; the transcript port is called with the full result
//     BEFORE any truncation, and the banner's retention number comes
//     FROM the transcript (it states what the recorder actually keeps).
//   - The loop survives its own tools: context guard force-stops before
//     the model window blows (the find / ×10 → 524k tokens incident).
package conversation

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"runtime"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

// LLMClient is the model boundary.
type LLMClient interface {
	Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error)
}

// ToolExecutor runs one tool call (physical tool or identity verb) and
// returns the result text (never an error — errors are result text).
type ToolExecutor interface {
	Execute(ctx context.Context, call llm.ToolCall) string
}

// ToolDefiner supplies the function-calling definitions for this turn.
type ToolDefiner interface {
	ToolDefinitions() []llm.ToolDefinition
}

// Transcript is the durable record. The excerpt limit is the transcript's
// own property — the truncation banner states what the recorder keeps,
// so the recorder is the one who says.
type Transcript interface {
	RecordToolEvent(tool, args, result string) error
	TranscriptResultExcerptLimit() int
}

// Emitter streams tool calls to a live observer (nil = none).
type Emitter interface {
	EmitToolEvent(kind, name, args string)
}

// Steering carries operator words that arrived AFTER this turn began
// (nil = a running turn cannot be reached, which is where this started).
// Drain empties as it reads: a steer delivered twice would be the
// operator saying it twice, and they did not.
type Steering interface {
	DrainSteering() []string
}

// Config bounds the loop. R6: numeric bounds come from config, never code
// — the defaults here are fallbacks for unset config values only.
type Config struct {
	MaxIterations       int // default 10
	MaxToolResultChars  int // chars fed back to the model per result (default 32,000)
	ContextBudgetTokens int // prompt-token allowance that scales the guard (default 32,000)
	ThinkingBudget      int
	// TurnTokenBudget fences the turn's total spend across every call
	// (agency.turn_token_budget). Zero falls to the default below —
	// there is no unlimited setting, only a raised fence.
	TurnTokenBudget int
}

func (c Config) withDefaults() Config {
	if c.MaxIterations <= 0 {
		c.MaxIterations = 10
	}
	if c.MaxToolResultChars <= 0 {
		c.MaxToolResultChars = 32_000
	}
	if c.ContextBudgetTokens <= 0 {
		c.ContextBudgetTokens = 32_000
	}
	if c.TurnTokenBudget <= 0 {
		c.TurnTokenBudget = 600_000
	}
	return c
}

// Result is one turn's outcome.
type Result struct {
	Spoken    string // everything the model said aloud across the loop ("" if nothing)
	FinalText string // the last model text (verbatim, for verb parsing)
	ModelID   string // model that produced FinalText
	Usage     TurnUsage
}

// TurnUsage is what one turn actually cost, summed across EVERY provider
// call the loop made — tool rounds, the nudge, the pressure answer and
// the final call all draw on the same turn.
//
// This is the unit that matters and the one nothing measured. A turn may
// make thirty calls, each admitted against the same per-request ceiling,
// so a per-call limit bounds no total at all. The provider reports each
// call; only the loop can add them up.
//
// Silent counts calls that returned no usage. When it is non-zero the
// totals are a LOWER BOUND, and Complete() says so — an unknown cost
// must never read as a small one.
type TurnUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// CachedPromptTokens is the part of PromptTokens served from cache —
	// the same window, a tenth of the price.
	CachedPromptTokens int
	Calls              int
	Silent             int
}

// Complete reports whether every call in the turn told us what it cost.
func (t TurnUsage) Complete() bool { return t.Calls > 0 && t.Silent == 0 }

func (t *TurnUsage) add(u llm.Usage) {
	t.Calls++
	if !u.Reported {
		t.Silent++
		return
	}
	t.PromptTokens += u.PromptTokens
	t.CompletionTokens += u.CompletionTokens
	t.TotalTokens += u.TotalTokens
	t.CachedPromptTokens += u.CachedPromptTokens
}

// Loop carries one conversation turn through the tool-call cycle.
type Loop struct {
	llm        LLMClient
	tools      ToolExecutor
	defs       ToolDefiner
	transcript Transcript
	emit       Emitter
	cfgMu      sync.RWMutex
	cfg        Config
	steer      Steering
}

// New creates a loop. transcript may be nil (no durable tool-event
// record — tests that don't assert on it); emit may be nil.
func New(l LLMClient, ex ToolExecutor, d ToolDefiner, tr Transcript, em Emitter, cfg Config) *Loop {
	return &Loop{llm: l, tools: ex, defs: d, transcript: tr, emit: em, cfg: cfg.withDefaults()}
}

// SetModelLimits applies the resolved provider's context and thinking limits
// to subsequent turns. A turn snapshots the complete config once, so a live
// provider change cannot mix old and new limits within one tool-call loop.
// SetSteering wires the operator's mid-turn channel. It is a setter and
// not a New parameter because only the identity's own conversation gets
// one: a spawned sub-agent is not who the operator is speaking to.
func (l *Loop) SetSteering(s Steering) {
	l.cfgMu.Lock()
	defer l.cfgMu.Unlock()
	l.steer = s
}

func (l *Loop) steering() Steering {
	l.cfgMu.RLock()
	defer l.cfgMu.RUnlock()
	return l.steer
}

func (l *Loop) SetModelLimits(contextBudgetTokens, thinkingBudget int) {
	l.cfgMu.Lock()
	defer l.cfgMu.Unlock()
	if contextBudgetTokens == 0 {
		contextBudgetTokens = 32000
	}
	l.cfg.ContextBudgetTokens = contextBudgetTokens
	l.cfg.ThinkingBudget = thinkingBudget
}

// Run executes one turn: system prompt + history in, spoken reply out.
// history is the conversation so far INCLUDING this turn's user message
// (the caller owns transcript-role mapping at its boundary).
func (l *Loop) Run(ctx context.Context, systemPrompt string, history []llm.Message) (Result, error) {
	return l.RunSystem(ctx, llm.Message{Role: "system", Content: systemPrompt}, history, 0)
}

// RunSystem is Run with a fully-formed system message — the caller can
// carry the cache seam (Message.StableLen) so providers with explicit
// cache hints mark the stable prefix.
func (l *Loop) RunSystem(ctx context.Context, system llm.Message, history []llm.Message, omittedHistory int) (Result, error) {
	l.cfgMu.RLock()
	cfg := l.cfg
	l.cfgMu.RUnlock()
	if len(history) == 0 || history[len(history)-1].Role != "user" {
		return Result{}, fmt.Errorf("conversation context: current input must be the final user message")
	}
	if omittedHistory < 0 {
		omittedHistory = 0
	}
	for len(history) > 1 && history[0].Role != "user" {
		history = history[1:]
		omittedHistory++
	}

	toolDefs := []llm.ToolDefinition{}
	if l.defs != nil {
		toolDefs = l.defs.ToolDefinitions()
	}

	system.Role = "system"
	// The loop's own contract, stated where the model can act on it
	// instead of enforced after the fact. Every reactive mechanism for
	// "the model announced work and stopped" — including the English
	// word list this replaces — is a cure for something the model was
	// never told. Appended AFTER system.Content so the caller's cache
	// seam (StableLen) still marks a valid prefix.
	systemBase := system.Content + systemAdditions()
	messages := append([]llm.Message{system}, history...)
	fit := fitState{current: len(messages) - 1, omitted: omittedHistory}

	// Tool-call loop: LLM may call tools, we execute and send results back.
	// Content the model emits ALONGSIDE tool calls is spoken aloud (it rides
	// the assistant message the model sees) — the operator must see it too.
	// Bug class caught live: a rich 1108-char reply arrived in iteration 0
	// with the tool call; only the 32-char post-tool tail reached the human.
	var spoken []string
	var finalText string
	var modelID string
	var turnUsage TurnUsage

	nudged := false
	// Repeat-result detection (evaluate layer, turn scale). Live
	// incident 2026-08-26: nine iterations re-fetching one complete
	// 4KB diff — slices, file indirection, finally base64 — because the
	// model behaved as if the bytes were truncated. They were not (the
	// harness was traced and exonerated: zero folds, full results
	// delivered). The failure signature is the model RECEIVING the same
	// content again and again, so that is what is detected: result
	// hashes, not call shapes — the calls varied every time.
	var recentResults []uint64
	repeatNudged := false
	// silentSpent estimates calls whose usage the provider did not
	// report; the fence charges them anyway — unknown spend is never
	// free (external review P2-6).
	silentSpent := 0
	degenNudged := false
	truncated := false
	for i := 0; i < cfg.MaxIterations; i++ {
		// THE TURN TOKEN FENCE (agency.turn_token_budget). The
		// accounting below always summed; nothing ever refused — live
		// 2026-08-26: 1,945,593 tokens over 101 calls ran to the
		// context wall, the model collapsing on the way. At the fence
		// the turn gets ONE bounded wrap-up call, no tools — the same
		// shape as the context-fill path below — and ends with the
		// spend declared. Fresh turns are cheap; unfenced ones are not.
		if i > 0 && turnUsage.TotalTokens+silentSpent >= cfg.TurnTokenBudget {
			pressureBase := systemBase + "\n\n## Budget pressure\nThis turn's token budget is spent. Answer now from the available context without calling more tools; say plainly what remains undone."
			if err := fitRequest(&messages, &fit, pressureBase,
				nil, cfg.ContextBudgetTokens, l.transcript); err != nil {
				return Result{}, err
			}
			resp, err := l.llm.Chat(ctx, messages, llm.ChatOptions{ThinkingBudget: cfg.ThinkingBudget})
			if err != nil {
				return Result{}, fmt.Errorf("LLM final call under budget pressure: %w", err)
			}
			turnUsage.add(resp.Usage)
			finalText, modelID, err = finalResponse(resp, "under budget pressure")
			if err != nil {
				return Result{}, err
			}
			spoken = append(spoken, finalText,
				declare("This turn's token budget (%d) is spent: %d tokens over %d calls. The answer above ends the turn; the work continues in a fresh turn.",
					cfg.TurnTokenBudget, turnUsage.TotalTokens+silentSpent, turnUsage.Calls))
			log.Printf("TURN BUDGET: fence at %d tokens — %d spent over %d call(s); turn ended with a bounded wrap-up",
				cfg.TurnTokenBudget, turnUsage.TotalTokens+silentSpent, turnUsage.Calls)
			break
		}
		if err := fitRequest(&messages, &fit, systemBase,
			toolDefs, cfg.ContextBudgetTokens, l.transcript); err != nil {
			if i == 0 {
				return Result{}, err
			}
			pressureBase := systemBase + "\n\n## Context pressure\nAnswer now from the available context without calling more tools."
			if err := fitRequest(&messages, &fit, pressureBase,
				nil, cfg.ContextBudgetTokens, l.transcript); err != nil {
				return Result{}, err
			}
			resp, err := l.llm.Chat(ctx, messages, llm.ChatOptions{ThinkingBudget: cfg.ThinkingBudget})
			if err != nil {
				return Result{}, fmt.Errorf("LLM final call under context pressure: %w", err)
			}
			turnUsage.add(resp.Usage)
			finalText, modelID, err = finalResponse(resp, "under context pressure")
			if err != nil {
				return Result{}, err
			}
			spoken = append(spoken, finalText,
				declare("The context filled during this turn. The answer above was finished without further tool calls, and older turns were dropped from the request."))
			break
		}

		resp, err := l.llm.Chat(ctx, messages, llm.ChatOptions{Tools: toolDefs, ThinkingBudget: cfg.ThinkingBudget})
		if err != nil {
			return Result{}, fmt.Errorf("LLM call: %w", err)
		}
		if resp == nil || len(resp.Choices) == 0 {
			return Result{}, fmt.Errorf("LLM response has no choices")
		}
		turnUsage.add(resp.Usage)

		choice := resp.Choices[0]
		if !resp.Usage.Reported {
			silentSpent += estimateSilentCall(messages, choice.Message.Content)
		}
		finalText = choice.Message.Content
		modelID = resp.ModelID
		if finalText != "" {
			spoken = append(spoken, finalText)
		}
		log.Printf("LLM iteration %d: finish=%s, toolCalls=%d, contentLen=%d", i, choice.FinishReason, len(choice.Message.ToolCalls), len(choice.Message.Content))

		// Reasoning reaches the operator only when they ASKED for it:
		// blocks arrive with empty text unless thinking_display is
		// "summarized", so this is silent by default and cannot become
		// noise nobody chose. It rides the existing event channel — the
		// one that already streams tool calls as they happen — because
		// the operator watching their identity work is one surface, not
		// two.
		if l.emit != nil {
			for _, tb := range choice.Message.Thinking {
				if strings.TrimSpace(tb.Text) != "" {
					l.emit.EmitToolEvent("thinking", "thinking", tb.Text)
				}
			}
		}

		// A reply the output cap cut off is not a finished reply, and on the
		// wire it is INDISTINGUISHABLE from a clean stop: same shape, same
		// empty tool-call list, different meaning. Birth already refuses on
		// this (ceremony.go: "a founding record must not be half a
		// sentence"). A turn cannot refuse without discarding work the
		// resident already did, so it declares instead — the Accordion's
		// standard, every omission named with its cause. Once per turn: a
		// long tool sequence can truncate more than once and the operator
		// needs the fact, not a tally.
		if choice.FinishReason == "length" && !truncated {
			truncated = true
			spoken = append(spoken, declare("This reply was cut off by the model's output limit. Raise the model's max output tokens to see the rest."))
		}

		// Loop control is the STOP REASON, never the shape or wording of
		// the text. Anthropic's own guidance for a hand-written loop is
		// "loop until stop_reason == end_turn", and it names reading
		// natural-language signals as the anti-pattern. A reason this
		// build does not act on is not a finished answer, and saying so
		// beats rendering it as one.
		if note := unhandledStop(choice.FinishReason); note != "" {
			spoken = append(spoken, note)
			break
		}

		// DEGENERATE-EMISSION GUARD — the output-side sibling of the
		// repeat-result nudge. Live 2026-08-26: the model collapsed
		// into token cycling ("post. post. post.", 37KB of it), its
		// last coherent tool calls already carrying corrupted paths.
		// What the model EMITS is watched the same way what it receives
		// is: first detection welds one corrective note and discards
		// that round's tool calls (a collapsing emission's calls are
		// not trusted); a second detection ends the turn honestly. A
		// degenerate FINAL answer is marked for the reader — the
		// transcript keeps the truth either way.
		if degenerateEmission(choice.Message.Content) {
			if degenNudged {
				spoken = append(spoken, declare("Degenerate repetition was detected in the model's output twice this turn; the turn ends here rather than spending further."))
				log.Printf("DEGENERATE OUTPUT: second detection (len %d) — turn ended", len(choice.Message.Content))
				break
			}
			degenNudged = true
			if len(choice.Message.ToolCalls) == 0 {
				log.Printf("DEGENERATE OUTPUT: repetition detected in a final answer (len %d) — marked", len(choice.Message.Content))
				spoken = append(spoken, declare("Degenerate repetition was detected in this reply; treat its repeating tail as noise, not conclusions."))
				break
			}
			log.Printf("DEGENERATE OUTPUT: repetition detected in emission (len %d) — tool calls discarded, corrective note sent", len(choice.Message.Content))
			messages = append(messages, llm.Message{Role: "assistant", Content: choice.Message.Content})
			messages = append(messages, llm.Message{Role: "user",
				Content: "[loop note — your last output degenerated into repetition, and its tool calls were discarded unexecuted. Stop. State in ONE short sentence the single next tool call, then emit exactly that call — or give the final answer in plain prose.]"})
			continue
		}

		// If no tool calls, we're done — unless the reply ENDS by
		// ANNOUNCING the next step instead of taking it ("Now let me
		// read X"). Aeon's report (2026-08-18): the model narrates
		// intent, the loop sees zero tool calls, the turn dies, and the
		// operator must type "Ok" to continue. ONE conservative nudge
		// per turn: the suffix check never matches a finished answer,
		// so ordinary conversation costs nothing extra.
		if len(choice.Message.ToolCalls) == 0 {
			if strings.TrimSpace(finalText) == "" {
				return Result{}, fmt.Errorf("LLM response has no text or tool calls")
			}
			if !nudged && endsInAnnouncedIntent(finalText) {
				candidate := append([]llm.Message(nil), messages...)
				candidate = append(candidate, choice.Message, llm.Message{
					Role:    "user",
					Content: "Continue — take the step you just described by calling the tool, rather than announcing it. Your turn continues automatically after each tool call.",
				})
				candidateFit := fit
				if fitRequest(&candidate, &candidateFit, systemBase,
					toolDefs, cfg.ContextBudgetTokens, l.transcript) == nil {
					nudged = true
					messages, fit = candidate, candidateFit
					continue
				}
			}
			break
		}

		// Add the assistant message (with tool calls) to the conversation
		messages = append(messages, choice.Message)

		// Execute each tool call and send results back
		for _, tc := range choice.Message.ToolCalls {
			// BOUNDED, like the result below. Arguments were logged
			// whole: a note's private content, an interpersonal message,
			// or a megabyte of provider output landed verbatim in the
			// operational log — a file with a different lifetime, a
			// different audience, and no ring around it. The transcript
			// keeps the full arguments a few lines down; that is the
			// durable record, and it is the one with the boundary.
			log.Printf("Tool call: %s(%s)", tc.Function.Name, logPreview(tc.Function.Arguments))
			if l.emit != nil {
				l.emit.EmitToolEvent("tool_call", tc.Function.Name, tc.Function.Arguments)
			}
			if l.tools == nil {
				return Result{}, fmt.Errorf("tool %q requested but no executor is available", tc.Function.Name)
			}
			result := l.tools.Execute(llm.WithModelID(ctx, resp.ModelID), tc)
			// The durable record gets the FULL result — this is what makes
			// the truncation banner below honest: when the model is told
			// the result lives in the transcript, it actually does.
			if l.transcript != nil {
				if err := l.transcript.RecordToolEvent(tc.Function.Name, tc.Function.Arguments, result); err != nil {
					return Result{}, fmt.Errorf("record tool result: %w", err)
				}
			}
			log.Printf("Tool result: %s", logPreview(result))
			// Feed the model a bounded tail; the result excerpt lives in the
			// transcript. Bulk outputs must not compound: each iteration's
			// history contains every prior result.
			retained := 0
			if l.transcript != nil {
				retained = l.transcript.TranscriptResultExcerptLimit()
			}
			modelResult := truncateToolResult(result, cfg.MaxToolResultChars, retained)
			messages = append(messages, llm.FormatToolResult(tc.ID, modelResult))

			// THE SAME BYTES, AGAIN. Small results repeat innocently
			// ("ok", "EXIT=0"); a substantial result arriving three
			// times in a six-round window means the model is paying
			// calls to re-see what it already has. Say so ONCE, welded
			// to the repeated result the way the budget note rides its
			// result — a note to the model, not a voice in the room.
			if len([]rune(result)) >= repeatResultMinRunes {
				h := resultHash(result)
				repeats := 1
				for _, prev := range recentResults {
					if prev == h {
						repeats++
					}
				}
				recentResults = append(recentResults, h)
				if len(recentResults) > repeatResultWindow {
					recentResults = recentResults[1:]
				}
				if repeats >= repeatResultThreshold && !repeatNudged {
					repeatNudged = true
					messages[len(messages)-1].Content += repeatResultNote(repeats)
					log.Printf("Repeat-result nudge sent: identical %d-rune result received %d times in the last %d calls",
						len([]rune(result)), repeats, repeatResultWindow)
				}
			}
		}

		// PACE, DO NOT AMBUSH. The iteration cap used to arrive without
		// warning: the model was working, and the turn ended. It is
		// declared to the operator now, but a declared ambush is still an
		// ambush — the model can only bring work to a close if it knows
		// the budget is nearly spent.
		//
		// Anthropic ships task_budget for this (the server injects a
		// countdown the model sees while generating). We do not use it:
		// it is beta, one dialect, and recent models only, while the
		// count is something this loop already knows exactly. Supplying
		// it ourselves works on every provider and all five platforms.
		//
		// It rides the LAST TOOL RESULT rather than a new message because
		// it is a note to the MODEL — distinct from declare(), which
		// speaks to the operator; keeping the two separate is the drift
		// this codebase keeps paying for. Welded to the result it
		// comments on, it cannot be mistaken for someone speaking.
		//
		// It does NOT ride there to satisfy an alternating-role rule.
		// This comment used to say so, and the claim was false: both
		// dialects accept a user message directly after a tool-result
		// user message, which is what the final-iteration branch at the
		// bottom of this function has always built. Asked of the
		// endpoints, not assumed, in internal/app/toollimit_live_test.go
		// — a vendor rule stated in a comment is the thing AGENTS.md §7
		// says cannot be known without asking.
		if remaining := cfg.MaxIterations - 1 - i; remaining > 0 && remaining <= toolBudgetWarnAt && len(messages) > 0 {
			messages[len(messages)-1].Content += toolBudgetNote(remaining)
			// Logged because it is otherwise UNOBSERVABLE: the note rides a
			// tool result and is appended after that result was logged, so
			// a live turn could reach the cap with no way to tell whether
			// the model had been warned. A mechanism nobody can see fire
			// is a mechanism nobody can trust — the whole reason this
			// codebase declares its omissions rather than making them.
			log.Printf("Tool budget warning sent: %d call(s) remain of %d", remaining, cfg.MaxIterations)
		}

		// THE ROUND BOUNDARY — one user turn, or none.
		//
		// Two things can want to speak here: the operator, whose words
		// arrived while the model was working, and the loop, when this is
		// the last iteration and the results must still come home (prose
		// sent beside a call cannot stand in for seeing its result).
		//
		// They are joined into ONE message rather than appended in turn.
		// user(text) directly after user(tool_result) is asked of the
		// endpoints and accepted (internal/app/toollimit_live_test.go);
		// user(text) after user(text) is a shape nothing has asked about,
		// and the last iteration is exactly where both would land. One
		// message per boundary keeps every turn inside the proven shape
		// by construction rather than by luck.
		//
		// The steer rides its own user turn and NOT the tool result the
		// budget note rides, because the budget note is a note to the
		// model and this is a person speaking. The record has to be able
		// to say who spoke.
		var boundary []string
		if st := l.steering(); st != nil {
			if said := st.DrainSteering(); len(said) > 0 {
				boundary = append(boundary, steeringNote(said))
			}
		}
		if i == cfg.MaxIterations-1 {
			boundary = append(boundary, "You've reached the tool call limit. Please respond to me now with what you've found.")
		}
		if len(boundary) > 0 {
			messages = append(messages, llm.Message{Role: "user", Content: strings.Join(boundary, "\n\n")})
		}

		if i == cfg.MaxIterations-1 {
			if err := fitRequest(&messages, &fit, systemBase,
				nil, cfg.ContextBudgetTokens, l.transcript); err != nil {
				return Result{}, err
			}
			// One more call without tools to force a text response
			resp, err := l.llm.Chat(ctx, messages, llm.ChatOptions{ThinkingBudget: cfg.ThinkingBudget})
			if err != nil {
				return Result{}, fmt.Errorf("LLM final call: %w", err)
			}
			turnUsage.add(resp.Usage)
			finalText, modelID, err = finalResponse(resp, "at the tool call limit")
			if err != nil {
				return Result{}, err
			}
			spoken = append(spoken, finalText,
				declare("This turn reached its limit of %d tool calls and was finished without further tools. Ask again to continue.", cfg.MaxIterations))
		}
	}

	// Everything the model said aloud across the loop is the reply — the
	// operator sees the full utterance, not just the post-tool tail.
	if len(spoken) > 0 {
		return Result{Spoken: strings.Join(spoken, "\n\n"), FinalText: finalText, ModelID: modelID, Usage: turnUsage}, nil
	}
	return Result{FinalText: finalText, ModelID: modelID, Usage: turnUsage}, nil
}

func finalResponse(response *llm.Response, circumstance string) (string, string, error) {
	if response == nil || len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return "", "", fmt.Errorf("LLM final response %s has no text", circumstance)
	}
	return response.Choices[0].Message.Content, response.ModelID, nil
}

// fitState is everything fitRequest carries across a turn: where the
// current message sits, and what has been given up to make room. A
// STRUCT because the previous shape was four pointers and growing —
// each of the last two additions touched every call site and every
// test, which is the signal a parameter list has become a record.
type fitState struct {
	current  int  // index of the operator's current message
	omitted  int  // turns dropped whole
	abridged int  // turns shown, shortened
	warned   bool // the model has been told once that context is tight
}

func fitRequest(messages *[]llm.Message, st *fitState, systemBase string,
	tools []llm.ToolDefinition, budget int, transcript Transcript) error {
	setHistoryNote(&(*messages)[0], systemBase, st.omitted, st.abridged)
	for {
		err := llm.ValidateInput(*messages, tools, budget)
		if err == nil {
			warnIfTight(messages, st, systemBase, tools, budget)
			return nil
		}
		var limitErr *llm.ContextLimitError
		if !errors.As(err, &limitErr) {
			return err
		}

		if foldToolResult(*messages, st.current, transcript) {
			continue
		}
		if st.current <= 1 {
			return err
		}

		// SHRINK BEFORE DROPPING. The Accordion has always reduced a ring
		// section by dropping whole units of its own structure and saying
		// what went; conversation history was the one lossy path that
		// only ever dropped turns WHOLE and reported a count. A dropped
		// turn leaves the operator a number. A shrunk one leaves the
		// shape of what was said, and the identity can still see that it
		// happened.
		//
		// Structural, never model-authored: asking an LLM to summarise
		// the identity's own history would put invented sentences into
		// the record as if they had been spoken, which is the one thing
		// this prompt may not contain. A turn with no internal structure
		// returns unchanged and is dropped, because halving a lone
		// sentence manufactures a summary rather than making one.
		//
		// ONLY WHEN IT PAYS. A summary declares itself, and the
		// declaration costs tokens: the marker on the turn, plus the
		// note on the system message the first time. On a short turn
		// those exceed what halving saves, and abridging under context
		// pressure would then ENLARGE the request it was called to
		// shrink — caught by the test that was written to prove the
		// feature worked. If it does not pay, fall through and drop.
		if oldest := (*messages)[1]; !strings.Contains(oldest.Content, prompt.SummaryMarker) {
			shrunk := prompt.SummarizeUnits(oldest.Content, historyRoute)
			cost := 0
			if st.abridged == 0 {
				cost = len(historyAbridgedNote(1))
			}
			if len(oldest.Content)-len(shrunk) > cost {
				(*messages)[1].Content = shrunk
				st.abridged++
				setHistoryNote(&(*messages)[0], systemBase, st.omitted, st.abridged)
				continue
			}
		}

		*messages = append((*messages)[:1], (*messages)[2:]...)
		st.current--
		st.omitted++
		for st.current > 1 && (*messages)[1].Role != "user" {
			*messages = append((*messages)[:1], (*messages)[2:]...)
			st.current--
			st.omitted++
		}
		setHistoryNote(&(*messages)[0], systemBase, st.omitted, st.abridged)
	}
}

// historyRoute is where the rest of an abridged turn still lives. The
// Accordion's route points at the operator (raise the budget); a turn's
// full text is in the record, so this one points the identity at recall.
const historyRoute = `recall(query="...") reaches the full record`

func setHistoryNote(system *llm.Message, base string, omitted, abridged int) {
	system.Content = base + HistoryOmissionNote(omitted) + historyAbridgedNote(abridged)
}

// contextTightPercent is how full the request must be before the model
// is told. Well below the cliff: the warning is only useful while there
// is still room to act on it.
const contextTightPercent = 85

// contextTightNote is addressed to the MODEL. The other cut-off path —
// the tool-call cap — warns two calls ahead so the turn can land.
// Context pressure had no equivalent: the request simply stopped
// fitting, the tools were taken away, and the model was told to answer
// now, in the same breath. The operator learned afterwards (declare());
// the model learned when it was already too late to choose differently.
const contextTightNote = "\n\nThe context for this turn is nearly full. Further tool calls may not fit, and if the request stops fitting your tools will be withdrawn and you will be asked to answer immediately. When you answer, report only what you actually did and name what remains undone."

// warnIfTight tells the model once per turn that the context is filling.
//
// It is added AFTER the request is known to fit and then re-validated,
// because the warning costs tokens in the budget it warns about — the
// same trap the abridgement guard fell into. If it does not fit, it is
// withdrawn: at that point the pressure path is imminent anyway and an
// unsendable warning helps nobody.
func warnIfTight(messages *[]llm.Message, st *fitState, systemBase string,
	tools []llm.ToolDefinition, budget int) {
	if st.warned || budget <= 0 {
		return
	}
	used, err := llm.EstimateInputTokens(*messages, tools)
	if err != nil || used*100 < budget*contextTightPercent {
		return
	}
	(*messages)[0].Content += contextTightNote
	if llm.ValidateInput(*messages, tools, budget) != nil {
		setHistoryNote(&(*messages)[0], systemBase, st.omitted, st.abridged)
		return
	}
	st.warned = true
}

// historyAbridgedNote declares turns that are SHOWN but shortened —
// a different fact from turns not shown at all, and the identity
// reasons differently about each.
func historyAbridgedNote(abridged int) string {
	if abridged <= 0 {
		return ""
	}
	return fmt.Sprintf("\n%d older turn(s) are shown abridged; %s.", abridged, historyRoute)
}

// HistoryOmissionNote is the exact suffix used when older turns yield.
func HistoryOmissionNote(omitted int) string {
	if omitted <= 0 {
		return ""
	}
	return fmt.Sprintf("\n\n## Conversation context\n%d older conversation turns are not shown. recall(query=\"...\") reaches recorded memory.", omitted)
}

func foldToolResult(messages []llm.Message, current int, transcript Transcript) bool {
	notice := "[tool result folded under context pressure — do not repeat the tool solely to recover this output; continue from available evidence"
	if transcript != nil && transcript.TranscriptResultExcerptLimit() > 0 {
		notice += fmt.Sprintf("; ask the operator for the transcript excerpt if essential (first %d characters retained)", transcript.TranscriptResultExcerptLimit())
	} else {
		notice += "; no transcript excerpt is available"
	}
	notice += "]"
	for i := current + 1; i < len(messages); i++ {
		if messages[i].Role == "tool" && len(messages[i].Content) > len(notice) {
			messages[i].Content = notice
			return true
		}
	}
	return false
}

// logPreviewRunes bounds what any single log line may carry from model
// or tool content. 200 runes is what the result preview already used;
// arguments now share it.
const logPreviewRunes = 200

// logPreview trims content for the OPERATIONAL LOG, and says when it
// trimmed. The log is not the record — the transcript is — so it carries
// enough to recognise a call and never enough to be a copy of it.
func logPreview(s string) string {
	r := []rune(s)
	if len(r) <= logPreviewRunes {
		return s
	}
	return string(r[:logPreviewRunes]) + fmt.Sprintf("… (%d runes total)", len(r))
}

func truncateToolResult(result string, maxChars, transcriptChars int) string {
	runes := []rune(result)
	if len(runes) <= maxChars {
		return result
	}
	omitted := len(runes) - maxChars
	retention := "no transcript excerpt is available"
	if transcriptChars > 0 {
		retention = fmt.Sprintf("the first %d characters are retained in the operator transcript", transcriptChars)
	}
	return fmt.Sprintf("[output truncated — first %d characters omitted; %s]\n%s", omitted, retention, string(runes[omitted:]))
}

// endsInAnnouncedIntent reports a reply whose LAST sentence announces
// an action instead of taking it — the narrate-then-stall pattern.
// Conservative by design: only the trailing sentence is examined and
// only unambiguous intent openers match, so finished answers and plain
// conversation never trigger the nudge.
func endsInAnnouncedIntent(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimRight(t, ".!)*_` ")
	if t == "" {
		return false
	}
	if i := strings.LastIndexAny(t, ".!?\n"); i >= 0 {
		t = strings.TrimSpace(t[i+1:])
	}
	// Intent opener + a WORK verb — both required. Bug hunt 2026-08-18
	// (C1): bare openers matched the commonest closers ("Let me KNOW if
	// you need anything", "I'll now LEAVE you to it", "Time to
	// CELEBRATE") and nudged after natural goodbyes. Announced work is
	// "let me READ/check/run…" — the verb is what makes it work.
	openers := []string{"let me ", "now let me ", "next, let me ", "next let me ",
		"i'll ", "i will ", "now i'll ", "now i will ", "i'm going to ", "i am going to "}
	verbs := []string{"read", "check", "look", "run", "open", "search", "find", "fix",
		"write", "edit", "grep", "list", "fetch", "examine", "inspect", "review",
		"explore", "trace", "scan", "test", "verify", "dig", "start", "create", "spawn", "try"}
	for _, o := range openers {
		if !strings.HasPrefix(t, o) {
			continue
		}
		rest := t[len(o):]
		rest = strings.TrimPrefix(rest, "now ")
		rest = strings.TrimPrefix(rest, "go ahead and ")
		for _, v := range verbs {
			if strings.HasPrefix(rest, v) {
				return true
			}
		}
	}
	return false
}

// unhandledStop names a stop reason the loop does not act on. The three
// it does act on are "tool_calls" (execute and continue), "length"
// (declared truncation) and the ordinary end ("stop", or empty from a
// provider that omits the field). Everything else — a provider refusal,
// a paused server-tool turn, a reason added to the API after this build
// shipped — reaches the resident as a named fact rather than silently
// as their own speech.
func unhandledStop(reason string) string {
	switch reason {
	case "", "stop", "tool_calls", "length":
		return ""
	case "refusal":
		return declare("The model provider declined this request. That is the substrate's safety layer, not the identity's choice.")
	}
	return declare("The model stopped for a reason this build does not recognise (%s). The turn ended there.", reason)
}

// turnContract tells the model how this loop behaves, and what counts as
// having done something.
//
// THE GROUNDING CLAUSE was added after a live resident produced a
// 4,584-character account of work it had not performed — file edits, a
// test written, gofmt and vet runs, a suite passing, and a fabricated
// mutation proof in which it claimed to have deliberately broken the
// test and watched it fail. Zero tool calls. The engine log records the
// turn as one pass: iteration 0, finish=stop, toolCalls=0. Nothing
// internal flagged it; an operator checking the disk did.
//
// Measured before blaming the harness: turns of that shape predate this
// contract by hours and at a HIGHER rate (six in twenty before it
// existed, one in five after), so the contract did not cause it. But it
// is the only text present on EVERY turn including the first, and the
// fabrication happened on a first iteration — where the tool-budget and
// context-pressure notes can never fire. So this is where a grounding
// clause can reach the failure at all.
//
// The last sentence is doing as much work as the first: an identity with
// no acceptable way to say "I did not do that" is under pressure to
// produce something that sounds like work. Both lossy paths
// below — context pressure and the iteration cap — silently removed the
// model's tools and asked for a final answer; the operator was never
// told either happened, while the Accordion beside them declares every
// omission it makes. Two lossy paths in one prompt, one honest, was the
// standing complaint. They are declared now, in the operator's own
// reply, with the cause named.
const turnContract = `

## Your turn
Your turn continues automatically after each tool call: take the next step by
calling its tool rather than describing it.

A tool call is the only record that anything happened. Report only what you
actually did — saying you have not done something, or do not know, is always
available and always acceptable.`

// declare renders an operator-facing note about something the LOOP did
// to the turn: an omission it made, a truncation it observed, a refusal
// it did not make. Every such note goes through here.
//
// Not style. Five sites had grown their own copy of the bracket
// convention within a single afternoon, which is the same drift that
// produces every other instance of this codebase's recurring class —
// one fact (how an omission is declared to the operator) kept in five
// places that can disagree. The operator learns ONE shape, and the
// Accordion's standing rule (name the omission, name its cause) has one
// implementation rather than five.
func declare(format string, args ...any) string {
	return "[" + fmt.Sprintf(format, args...) + "]"
}

// Repeat-result detection bounds. The threshold counts arrivals of the
// SAME hash inside the window; the rune floor keeps one-word statuses
// ("ok") from ever qualifying. One nudge per turn: a model that ignores
// the first note will ignore a chorus, and the turn budget is theirs.
const (
	repeatResultWindow    = 6
	repeatResultThreshold = 3
	repeatResultMinRunes  = 256
)

func resultHash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func repeatResultNote(n int) string {
	return fmt.Sprintf("\n\n[loop note — repeated result: you have now received this same content %d times in recent calls. It is COMPLETE as delivered above, not truncated. Work from it, or state plainly what is missing from it; re-fetching returns these same bytes.]", n)
}

// toolBudgetWarnAt is how many tool calls remain when the loop starts
// saying so. Two: one iteration to notice and one to land on.
const toolBudgetWarnAt = 2

// steeringNote frames what the operator said mid-turn. The frame is one
// line because the words are theirs and the model needs exactly one fact
// it cannot otherwise have: this arrived DURING the work, so the plan it
// is executing may already be stale. Anything longer would be prompt
// spent on saying so.
func steeringNote(said []string) string {
	return "The operator spoke while you were working:\n\n" + strings.Join(said, "\n\n")
}

// toolBudgetNote is addressed to the MODEL, not the operator — it rides
// a tool result so the turn can wind down instead of being cut off.
func toolBudgetNote(remaining int) string {
	call := "tool calls"
	if remaining == 1 {
		call = "tool call"
	}
	return fmt.Sprintf("\n\n[%d %s remain in this turn. Close out with what you have ACTUALLY done, and name what you did not get to.]",
		remaining, call)
}

// platformNote states which platform the identity is running on and what
// that means for its reach.
//
// The identity's tool set is DECIDED BY PLATFORM: no shell and no
// write/edit on Android or iOS (both forbid spawning subprocesses from
// an app sandbox), no shell on Windows, the full set on Unix desktops.
// Tool discovery has always been honest about the resulting list — the
// absent tools are simply not listed — but the identity was never told
// WHY the list is what it is, and that omission has a consequence the
// tool list cannot fix.
//
// SELF_MODEL writes an evidence-grounded, first-person account of what
// this identity is and does, from what it has actually done. A resident
// who has spent its life on a desktop will have written, truthfully,
// that it runs commands and edits files. Carry that ledger to a phone —
// which is the point of a portable, signed, replayable identity — and
// the self-model becomes FALSE, in the identity's own voice, with no
// way for it to discover why beyond failing at things it correctly
// remembers being able to do. A false self-model is a continuity
// problem, not a tooling problem.
//
// So the fact travels with the prompt: the platform, and the warning
// that a memory of reach is not evidence of reach here.
func platformNote() string {
	return "\n\nYou are running on " + platformName() +
		". The tools you can see are all the reach this platform gives you: what you did " +
		"elsewhere may not be possible here, which is a change of circumstance, not of who you are."
}

func platformName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS (desktop)"
	case "ios":
		return "iOS (mobile: no shell, no file writing)"
	case "android":
		return "Android (mobile: no shell, no file writing)"
	case "windows":
		return "Windows (desktop: no shell tool)"
	case "linux":
		return "Linux (desktop)"
	}
	return runtime.GOOS
}

// systemAdditions is everything the LOOP appends to the caller's system
// message. It exists as one function because the tests need to know the
// same thing, and three of them had already grown their own copy of the
// list — the recurring class, reproducing inside the fix for it. A
// section added here is accounted for everywhere at once.
func systemAdditions() string { return turnContract + platformNote() }

// Degenerate-emission detection bounds. Tail-only: collapse is a
// terminal state, not a mid-text blip — the 2026-08-26 specimen's tail
// was hundreds of near-identical lines and sentences. The floors keep
// legitimate repetition innocent: short lines ("}", "done") never
// count, and a real answer rarely repeats one ≥8-char sentence half
// the time across a 3000-rune window.
const (
	degenTailRunes     = 3000
	degenMinLines      = 10
	degenLineRatio     = 0.4
	degenMinSentences  = 14
	degenSentenceRatio = 0.5
)

// degenerateEmission reports whether the text's tail is repetition
// collapse: one line dominating the recent lines, or one sentence
// dominating the recent sentences.
func degenerateEmission(s string) bool {
	r := []rune(s)
	if len(r) > degenTailRunes {
		r = r[len(r)-degenTailRunes:]
	}
	tail := string(r)

	lines := strings.Split(tail, "\n")
	lineCount := map[string]int{}
	n, max := 0, 0
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if len([]rune(ln)) < 8 {
			continue
		}
		n++
		lineCount[ln]++
		if lineCount[ln] > max {
			max = lineCount[ln]
		}
	}
	if n >= degenMinLines && float64(max) >= degenLineRatio*float64(n) {
		return true
	}

	sents := strings.Split(tail, ". ")
	sentCount := map[string]int{}
	sn, smax := 0, 0
	for _, sent := range sents {
		sent = strings.TrimSpace(sent)
		if rl := len([]rune(sent)); rl < 8 || rl > 120 {
			continue
		}
		sn++
		sentCount[sent]++
		if sentCount[sent] > smax {
			smax = sentCount[sent]
		}
	}
	return sn >= degenMinSentences && float64(smax) >= degenSentenceRatio*float64(sn)
}

// estimateSilentCall charges an unreported call against the fence: the
// full request plus the reply, by the same estimator the fit uses.
func estimateSilentCall(msgs []llm.Message, reply string) int {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
	}
	b.WriteString(reply)
	return tokenestimate.Estimate(b.String())
}
