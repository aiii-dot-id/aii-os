package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"math"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

type LLMClient interface {
	Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error)
}

type toolWithholder interface {
	WithheldTools(defs []llm.ToolDefinition) (offered []llm.ToolDefinition, withheld []string)
}

func toolNotOffered(name string) string {
	return declare("%s is not offered this turn: the model provider would not accept its definition", name)
}

type Observation struct {
	Image                                  *llm.Image
	DurationMS                             *int64
	WorkSession, SessionReason, ReasonCode string
	Text                                   string
	Failed                                 bool

	Cancelled bool

	Refused   bool
	Truncated bool

	HostNote string

	EndTurn bool

	EndTurnAnswer string

	ExitStatus *int
}

type ToolExecutor interface {
	Execute(ctx context.Context, call llm.ToolCall) Observation
}

type ParallelSafeExecutor interface {
	ParallelSafe(call llm.ToolCall) bool
}

type ToolDefiner interface {
	ToolDefinitions(turn Checkpoint) []llm.ToolDefinition
}

type Checkpoint struct {
	Calls              limits.Ceiling
	GraceRounds, Slack int
}

func (c Checkpoint) declaredLimit(p int) int {
	if p > math.MaxInt/c.Slack {
		return math.MaxInt
	}
	return p * c.Slack
}

type shownKey struct{}

func withShown(ctx context.Context, defs []llm.ToolDefinition) context.Context {
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Function.Name] = true
	}
	return context.WithValue(ctx, shownKey{}, names)
}

func ShownThisTurn(ctx context.Context, name string) (shown, inTurn bool) {
	names, ok := ctx.Value(shownKey{}).(map[string]bool)
	return names[name], ok
}

type Transcript interface {
	RecordToolStart(turnID string, ordinal int, callID, tool, args, model string) error

	RecordToolDone(turnID string, ordinal int, tool, args string, obs Observation) error
	TranscriptResultExcerptLimit() int
}

type durableToolTranscript interface {
	RecordToolDoneDurable(turnID string, ordinal int, tool, args string, obs Observation) (bool, error)
}

type Emitter interface {
	EmitToolEvent(kind, name, args string)
}

type Steering interface {
	DrainSteering() []string
}

type Config struct {
	MaxIterations       limits.Ceiling
	MaxToolResultChars  int
	ContextBudgetTokens int

	HistoryChunk int

	ContextBudgetFallback bool
	ThinkingBudget        int

	ToolSchemaRefusals int

	TurnTokenBudget limits.Ceiling

	PredictedThisTurn func() (remaining, ordinal int)

	Checkpoint Checkpoint

	ReplaySafe func(call llm.ToolCall) bool

	ImagesRefused func(ctx context.Context, refusal error, answered bool)

	BeforeFirstCall func() error

	ThinkingKept func() bool

	MaxToolCalls limits.Ceiling

	WrapUpAtCallLimit bool
}

const (
	callLimitResult = "not executed: this turn's call limit (%d) is reached"
	callLimitLine   = "This turn reached its limit of %d tool calls and was finished without further tools. Ask again to continue."
)

type Bounds struct {
	Rounds, TurnTokens, Calls limits.Ceiling
	Checkpoint                Checkpoint

	ToolResultChars int
}

type UnsetBound string

func (e UnsetBound) Error() string {
	return "conversation: " + string(e) + " is not set — the caller names every limit a turn is held to, and the loop holds no default of its own"
}

func (c Config) unset() error {
	for _, f := range []struct {
		name string
		zero bool
	}{
		{"MaxIterations", c.MaxIterations.IsZero()},
		{"TurnTokenBudget", c.TurnTokenBudget.IsZero()},
		{"MaxToolCalls", c.MaxToolCalls.IsZero()},
		{"Checkpoint.Calls", c.Checkpoint.Calls.IsZero()},
		{"Checkpoint.GraceRounds", c.Checkpoint.GraceRounds <= 0},
		{"Checkpoint.Slack", c.Checkpoint.Slack <= 0},
		{"ToolSchemaRefusals", c.ToolSchemaRefusals <= 0},
		{"MaxToolResultChars", c.MaxToolResultChars <= 0},
	} {
		if f.zero {
			return UnsetBound(f.name)
		}
	}
	return nil
}

type CallReasoning struct {
	Call  int
	Model string
	Text  string

	Account bool
}

type Result struct {
	Spoken    string
	FinalText string
	ModelID   string
	Usage     TurnUsage

	ContractErr error

	Reasoning []CallReasoning

	Interrupted string

	Unanswered []string

	ContinuedAtCap bool

	ContinuedAtPressure bool

	ContinuedAtTokenBudget bool

	Yielded bool

	RoundsUsed int

	ToolCallsUsed int

	ToolCallsEmitted int

	ToolCallCoverageComplete bool

	ExhaustedBudget bool

	ExhaustedCallBudget bool

	budgetLine string
}

func (r Result) NothingContinues(why string) string {
	if r.budgetLine == "" {
		return r.Spoken
	}
	return strings.TrimSuffix(r.Spoken, r.budgetLine) +
		declare("This turn ended at its budget after %d tool calls. Nothing continues by itself: %s. Ask again to continue.", r.ToolCallsUsed, why)
}

type TurnUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int

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

func (t TurnUsage) Complete() bool { return t.Calls > 0 && t.Silent == 0 && t.UnknownAttempts == 0 }

func (t TurnUsage) unreported() string {
	s := ""
	if t.Silent > 0 {
		s = fmt.Sprintf(", %d of them unreported", t.Silent)
	}
	if t.UnknownAttempts > 0 {
		s += fmt.Sprintf(", %d lost attempt(s) uncounted", t.UnknownAttempts)
	}
	return s
}

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

func reportedTotal(u llm.Usage) string {
	if !u.Reported {
		return "not reported"
	}
	return fmt.Sprint(u.TotalTokens)
}

func OneCall(u llm.Usage) TurnUsage {
	var t TurnUsage
	t.add(u)
	return t
}

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

func New(l LLMClient, ex ToolExecutor, d ToolDefiner, tr Transcript, em Emitter, cfg Config) *Loop {
	if cfg.ContextBudgetTokens <= 0 {
		cfg.ContextBudgetTokens = llm.DefaultContextBudgetTokens
	}
	return &Loop{llm: l, tools: ex, defs: d, transcript: tr, emit: em, cfg: cfg}
}

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
		contextBudgetTokens = llm.DefaultContextBudgetTokens
	}
	l.cfg.ContextBudgetTokens = contextBudgetTokens
	l.cfg.ThinkingBudget = thinkingBudget
}

func (l *Loop) SetTurnBounds(b Bounds) error {
	l.cfgMu.Lock()
	defer l.cfgMu.Unlock()
	next := l.cfg
	next.MaxIterations, next.TurnTokenBudget, next.MaxToolCalls, next.Checkpoint, next.MaxToolResultChars = b.Rounds, b.TurnTokens, b.Calls, b.Checkpoint, b.ToolResultChars
	for _, f := range []struct {
		name string
		zero bool
	}{
		{"Rounds", b.Rounds.IsZero()}, {"TurnTokens", b.TurnTokens.IsZero()}, {"Calls", b.Calls.IsZero()},
		{"Checkpoint.Calls", b.Checkpoint.Calls.IsZero()}, {"Checkpoint.GraceRounds", b.Checkpoint.GraceRounds <= 0}, {"Checkpoint.Slack", b.Checkpoint.Slack <= 0},
		{"ToolResultChars", b.ToolResultChars <= 0},
	} {
		if f.zero {
			return UnsetBound(f.name)
		}
	}
	l.cfg = next
	return nil
}

func (l *Loop) SetToolSchemaRefusals(n int) error {
	if n <= 0 {
		return UnsetBound("ToolSchemaRefusals")
	}
	l.cfgMu.Lock()
	defer l.cfgMu.Unlock()
	l.cfg.ToolSchemaRefusals = n
	return nil
}

func (l *Loop) SetContextBudgetFallback(fallback bool) {
	l.cfgMu.Lock()
	defer l.cfgMu.Unlock()
	l.cfg.ContextBudgetFallback = fallback
}

func (l *Loop) ContextBudgetFallback() bool {
	l.cfgMu.RLock()
	defer l.cfgMu.RUnlock()
	return l.cfg.ContextBudgetFallback
}

