// .
// .
// .
// .
// .
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
func workspaceFiles(entries []os.DirEntry) []dashboard.WorkspaceFile {
	files := make([]dashboard.WorkspaceFile, 0, len(entries))
	for _, e := range entries {
		f := dashboard.WorkspaceFile{Name: e.Name(), Dir: e.IsDir()}
		if info, ierr := e.Info(); ierr == nil {
			f.Size = info.Size()
		}
		files = append(files, f)
	}
	return files
}

// .
// .
// .
// .
// .
// .
const workspaceFileCap = 500

// .
// .
// .
const workspaceWorkCap = 20

// .
// .
// .
// .
// .
func (a *App) getProjectRoot(id string) (string, bool) {
	p, err := a.projects.Load(id)
	if err != nil {
		return "", false
	}
	if p.Dir == "" {
		return "", false
	}
	return p.Dir, true
}

// .
// .
// .
// .
// .
// .
func (a *App) getProjectWorkspace(id string) (*dashboard.WorkspaceState, error) {
	p, err := a.projects.Load(id)
	if err != nil {
		return nil, fmt.Errorf("load project: %w", err)
	}
	activeID := ""
	if ap, _ := a.activeOpenProject(); ap != nil {
		activeID = ap.ID
	}
	ws := &dashboard.WorkspaceState{
		Project: dashboard.ProjectState{
			ID: p.ID, Name: p.Name, Description: p.Description,
			State: p.State, Focus: p.Focus, Dir: p.Dir,
			// .
			// .
			// .
			// .
			// .
			Active: p.ID == activeID,
		},
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return nil, fmt.Errorf("read project dir: %w", err)
	}
	// .
	// .
	// .
	ws.FilesTotal = len(entries)
	all := workspaceFiles(entries)
	if len(all) > workspaceFileCap {
		all = all[:workspaceFileCap]
		ws.FilesCapped = true
	}
	ws.Files = all
	// .
	// .
	// .
	sessions, capped, err := a.store.WorkSessionsByProject(p.ID, workspaceWorkCap)
	if err != nil {
		return nil, fmt.Errorf("load project work: %w", err)
	}
	ws.WorkCapped = capped
	for _, s := range sessions {
		ws.Work = append(ws.Work, dashboard.WorkSessionItem{
			ID: s.ID, Description: store.SubagentGoal(s.Description),
			Status: s.Status, Project: s.Project, Result: compactText(s.Result, 240),
		})
	}
	return ws, nil
}

func (a *App) sandboxState() (*dashboard.SandboxState, error) {
	root, extra := a.toolReg.Roots()
	return &dashboard.SandboxState{Root: root, ExtraRoots: extra}, nil
}

