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
package identity

import (
	"context"
	"fmt"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
type ToolInfo struct {
	Name        string
	Description string
}

// .
// .
// .
type ToolDiscoverer interface {
	Discover(depth int) []ToolInfo
}

// .
// .
// .
// .
// .
// .
type ToolPlane interface {
	Brief() ToolBrief
	Search(query string, limit int) []ToolHit
	Show(ref string) (ToolCard, error)
	Offer(ref string) (string, error)
	Release(ref string) (string, error)
}

// .
type ToolFamily struct {
	Name  string
	Count int
	Names []string
	More  int
}

// .
type ToolBrief struct {
	Total        int
	Offered      int
	Unavailable  int
	Families     []ToolFamily
	MoreFamilies int
	OfferedNames []string
}

// .
type ToolHit struct {
	Name      string
	Operation string
	Plugin    string
	Summary   string
	Effects   string
	State     string
}

// .
// .
type ToolCard struct {
	Name           string
	Operation      string
	Plugin         string
	Version        string
	Tier           string
	Family         string
	Summary        string
	Effects        string
	Capabilities   []string
	MaxResultBytes int
	Examples       []string
	State          string
	Reason         string
	Receipt        string
	Parameters     map[string]interface{}
}

type EventWriter interface {
	Append(ledger.EventType, int, interface{}, string) (*ledger.Event, error)
}

// .
type Engine struct {
	// .
	// .
	// .
	// .
	// .
	// .
	safeMu         sync.RWMutex
	safeReason     string
	safeTranscript []SafeTurn

	// .
	// .
	// .
	// .
	// .
	fetchMu      sync.Mutex
	fetchedURLs  map[string]bool
	fetchedOrder []string

	reachable func(name string) bool
	// .
	// .
	// .
	// .
	askProposer func(session, text string, choices []string, connector string) error

	// .
	// .
	// .
	// .
	// .
	agencyMu             sync.RWMutex
	maxSubagentDepth     int
	maxParallelSubagents int
	maxSubagentMints     int
	subagentWallSeconds  int
	// .
	// .
	// .
	subagentWallSecondsLocal int
	routeIsLocal             func(role string) bool
	workWake                 func()
	// .
	// .
	// .
	// .
	yieldGate func() (need bool, why string)
	// .
	// .
	spawnQueueOff bool
	// .
	// .
	// .
	spawnRounds, spawnCalls, spawnLegs int

	store    *store.Store
	ledger   EventWriter
	rings    *ring.Manager
	projects ProjectPort
	voice    VoicePort
	// .
	// .
	continuity ContinuityPort
	toolDisc   ToolDiscoverer
	// .
	// .
	instruments *memory.Facility
	timers      TimerSetter
}

// .
func NewEngine(s *store.Store, l EventWriter, rm *ring.Manager, td ToolDiscoverer) *Engine {
	return &Engine{
		store:       s,
		ledger:      l,
		rings:       rm,
		toolDisc:    td,
		instruments: memory.New(s),
	}
}

// .
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

// .
// .
// .
// .
type SubagentDepth struct{}

// .
// .
// .
// .
// .
type SubagentBudget struct{}

// .
type SubagentWorkSession struct{}

// .
// .
// .
// .
// .
// .
func (e *Engine) SetReachable(fn func(name string) bool) { e.reachable = fn }

// .
// .
func (e *Engine) SetAskProposer(fn func(session, text string, choices []string, connector string) error) {
	e.askProposer = fn
}

// .
func (e *Engine) SetSpawnQueue(on bool) {
	e.agencyMu.Lock()
	defer e.agencyMu.Unlock()
	e.spawnQueueOff = !on
}

// .
// .
func (e *Engine) SetSpawnBudget(rounds, calls, legs int) {
	e.agencyMu.Lock()
	defer e.agencyMu.Unlock()
	e.spawnRounds, e.spawnCalls, e.spawnLegs = rounds, calls, legs
}

func (e *Engine) spawnBudget() (rounds, calls, legs int) {
	e.agencyMu.RLock()
	defer e.agencyMu.RUnlock()
	return e.spawnRounds, e.spawnCalls, e.spawnLegs
}

// .
// .
func (e *Engine) spawnBudgetLine(wallSeconds int) string {
	rounds, calls, legs := e.spawnBudget()
	if rounds <= 0 && calls <= 0 {
		return ""
	}
	if legs < 1 {
		legs = 1
	}
	return fmt.Sprintf(" Its budget: %d rounds and %d calls per leg, up to %d leg(s), %d s wall per leg — size the deliverable to that; a leg that ends at its budget continues from the child's recorded next move.", rounds, calls, legs, wallSeconds)
}

func (e *Engine) spawnQueueOn() bool {
	e.agencyMu.RLock()
	defer e.agencyMu.RUnlock()
	return !e.spawnQueueOff
}

// .
// .
// .
func (e *Engine) SetYieldGate(fn func() (need bool, why string)) {
	e.agencyMu.Lock()
	defer e.agencyMu.Unlock()
	e.yieldGate = fn
}

func (e *Engine) SetAgencyLimits(maxDepth, maxParallel, maxMints, wallSeconds int) {
	e.agencyMu.Lock()
	defer e.agencyMu.Unlock()
	e.maxSubagentDepth = maxDepth
	e.maxParallelSubagents = maxParallel
	e.maxSubagentMints = maxMints
	e.subagentWallSeconds = wallSeconds
}

// .
// .
// .
func (e *Engine) SetLocalSpawnWall(seconds int) {
	e.agencyMu.Lock()
	defer e.agencyMu.Unlock()
	e.subagentWallSecondsLocal = seconds
}

// .
// .
func (e *Engine) SetRouteIsLocal(fn func(role string) bool) {
	e.agencyMu.Lock()
	defer e.agencyMu.Unlock()
	e.routeIsLocal = fn
}

// .
// .
// .
// .
// .
func (e *Engine) spawnWall(role string) int {
	e.agencyMu.RLock()
	wall, local, oracle := e.subagentWallSeconds, e.subagentWallSecondsLocal, e.routeIsLocal
	e.agencyMu.RUnlock()
	if oracle != nil && local > 0 && oracle(role) {
		return local
	}
	return wall
}

// .
// .
// .
// .
func (e *Engine) agencyLimits() (maxDepth, maxParallel, maxMints, wallSeconds int) {
	e.agencyMu.RLock()
	defer e.agencyMu.RUnlock()
	return e.maxSubagentDepth, e.maxParallelSubagents, e.maxSubagentMints, e.subagentWallSeconds
}

// .
func (e *Engine) SetWorkWake(wake func()) { e.workWake = wake }

// .
// .
// .
type SubagentMints struct{}

func (e *Engine) executeVerb(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if name == "work" {
		delete(args, "_subagent_depth")
		delete(args, "_subagent_budget")
		if d, ok := ctx.Value(SubagentDepth{}).(int); ok {
			args["_subagent_depth"] = d
		}
		if b, ok := ctx.Value(SubagentBudget{}).(int); ok {
			args["_subagent_budget"] = b
		}
	}
	// .
	// .
	if name == "note" || name == "commit" {
		if counter, ok := ctx.Value(SubagentMints{}).(*int); ok && counter != nil {
			_, _, maxMints, _ := e.agencyLimits()
			if maxMints > 0 && *counter >= maxMints {
				return "", fmt.Errorf("mint envelope reached (%d ledger-writing verb calls this run — agency.subagent_max_mints): deliver your outcome; the person notes what else deserves the record", maxMints)
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
	// .
	// .
	// .
	// .
	// .
	// .
	if v := lookupVerb(name); v != nil {
		return v.Handler(e, ctx, args)
	}
	return "", fmt.Errorf("unknown verb: %s", name)
}
