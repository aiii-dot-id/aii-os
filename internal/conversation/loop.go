// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

	"github.com/google/uuid"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

// .
type LLMClient interface {
	Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error)
}

// .
// .
// .
// .
// .
// .
type Observation struct {
	Text      string
	Failed    bool
	Truncated bool
	// .
	// .
	// .
	// .
	// .
	// .
	EndTurn bool
	// .
	// .
	// .
	EndTurnAnswer string
}

// .
// .
// .
type ToolExecutor interface {
	Execute(ctx context.Context, call llm.ToolCall) Observation
}

// .
// .
// .
type ParallelSafeExecutor interface {
	ParallelSafe(call llm.ToolCall) bool
}

// .
type ToolDefiner interface {
	ToolDefinitions() []llm.ToolDefinition
}

// .
// .
// .
type Transcript interface {
	// .
	// .
	// .
	// .
	// .
	// .
	RecordToolStart(turnID string, ordinal int, callID, tool, args, model string) error
	// .
	RecordToolDone(turnID string, ordinal int, tool, args, result string, failed, truncated bool) error
	TranscriptResultExcerptLimit() int
}

// .
type Emitter interface {
	EmitToolEvent(kind, name, args string)
}

// .
// .
// .
// .
type Steering interface {
	DrainSteering() []string
}