// .
// .
func (a *App) setSandboxRoots(roots []string) error {
	normalized := make([]string, 0, len(roots))
	for _, r := range roots {
		if reason := a.toolReg.RootRejectionReason(r); reason != "" {
			return fmt.Errorf("%q: %s", strings.TrimSpace(r), reason)
		}
		r = filepath.Clean(strings.TrimSpace(r))
		if resolved, err := filepath.EvalSymlinks(r); err == nil {
			r = resolved
		}
		normalized = append(normalized, r)
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	candidate := *a.cfg
	candidate.Tools.ExtraRoots = normalized
	published, persistErr := saveConfig(&candidate)
	if persistErr != nil && !published {
		return fmt.Errorf("persist sandbox roots: %w", persistErr)
	}
	if published {
		*a.cfg = candidate
		a.toolReg.SetExtraRoots(normalized)
		a.loadRing5()
		log.Printf("Ring 5: extra sandbox roots -> %v (operator setting, floor updated live)", normalized)
	}
	if persistErr != nil {
		return fmt.Errorf("sandbox roots were published and applied live, but directory durability is unconfirmed: %w", persistErr)
	}
	return nil
}

func (a *App) setToolEnabled(name string, enabled bool) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	states := a.toolReg.ToolStates()
	found := false
	disabled := make([]string, 0, len(states))
	for _, state := range states {
		if state.Name == name {
			found = true
			state.Enabled = enabled
		}
		if !state.Enabled {
			disabled = append(disabled, state.Name)
		}
	}
	if !found {
		return fmt.Errorf("unknown tool: %s", name)
	}
	candidate := *a.cfg
	candidate.Tools.Disabled = disabled
	published, persistErr := saveConfig(&candidate)
	if persistErr != nil && !published {
		return fmt.Errorf("persist tool toggle: %w", persistErr)
	}
	if published {
		*a.cfg = candidate
		a.toolReg.SetToolEnabled(name, enabled)
		log.Printf("Ring 5: tool %q -> %v (operator toggle)", name, enabled)
	}
	if persistErr != nil {
		return fmt.Errorf("tool toggle was published and applied live, but directory durability is unconfirmed: %w", persistErr)
	}
	return nil
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
func (a *App) emitToolEvent(kind, name, args string) {
	a.toolEmitMu.Lock()
	emit := a.toolEmit
	a.toolEmitMu.Unlock()
	if emit != nil {
		emit(kind, name, args)
		return
	}
	if a.dashboard != nil {
		a.dashboard.BroadcastEvent(kind, name, args)
	}
}

// .
// .
// .
// .
// .
// .

func (a *App) statsState() (*dashboard.StatsResponse, error) {
	stats, err := a.store.GetStats()
	if err != nil {
		return nil, err
	}
	// .
	// .
	// .
	display := a.resolveDisplayName()
	return &dashboard.StatsResponse{
		Version:           VersionString(),
		Build:             BuildIdentity(),
		Name:              display,
		BeliefCount:       stats.BeliefCount,
		ReflectionCount:   stats.ReflectionCount,
		ExperienceCount:   stats.ExperienceCount,
		IntentionCount:    stats.IntentionCount,
		LedgerSeq:         stats.LedgerSeq,
		LifetimeTicks:     stats.LifetimeTicks,
		CredentialWarning: a.credentialWarning(),
		LastTurn:          a.lastTurnCost(),
		MalformedCalls:    a.toolReg.MalformedCallCount(),
		SuspiciousPaths:   a.toolReg.SuspiciousPathCount(),
		DuplicateArgKeys:  a.toolReg.DuplicateArgKeyCount(),
		Update:            a.updateStateView(),
		PluginUpdates:     a.catalogUpdateCount(),
		ForegroundHolds:   a.fg.Active(),
	}, nil
}

func (a *App) outboxItems() ([]dashboard.OutboxItem, error) {
	msgs, err := a.engine.UndeliveredMessages()
	if err != nil {
		return nil, err
	}
	items := make([]dashboard.OutboxItem, len(msgs))
	for i, m := range msgs {
		items[i] = dashboard.OutboxItem{ID: m.ID, To: m.ToRole, Content: m.Content}
	}
	return items, nil
}

func (a *App) recentTurnViews() ([]dashboard.HistoryTurn, error) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	turns, err := a.store.RecentTurnsIncludingSystem(50)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.HistoryTurn, 0, len(turns))
	attributions, refs := a.speakerAnnotations(turns), a.voiceRefs(turns)
	for _, t := range turns {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		ht := dashboard.HistoryTurn{Role: t.Role, Content: replayContent(t.Role, t.Content), VoiceRef: refs[t.TurnSeq]}
		if payload, ok := attributions[t.TurnSeq]; ok {
			ht.Note = attributionOf(payload)
		}
		out = append(out, ht)
	}
	return out, nil
}

func (a *App) identityState() (*dashboard.IdentityState, error) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	state := &dashboard.IdentityState{}
	beliefs, err := a.store.ListBeliefs()
	if err != nil {
		return nil, fmt.Errorf("identity state: beliefs: %w", err)
	}
	for _, b := range beliefs {
		state.Beliefs = append(state.Beliefs, dashboard.BeliefItem{
			ID: b.ID, Statement: b.Statement, Ring: b.Ring,
			// .
			// .
			// .
			Status:        standingOrUnavailable(a.store, b.ID),
			EvidenceCount: b.EvidenceCount, Confidence: b.Confidence,
		})
	}
	ints, err := a.store.ListIntentions()
	if err != nil {
		return nil, fmt.Errorf("identity state: intentions: %w", err)
	}
	for _, i := range ints {
		state.Intentions = append(state.Intentions, dashboard.IntentionItem{ID: i.ID, Statement: i.Statement, State: i.State})
	}
	exps, err := a.store.ListExperiences(50)
	if err != nil {
		return nil, fmt.Errorf("identity state: experiences: %w", err)
	}
	for _, e := range exps {
		if e.Private == 1 {
			state.PrivateCount++
			continue
		}
		state.Experiences = append(state.Experiences, dashboard.ExperienceItem{
			ID: e.ID, Content: e.Content, Category: e.Category, CreatedAt: e.CreatedAt, Provenance: e.Provenance})
	}
	current, err := a.store.CurrentSelfModel()
	if err != nil {
		return nil, fmt.Errorf("identity state: self-model: %w", err)
	}
	if current != nil {
		state.Synthesis = current.SynthesisText
	}
	brief, err := a.store.GetBrief()
	if err != nil {
		return nil, fmt.Errorf("identity state: brief: %w", err)
	}
	state.Brief = brief
	rel, err := a.store.FoundingRelationship()
	if err != nil {
		return nil, fmt.Errorf("identity state: founding relationship: %w", err)
	}
	if rel != nil {
		state.Charter = rel.CharterText
		state.TrustLevel = rel.TrustLevel
		state.AutonomyLevel = rel.AutonomyLevel
	}
	return state, nil
}