func (l *Loop) Run(ctx context.Context, systemPrompt string, history []llm.Message) (Result, error) {
	return l.RunSystem(ctx, llm.Message{Role: "system", Content: systemPrompt}, history, 0)
}

func (l *Loop) RunSystem(ctx context.Context, system llm.Message, history []llm.Message, omittedHistory int) (Result, error) {
	return l.RunTurn(ctx, system, history, omittedHistory, "")
}

func (l *Loop) RunTurn(ctx context.Context, system llm.Message, history []llm.Message, omittedHistory int, facts string) (Result, error) {
	t, err := l.newTurn(ctx, system, history, omittedHistory, facts)
	if err != nil {
		return Result{}, err
	}

	softCapFinal := -1
	yielded := false
	yieldAnswer := ""
	continuedAtCap := false
	continuedAtPressure := false
	continuedAtTokenBudget := false
	exhaustedBudget := false
	exhaustedCallBudget := false
	budgetLine := ""

	silentSpent := 0
	degenNudged := false
	truncated := false
	for i := 0; t.cfg.MaxIterations.Allows(i); i++ {

		if err := t.ctx.Err(); err != nil {
			return t.fail(err)
		}
		t.roundsUsed = i + 1

		if i > 0 && !t.cfg.TurnTokenBudget.Allows(t.turnUsage.TotalTokens+silentSpent) {
			continuedAtTokenBudget = true
			addBoundaryNote(&t.messages, &t.fit, budgetPressureNote)
			if err := t.finalCall("LLM final call under budget pressure", "under budget pressure", false); err != nil {
				return t.fail(err)
			}

			spent := fmt.Sprint(t.turnUsage.TotalTokens + silentSpent)
			if !t.turnUsage.Complete() {
				spent = "about " + spent
			}
			budgetLine = declare("This turn's token budget (%s) is spent: %s tokens over %d calls. The answer above ends the turn; while its work session is still active, the work continues automatically in a fresh turn.",
				t.cfg.TurnTokenBudget, spent, t.turnUsage.Calls)
			t.spoken = append(t.spoken, t.finalText, budgetLine)
			logsink.InfoCtx(t.ctx, "turn.budget", "TURN BUDGET: fence at %s tokens — %s spent over %d call(s)%s; turn ended with a bounded wrap-up",
				t.cfg.TurnTokenBudget, spent, t.turnUsage.Calls, t.turnUsage.unreported())
			break
		}
		if i > 0 {
			t.refreshDefs()
		}
		if err := fitRequest(&t.messages, &t.fit, t.systemBase,
			t.toolDefs, t.cfg.ContextBudgetTokens, l.transcript); err != nil {
			if i == 0 {
				return t.fail(err)
			}

			continuedAtPressure = true
			addBoundaryNote(&t.messages, &t.fit, contextPressureNote)
			if err := t.finalCall("LLM final call under context pressure", "under context pressure", false); err != nil {
				return t.fail(err)
			}
			budgetLine = declare("The context filled during this turn after %d tool calls. The answer above was finished without further tool calls, and older turns were dropped from the request; while its work session is still active, the work continues automatically in a fresh turn.", t.toolCallsSoFar)
			t.spoken = append(t.spoken, t.finalText, budgetLine)
			break
		}

		if i == 0 && t.cfg.BeforeFirstCall != nil {
			if err := t.cfg.BeforeFirstCall(); err != nil {
				return t.fail(fmt.Errorf("before first provider call: %w", err))
			}
		}
		resp, err := t.ask(func() llm.ChatOptions {
			return llm.ChatOptions{Tools: t.toolDefs, ExactTools: t.scoped, ThinkingBudget: t.cfg.ThinkingBudget, PreviousResponseID: t.previousResponseID}
		})
		if err != nil {
			t.turnUsage.failed(err)
			return t.fail(fmt.Errorf("LLM call: %w", err))
		}
		if resp == nil || len(resp.Choices) == 0 {
			if resp != nil {

				t.turnUsage.add(resp.Usage)
			} else {

				t.turnUsage.failed(err)
				return t.fail(fmt.Errorf("LLM response has no choices"))
			}
			return t.fail(emptyReply("LLM response has no choices"))
		}
		t.turnUsage.add(resp.Usage)
		t.keepReasoning(resp)
		t.previousResponseID = resp.ID

		t.drainedSteering = t.drainedSteering[:0]

		choice := t.numberEmissions(resp.Choices[0])
		if !resp.Usage.Reported {
			silentSpent += estimateSilentCall(t.messages, t.toolDefs, choice.Message.Content)
		}
		silentSpent += resp.Usage.UnknownAttempts * estimateSilentCall(t.messages, t.toolDefs, choice.Message.Content)
		t.finalText = choice.Message.Content
		t.modelID = resp.ModelID
		if t.finalText != "" {
			t.spoken = append(t.spoken, t.finalText)
		}

		logsink.InfoCtx(t.ctx, "llm.return", "LLM iteration %d: finish=%s, toolCalls=%d, contentLen=%d, model=%s, tokens=%s, %dms, id=%s",
			i, choice.FinishReason, len(choice.Message.ToolCalls), len(choice.Message.Content),
			resp.ModelID, reportedTotal(resp.Usage), resp.Duration.Milliseconds(), resp.CallID)

		if l.emit != nil {
			for _, tb := range choice.Message.Thinking {
				if strings.TrimSpace(tb.Text) != "" {
					l.emit.EmitToolEvent("thinking", "thinking", tb.Text)
				}
			}
		}

		if choice.FinishReason == "length" && !truncated {
			truncated = true
			t.spoken = append(t.spoken, declare("This reply was cut off by the model's output limit. Raise the model's max output tokens to see the rest."))
		}

		if note := unhandledStop(choice.FinishReason); note != "" {
			if err := t.refuseCalls(resp.ModelID, choice.Message.ToolCalls, ReasonToolStopRefused, "Not run: the provider ended this response with an unsupported or refused stop reason."); err != nil {
				return t.fail(err)
			}
			t.spoken = append(t.spoken, note)
			break
		}

		if degenerateEmission(choice.Message.Content) {
			if err := t.refuseCalls(resp.ModelID, choice.Message.ToolCalls, ReasonToolDegenerate, "Not run: degenerate repetition made this response's calls untrustworthy."); err != nil {
				return t.fail(err)
			}
			if degenNudged {
				t.spoken = append(t.spoken, declare("Degenerate repetition was detected in the model's output twice this turn; the turn ends here rather than spending further."))
				logsink.WarnCtx(t.ctx, "turn.decision", "DEGENERATE OUTPUT: second detection (len %d) — turn ended", len(choice.Message.Content))
				break
			}
			degenNudged = true
			if len(choice.Message.ToolCalls) == 0 {
				logsink.WarnCtx(t.ctx, "turn.decision", "DEGENERATE OUTPUT: repetition detected in a final answer (len %d) — marked", len(choice.Message.Content))
				t.spoken = append(t.spoken, declare("Degenerate repetition was detected in this reply; treat its repeating tail as noise, not conclusions."))
				break
			}
			logsink.WarnCtx(t.ctx, "turn.decision", "DEGENERATE OUTPUT: repetition detected in emission (len %d) — tool calls discarded, corrective note sent", len(choice.Message.Content))
			t.messages = append(t.messages, llm.Message{Role: "assistant", Content: choice.Message.Content})
			t.messages = append(t.messages, llm.Message{Role: "user",
				Content: "[loop note — your last output degenerated into repetition, and its tool calls were discarded unexecuted. Stop. State in ONE short sentence the single next tool call, then emit exactly that call — or give the final answer in plain prose.]"})
			continue
		}

		if len(choice.Message.ToolCalls) == 0 {
			if strings.TrimSpace(t.finalText) == "" && choice.FinishReason != "length" {
				return t.fail(emptyReply("LLM response has no text or tool calls"))
			}
			break
		}

		t.messages = append(t.messages, choice.Message)

		var callBudgetSurplus []llm.ToolCall
		if !t.cfg.MaxToolCalls.Unlimited && t.toolCallsSoFar+len(choice.Message.ToolCalls) > t.cfg.MaxToolCalls.N {
			allowed := t.cfg.MaxToolCalls.N - t.toolCallsSoFar
			if allowed < 0 {
				allowed = 0
			}
			callBudgetSurplus = append(callBudgetSurplus, choice.Message.ToolCalls[allowed:]...)
			choice.Message.ToolCalls = choice.Message.ToolCalls[:allowed]
			logsink.InfoCtx(t.ctx, "turn.budget", "CALL BUDGET: %d of %d calls spent — %d call(s) in this batch not executed",
				t.toolCallsSoFar+allowed, t.cfg.MaxToolCalls.N, len(callBudgetSurplus))
		}

		yielded, yieldAnswer, err = t.executeCalls(resp.ModelID, choice.Message.ToolCalls)
		if err != nil {
			return t.fail(err)
		}

		if len(callBudgetSurplus) > 0 && t.cfg.WrapUpAtCallLimit {
			for _, tc := range callBudgetSurplus {
				t.messages = append(t.messages, llm.FormatToolResult(tc.ID, fmt.Sprintf(callLimitResult, t.cfg.MaxToolCalls.N)))
			}
			exhaustedCallBudget = true
			if err := t.finalCall("LLM final call at the call limit", "at the call limit", true); err != nil {
				return t.fail(err)
			}
			t.spoken = append(t.spoken, t.finalText, declare(callLimitLine, t.cfg.MaxToolCalls.N))
			break
		}
		if len(callBudgetSurplus) > 0 {
			if err := t.refuseCalls(resp.ModelID, callBudgetSurplus, ReasonToolCallBudget, "not executed: child call budget exhausted"); err != nil {
				return t.fail(err)
			}
			for _, tc := range callBudgetSurplus {
				t.messages = append(t.messages, llm.FormatToolResult(tc.ID,
					"not executed: child call budget exhausted"))
			}
			exhaustedCallBudget = true
			t.spoken = append(t.spoken, declare(
				"This run reached its budget of %d tool calls with work unfinished; %d call(s) in the final batch were not executed.",
				t.cfg.MaxToolCalls.N, len(callBudgetSurplus)))
			break
		}

		if softCapFinal < 0 && t.cfg.PredictedThisTurn != nil && len(t.messages) > 0 {
			limit, grace := t.cfg.Checkpoint.Calls, t.cfg.Checkpoint.GraceRounds
			declared, declarationOrdinal := 0, 0
			spent := t.toolCallsSoFar
			if p, ordinal := t.cfg.PredictedThisTurn(); p > 0 && ordinal >= 0 && ordinal <= t.ordinal {
				declared, declarationOrdinal = p, ordinal
				limit = limits.Of(t.cfg.Checkpoint.declaredLimit(p))

				spent = t.ordinal - ordinal
			}

			if !limit.Allows(spent) && (t.cfg.MaxIterations.Unlimited || grace < t.cfg.MaxIterations.N-1-i) {
				softCapFinal = i + grace
				t.messages[len(t.messages)-1].Content += checkpointNote(declared, spent, grace)
				logsink.InfoCtx(t.ctx, "nudge.decision", "NUDGE checkpoint sent: round %d, %d calls spent in budget window (declared %d at ordinal %d, limit %s) — turn will continue in a fresh turn after %d more rounds",
					i+1, spent, declared, declarationOrdinal, limit, grace)
			}
		}

		if remaining := t.cfg.MaxIterations.N - 1 - i; !t.cfg.MaxIterations.Unlimited && remaining > 0 && remaining <= toolBudgetWarnAt && len(t.messages) > 0 {
			t.messages[len(t.messages)-1].Content += toolBudgetNote(remaining)

			logsink.InfoCtx(t.ctx, "turn.budget", "Tool budget warning sent: %d round(s) remain of %d", remaining, t.cfg.MaxIterations.N)
		}

		var boundary []string

		shown := t.roundImages
		t.roundImages = nil
		if len(shown) > 0 {

			boundary = append(boundary, declare("sent here: the %d picture(s) this round's results describe, in the order read", len(shown)))
			logsink.InfoCtx(t.ctx, "turn.decision", "%d picture(s) the identity read this round ride the round's boundary message", len(shown))
		}
		if st := l.steering(); st != nil {
			if said := st.DrainSteering(); len(said) > 0 {
				t.drainedSteering = append(t.drainedSteering, said...)
				boundary = append(boundary, steeringNote(said))
			}
		}
		if t.cfg.MaxIterations.Last(i) {
			boundary = append(boundary, "You've reached this turn's round limit. Please respond to me now with what you've found.")
		} else if yielded {
			boundary = append(boundary, "You yielded this turn. Reply with one line of status — your gate is free, and a delivery or message wakes you next.")
		} else if i == softCapFinal {
			boundary = append(boundary, "Budget checkpoint: reply now with a brief status of where this leg ended. While the work session is still active, the work continues automatically in a fresh turn.")
		}
		if len(boundary) > 0 {
			t.messages = append(t.messages, llm.Message{Role: "user", Content: strings.Join(boundary, "\n\n"), Images: shown})
		}

		if t.cfg.MaxIterations.Last(i) || i == softCapFinal || yielded {

			if !yielded && i == softCapFinal && !t.cfg.MaxIterations.Last(i) {
				continuedAtCap = true
			}

			if err := t.finalCall("LLM final call", "at the round limit", true); err != nil {
				return t.fail(err)
			}
			capLine := declare("This turn reached its limit of %d rounds and was finished without further tools. Ask again to continue.", t.cfg.MaxIterations.N)

			exhaustedBudget = !yielded && !continuedAtCap
			if yielded {
				capLine = declare("Turn yielded after %d tool calls — the gate is free; a delivery or message wakes the next turn.", t.toolCallsSoFar)
			} else if continuedAtCap {
				capLine = declare("This turn ended at its declared budget after %d tool calls; the work continues automatically in a fresh turn.", t.toolCallsSoFar)
				budgetLine = capLine
			}
			if yielded && yieldAnswer != "" {

				t.spoken = append(t.spoken, t.finalText, "Best current answer: "+yieldAnswer, capLine)
			} else {
				t.spoken = append(t.spoken, t.finalText, capLine)
			}
			break
		}
	}

	result := Result{Spoken: strings.Join(t.spoken, "\n\n"), FinalText: t.finalText, ModelID: t.modelID, Usage: t.turnUsage, ContinuedAtCap: continuedAtCap, ContinuedAtPressure: continuedAtPressure, ContinuedAtTokenBudget: continuedAtTokenBudget, Yielded: yielded, ExhaustedBudget: exhaustedBudget, ExhaustedCallBudget: exhaustedCallBudget, RoundsUsed: t.roundsUsed, ToolCallsUsed: t.toolCallsSoFar, ToolCallsEmitted: t.ordinal, ToolCallCoverageComplete: t.emissionCoverageComplete(), Reasoning: t.reasoning, budgetLine: budgetLine}

	if format := requestResponseFormat(t.ctx); format != nil {
		result.ContractErr = llm.ValidateStructuredOutput(format, t.finalText)
	}
	return result, nil
}