// .
// .
type Config struct {
	MaxIterations       int
	MaxToolResultChars  int
	ContextBudgetTokens int
	ThinkingBudget      int
	// .
	// .
	// .
	TurnTokenBudget int
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	FanoutState func() (declared, spawned int)

	// .
	// .
	// .
	// .
	// .
	// .
	PredictedThisTurn func() int

	// .
	// .
	// .
	// .
	// .
	Calibration func() (plans, predicted, actual int)

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	PlanState func() (planned, active bool)

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	IsAct func(call llm.ToolCall) bool

	// .
	// .
	// .
	// .
	// .
	BreadthNudge *bool
	// .
	// .
	// .
	// .
	// .
	// .
	MaxToolCalls int

	// .
	// .
	// .
	// .
	// .
	HeuristicNudges bool
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

// .
type Result struct {
	Spoken    string
	FinalText string
	ModelID   string
	Usage     TurnUsage
	// .
	// .
	// .
	// .
	Interrupted string
	// .
	// .
	// .
	// .
	// .
	// .
	ContinuedAtCap bool
	// .
	// .
	// .
	Yielded bool
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	RoundsUsed int

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	ToolCallsUsed int
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	ExhaustedBudget bool
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	ExhaustedCallBudget bool
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
type TurnUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// .
	// .
	CachedPromptTokens int
	CacheWriteTokens   int
	CacheWrite5mTokens int
	CacheWrite1hTokens int
	CacheReadReports   int
	CacheWriteReports  int
	UnknownAttempts    int
	Calls              int
	Silent             int
}

// .
func (t TurnUsage) Complete() bool { return t.Calls > 0 && t.Silent == 0 && t.UnknownAttempts == 0 }

// .
// .
// .
// .
// .
// .
// .
func (t *TurnUsage) failed(err error) {
	t.UnknownAttempts += llm.UnknownAttempts(err)
	t.Calls++
	t.Silent++
}

func (t *TurnUsage) add(u llm.Usage) {
	t.Calls++
	t.UnknownAttempts += u.UnknownAttempts
	if !u.Reported {
		t.Silent++
		return
	}
	t.PromptTokens += u.PromptTokens
	t.CompletionTokens += u.CompletionTokens
	t.TotalTokens += u.TotalTokens
	t.CachedPromptTokens += u.CachedPromptTokens
	t.CacheWriteTokens += u.CacheWriteTokens
	t.CacheWrite5mTokens += u.CacheWrite5mTokens
	t.CacheWrite1hTokens += u.CacheWrite1hTokens
	if u.CacheReadReported {
		t.CacheReadReports++
	}
	if u.CacheWriteReported {
		t.CacheWriteReports++
	}
}

// .
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

// .
// .
func New(l LLMClient, ex ToolExecutor, d ToolDefiner, tr Transcript, em Emitter, cfg Config) *Loop {
	return &Loop{llm: l, tools: ex, defs: d, transcript: tr, emit: em, cfg: cfg.withDefaults()}
}

// .
// .
// .
// .
// .
// .
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

// .
// .
// .
func (l *Loop) Run(ctx context.Context, systemPrompt string, history []llm.Message) (Result, error) {
	return l.RunSystem(ctx, llm.Message{Role: "system", Content: systemPrompt}, history, 0)
}

// .
// .
// .
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
	// .
	// .
	// .
	// .
	// .
	// .
	systemBase := system.Content + systemAdditions()
	messages := append([]llm.Message{system}, history...)
	fit := fitState{current: len(messages) - 1, omitted: omittedHistory}

	// .
	// .
	// .
	// .
	// .
	var spoken []string
	var finalText string
	var modelID string
	var turnUsage TurnUsage
	toolCallsSoFar, roundsUsed := 0, 0
	previousResponseID := ""
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var drainedSteering []string
	fail := func(err error) (Result, error) {
		out := spoken
		if len(drainedSteering) > 0 {
			out = append(append([]string{}, spoken...), steeringUnansweredNote(drainedSteering))
		}
		if len(out) == 0 {
			return Result{Usage: turnUsage, ModelID: modelID, ToolCallsUsed: toolCallsSoFar, RoundsUsed: roundsUsed}, err
		}
		return Result{
			ToolCallsUsed: toolCallsSoFar, RoundsUsed: roundsUsed,
			Spoken:      strings.Join(out, "\n\n"),
			ModelID:     modelID,
			Usage:       turnUsage,
			Interrupted: interruptCause(ctx, err),
		}, err
	}

	nudged := false
	planNudged := false
	fanoutNudged := false
	scalarNudged := false
	firstActAsked := false
	actSeen := false
	softCapFinal := -1
	yielded := false
	yieldAnswer := ""
	continuedAtCap := false
	exhaustedBudget := false
	exhaustedCallBudget := false
	// .
	// .
	turnID := "turn_" + uuid.New().String()
	ordinal := 0
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var recentResults []uint64
	repeatNudged := false
	// .
	// .
	// .
	silentSpent := 0
	degenNudged := false
	truncated := false
	for i := 0; i < cfg.MaxIterations; i++ {
		roundsUsed = i + 1
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if i > 0 && turnUsage.TotalTokens+silentSpent >= cfg.TurnTokenBudget {
			pressureBase := systemBase + "\n\n## Budget pressure\nThis turn's token budget is spent. Answer now from the available context without calling more tools; say plainly what remains undone."
			finalTools, fitErr := fitFinalRequest(&messages, &fit, pressureBase, toolDefs, cfg.ContextBudgetTokens, l.transcript)
			if fitErr != nil {
				return fail(fitErr)
			}
			resp, err := l.llm.Chat(ctx, messages, llm.ChatOptions{Tools: finalTools, DisableTools: true, ThinkingBudget: cfg.ThinkingBudget, PreviousResponseID: previousResponseID})
			if err != nil {
				turnUsage.failed(err)
				return fail(fmt.Errorf("LLM final call under budget pressure: %w", err))
			}
			turnUsage.add(resp.Usage)
			// .
			// .
			// .
			finalText, modelID, err = finalResponse(resp, "under budget pressure")
			if err != nil {
				return fail(err)
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
				return fail(err)
			}
			pressureBase := systemBase + "\n\n## Context pressure\nAnswer now from the available context without calling more tools."
			finalTools, fitErr := fitFinalRequest(&messages, &fit, pressureBase, toolDefs, cfg.ContextBudgetTokens, l.transcript)
			if fitErr != nil {
				return fail(fitErr)
			}
			resp, err := l.llm.Chat(ctx, messages, llm.ChatOptions{Tools: finalTools, DisableTools: true, ThinkingBudget: cfg.ThinkingBudget, PreviousResponseID: previousResponseID})
			if err != nil {
				turnUsage.failed(err)
				return fail(fmt.Errorf("LLM final call under context pressure: %w", err))
			}
			turnUsage.add(resp.Usage)
			// .
			// .
			// .
			finalText, modelID, err = finalResponse(resp, "under context pressure")
			if err != nil {
				return fail(err)
			}
			spoken = append(spoken, finalText,
				declare("The context filled during this turn. The answer above was finished without further tool calls, and older turns were dropped from the request."))
			break
		}

		resp, err := l.llm.Chat(ctx, messages, llm.ChatOptions{Tools: toolDefs, ThinkingBudget: cfg.ThinkingBudget, PreviousResponseID: previousResponseID})
		if err != nil {
			turnUsage.failed(err)
			return fail(fmt.Errorf("LLM call: %w", err))
		}
		if resp == nil || len(resp.Choices) == 0 {
			if resp != nil {
				// .
				// .
				// .
				turnUsage.add(resp.Usage)
			} else {
				turnUsage.failed(err)
			}
			return fail(fmt.Errorf("LLM response has no choices"))
		}
		turnUsage.add(resp.Usage)
		previousResponseID = resp.ID
		// .
		// .
		// .
		// .
		drainedSteering = drainedSteering[:0]

		choice := resp.Choices[0]
		if !resp.Usage.Reported {
			silentSpent += estimateSilentCall(messages, toolDefs, choice.Message.Content)
		}
		silentSpent += resp.Usage.UnknownAttempts * estimateSilentCall(messages, toolDefs, choice.Message.Content)
		finalText = choice.Message.Content
		modelID = resp.ModelID
		if finalText != "" {
			spoken = append(spoken, finalText)
		}
		log.Printf("LLM iteration %d: finish=%s, toolCalls=%d, contentLen=%d", i, choice.FinishReason, len(choice.Message.ToolCalls), len(choice.Message.Content))

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if l.emit != nil {
			for _, tb := range choice.Message.Thinking {
				if strings.TrimSpace(tb.Text) != "" {
					l.emit.EmitToolEvent("thinking", "thinking", tb.Text)
				}
			}
		}

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if choice.FinishReason == "length" && !truncated {
			truncated = true
			spoken = append(spoken, declare("This reply was cut off by the model's output limit. Raise the model's max output tokens to see the rest."))
		}

		// .
		// .
		// .
		// .
		// .
		// .
		if note := unhandledStop(choice.FinishReason); note != "" {
			spoken = append(spoken, note)
			break
		}

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
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

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if len(choice.Message.ToolCalls) == 0 {
			if strings.TrimSpace(finalText) == "" {
				return fail(fmt.Errorf("LLM response has no text or tool calls"))
			}
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			if cfg.HeuristicNudges && !nudged && replyAnnouncesUntakenWork(finalText) {
				// .
				// .
				// .
				// .
				// .
				kind := "serial"
				nudge := "Continue — take the step you just described by calling the tool, rather than announcing it. Your turn continues automatically after each tool call."
				if breadthNudgeOn(cfg) && announcesIndependentSubgoals(finalText) {
					kind = "breadth"
					nudge = "Continue — take the step you just described. These steps look independent and read-only: consider spawning sub-agents for them (`work spawn`, one per step) and harvesting the outcomes, or proceed serially here if they are coupled."
				}
				candidate := append([]llm.Message(nil), messages...)
				candidate = append(candidate, choice.Message, llm.Message{
					Role:    "user",
					Content: nudge,
				})
				candidateFit := fit
				if fitRequest(&candidate, &candidateFit, systemBase,
					toolDefs, cfg.ContextBudgetTokens, l.transcript) == nil {
					nudged = true
					// .
					// .
					// .
					// .
					// .
					log.Printf("NUDGE %s sent: reply announced untaken work (fit ok)", kind)
					messages, fit = candidate, candidateFit
					continue
				}
			}
			break
		}

		// .
		messages = append(messages, choice.Message)

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		var callBudgetSurplus []llm.ToolCall
		if cfg.MaxToolCalls > 0 && toolCallsSoFar+len(choice.Message.ToolCalls) > cfg.MaxToolCalls {
			allowed := cfg.MaxToolCalls - toolCallsSoFar
			if allowed < 0 {
				allowed = 0
			}
			callBudgetSurplus = append(callBudgetSurplus, choice.Message.ToolCalls[allowed:]...)
			choice.Message.ToolCalls = choice.Message.ToolCalls[:allowed]
			log.Printf("CALL BUDGET: %d of %d calls spent — %d call(s) in this batch not executed",
				toolCallsSoFar+allowed, cfg.MaxToolCalls, len(callBudgetSurplus))
		}

		respOrdinals := make([]int, len(choice.Message.ToolCalls))
		for idx := range choice.Message.ToolCalls {
			ordinal++
			respOrdinals[idx] = ordinal
		}
		toolCallsSoFar += len(choice.Message.ToolCalls)

		preObs := map[int]Observation{}
		preStarted := map[int]bool{}
		if ps, ok := l.tools.(ParallelSafeExecutor); ok && len(choice.Message.ToolCalls) >= 2 {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			allSafe := true
			for _, tc := range choice.Message.ToolCalls {
				if !ps.ParallelSafe(tc) {
					allSafe = false
					break
				}
			}
			if allSafe {
				// .
				// .
				// .
				for idx, tc := range choice.Message.ToolCalls {
					log.Printf("Tool call: %s(%s)", tc.Function.Name, logPreview(tc.Function.Arguments))
					if l.emit != nil {
						l.emit.EmitToolEvent("tool_call", tc.Function.Name, tc.Function.Arguments)
					}
					if l.transcript != nil {
						if err := l.transcript.RecordToolStart(turnID, respOrdinals[idx], tc.ID, tc.Function.Name, tc.Function.Arguments, resp.ModelID); err != nil {
							return fail(fmt.Errorf("record tool start: %w", err))
						}
					}
					preStarted[idx] = true
				}
				sem := make(chan struct{}, 4)
				var mu sync.Mutex
				var wg sync.WaitGroup
				for idx := range choice.Message.ToolCalls {
					idx := idx
					tc := choice.Message.ToolCalls[idx]
					tc.EmissionOrdinal = respOrdinals[idx]
					wg.Add(1)
					sem <- struct{}{}
					go func() {
						defer wg.Done()
						defer func() { <-sem }()
						o := l.tools.Execute(llm.WithModelID(ctx, resp.ModelID), tc)
						mu.Lock()
						preObs[idx] = o
						if o.EndTurn {
							yielded = true
							if o.EndTurnAnswer != "" {
								yieldAnswer = o.EndTurnAnswer
							}
						}
						mu.Unlock()
					}()
				}
				wg.Wait()
				log.Printf("Parallel batch: %d read-only call(s) executed concurrently", len(choice.Message.ToolCalls))
			}
		}

		// .
		for tcIdx, tc := range choice.Message.ToolCalls {
			tc.EmissionOrdinal = respOrdinals[tcIdx]
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			if !preStarted[tcIdx] {
				log.Printf("Tool call: %s(%s)", tc.Function.Name, logPreview(tc.Function.Arguments))
				if l.emit != nil {
					l.emit.EmitToolEvent("tool_call", tc.Function.Name, tc.Function.Arguments)
				}
			}
			if l.tools == nil {
				return fail(fmt.Errorf("tool %q requested but no executor is available", tc.Function.Name))
			}
			obs, pre := preObs[tcIdx]
			if l.transcript != nil && !preStarted[tcIdx] {
				if err := l.transcript.RecordToolStart(turnID, respOrdinals[tcIdx], tc.ID, tc.Function.Name, tc.Function.Arguments, resp.ModelID); err != nil {
					return fail(fmt.Errorf("record tool start: %w", err))
				}
			}
			if !pre {
				obs = l.tools.Execute(llm.WithModelID(ctx, resp.ModelID), tc)
			}
			if obs.EndTurn {
				yielded = true
				if obs.EndTurnAnswer != "" {
					yieldAnswer = obs.EndTurnAnswer
				}
			}
			result := obs.Text
			// .
			// .
			// .
			if l.transcript != nil {
				if err := l.transcript.RecordToolDone(turnID, respOrdinals[tcIdx], tc.Function.Name, tc.Function.Arguments, result, obs.Failed, obs.Truncated); err != nil {
					return fail(fmt.Errorf("record tool result: %w", err))
				}
			}
			log.Printf("Tool result: %s", logPreview(result))
			// .
			// .
			// .
			retained := 0
			if l.transcript != nil {
				retained = l.transcript.TranscriptResultExcerptLimit()
			}
			modelResult := truncateToolResult(result, cfg.MaxToolResultChars, retained)
			messages = append(messages, llm.FormatToolResult(tc.ID, modelResult))

			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			truncated := modelResult != result
			if len([]rune(modelResult)) >= repeatResultMinRunes {
				h := resultHash(modelResult)
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
				if cfg.HeuristicNudges && repeats >= repeatResultThreshold && !repeatNudged {
					repeatNudged = true
					messages[len(messages)-1].Content += repeatResultNote(repeats, truncated)
					log.Printf("Repeat-result nudge sent: identical %d-rune result received %d times in the last %d calls (truncated=%v)",
						len([]rune(modelResult)), repeats, repeatResultWindow, truncated)
				}
			}
		}

		// .
		// .
		// .
		// .
		if len(callBudgetSurplus) > 0 {
			for _, tc := range callBudgetSurplus {
				messages = append(messages, llm.FormatToolResult(tc.ID,
					"not executed: child call budget exhausted"))
			}
			exhaustedCallBudget = true
			spoken = append(spoken, declare(
				"This run reached its budget of %d tool calls with work unfinished; %d call(s) in the final batch were not executed.",
				cfg.MaxToolCalls, len(callBudgetSurplus)))
			break
		}

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		acts := 0
		if cfg.IsAct != nil {
			for _, tc := range choice.Message.ToolCalls {
				if cfg.IsAct(tc) {
					acts++
				}
			}
			if acts > 0 {
				actSeen = true
			}
		}
		if cfg.IsAct != nil && cfg.PredictedThisTurn != nil && !firstActAsked && len(messages) > 0 && cfg.PredictedThisTurn() == 0 {
			if acts > 0 {
				firstActAsked = true
				scalarNudged = true
				active := true
				if cfg.PlanState != nil {
					_, active = cfg.PlanState()
				}
				messages[len(messages)-1].Content += firstActNote(active) + calibrationMirror(cfg.Calibration)
				log.Printf("NUDGE first-act sent: round %d, %d act(s) in the batch, session active=%v", i+1, acts, active)
			}
		}

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if cfg.PlanState != nil && !planNudged && i+1 >= planNudgeAfterRounds && len(messages) > 0 {
			if planned, active := cfg.PlanState(); !planned {
				planNudged = true
				messages[len(messages)-1].Content += planNudgeNote(i+1, active) + calibrationMirror(cfg.Calibration)
				variant := "no session — asked to start one and plan"
				if active {
					variant = "session active without a plan — asked to update it"
				}
				log.Printf("NUDGE plan sent: round %d, %s", i+1, variant)
			}
		}

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if cfg.PlanState != nil && cfg.PredictedThisTurn != nil && !scalarNudged && i+1 >= 1 && len(messages) > 0 && (cfg.IsAct == nil || actSeen) {
			if planned, _ := cfg.PlanState(); planned {
				missingSteps := cfg.PredictedThisTurn() == 0
				missingIndep := false
				if cfg.FanoutState != nil {
					declared, _ := cfg.FanoutState()
					missingIndep = declared == 0
				}
				if missingSteps || missingIndep {
					scalarNudged = true
					messages[len(messages)-1].Content += scalarAskNote(i+1, missingSteps, missingIndep, calibrationMirror(cfg.Calibration))
					log.Printf("NUDGE scalar sent: round %d, missing steps=%v independent=%v", i+1, missingSteps, missingIndep)
				}
			}
		}

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if softCapFinal < 0 && cfg.PredictedThisTurn != nil && len(messages) > 0 {
			limit := undeclaredSoftCapCalls
			declared := 0
			if p := cfg.PredictedThisTurn(); p > 0 {
				declared = p
				limit = p * declaredBudgetSlack
			}
			if toolCallsSoFar >= limit && i+softCapGraceRounds < cfg.MaxIterations-1 {
				softCapFinal = i + softCapGraceRounds
				messages[len(messages)-1].Content += checkpointNote(declared, toolCallsSoFar, softCapGraceRounds)
				log.Printf("NUDGE checkpoint sent: round %d, %d calls spent (declared %d, limit %d) — turn will continue in a fresh turn after %d more rounds",
					i+1, toolCallsSoFar, declared, limit, softCapGraceRounds)
			}
		}

		// .
		// .
		// .
		// .
		if cfg.FanoutState != nil && !fanoutNudged && i+1 >= fanoutNudgeAfterRounds && len(messages) > 0 {
			if declared, spawnedNow := cfg.FanoutState(); declared >= 2 && spawnedNow == 0 {
				fanoutNudged = true
				messages[len(messages)-1].Content += fanoutNudgeNote(declared)
				log.Printf("NUDGE fanout sent: round %d, %d independent step(s) declared, 0 spawned", i+1, declared)
			}
		}

		if remaining := cfg.MaxIterations - 1 - i; remaining > 0 && remaining <= toolBudgetWarnAt && len(messages) > 0 {
			messages[len(messages)-1].Content += toolBudgetNote(remaining)
			// .
			// .
			// .
			// .
			// .
			// .
			log.Printf("Tool budget warning sent: %d round(s) remain of %d", remaining, cfg.MaxIterations)
		}

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		var boundary []string
		if st := l.steering(); st != nil {
			if said := st.DrainSteering(); len(said) > 0 {
				drainedSteering = append(drainedSteering, said...)
				boundary = append(boundary, steeringNote(said))
			}
		}
		if i == cfg.MaxIterations-1 {
			boundary = append(boundary, "You've reached this turn's round limit. Please respond to me now with what you've found.")
		} else if yielded {
			boundary = append(boundary, "You yielded this turn. Reply with one line of status — your gate is free, and a delivery or message wakes you next.")
		} else if i == softCapFinal {
			boundary = append(boundary, "Budget checkpoint: reply now with a brief status of where this leg ended. The work session stays active and continues automatically in a fresh turn.")
		}
		if len(boundary) > 0 {
			messages = append(messages, llm.Message{Role: "user", Content: strings.Join(boundary, "\n\n")})
		}

		if i == cfg.MaxIterations-1 || i == softCapFinal || yielded {
			// .
			// .
			// .
			if !yielded && i == softCapFinal && i != cfg.MaxIterations-1 {
				continuedAtCap = true
			}
			finalTools, fitErr := fitFinalRequest(&messages, &fit, systemBase, toolDefs, cfg.ContextBudgetTokens, l.transcript)
			if fitErr != nil {
				return fail(fitErr)
			}
			// .
			resp, err := l.llm.Chat(ctx, messages, llm.ChatOptions{Tools: finalTools, DisableTools: true, ThinkingBudget: cfg.ThinkingBudget, PreviousResponseID: previousResponseID})
			if err != nil {
				turnUsage.failed(err)
				return fail(fmt.Errorf("LLM final call: %w", err))
			}
			turnUsage.add(resp.Usage)
			// .
			// .
			// .
			finalText, modelID, err = finalResponse(resp, "at the round limit")
			if err != nil {
				return fail(err)
			}
			capLine := declare("This turn reached its limit of %d rounds and was finished without further tools. Ask again to continue.", cfg.MaxIterations)
			// .
			// .
			// .
			exhaustedBudget = !yielded && !continuedAtCap
			if yielded {
				capLine = declare("Turn yielded after %d tool calls — the gate is free; a delivery or message wakes the next turn.", toolCallsSoFar)
			} else if continuedAtCap {
				capLine = declare("This turn ended at its declared budget after %d tool calls; the work continues automatically in a fresh turn.", toolCallsSoFar)
			}
			if yielded && yieldAnswer != "" {
				// .
				// .
				// .
				spoken = append(spoken, finalText, "Best current answer: "+yieldAnswer, capLine)
			} else {
				spoken = append(spoken, finalText, capLine)
			}
			break
		}
	}

	// .
	// .
	if len(spoken) > 0 {
		return Result{Spoken: strings.Join(spoken, "\n\n"), FinalText: finalText, ModelID: modelID, Usage: turnUsage, ContinuedAtCap: continuedAtCap, Yielded: yielded, ExhaustedBudget: exhaustedBudget, ExhaustedCallBudget: exhaustedCallBudget, RoundsUsed: roundsUsed, ToolCallsUsed: toolCallsSoFar}, nil
	}
	return Result{FinalText: finalText, ModelID: modelID, Usage: turnUsage, ContinuedAtCap: continuedAtCap, Yielded: yielded, ExhaustedBudget: exhaustedBudget, ExhaustedCallBudget: exhaustedCallBudget, RoundsUsed: roundsUsed, ToolCallsUsed: toolCallsSoFar}, nil
}

