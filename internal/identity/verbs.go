// Package identity implements the five identity verbs plus tools discovery.
//
// note    — noticing: raw capture, never gated by budget or permission (R2, R34).
//
//	(In SAFE mode note refuses honestly — the record it would write
//	cannot be verified; that is honesty about the substrate, not a gate.)
//
// timer   — the resident's requested alarms: set/cancel/list (never gated)
// recall  — remembering: grouped honest read (R1)
// send    — interpersonal: writes to outbox, delivery channels consume
// work    — doing: durable sessions with lease/resume/deliver + Ring 4 state
// commit  — conscious self-authorship: ring-gated (R3), consent gate (C11)
// tools   — discovery: three depths, never silent omission (R24)
package identity

import (
	"context"
	"fmt"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// ToolInfo is the identity domain's view of a discoverable tool.
type ToolInfo struct {
	Name        string
	Description string
}

// ToolDiscoverer is what the identity domain needs from the tool layer:
// discovery, never execution — and not even the tool layer's types. The
// domain stays out of the sandbox entirely.
type ToolDiscoverer interface {
	Discover(depth int) []ToolInfo
}

type EventWriter interface {
	Append(ledger.EventType, int, interface{}, string) (*ledger.Event, error)
}

// Engine ties the identity verbs together with their dependencies.
type Engine struct {
	// SAFE-mode refusal reason ("" = normal). Written by the app's mode
	// machinery, read by every minting verb on its own goroutine — a
	// -race finding (S6) while unguarded. safeTranscript holds the
	// transient conversation while SAFE (canon §10: no DB writes with
	// integrity unverified; the conversation itself continues). Both
	// guarded by safeMu.
	safeMu         sync.RWMutex
	safeReason     string
	safeTranscript []SafeTurn

	// Fetch registry (H3): web_fetch reports every successful URL here so
	// a note citing source_url can be verified against fetches that
	// actually happened — external provenance is checkable, not claimed.
	// Session-scoped and bounded; the durable citation lives in the
	// signed experience payload.
	fetchMu      sync.Mutex
	fetchedURLs  map[string]bool
	fetchedOrder []string

	// Agency ceilings (operator config; 0 = spawning disabled).
	maxSubagentDepth     int
	reachable            func(name string) bool
	maxParallelSubagents int
	maxSubagentMints     int
	subagentWallSeconds  int
	workWake             func()

	store    *store.Store
	ledger   EventWriter
	rings    *ring.Manager
	projects ProjectPort // R62 workrooms — nil until the substrate wires it
	toolDisc ToolDiscoverer
	timers   TimerSetter // resident timers (durable alarm rows; nil = timer verb refuses honestly)
}

// NewEngine creates an identity engine with all dependencies.
func NewEngine(s *store.Store, l EventWriter, rm *ring.Manager, td ToolDiscoverer) *Engine {
	return &Engine{
		store:    s,
		ledger:   l,
		rings:    rm,
		toolDisc: td,
	}
}

// ExecuteAction dispatches a parsed action (verb or tool) from the LLM.
func (e *Engine) ExecuteAction(ctx context.Context, actionType, name string, args map[string]interface{}) (string, error) {
	switch actionType {
	case "verb":
		return e.executeVerb(ctx, name, args)
	default:
		return "", fmt.Errorf("unknown action type: %s", actionType)
	}
}

func (e *Engine) append(ctx context.Context, eventType ledger.EventType, ring int, payload interface{}) (*ledger.Event, error) {
	return e.ledger.Append(eventType, ring, payload, llm.ModelIDFromContext(ctx))
}

// SubagentDepth is the context key carrying the CURRENT sub-agent
// nesting depth (0 = the main conversation). Set by the sub-agent
// runner; read here so spawn depth is ENGINE-STAMPED — a model-supplied
// depth claim is always overridden (the R52 stamping pattern).
type SubagentDepth struct{}

// SubagentBudget carries the CURRENT thread's effective thinking budget
// (0 = main thread, whose level is the config's). Spawn clamps a
// child's budget against THIS — effort directs downward per thread,
// never against the global ceiling (the ruling is "current level or
// lower", and a directed hand's current level is its directed one).
type SubagentBudget struct{}

// SubagentWorkSession binds work verbs to their queue item's session.
type SubagentWorkSession struct{}

// SetAgencyLimits wires the operator's agency config (spawn nesting +
// parallelism ceilings live in config, never in code — 2026-08-18
// ruling).
// SetReachable wires the address book as the identity may see it: a
// name either can be reached or cannot. The addresses stay with the
// host, which is the only place that needs them.
func (e *Engine) SetReachable(fn func(name string) bool) { e.reachable = fn }

func (e *Engine) SetAgencyLimits(maxDepth, maxParallel, maxMints, wallSeconds int) {
	e.maxSubagentDepth = maxDepth
	e.maxParallelSubagents = maxParallel
	e.maxSubagentMints = maxMints
	e.subagentWallSeconds = wallSeconds
}

// SetWorkWake connects a successful spawn to the existing executor wake path.
func (e *Engine) SetWorkWake(wake func()) { e.workWake = wake }

// SubagentMints carries a spawned run's ledger-write counter (set by
// the runner). The envelope is flat per run — the same bound class as
// rounds and wall time; never a curve, never the main thread.
type SubagentMints struct{}

func (e *Engine) executeVerb(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if name == "work" {
		delete(args, "_subagent_depth") // engine-stamped, never model-claimed
		delete(args, "_subagent_budget")
		if d, ok := ctx.Value(SubagentDepth{}).(int); ok {
			args["_subagent_depth"] = d
		}
		if b, ok := ctx.Value(SubagentBudget{}).(int); ok {
			args["_subagent_budget"] = b
		}
	}
	// Per-run mint envelope (R60): a spawned run's DIRECT ledger channel
	// is bounded; the unbounded channel is outcome → person → note.
	if name == "note" || name == "commit" {
		if counter, ok := ctx.Value(SubagentMints{}).(*int); ok && counter != nil {
			if e.maxSubagentMints > 0 && *counter >= e.maxSubagentMints {
				return "", fmt.Errorf("mint envelope reached (%d ledger-writing verb calls this run — agency.subagent_max_mints): deliver your outcome; the person notes what else deserves the record", e.maxSubagentMints)
			}
			out, err := e.dispatchVerb(ctx, name, args)
			if err == nil {
				*counter++
			}
			return out, err
		}
	}
	return e.dispatchVerb(ctx, name, args)
}

func (e *Engine) dispatchVerb(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	// Derived from the registry — a verb cannot be declared without its
	// handler (init() refuses), so this lookup cannot miss what the
	// definitions advertise.
	for i := range VerbRegistry {
		if VerbRegistry[i].Name == name {
			return VerbRegistry[i].Handler(e, ctx, args)
		}
	}
	return "", fmt.Errorf("unknown verb: %s", name)
}