type turn struct {
	l   *Loop
	cfg Config

	ctx context.Context

	messages []llm.Message
	fit      fitState

	systemBase         string
	toolDefs           []llm.ToolDefinition
	previousResponseID string

	spoken                     []string
	finalText                  string
	modelID                    string
	turnUsage                  TurnUsage
	toolCallsSoFar             int
	toolCallsRecorded          int
	emissionCoverageIncomplete bool

	schemaRefusals int

	refusedDefs map[string]bool
	roundsUsed  int
	reasoning   []CallReasoning
	calls       int

	drainedSteering []string

	roundImages []llm.Image

	turnID  string
	ordinal int

	scoped  bool
	offered map[string]bool

	plainFacts string
}

func (l *Loop) newTurn(ctx context.Context, system llm.Message, history []llm.Message, omittedHistory int, facts string) (*turn, error) {
	l.cfgMu.RLock()
	cfg := l.cfg
	l.cfgMu.RUnlock()
	if err := cfg.unset(); err != nil {
		return nil, err
	}
	if len(history) == 0 || history[len(history)-1].Role != "user" {
		return nil, fmt.Errorf("conversation context: current input must be the final user message")
	}
	if omittedHistory < 0 {
		omittedHistory = 0
	}
	for len(history) > 1 && !opensExchange(history[0]) {
		if !history[0].Qualifies {
			omittedHistory++
		}
		history = history[1:]
	}

	toolDefs := []llm.ToolDefinition{}
	if l.defs != nil {
		toolDefs = l.defs.ToolDefinitions(cfg.Checkpoint)
	}

	toolDefs, err := FilterRequestTools(ctx, toolDefs)
	if err != nil {
		return nil, err
	}

	var withheld []string
	if w, ok := l.llm.(toolWithholder); ok {
		toolDefs, withheld = w.WithheldTools(toolDefs)
	}

	plainFacts := facts
	limits, err := LimitsNote(ctx, toolDefs)
	if err != nil {
		return nil, err
	}
	facts = withLimits(limits, plainFacts)

	system.Role = "system"

	messages := append([]llm.Message{system}, history...)
	fit := fitState{current: len(messages) - 1, omitted: omittedHistory, baseOmitted: omittedHistory,
		fallback: cfg.ContextBudgetFallback, readOnly: cfg.ReplaySafe, facts: facts, chunk: cfg.HistoryChunk,
		thinking: cfg.ThinkingKept != nil && cfg.ThinkingKept()}
	fit.base, fit.based = messages[fit.current].Content, true
	for _, name := range withheld {
		fit.base += "\n" + toolNotOffered(name)
	}

	messages[fit.current].CacheBefore = true
	renderCurrent(messages, &fit)

	turnID := interaction.TurnID(ctx)
	if turnID == "" {
		turnID = "turn_" + uuid.New().String()
	}
	ctx = interaction.WithTurn(ctx, turnID)

	ctx = withShown(ctx, toolDefs)

	ctx = logsink.WithSource(ctx, logsink.Source{Turn: shortTurn(turnID)})
	refusedDefs := map[string]bool{}
	for _, name := range withheld {
		refusedDefs[name] = true
	}
	offered := make(map[string]bool, len(toolDefs))
	for _, d := range toolDefs {
		offered[d.Function.Name] = true
	}
	return &turn{l: l, cfg: cfg, ctx: ctx, messages: messages, fit: fit,
		systemBase: system.Content, toolDefs: toolDefs, turnID: turnID, refusedDefs: refusedDefs,
		scoped: requestScoped(ctx), offered: offered, plainFacts: plainFacts}, nil
}