func (a *App) continuityState() (*dashboard.ContinuityState, error) {
	cfg := a.configSnapshot()
	c := &dashboard.ContinuityState{WitnessURL: cfg.Witness.URL}
	if a.ledger != nil {
		c.LedgerSeq = a.ledger.LastSeq()
	}
	if ticks, err := a.store.LifetimeTicks(); err == nil {
		c.LifeTicks = ticks
	}
	// .
	// .
	// .
	// .
	// .
	if fac := a.reviewFacility.Load(); fac != nil {
		if snap := fac.LastReview(); !snap.At.IsZero() {
			c.ReviewAt = snap.At.Format("15:04 Mon Jan 2")
			if snap.Clear {
				c.ReviewStatus = "clear"
			} else {
				c.ReviewStatus = "issues"
				c.ReviewIssues = snap.IssueCount
			}
		}
	}
	// .
	switch a.currentMode() {
	case ModeSafe:
		c.Mode = "safe"
		c.SafeReason, _ = func() (string, bool) { return a.SafeMode() }()
	case ModeDegradedWitness:
		c.Mode = "degraded_witness"
		if since, ok := a.DegradedWitnessSince(); ok {
			c.DegradedSince = since.Format("15:04 MST Mon Jan 2")
		}
	}
	if a.anchorer != nil {
		c.AnchoredSeq = int64(a.anchorer.LastAnchoredSeq())
		c.Unanchored = int64(a.anchorer.UnanchoredCount())
		if seq, js, err := a.store.LastWitnessReceipt(); err == nil && seq > 0 {
			var r struct {
				WitnessedAt string `json:"witnessed_at"`
			}
			if json.Unmarshal(js, &r) == nil {
				c.WitnessedAt = r.WitnessedAt
			}
			if c.AnchoredSeq == 0 {
				c.AnchoredSeq = seq
			}
		}
	}
	return c, nil
}

func (a *App) workQueueState() (*dashboard.WorkState, error) {
	w := &dashboard.WorkState{}
	live, err := a.store.LiveSubagentSessions()
	if err != nil {
		return nil, fmt.Errorf("load live work: %w", err)
	}
	for _, s := range live {
		w.Live = append(w.Live, dashboard.WorkSessionItem{
			ID: s.ID, Description: store.SubagentGoal(s.Description), Status: s.Status, Project: s.Project,
		})
	}
	w.Queued, err = a.store.CountLiveWork(identity.SubagentWorkKind)
	if err != nil {
		return nil, fmt.Errorf("count queued work: %w", err)
	}
	done, err := a.store.RecentDeliveredSubagents(3)
	if err != nil {
		return nil, fmt.Errorf("load delivered work: %w", err)
	}
	// .
	// .
	// .
	ids := make([]string, 0, len(done))
	for _, s := range done {
		ids = append(ids, s.ID)
	}
	grades := a.gradesOf(ids)
	for _, s := range done {
		item := dashboard.WorkSessionItem{
			ID: s.ID, Description: store.SubagentGoal(s.Description), Status: s.Status, Project: s.Project, Result: compactText(s.Result, 240),
		}
		if g, ok := grades[s.ID]; ok {
			gv := g
			item.Grade = &gv
		}
		w.Delivered = append(w.Delivered, item)
	}
	return w, nil
}

func (a *App) projectsState() ([]dashboard.ProjectState, error) {
	ps, err := a.projects.List()
	if err != nil {
		return nil, err
	}
	active := ""
	if p, _ := a.activeOpenProject(); p != nil {
		active = p.ID
	}
	out := make([]dashboard.ProjectState, 0, len(ps))
	for _, p := range ps {
		ps := dashboard.ProjectState{
			ID: p.ID, Name: p.Name, Description: p.Description,
			State: p.State, Focus: p.Focus, Dir: p.Dir, Active: p.ID == active,
			Attributes: p.Attributes, Parent: p.Parent,
		}
		if !p.Contract.IsZero() {
			ps.Contract = &dashboard.ProjectContract{
				Outcome:     p.Contract.Outcome,
				Acceptance:  p.Contract.Acceptance,
				Constraints: p.Contract.Constraints,
			}
		}
		ps.Progress = projectDashboardProgress(p)
		out = append(out, ps)
	}
	return out, nil
}

// .
// .
func standingOrUnavailable(src interface {
	StandingFor(id string) (string, error)
}, id string) string {
	standing, err := src.StandingFor(id)
	if err != nil {
		return "unavailable"
	}
	return standing
}

// .
// .
// .
// .
func (a *App) recallForDashboard(query string) (string, error) {
	if a.engine == nil {
		return "", fmt.Errorf("no identity engine is running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return a.engine.ExecuteAction(ctx, "verb", "recall", map[string]interface{}{"query": query})
}