func finalResponse(response *llm.Response, circumstance string) (string, string, error) {
	if response == nil || len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return "", "", fmt.Errorf("LLM final response %s has no text", circumstance)
	}
	choice := response.Choices[0]
	if len(choice.Message.ToolCalls) > 0 {
		return "", response.ModelID, fmt.Errorf("LLM final response %s requested tools that were disabled", circumstance)
	}
	text := choice.Message.Content
	if choice.FinishReason == "length" {
		text += "\n\n" + declare("This reply was cut off by the model's output limit. Raise the model's max output tokens to see the rest.")
	}
	if note := unhandledStop(choice.FinishReason); note != "" {
		text += "\n\n" + note
	}
	return text, response.ModelID, nil
}

// .
// .
// .
// .
// .
type fitState struct {
	current  int
	omitted  int
	abridged int
	warned   bool
}

func fitRequest(messages *[]llm.Message, st *fitState, systemBase string,
	tools []llm.ToolDefinition, budget int, transcript Transcript) error {
	oldSystem := (*messages)[0].Content
	setFitSystem(&(*messages)[0], systemBase, st)
	if oldSystem != (*messages)[0].Content {
		resetProviderReasoning(messages)
	}
	for {
		err := llm.ValidateInput(*messages, tools, budget)
		if err == nil {
			before := (*messages)[0].Content
			warnIfTight(messages, st, systemBase, tools, budget)
			if before != (*messages)[0].Content {
				resetProviderReasoning(messages)
			}
			return llm.ValidateInput(*messages, tools, budget)
		}
		var limitErr *llm.ContextLimitError
		if !errors.As(err, &limitErr) {
			return err
		}

		if foldToolResult(*messages, st.current, transcript) {
			resetProviderReasoning(messages)
			continue
		}
		if st.current <= 1 {
			return err
		}

		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if oldest := (*messages)[1]; !strings.Contains(oldest.Content, prompt.SummaryMarker) {
			shrunk := prompt.SummarizeUnits(oldest.Content, historyRoute)
			cost := 0
			if st.abridged == 0 {
				cost = len(historyAbridgedNote(1))
			}
			if len(oldest.Content)-len(shrunk) > cost {
				(*messages)[1].Content = shrunk
				resetProviderReasoning(messages)
				st.abridged++
				setFitSystem(&(*messages)[0], systemBase, st)
				continue
			}
		}

		*messages = append((*messages)[:1], (*messages)[2:]...)
		resetProviderReasoning(messages)
		st.current--
		st.omitted++
		for st.current > 1 && (*messages)[1].Role != "user" {
			*messages = append((*messages)[:1], (*messages)[2:]...)
			resetProviderReasoning(messages)
			st.current--
			st.omitted++
		}
		setFitSystem(&(*messages)[0], systemBase, st)
	}
}