func (t *turn) keepReasoning(r *llm.Response) {
	t.calls++
	if r == nil || len(r.Choices) == 0 {
		return
	}
	text, account := r.Choices[0].Reasoning, false
	if strings.TrimSpace(text) == "" {
		text, account = r.Choices[0].ReasoningAccount, true
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	t.reasoning = append(t.reasoning, CallReasoning{Call: t.calls, Model: r.ModelID, Text: text, Account: account})
}

func (t *turn) fail(err error) (Result, error) {
	r := Result{Usage: t.turnUsage, ModelID: t.modelID, ToolCallsUsed: t.toolCallsSoFar, ToolCallsEmitted: t.ordinal, ToolCallCoverageComplete: t.emissionCoverageComplete(), RoundsUsed: t.roundsUsed, Reasoning: t.reasoning}
	if len(t.drainedSteering) > 0 {
		r.Unanswered = append([]string(nil), t.drainedSteering...)
	}
	if len(t.spoken) > 0 {
		r.Spoken = strings.Join(t.spoken, "\n\n")
	}
	if len(t.spoken) > 0 || len(r.Unanswered) > 0 {
		r.Interrupted = interruptCause(t.ctx, err)
	}

	var stop TurnStop
	if errors.Is(err, context.Canceled) && errors.As(context.Cause(t.ctx), &stop) && !errors.Is(err, stop) {
		err = fmt.Errorf("%w: %w", err, stop)
	}
	return r, err
}

func (t *turn) refreshDefs() {
	if t.l.defs == nil {
		return
	}
	defs, err := FilterRequestTools(t.ctx, t.l.defs.ToolDefinitions(t.cfg.Checkpoint))
	if err != nil {
		return
	}
	if w, ok := t.l.llm.(toolWithholder); ok {
		defs, _ = w.WithheldTools(defs)
	}
	kept := withoutRefused(defs, t.refusedDefs)
	if sameToolNames(kept, t.toolDefs) {
		return
	}
	t.toolDefs = kept
	t.ctx = withShown(t.ctx, kept)
	t.offered = make(map[string]bool, len(kept))
	for _, d := range kept {
		t.offered[d.Function.Name] = true
	}
	if limits, lerr := LimitsNote(t.ctx, t.toolDefs); lerr == nil && limits != "" {
		t.fit.facts = withLimits(limits, t.plainFacts)
	}
}

func sameToolNames(a, b []llm.ToolDefinition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Function.Name != b[i].Function.Name {
			return false
		}
	}
	return true
}

func (t *turn) leaveOutRefusedTool(err error) bool {
	var refused *llm.ToolSchemaRefusedError
	if !errors.As(err, &refused) || t.schemaRefusals >= t.cfg.ToolSchemaRefusals {
		return false
	}
	at := -1
	for i, d := range t.toolDefs {
		if d.Function.Name == refused.Tool {
			at = i
			break
		}
	}
	if at < 0 {
		return false
	}

	kept := make([]llm.ToolDefinition, 0, len(t.toolDefs)-1)
	kept = append(append(kept, t.toolDefs[:at]...), t.toolDefs[at+1:]...)
	t.toolDefs = kept
	t.schemaRefusals++
	t.refusedDefs[refused.Tool] = true

	if limits, lerr := LimitsNote(t.ctx, t.toolDefs); lerr == nil && limits != "" {
		t.fit.facts = withLimits(limits, t.plainFacts)
	}
	note := toolNotOffered(refused.Tool)
	if last := len(t.messages) - 1; last == t.fit.current {
		t.fit.base += "\n" + note
	} else {
		t.messages[last].Content += "\n" + note
	}
	renderCurrent(t.messages, &t.fit)
	logsink.WarnCtx(t.ctx, "llm.refusal", "the tool %q is left out of this turn: the model provider would not accept its definition (%d of at most %d this turn)",
		refused.Tool, t.schemaRefusals, t.cfg.ToolSchemaRefusals)
	return true
}

func (t *turn) leaveOutRefusedImages(err error) bool {
	var refused *llm.HTTPError
	var tool *llm.ToolSchemaRefusedError
	if !errors.As(err, &refused) || errors.As(err, &tool) {
		return false
	}

	switch refused.StatusCode {
	case 400, 413, 415, 422:
	default:
		return false
	}
	left := 0
	note := declare("the model's provider refused the request that carried the picture(s) sent here (HTTP %d), so it was sent again without them and you were not shown them", refused.StatusCode)
	for i := range t.messages {
		if len(t.messages[i].Images) == 0 {
			continue
		}
		left += len(t.messages[i].Images)
		t.messages[i].Images = nil
		if i == t.fit.current {
			t.fit.base += "\n" + note
			renderCurrent(t.messages, &t.fit)
		} else {
			t.messages[i].Content += "\n" + note
		}
	}
	if left == 0 {
		return false
	}
	logsink.WarnCtx(t.ctx, "llm.refusal", "the provider refused a request that carried %d picture(s) (HTTP %d): sent again without them", left, refused.StatusCode)
	if t.cfg.ImagesRefused != nil {
		t.cfg.ImagesRefused(t.ctx, err, t.toolCallsSoFar > 0 || len(t.spoken) > 0)
	}
	return true
}

func (t *turn) ask(opts func() llm.ChatOptions) (*llm.Response, error) {
	resp, err := t.l.llm.Chat(t.ctx, t.messages, opts())
	for err != nil && (t.leaveOutRefusedTool(err) || t.leaveOutRefusedImages(err)) {
		if llm.UnknownAttempts(err) > 0 {
			t.emissionCoverageIncomplete = true
		}
		t.turnUsage.failed(err)
		resp, err = t.l.llm.Chat(t.ctx, t.messages, opts())
	}

	if err != nil || resp == nil || len(resp.Choices) == 0 {
		t.emissionCoverageIncomplete = true
	} else if resp.Usage.UnknownAttempts > 0 {
		t.emissionCoverageIncomplete = true
	}
	return resp, err
}

func (t *turn) numberEmissions(choice llm.Choice) llm.Choice {
	if choice.FinishReason == "length" || choice.FinishReason == "model_context_window_exceeded" {

		t.emissionCoverageIncomplete = true
	}
	choice.Message.ToolCalls = slices.Clone(choice.Message.ToolCalls)
	for i := range choice.Message.ToolCalls {
		t.ordinal++
		choice.Message.ToolCalls[i].EmissionOrdinal = t.ordinal
	}
	return choice
}

func (t *turn) emissionCoverageComplete() bool {
	return t.l.transcript != nil && !t.emissionCoverageIncomplete && t.toolCallsRecorded == t.ordinal
}

func (t *turn) recordToolStart(model string, tc llm.ToolCall) error {
	if t.l.transcript != nil {
		if err := t.l.transcript.RecordToolStart(t.turnID, tc.EmissionOrdinal, tc.ID, tc.Function.Name, tc.Function.Arguments, model); err != nil {
			return fmt.Errorf("record tool start: %w", err)
		}
	}
	return nil
}

func (t *turn) recordToolDone(tc llm.ToolCall, obs Observation) error {
	if t.l.transcript != nil {
		durable := true
		var err error
		if tr, ok := t.l.transcript.(durableToolTranscript); ok {
			durable, err = tr.RecordToolDoneDurable(t.turnID, tc.EmissionOrdinal, tc.Function.Name, tc.Function.Arguments, obs)
		} else {
			err = t.l.transcript.RecordToolDone(t.turnID, tc.EmissionOrdinal, tc.Function.Name, tc.Function.Arguments, obs)
		}
		if err != nil {
			return fmt.Errorf("record tool result: %w", err)
		}
		if durable {
			t.toolCallsRecorded++
		}
	}
	return nil
}

const (
	ReasonToolStopRefused = "TOOL_RESPONSE_STOP_REFUSED"
	ReasonToolDegenerate  = "TOOL_DEGENERATE_RESPONSE"
	ReasonToolCallBudget  = "TOOL_CALL_BUDGET_EXHAUSTED"
	ReasonToolDisabled    = "TOOL_DISABLED_FOR_FINAL_RESPONSE"
	ReasonToolNoExecutor  = "TOOL_EXECUTOR_UNAVAILABLE"
)

func (t *turn) refuseCalls(model string, calls []llm.ToolCall, reason, text string) error {
	for _, tc := range calls {
		if err := t.recordToolStart(model, tc); err != nil {
			return err
		}
		if err := t.recordToolDone(tc, Observation{Text: text, Failed: true, Refused: true, ReasonCode: reason}); err != nil {
			return err
		}
	}
	return nil
}

func (t *turn) execute(ctx context.Context, tc llm.ToolCall) Observation {
	name := tc.Function.Name
	if t.refusedDefs[name] {
		return Observation{Text: "Error: " + toolNotOffered(name), Failed: true, Refused: true, ReasonCode: ReasonToolDefinitionRefused}
	}

	if t.scoped && !t.offered[name] {
		return Observation{Text: "Error: " + toolOutsideRequest(name), Failed: true, Refused: true, ReasonCode: ReasonToolOutsideRequest}
	}
	return t.l.tools.Execute(ctx, tc)
}

const ReasonToolDefinitionRefused = "TOOL_DEFINITION_REFUSED"

func withoutRefused(defs []llm.ToolDefinition, refused map[string]bool) []llm.ToolDefinition {
	if !slices.ContainsFunc(defs, func(d llm.ToolDefinition) bool { return refused[d.Function.Name] }) {
		return defs
	}
	kept := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		if !refused[d.Function.Name] {
			kept = append(kept, d)
		}
	}
	return kept
}