// .
// .
// .
const historyRoute = `recall(query="<a distinctive word or phrase>", source=conversation) reaches the full turn`

func setHistoryNote(system *llm.Message, base string, omitted, abridged int) {
	system.Content = base + HistoryOmissionNote(omitted) + historyAbridgedNote(abridged)
}

// .
// .
// .
const contextTightPercent = 85

// .
// .
// .
// .
// .
// .
const contextTightNote = "\n\nThe context for this turn is nearly full. Further tool calls may not fit, and if the request stops fitting your tools will be withdrawn and you will be asked to answer immediately. When you answer, report only what you actually did and name what remains undone."

// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
func historyAbridgedNote(abridged int) string {
	if abridged <= 0 {
		return ""
	}
	return fmt.Sprintf("\n%d older turn(s) are shown abridged; %s.", abridged, historyRoute)
}

// .
func HistoryOmissionNote(omitted int) string {
	if omitted <= 0 {
		return ""
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	return fmt.Sprintf("\n\n## Conversation context\n%d older conversation turns are not shown. Each is still individually searchable by its own words: recall(query=\"<a distinctive word or phrase>\", source=conversation).", omitted)
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

// .
// .
// .
const logPreviewRunes = 200

// .
// .
// .
// .
// .
// .
// .
// .
// .
func logPreview(s string) string {
	r := []rune(s)
	total := len(r)
	if total > logPreviewRunes {
		r = r[:logPreviewRunes]
	}
	flat := strings.NewReplacer("\n", "⏎", "\r", "").Replace(string(r))
	if total > logPreviewRunes {
		return flat + fmt.Sprintf("… (%d runes total)", total)
	}
	return flat
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

// .
// .
// .
// .
// .
func endsInAnnouncedIntent(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimRight(t, ".!)*_` ")
	if t == "" {
		return false
	}
	if i := strings.LastIndexAny(t, ".!?\n"); i >= 0 {
		t = strings.TrimSpace(t[i+1:])
	}
	// .
	// .
	// .
	// .
	// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func replyAnnouncesUntakenWork(text string) bool {
	t := strings.ToLower(text)
	for _, g := range []string{"let me know if", "leave you to it",
		"that's everything", "time to celebrate", "going to miss you"} {
		if strings.Contains(t, g) {
			return false
		}
	}
	if endsInAnnouncedIntent(text) {
		return true
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	markers := []string{"1.", "2.", "3.", "first", "second", "third", "next", "then", "after that"}
	hits := 0
	for _, m := range markers {
		if strings.Contains(t, m) {
			hits++
		}
	}
	if hits < 2 {
		return false
	}
	for _, sent := range strings.Split(t, ".!?\n") {
		if sentenceAnnouncesWork(sent) {
			return true
		}
	}
	return false
}

// .
// .
// .
func sentenceAnnouncesWork(sent string) bool {
	openers := []string{"let me ", "now let me ", "i'll ", "i will ", "i'm going to ", "i am going to "}
	verbs := []string{"read", "check", "look", "run", "open", "search", "find", "fix",
		"write", "edit", "grep", "list", "fetch", "examine", "inspect", "review",
		"explore", "trace", "scan", "test", "verify", "dig", "start", "create", "spawn", "try"}
	for _, o := range openers {
		i := strings.Index(sent, o)
		if i < 0 {
			continue
		}
		rest := strings.TrimLeft(sent[i+len(o):], " ")
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

// .
// .
// .
// .
// .
// .
// .
// .
func announcesIndependentSubgoals(text string) bool {
	// .
	// .
	// .
	// .
	// .
	// .
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	// .
	// .
	for _, c := range []string{"then", "after that", "based on", "depending on", "which will"} {
		if strings.Contains(t, c) {
			return false
		}
	}
	// .
	markers := []string{"1.", "2.", " - ", "first", "second", "next"}
	hits := 0
	for _, m := range markers {
		if strings.Contains(t, m) {
			hits++
		}
	}
	if hits < 2 {
		return false
	}
	// .
	// .
	roVerbs := []string{"read", "check", "look", "search", "find", "grep",
		"list", "fetch", "examine", "inspect", "review", "explore", "trace",
		"scan", "verify"}
	distinct := map[string]bool{}
	for _, v := range roVerbs {
		if strings.Contains(t, v) {
			distinct[v] = true
		}
	}
	return len(distinct) >= 2
}

// .
// .
// .
func breadthNudgeOn(cfg Config) bool {
	if !cfg.HeuristicNudges {
		return false
	}
	return cfg.BreadthNudge == nil || *cfg.BreadthNudge
}

// .
// .
// .
// .
// .
// .
// .
func unhandledStop(reason string) string {
	switch reason {
	case "", "stop", "tool_calls", "length":
		return ""
	case "model_context_window_exceeded":
		return declare("The reply was cut off because the model context window filled (model_context_window_exceeded). Continue in a fresh turn or reduce the retained context.")
	case "refusal":
		return declare("The model provider declined this request. That is the substrate's safety layer, not the identity's choice.")
	}
	return declare("The model stopped for a reason this build does not recognise (%s). The turn ended there.", reason)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
const turnContract = `

## Your turn
Your turn continues automatically after each tool call: take the next step by
calling its tool rather than describing it.

A tool call is the only record that anything happened. Report only what you
actually did — saying you have not done something, or do not know, is always
available and always acceptable. That grounding law governs what you claim, not
how you speak.`

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func declare(format string, args ...any) string {
	return "[" + fmt.Sprintf(format, args...) + "]"
}

// .
// .
// .
// .
const (
	repeatResultWindow    = 6
	repeatResultThreshold = 3
	repeatResultMinRunes  = 256
)

// .
// .
// .
// .
func interruptCause(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		return "stopped by the operator"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "the turn ran out of time"
	default:
		return "the substrate failed mid-turn"
	}
}

func resultHash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// .
// .
// .
// .
// .
// .
// .
func repeatResultNote(n int, truncated bool) string {
	if truncated {
		return fmt.Sprintf("\n\n[loop note — repeated result: you have now received this same content %d times in recent calls, TRUNCATED each time exactly as delivered above. Re-fetching returns the same truncated bytes, so it cannot recover the omitted part: work from what is here, or say plainly what is missing and how you would reach it.]", n)
	}
	return fmt.Sprintf("\n\n[loop note — repeated result: you have now received this same content %d times in recent calls. It is COMPLETE as delivered above, not truncated. Work from it, or state plainly what is missing from it; re-fetching returns these same bytes.]", n)
}

// .
// .
const toolBudgetWarnAt = 2

// .
// .
// .
// .
// .
// .
// .
// .
func steeringUnansweredNote(said []string) string {
	return "You said while I was working: " + strings.Join(said, " / ") + " — the turn failed before I could act on it, so I am repeating it here rather than losing it."
}

func steeringNote(said []string) string {
	return "The operator spoke while you were working:\n\n" + strings.Join(said, "\n\n")
}

// .
// .
// .
// .
// .
// .
const (
	declaredBudgetSlack    = 2
	undeclaredSoftCapCalls = 32
	softCapGraceRounds     = 4
)

// .
// .
// .
func checkpointNote(declared, spent, grace int) string {
	if declared > 0 {
		return declare("Declared budget reached: you estimated %d calls and have spent %d. "+
			"Checkpoint NOW — `work update next_move=` with the exact resume point (revise plan= if it drifted), "+
			"and say which this is: done with its scope, continue from that point, or stop with why. "+
			"You have %d more rounds; then this turn ends and the work continues automatically in a fresh turn.",
			declared, spent, grace)
	}
	return declare("This turn has spent %d calls with no declared budget. "+
		"Checkpoint NOW — `work update next_move=` with the exact resume point, declare steps= for the remainder, "+
		"and say which this is: done with its scope, continue from that point, or stop with why. "+
		"You have %d more rounds; then this turn ends and the work continues automatically in a fresh turn.",
		spent, grace)
}

// .
// .
// .
// .
const planNudgeAfterRounds = 8

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func planNudgeNote(round int, sessionActive bool) string {
	if sessionActive {
		// .
		// .
		// .
		return declare("The brief above asked for a plan before your first act; you are %d rounds into this turn and the active work session has no plan. "+
			"Call `work update plan=` on it with what you still need to know before you can act "+
			"and the steps that remain, marking which are independent — and declare the numbers: "+
			"`steps=` (tool calls you expect) and `independent=` (how many remaining steps could run "+
			"as concurrent spawns; 0 if coupled). Do not start another session. Then continue.", round)
	}
	return declare("You are %d rounds into this turn with no work session. "+
		"`work start` one — `work update` refuses without it — "+
		"then `work update plan=` with what you still need to know before you can act "+
		"and the steps that remain, marking which are independent — and declare the numbers: "+
		"`steps=` (tool calls you expect) and `independent=` (how many remaining steps could run "+
		"as concurrent spawns; 0 if coupled). Then continue.", round)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func firstActNote(sessionActive bool) string {
	const plan = "`work update plan=` with what you examined and what it IS, one line naming the falsifier, " +
		"`steps=` as the count the traced plan implies, and `independent=` (0 if coupled) — then act."
	if sessionActive {
		return declare("This round made your first call that changes something and this turn has no `steps=`. " +
			"Write the plan now, once: " + plan)
	}
	return declare("This round made your first call that changes something with no work session and no `steps=`. " +
		"`work start` one — `work update` refuses without it — then, once, " + plan)
}

// .
// .
// .
// .
func calibrationMirror(calib func() (int, int, int)) string {
	if calib == nil {
		return ""
	}
	plans, predicted, actual := calib()
	if plans == 0 {
		return ""
	}
	return fmt.Sprintf(" Mirror: your last %d estimate(s) totalled %d calls; the turns took %d.", plans, predicted, actual)
}

// .
// .
// .
func scalarAskNote(round int, missingSteps, missingIndep bool, mirror string) string {
	ask := ""
	switch {
	case missingSteps && missingIndep:
		ask = "declare `steps=` (calls you expect this turn) and `independent=` (remaining steps that could run as concurrent spawns; 0 if coupled)"
	case missingSteps:
		ask = "declare `steps=` (calls you expect this turn)"
	default:
		ask = "declare `independent=` (remaining steps that could run as concurrent spawns; 0 if coupled)"
	}
	return declare("You are %d rounds into this turn under a standing plan, but this turn's numbers are undeclared. "+
		"Call `work update` and %s — then continue.%s", round, ask, mirror)
}

// .
// .
// .
const fanoutNudgeAfterRounds = planNudgeAfterRounds + 4

// .
// .
// .
// .
func fanoutNudgeNote(declared int) string {
	return declare("You declared %d independent steps this turn and have spawned none. "+
		"Spawn them (`work spawn`, one per step — `role=` routes a read-heavy sub-goal to a cheaper configured model) "+
		"and harvest the outcomes, or state in your working state that they are coupled and continue serially.", declared)
}

// .
// .
func toolBudgetNote(remaining int) string {
	word := "rounds"
	if remaining == 1 {
		word = "round"
	}
	// .
	// .
	// .
	// .
	return fmt.Sprintf("\n\n[%d %s remain in this turn — a round may include several parallel tool calls. Close out with what you have ACTUALLY done, and name what you did not get to.]",
		remaining, word)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func platformNote() string {
	return "\n\nYou are running on " + platformName() +
		". The tools you can see are all the reach this platform gives you: what you did " +
		"elsewhere may not be possible here, which is a change of circumstance, not of who you are."
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func platformName() string {
	var base, tail string
	switch runtime.GOOS {
	case "darwin":
		base = "macOS (desktop"
	case "linux":
		base = "Linux (desktop"
	case "windows":
		base = "Windows (desktop"
	case "ios":
		base, tail = "iOS (mobile", ", no file writing"
	case "android":
		base, tail = "Android (mobile", ", no file writing"
	default:
		return runtime.GOOS
	}
	var lacks string
	if !hostcap.Can(hostcap.Shell).Available {
		lacks += ", no shell"
	}
	if !hostcap.Can(hostcap.Subprocess).Available {
		lacks += ", no subprocesses"
	}
	return base + lacks + tail + ")"
}

// .
// .
// .
// .
// .
func systemAdditions() string { return turnContract + platformNote() }

// .
// .
// .
// .
// .
// .
const (
	degenTailRunes     = 3000
	degenMinLines      = 10
	degenLineRatio     = 0.4
	degenMinSentences  = 14
	degenSentenceRatio = 0.5
)

// .
// .
// .
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

// .
// .
func estimateSilentCall(msgs []llm.Message, tools []llm.ToolDefinition, reply string) int {
	// .
	// .
	// .
	// .
	// .
	// .
	if n, err := llm.EstimateInputTokens(msgs, tools); err == nil {
		return n + tokenestimate.Estimate(reply)
	}
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
	}
	b.WriteString(reply)
	return tokenestimate.Estimate(b.String())
}

// .
// .
func resetProviderReasoning(messages *[]llm.Message) {
	if llm.ResetThinking(*messages) == 0 || len(*messages) == 0 {
		return
	}
	last := len(*messages) - 1
	(*messages)[last].Content += "\n[Earlier provider reasoning was reset because its context changed. Re-derive from the retained evidence; recall reaches recorded facts.]"
}
func fitFinalRequest(messages *[]llm.Message, st *fitState, base string, defs []llm.ToolDefinition, budget int, tr Transcript) ([]llm.ToolDefinition, error) {
	err := fitRequest(messages, st, base, defs, budget, tr)
	if err == nil {
		return defs, nil
	}
	var limit *llm.ContextLimitError
	if !errors.As(err, &limit) {
		return nil, err
	}
	resetProviderReasoning(messages)
	return nil, fitRequest(messages, st, base, nil, budget, tr)
}

func setFitSystem(system *llm.Message, base string, st *fitState) {
	setHistoryNote(system, base, st.omitted, st.abridged)
	if st.warned {
		system.Content += contextTightNote
	}
}