func (t *turn) executeCalls(model string, calls []llm.ToolCall) (yielded bool, yieldAnswer string, err error) {
	t.toolCallsSoFar += len(calls)

	preObs := map[int]Observation{}
	preStarted := map[int]bool{}
	if ps, ok := t.l.tools.(ParallelSafeExecutor); ok && len(calls) >= 2 {

		allSafe := true
		for _, tc := range calls {
			if !ps.ParallelSafe(tc) {
				allSafe = false
				break
			}
		}
		if allSafe {

			for idx, tc := range calls {
				logsink.InfoCtx(t.ctx, "tool.start", "Tool call: %s(%s)", tc.Function.Name, logPreview(tc.Function.Arguments))
				if t.l.emit != nil {
					t.l.emit.EmitToolEvent("tool_call", tc.Function.Name, tc.Function.Arguments)
				}
				if err := t.recordToolStart(model, tc); err != nil {
					return false, "", err
				}
				preStarted[idx] = true
			}
			sem := make(chan struct{}, 4)
			var mu sync.Mutex
			var wg sync.WaitGroup
			for idx := range calls {
				idx := idx
				tc := calls[idx]
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()

					o := notRun()
					if t.ctx.Err() == nil {
						o = t.execute(llm.WithModelID(t.ctx, model), tc)
					}
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
			logsink.InfoCtx(t.ctx, "tool.start", "Parallel batch: %d read-only call(s) executed concurrently", len(calls))
		}
	}

	for tcIdx, tc := range calls {

		if !preStarted[tcIdx] {
			logsink.InfoCtx(t.ctx, "tool.start", "Tool call: %s(%s)", tc.Function.Name, logPreview(tc.Function.Arguments))
			if t.l.emit != nil {
				t.l.emit.EmitToolEvent("tool_call", tc.Function.Name, tc.Function.Arguments)
			}
		}
		if t.l.tools == nil {
			if err := t.refuseCalls(model, calls[tcIdx:], ReasonToolNoExecutor, "Not run: no tool executor is available."); err != nil {
				return false, "", err
			}
			return false, "", fmt.Errorf("tool %q requested but no executor is available", tc.Function.Name)
		}
		obs, pre := preObs[tcIdx]
		if !preStarted[tcIdx] {
			if err := t.recordToolStart(model, tc); err != nil {
				return false, "", err
			}
		}
		if !pre {
			if t.ctx.Err() != nil {

				obs = notRun()
			} else {
				obs = t.execute(llm.WithModelID(t.ctx, model), tc)
			}
		}
		if obs.EndTurn {
			yielded = true
			if obs.EndTurnAnswer != "" {
				yieldAnswer = obs.EndTurnAnswer
			}
		}
		result := obs.Text
		if obs.Image != nil {
			t.roundImages = append(t.roundImages, *obs.Image)
		}

		heard := isHeardRecall(tc.Function.Name, tc.Function.Arguments)
		if err := t.recordToolDone(tc, obs); err != nil {
			return false, "", err
		}
		logsink.InfoCtx(t.ctx, "tool.end", "Tool result: %s", logPreview(result))

		retained := 0
		if t.l.transcript != nil {
			retained = t.l.transcript.TranscriptResultExcerptLimit()
		}
		if heard && retained > 0 {
			retained = retainedWhole
		}
		window, scope := t.cfg.MaxToolResultChars, ""

		if obs.HostNote != "" {
			window = max(0, window-utf8.RuneCountInString(obs.HostNote)-1)
		}
		if heard && utf8.RuneCountInString(result) > window {

			if scope = heardScope(result); scope != "" {
				window = max(window-utf8.RuneCountInString(scope)-1, 0)
			}
		}
		modelResult := truncateToolResult(result, window, retained)
		if heard {

			modelResult = untrusted.Wrap("heard context", modelResult)
			if scope != "" {
				modelResult = scope + "\n" + modelResult
			}
		}
		if obs.HostNote != "" {
			modelResult = obs.HostNote + "\n" + modelResult
		}
		t.messages = append(t.messages, llm.FormatToolResult(tc.ID, modelResult))
	}
	return yielded, yieldAnswer, nil
}

func (t *turn) finalCall(failed, circumstance string, afterTools bool) error {
	finalTools, err := fitFinalRequest(&t.messages, &t.fit, t.systemBase, t.toolDefs, t.cfg.ContextBudgetTokens, t.l.transcript)
	if err != nil {
		return err
	}
	if afterTools {
		if err := t.ctx.Err(); err != nil {
			return err
		}
	}

	resp, err := t.ask(func() llm.ChatOptions {
		finalTools = withoutRefused(finalTools, t.refusedDefs)
		return llm.ChatOptions{Tools: finalTools, ExactTools: t.scoped, DisableTools: true, ThinkingBudget: t.cfg.ThinkingBudget, PreviousResponseID: t.previousResponseID}
	})
	if err != nil {
		t.turnUsage.failed(err)
		return fmt.Errorf("%s: %w", failed, err)
	}
	if resp != nil {
		t.turnUsage.add(resp.Usage)
		t.keepReasoning(resp)
		if len(resp.Choices) > 0 {
			choice := t.numberEmissions(resp.Choices[0])
			t.modelID = resp.ModelID
			if err := t.refuseCalls(resp.ModelID, choice.Message.ToolCalls, ReasonToolDisabled, "Not run: tools were disabled for this closing response."); err != nil {
				return err
			}
		}
	}

	t.finalText, t.modelID, err = finalResponse(resp, circumstance)
	return err
}

func finalResponse(response *llm.Response, circumstance string) (string, string, error) {
	if response == nil {
		return "", "", fmt.Errorf("LLM final response %s has no text", circumstance)
	}
	if len(response.Choices) == 0 {
		return "", "", emptyReply(fmt.Sprintf("LLM final response %s has no text", circumstance))
	}
	choice := response.Choices[0]

	if len(choice.Message.ToolCalls) > 0 {
		return "", response.ModelID, fmt.Errorf("LLM final response %s requested tools that were disabled", circumstance)
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		return "", "", emptyReply(fmt.Sprintf("LLM final response %s has no text", circumstance))
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

type fitState struct {
	current  int
	omitted  int
	abridged int
	warned   bool

	thinking bool

	chunk int

	baseOmitted int

	folded int

	readOnly func(llm.ToolCall) bool

	fallback bool

	facts string
	base  string
	based bool

	tight bool

	replayOff bool
}

func fitRequest(messages *[]llm.Message, st *fitState, systemBase string,
	tools []llm.ToolDefinition, budget int, transcript Transcript) error {
	changed := (*messages)[0].Content != systemBase
	(*messages)[0].Content = systemBase
	if renderCurrent(*messages, st) {
		changed = true
	}
	if changed {
		resetProviderReasoning(messages)
	}
	if st.replayOff {
		llm.DropReasoning(*messages)
	}

	toolTokens, err := llm.EstimateToolTokens(tools)
	if err != nil {
		return err
	}
	for {
		used, err := llm.AdmitPriced(*messages, toolTokens, budget)
		if err == nil {
			used = warnIfTight(messages, st, used, budget)
			noteIfPressured(messages, st, used, budget)
			return nil
		}
		var limitErr *llm.ContextLimitError
		if !errors.As(err, &limitErr) {
			return err
		}

		if !st.replayOff && llm.DropReasoning(*messages) > 0 {
			st.replayOff = true
			continue
		}
		if st.current > 1 {
			yieldOldestChunk(messages, st)
			continue
		}

		if foldToolResult(*messages, st, transcript, true) || foldToolResult(*messages, st, transcript, false) {
			resetProviderReasoning(messages)
			continue
		}
		return err
	}
}

const defaultHistoryChunk = 10

func yieldOldestChunk(messages *[]llm.Message, st *fitState) {
	end := historyChunkEnd(*messages, st)
	last := end == st.current
	if last {
		end = exchangeEnd(*messages, st)
	}
	if !showsAbridged((*messages)[1:end]) && abridgeRange(messages, st, 1, end) {
		renderCurrent(*messages, st)
		return
	}
	dropHistory(messages, st, end)
	if !last && st.current > 1 {
		abridgeRange(messages, st, 1, historyChunkEnd(*messages, st))
	}
	renderCurrent(*messages, st)
}

func opensExchange(m llm.Message) bool {
	return m.Role == "user" && !m.Qualifies
}

func exchangeEnd(messages []llm.Message, st *fitState) int {
	end := 2
	for end < st.current && !opensExchange(messages[end]) {
		end++
	}
	if end > st.current {
		end = st.current
	}
	return end
}

func historyChunkEnd(messages []llm.Message, st *fitState) int {
	chunk := st.chunk
	if chunk <= 0 {
		chunk = defaultHistoryChunk
	}
	end := 1 + chunk
	if end >= st.current {
		return st.current
	}
	for end < st.current && !opensExchange(messages[end]) {
		end++
	}
	return end
}

func showsAbridged(rows []llm.Message) bool {
	for _, m := range rows {
		if m.Abridged {
			return true
		}
	}
	return false
}

func abridgeRange(messages *[]llm.Message, st *fitState, from, to int) bool {
	changed := false
	for i := from; i < to; i++ {
		row := (*messages)[i]
		if row.Abridged || strings.Contains(row.Content, prompt.SummaryMarker) {
			continue
		}
		shrunk := prompt.SummarizeUnits(row.Content, historyRoute)

		cost := len(TurnBlock(st.facts, st.omitted, st.abridged+1, st.tight, st.thinking)) -
			len(TurnBlock(st.facts, st.omitted, st.abridged, st.tight, st.thinking))
		if len(row.Content)-len(shrunk) > cost {
			(*messages)[i].Content = shrunk
			(*messages)[i].Abridged = true
			st.abridged++
			changed = true
		}
	}
	if changed {
		resetProviderReasoning(messages)
	}
	return changed
}

func dropHistory(messages *[]llm.Message, st *fitState, end int) {
	if end > st.current {
		end = st.current
	}
	for end < st.current && !opensExchange((*messages)[end]) {
		end++
	}
	for _, m := range (*messages)[1:end] {
		if m.Abridged {
			st.abridged--
		}
		if !m.Qualifies {
			st.omitted++
		}
	}
	n := end - 1
	*messages = append((*messages)[:1], (*messages)[end:]...)
	st.current -= n
	resetProviderReasoning(messages)
}

const contextLinePrefix = "\n[context: "

func noteIfPressured(messages *[]llm.Message, st *fitState, used, budget int) {
	if budget <= 0 || len(*messages) == 0 {
		return
	}
	last := &(*messages)[len(*messages)-1]
	if last.Role != "tool" || strings.Contains(last.Content, contextLinePrefix) {
		return
	}
	dropped := st.omitted - st.baseOmitted
	if st.folded == 0 && st.abridged == 0 && dropped <= 0 && !st.replayOff && used*100 < budget*contextTightPercent {
		return
	}
	line := contextLine(used, budget, dropped, st)
	was := *last
	last.Content += line
	if after, err := reprice(used, was, *last); err != nil || after > budget {
		last.Content = strings.TrimSuffix(last.Content, line)
	}
}

func reprice(used int, was, now llm.Message) (int, error) {
	before, err := llm.EstimateMessageTokens(was)
	if err != nil {
		return 0, err
	}
	after, err := llm.EstimateMessageTokens(now)
	if err != nil {
		return 0, err
	}
	return used - before + after, nil
}

func contextLine(used, budget, dropped int, st *fitState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s~%d of %d tokens used", contextLinePrefix, used, budget)
	if st.fallback {
		b.WriteString(" — that window is a FALLBACK guess: the provider declares no context_length and none was discovered, so the model's real window may be far larger; your operator can set it on the provider entry")
	}
	fmt.Fprintf(&b, "; %d tool result(s) folded this turn", st.folded)
	if dropped > 0 || st.abridged > 0 {
		fmt.Fprintf(&b, "; %d older turn(s) dropped and %d abridged under pressure", max(dropped, 0), st.abridged)
	}
	if st.replayOff {
		b.WriteString("; your reasoning from earlier calls this turn was dropped to fit and is not sent again this turn")
	}
	b.WriteString(". A spawned sub-agent starts with an empty history.]")
	return b.String()
}

const historyRoute = `recall(query="<a distinctive word or phrase>", source=conversation) reaches the full turn`

const (
	TurnOpen  = "[substrate · this turn]"
	TurnClose = "[substrate · end]"
)

const forgedTurnMarker = "(forged substrate marker removed)"

func scrubTurnMarkers(s string) string {
	s = strings.ReplaceAll(s, TurnClose, forgedTurnMarker)
	return strings.ReplaceAll(s, TurnOpen, forgedTurnMarker)
}

func TurnBlock(facts string, omitted, abridged int, tight, thinking bool) string {
	var parts []string
	if f := strings.TrimSpace(scrubTurnMarkers(facts)); f != "" {
		parts = append(parts, f)
	}
	if n := historyNote(omitted, abridged, thinking); n != "" {
		parts = append(parts, n)
	}
	if tight {
		parts = append(parts, strings.TrimSpace(contextTightNote))
	}
	if len(parts) == 0 {
		return ""
	}
	return wrapBlock(strings.Join(parts, "\n\n"))
}

func wrapBlock(body string) string {
	return "\n\n" + TurnOpen + "\n" + body + "\n" + TurnClose
}

func TurnBlockFrame(omitted int, thinking bool) string {
	if n := historyNote(omitted, 0, thinking); n != "" {
		return wrapBlock("\n\n" + n)
	}
	return wrapBlock("")
}

func renderCurrent(messages []llm.Message, st *fitState) bool {
	if st.current <= 0 || st.current >= len(messages) {
		return false
	}
	m := &messages[st.current]
	if !st.based {
		st.base, st.based = m.Content, true
	}
	want := st.base + TurnBlock(st.facts, st.omitted, st.abridged, st.tight, st.thinking)
	if m.Content == want {
		return false
	}
	m.Content = want
	return true
}

const (
	budgetPressureNote  = "[loop note — this turn's token budget is spent. Your tools are withdrawn for this call, the turn's last.]"
	contextPressureNote = "[loop note — the context is full. Your tools are withdrawn for this call, the turn's last.]"
)

func addBoundaryNote(messages *[]llm.Message, st *fitState, note string) {
	last := len(*messages) - 1
	if last > st.current && (*messages)[last].Role == "user" {
		(*messages)[last].Content += "\n\n" + note
		return
	}
	*messages = append(*messages, llm.Message{Role: "user", Content: note})
}

const contextTightPercent = 85

const contextTightNote = "\n\nThe context for this turn is nearly full. Further tool calls may not fit, and if the request stops fitting your tools will be withdrawn for one last call of this turn."

func warnIfTight(messages *[]llm.Message, st *fitState, used, budget int) int {
	if st.warned || budget <= 0 {
		return used
	}

	bare, err := withoutReplay(*messages, used)
	if err != nil || bare*100 < budget*contextTightPercent {
		return used
	}
	last := len(*messages) - 1
	was := (*messages)[last]
	if last == st.current {
		st.tight = true
		renderCurrent(*messages, st)
	} else {
		(*messages)[last].Content += contextTightNote
	}
	after, err := reprice(used, was, (*messages)[last])
	if err != nil || after > budget {
		if last == st.current {
			st.tight = false
			renderCurrent(*messages, st)
		} else {
			(*messages)[last].Content = strings.TrimSuffix((*messages)[last].Content, contextTightNote)
		}
		return used
	}
	st.warned = true
	return after
}

func withoutReplay(messages []llm.Message, used int) (int, error) {
	for _, m := range messages {
		if m.Reasoning == "" {
			continue
		}
		bare := m
		bare.Reasoning = ""
		var err error
		if used, err = reprice(used, m, bare); err != nil {
			return 0, err
		}
	}
	return used, nil
}

func historyAbridgedNote(abridged int) string {
	if abridged <= 0 {
		return ""
	}
	return fmt.Sprintf("%d older turn(s) are shown abridged; %s.", abridged, historyRoute)
}

func historyNote(omitted, abridged int, thinking bool) string {
	var lines []string
	if n := HistoryOmissionNote(omitted); n != "" {
		lines = append(lines, n)
	}
	if n := historyAbridgedNote(abridged); n != "" {
		lines = append(lines, n)
	}
	if len(lines) == 0 {
		return ""
	}
	if thinking {
		lines = append(lines, ThinkingRouteNote)
	}
	return "### Conversation context\n" + strings.Join(lines, "\n")
}

const ThinkingRouteNote = `The thinking behind your replies is never shown here. Where it was recorded it is kept beside the reply: recall(source=reasoning) lists the newest, recall(query="<a distinctive word or phrase>", source=reasoning) finds it by its words, and query="turn:N" reads one reply's whole.`

func HistoryOmissionNote(omitted int) string {
	if omitted <= 0 {
		return ""
	}

	return fmt.Sprintf("%d older conversation turns are not shown. Each is still individually searchable by its own words: recall(query=\"<a distinctive word or phrase>\", source=conversation).", omitted)
}

const foldNoticePrefix = "[tool result folded under context pressure"

func foldToolResult(messages []llm.Message, st *fitState, transcript Transcript, mustPay bool) bool {
	ordinal := 0
	for i := st.current + 1; i < len(messages); i++ {
		m := messages[i]
		if m.Role != "tool" {
			continue
		}
		ordinal++
		if strings.HasPrefix(m.Content, foldNoticePrefix) {
			continue
		}
		call, found := callFor(messages, st.current, i, m.ToolCallID)
		notice := foldNotice(call, found, st.readOnly, transcript)
		if strings.Contains(m.Content, contextTightNote) {

			notice += contextTightNote
		}
		if len(m.Content) <= len(notice) || (mustPay && len(m.Content) < 2*len(notice)) {
			continue
		}
		name := "a tool"
		if found {
			name = call.Function.Name
		}
		how := "folded"
		if !mustPay {
			how = "folded as a last resort, saving little"
		}
		logsink.Info("turn.budget", "fold: result %d of this turn (%s, %d runes) %s under context pressure — %d folded so far", ordinal, name, utf8.RuneCountInString(m.Content), how, st.folded+1)
		messages[i].Content = notice
		st.folded++
		return true
	}
	return false
}

func callFor(messages []llm.Message, from, to int, id string) (llm.ToolCall, bool) {
	if id == "" {
		return llm.ToolCall{}, false
	}
	for j := to - 1; j > from; j-- {
		if messages[j].Role != "assistant" {
			continue
		}
		for _, tc := range messages[j].ToolCalls {
			if tc.ID == id {
				return tc, true
			}
		}
	}
	return llm.ToolCall{}, false
}

const foldNoticeArgRunes = 80

func foldNotice(call llm.ToolCall, found bool, readOnly func(llm.ToolCall) bool, transcript Transcript) string {
	if found && readOnly != nil && readOnly(call) {
		return fmt.Sprintf("%s — it was the read-only call %s(%s) and may be repeated exactly if its content is needed]",
			foldNoticePrefix, call.Function.Name, argExcerpt(call.Function.Arguments))
	}
	notice := foldNoticePrefix
	if found {
		notice += fmt.Sprintf(" — it was %s(%s);", call.Function.Name, argExcerpt(call.Function.Arguments))
	} else {
		notice += " —"
	}
	notice += " do not repeat the tool solely to recover this output; continue from available evidence"
	if kept := transcript != nil && transcript.TranscriptResultExcerptLimit() > 0; kept && found && isHeardRecall(call.Function.Name, call.Function.Arguments) {
		notice += "; ask the operator for the transcript if essential (the whole result is retained)"
	} else if kept {
		notice += fmt.Sprintf("; ask the operator for the transcript excerpt if essential (first %d characters retained)", transcript.TranscriptResultExcerptLimit())
	} else {
		notice += "; no transcript excerpt is available"
	}
	return notice + "]"
}

func argExcerpt(args string) string {
	r := []rune(strings.TrimSpace(args))
	if len(r) <= foldNoticeArgRunes {
		return string(r)
	}
	return string(r[:foldNoticeArgRunes]) + "…"
}

func logPreview(s string) string { return logsink.Preview(s) }

func isHeardRecall(tool, args string) bool {
	if tool != "recall" {
		return false
	}
	var call map[string]any
	return json.Unmarshal([]byte(args), &call) == nil && call["source"] == "heard"
}

func heardScope(result string) string {
	var page struct {
		Scope string `json:"scope"`
	}
	if json.Unmarshal([]byte(result), &page) != nil {
		return ""
	}
	return page.Scope
}

const retainedWhole = -1

func truncateToolResult(result string, maxChars, transcriptChars int) string {

	if utf8.RuneCountInString(result) <= maxChars {
		return result
	}
	runes := []rune(result)
	omitted := len(runes) - maxChars
	retention := "no transcript excerpt is available"
	switch {
	case transcriptChars == retainedWhole:
		retention = "the whole result is retained in the operator transcript"
	case transcriptChars > 0:
		retention = fmt.Sprintf("the first %d characters are retained in the operator transcript", transcriptChars)
	}

	wrapped, hostNote, hostPrefix := result, "", ""
	if strings.HasPrefix(wrapped, "Error: "+untrusted.Open) {
		wrapped = strings.TrimPrefix(wrapped, "Error: ")
		hostPrefix = "Error: "
	}
	if strings.HasPrefix(wrapped, untrusted.Open) && strings.HasSuffix(wrapped, "\n"+untrusted.Close+"\n[truncated]") {
		wrapped = strings.TrimSuffix(wrapped, "\n[truncated]")
		hostNote = "\n[truncated]"
	}
	if strings.HasPrefix(wrapped, untrusted.Open) && strings.HasSuffix(wrapped, "\n"+untrusted.Close) {
		head, _, _ := strings.Cut(wrapped, "\n")
		wrappedRunes := runes[utf8.RuneCountInString(hostPrefix) : len(runes)-utf8.RuneCountInString(hostNote)]
		first := utf8.RuneCountInString(head) + 1
		last := len(wrappedRunes) - utf8.RuneCountInString(untrusted.Close) - 1
		if first <= last {
			keep := maxChars - utf8.RuneCountInString(hostPrefix) - first - utf8.RuneCountInString(untrusted.Close) - 1 - utf8.RuneCountInString(hostNote)
			if keep < 0 {
				keep = 0
			}
			if keep >= last-first {
				return result
			}
			omitted := last - first - keep
			return hostPrefix + fmt.Sprintf("[output truncated — first %d content characters omitted; %s]\n%s\n%s\n%s%s",
				omitted, retention, head, string(wrappedRunes[last-keep:last]), untrusted.Close, hostNote)
		}
	}
	return fmt.Sprintf("[output truncated — first %d characters omitted; %s]\n%s", omitted, retention, string(runes[omitted:]))
}

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

const turnContract = `

## Your turn
Your turn continues automatically after each tool call: take the next step by
calling its tool rather than describing it.

A tool call is the only record that anything happened. Report only what you
actually did — saying you have not done something, or do not know, is always
available and always acceptable. That grounding law governs what you claim, not
how you speak.

Your newest message may end with a block from ` + TurnOpen + ` to ` + TurnClose + `,
after the words that arrived. Your substrate writes it, never the person: what is right in front of you (your
intentions, your work session and its plan, your standing state), this turn's
rhythm, what happened since your last turn, and what of the conversation is out
of view. It is written fresh each turn from what you have recorded and is not
kept in your history: change what it shows with your tools, and note what should
outlast the turn.`

func declare(format string, args ...any) string {
	return "[" + fmt.Sprintf(format, args...) + "]"
}

type TurnStop string

func (s TurnStop) Error() string { return string(s) }

func (TurnStop) Is(target error) bool { return target == context.Canceled }

const (
	StoppedByOperator TurnStop = "stopped by the operator"

	RuntimeShuttingDown TurnStop = "the runtime was shutting down"
	RuntimeRestarting   TurnStop = "the runtime was restarting"
)

const cancelledUnnamed = "the turn was cancelled; what cancelled it is not known"

const SubstrateFailed = "the substrate failed mid-turn"

type emptyReply string

func (e emptyReply) Error() string { return string(e) }

func interruptCause(ctx context.Context, err error) string {
	var answered *llm.HTTPError
	var stop TurnStop
	var empty emptyReply
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		if errors.As(context.Cause(ctx), &stop) {
			return string(stop)
		}
		return cancelledUnnamed
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "the turn ran out of time"
	case errors.As(err, &empty):
		return "the model returned an empty reply"
	case errors.As(err, &answered):
		return providerAnswer(answered)
	default:
		return SubstrateFailed
	}
}

func FailureCause(err error) string {
	var stop TurnStop
	if errors.As(err, &stop) {
		return string(stop)
	}
	return interruptCause(context.Background(), err)
}

func providerAnswer(e *llm.HTTPError) string {
	status := fmt.Sprintf("HTTP %d", e.StatusCode)
	if text := http.StatusText(e.StatusCode); text != "" {
		status += " " + text
	}
	said := "the model provider refused the request: " + status
	if e.StatusCode >= 500 {
		said = "the model provider failed: " + status
	}
	if e.RetryAfter > 0 {
		said += fmt.Sprintf("; it asked for a wait of %s", e.RetryAfter.Round(time.Second))
	}
	return said
}

const toolBudgetWarnAt = 2

func steeringNote(said []string) string {
	return "Someone spoke while you were working:\n\n" + strings.Join(said, "\n\n")
}

func checkpointNote(declared, spent, grace int) string {
	if declared > 0 {
		return declare("Declared budget reached: you estimated %d remaining calls and have spent %d since that declaration. "+
			"Checkpoint NOW — `work update next_move=` with the exact resume point (revise plan= if it drifted), "+
			"and say which this is: done with its scope, continue from that point, or stop with why. "+
			"You have %s; then this turn ends and, while its work session is still active, the work continues automatically in a fresh turn.",
			declared, spent, moreRounds(grace))
	}
	return declare("This turn has spent %d calls with no declared budget. "+
		"Checkpoint NOW — `work update next_move=` with the exact resume point, declare steps= for the remainder, "+
		"and say which this is: done with its scope, continue from that point, or stop with why. "+
		"You have %s; then this turn ends and, while its work session is still active, the work continues automatically in a fresh turn.",
		spent, moreRounds(grace))
}

func moreRounds(n int) string {
	if n == 1 {
		return "1 more round"
	}
	return fmt.Sprintf("%d more rounds", n)
}

func toolBudgetNote(remaining int) string {
	rounds := "rounds remain"
	if remaining == 1 {
		rounds = "round remains"
	}

	return fmt.Sprintf("\n\n[%d %s in this turn — a round may include several parallel tool calls.]", remaining, rounds)
}

func platformNote() string {
	return "\n\nYou are running on " + platformName() +
		". The tools you can see are all the reach this platform gives you: what you did " +
		"elsewhere may not be possible here, which is a change of circumstance, not of who you are."
}

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

func systemAdditions() string { return turnContract + platformNote() + showNote }

const showNote = "\n\nIn the chat, to show your operator an image, write `![caption](path)` on a line of its own, " +
	"with the path of an image file in your folder. To hand them a file, write `[name](path)` the same way."

func SystemAdditions() string { return systemAdditions() }

const (
	degenTailRunes     = 3000
	degenMinLines      = 10
	degenLineRatio     = 0.4
	degenMinSentences  = 14
	degenSentenceRatio = 0.5
)

func degenerateEmission(s string) bool {
	r := []rune(s)
	if len(r) > degenTailRunes {
		r = r[len(r)-degenTailRunes:]
	}
	tail := string(r)

	lines := strings.Split(tail, "\n")
	lineCount := map[string]int{}
	n, top := 0, 0
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if len([]rune(ln)) < 8 {
			continue
		}
		n++
		lineCount[ln]++
		if lineCount[ln] > top {
			top = lineCount[ln]
		}
	}
	if n >= degenMinLines && float64(top) >= degenLineRatio*float64(n) {
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

func estimateSilentCall(msgs []llm.Message, tools []llm.ToolDefinition, reply string) int {

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

func resetProviderReasoning(messages *[]llm.Message) {
	if llm.ResetThinking(*messages) == 0 || len(*messages) == 0 {
		return
	}
	last := len(*messages) - 1
	(*messages)[last].Content += "\n[Earlier provider reasoning was reset because its context changed. Re-derive from the retained evidence; recall reaches recorded facts.]"
}
func fitFinalRequest(messages *[]llm.Message, st *fitState, base string, defs []llm.ToolDefinition, budget int, tr Transcript) ([]llm.ToolDefinition, error) {

	st.warned = true
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

func shortTurn(id string) string {
	id = strings.TrimPrefix(id, "turn_")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func notRun() Observation {
	return Observation{Text: "Not run: the turn ended before this call began.", Failed: true, Cancelled: true}
}

func withLimits(limits, facts string) string {
	switch {
	case limits == "":
		return facts
	case strings.TrimSpace(facts) == "":
		return limits
	}
	return limits + "\n\n" + facts
}
