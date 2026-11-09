package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

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

const workspaceFileCap = 500

const workspaceWorkCap = 20

func (a *App) admitShownFile(path string) (root, rel string, ok bool) {
	if a.toolReg == nil || strings.TrimSpace(path) == "" {
		return "", "", false
	}
	root, rel, err := a.toolReg.Admit("show", path)
	if err != nil {
		return "", "", false
	}
	return root, rel, true
}

func (a *App) toldPath(root, rel string) string {
	if a.toolReg != nil {
		if home, _ := a.toolReg.Roots(); root == home {
			return rel
		}
	}
	return filepath.Join(root, rel)
}

func (a *App) filePolicy() dashboard.FilePolicy {
	f := a.configSnapshot().Dashboard.Files
	return dashboard.FilePolicy{MaxBytes: f.MaxBytes, Types: f.Types, MaxPerMessage: f.MaxPerMessage}
}

const uploadsName = "uploads"

func (a *App) uploadDir() string {
	if a.store != nil && a.projects != nil {
		if id := a.store.ActiveProjectID(); id != "" {
			if p, err := a.projects.Load(id); err == nil && p.Dir != "" {
				return filepath.Join(p.Dir, uploadsName)
			}
		}
	}
	return uploadsName
}

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

			Active: p.ID == activeID,
		},
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return nil, fmt.Errorf("read project dir: %w", err)
	}

	ws.FilesTotal = len(entries)
	all := workspaceFiles(entries)
	if len(all) > workspaceFileCap {
		all = all[:workspaceFileCap]
		ws.FilesCapped = true
	}
	ws.Files = all

	sessions, capped, err := a.store.WorkSessionsByProject(p.ID, workspaceWorkCap)
	if err != nil {
		return nil, fmt.Errorf("load project work: %w", err)
	}
	ws.WorkCapped = capped
	for _, s := range sessions {
		ws.Work = append(ws.Work, a.recordedOf(dashboard.WorkSessionItem{
			ID: s.ID, Description: store.SubagentGoal(s.Description),
			Status: s.Status, Project: s.Project, Result: compactText(s.Result, 240),
		}, s))
	}
	return ws, nil
}

func (a *App) sandboxState() (*dashboard.SandboxState, error) {
	root, extra := a.toolReg.Roots()
	cfg := a.configSnapshot()
	why, held := a.sandboxHeld(cfg)

	_, open, _ := sandboxOffPlaces(a.activeConfig(cfg), extra)
	return &dashboard.SandboxState{Root: root, ExtraRoots: extra, Off: a.toolReg.SandboxOff(), HeldWhy: why, Held: held, NotKept: open, Labelled: tools.SandboxLabelNote, AgentNotes: agentNotesState(root)}, nil
}

func (a *App) changeSandboxRoot(root string, add bool) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	_, roots := a.toolReg.Roots()
	if add {
		if reason := a.toolReg.RootRejectionReason(root); reason != "" {
			return fmt.Errorf("%q: %s", strings.TrimSpace(root), reason)
		}
		root = filepath.Clean(strings.TrimSpace(root))
		if _, err := os.Stat(root); err == nil {
			root = firewall.ResolvePath(root)
		}
		if slices.Contains(roots, root) {
			return fmt.Errorf("%q is already an extra root; nothing was changed", root)
		}
		roots = append(roots, root)
	} else {
		i := slices.Index(roots, root)
		if i < 0 {
			return fmt.Errorf("%q is not among the extra roots; nothing was changed", root)
		}
		roots = slices.Delete(roots, i, i+1)
	}
	candidate := *a.cfg
	candidate.Tools.ExtraRoots = roots
	published, err := a.persistLocked(&candidate, saveConfig)
	if published {
		a.toolReg.SetExtraRoots(roots)

		if a.toolReg.SandboxOff() {
			a.applySubstrate(candidate)
		}
		a.loadRing5()
		logsink.Info("ring.decision", "extra sandbox roots -> %v (operator setting, floor updated live)", roots)
	}
	if err != nil {
		return fmt.Errorf("sandbox roots: %w", err)
	}
	return nil
}

func (a *App) setSandboxOff(off, signedIn bool) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	candidate := *a.cfg
	candidate.Tools.SandboxOff = off
	if off {

		if _, safe := a.SafeMode(); safe {
			held := a.sandboxHeldInPosture()
			logsink.Warn("ring.refusal", "sandbox switch refused: %v", held)
			return held
		}
		if held := errSandboxHeldNoToken; !a.accessTokenInForce() || !signedIn {
			if a.accessTokenInForce() {
				held = errSandboxHeldUnsigned
			}
			logsink.Warn("ring.refusal", "sandbox switch refused: %v", held)
			return held
		}
		if _, _, err := sandboxOffPlaces(a.activeConfig(candidate), a.grantedRoots()); err != nil {
			logsink.Warn("ring.refusal", "sandbox switch refused: %v", err)
			return err
		}
	}
	published, err := a.persistLocked(&candidate, saveConfig)
	if published {

		if held := a.moveSandbox(candidate, off); held != nil {
			a.loadRing5()
			logsink.Warn("ring.refusal", "sandbox held ON though the saved setting is off: %v", held)
			return held
		}
		a.loadRing5()
		logsink.Info("ring.decision", "%s", sandboxSwitchLine(off, "operator setting, floor updated live"))
	}
	if err != nil {
		return fmt.Errorf("sandbox switch: %w", err)
	}
	return nil
}

func sandboxSwitchLine(off bool, how string) string {
	if off {
		return "sandbox OFF -> the identity's own tools reach beyond the home and the extra roots; the tools still refuse the substrate, other identities' slots and the process table (" + how + ")"
	}
	return "sandbox ON -> the identity's own tools are held to the home and the extra roots (" + how + ")"
}

func (a *App) setToolEnabled(name string, enabled bool) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	found := false
	for _, state := range a.toolReg.ToolStates() {
		found = found || state.Name == name
	}
	if !found {
		return fmt.Errorf("unknown tool: %s", name)
	}

	held, err := disabledTools(a.cfg.Tools.Disabled)
	if err != nil {
		return fmt.Errorf("tool toggle: %w", err)
	}
	disabled := slices.DeleteFunc(held, func(entry string) bool { return entry == name })
	if !enabled {
		disabled = append(disabled, name)
	}
	candidate := *a.cfg
	candidate.Tools.Disabled = disabled
	published, err := a.persistLocked(&candidate, saveConfig)
	if published {
		a.toolReg.SetToolEnabled(name, enabled)
		logsink.Info("ring.decision", "tool %q -> %v (operator toggle)", name, enabled)
	}
	if err != nil {
		return fmt.Errorf("tool toggle: %w", err)
	}
	return nil
}

func (a *App) emitToolEvent(kind, name, args string) {
	a.turn.mu.Lock()
	emit := a.turn.toolEmit
	a.turn.mu.Unlock()
	if emit != nil {
		emit(kind, name, args)
		return
	}
	if a.dashboard != nil {
		a.dashboard.BroadcastEvent(kind, name, args)
	}
}

func (a *App) statsState() (*dashboard.StatsResponse, error) {
	stats, err := a.store.GetStats()
	if err != nil {
		return nil, err
	}

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
		Accounted:         a.accountedCount(),
		MalformedCalls:    a.toolReg.MalformedCallCount(),
		SuspiciousPaths:   a.toolReg.SuspiciousPathCount(),
		DuplicateArgKeys:  a.toolReg.DuplicateArgKeyCount(),
		Update:            a.updateStateView(),
		PluginUpdates:     a.catalogUpdateCount(),
		UninstallOffered:  uninstallBundle() != "",
		ForegroundHolds:   a.fg.Active(),
		Expert:            a.configSnapshot().Dashboard.Expert,
		HostedOnPhone:     a.embeddedAuthentication,
		Notices:           a.noticeRouteState(),
	}, nil
}

func (a *App) recentTurnViews() ([]dashboard.HistoryTurn, error) {

	turns, err := a.store.RecentTurnsIncludingSystem(50)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.HistoryTurn, 0, len(turns))
	attributions, refs := a.speakerAnnotations(turns), a.voiceRefs(turns)
	for _, t := range turns {

		ht := dashboard.HistoryTurn{Role: t.Role, Content: replayContent(t.Role, t.Content), VoiceRef: refs[t.TurnSeq]}
		payload, attributed := attributions[t.TurnSeq]
		if attributed {
			ht.Note = attributionOf(payload)
		}
		if words, named, spoken := a.store.SpokenForThePage(t.TurnSeq, t.Role, t.Content, payload); spoken {
			ht.Content = words
			if named {
				ht.Role, ht.Note = roleOperator, ""
			}
		}
		out = append(out, ht)
	}
	return out, nil
}

const retiredBeliefsShown = 50

func (a *App) identityState() (*dashboard.IdentityState, error) {

	state := &dashboard.IdentityState{}
	beliefs, err := a.store.ListBeliefs()
	if err != nil {
		return nil, fmt.Errorf("identity state: beliefs: %w", err)
	}
	for _, b := range beliefs {
		item := dashboard.BeliefItem{
			ID: b.ID, Statement: b.Statement, Ring: b.Ring,

			Status:        standingOrUnavailable(a.store, b.ID),
			EvidenceCount: b.EvidenceCount, Confidence: b.Confidence,
		}

		changed, ok, err := a.store.LedgerTimeAt(b.LastSeq)
		if err != nil {
			return nil, fmt.Errorf("identity state: belief %s changed: %w", b.ID, err)
		}
		if ok {
			item.ChangedAt = changed.UTC().Format(time.RFC3339)
		}
		state.Beliefs = append(state.Beliefs, item)
	}

	retired, more, err := a.store.RetiredBeliefs(0, retiredBeliefsShown)
	if err != nil {
		return nil, fmt.Errorf("identity state: retired beliefs: %w", err)
	}
	state.Retired = make([]dashboard.RetiredBeliefItem, 0, len(retired))
	for _, r := range retired {
		item := dashboard.RetiredBeliefItem{ID: r.ID, Statement: r.Statement, RetiredAt: r.RetiredAt}
		if r.Replacement.ID != "" {
			item.ReplacedBy = &dashboard.ReplacementItem{ID: r.Replacement.ID, Statement: r.Replacement.Text, Retired: r.Replacement.Retired}
		}
		state.Retired = append(state.Retired, item)
	}
	state.RetiredMore = more
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

		item := dashboard.ExperienceItem{
			ID: e.ID, Content: e.Content, Category: e.Category, CreatedAt: e.CreatedAt, Provenance: e.Provenance,
			Unmetabolized: e.Unmetabolized == 1, OperatorVerbatim: e.OperatorVerbatim}
		if e.CreatedSeq != 0 {
			item.CreatedSeq = strconv.FormatUint(e.CreatedSeq, 10)
		}
		state.Experiences = append(state.Experiences, item)
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

	if state.Note, err = a.operatorNoteView(a.lifetime()); err != nil {
		return nil, fmt.Errorf("the note to the operator: %w", err)
	}

	rel, err := a.store.CurrentOperatorRelationship()
	if err != nil {
		return nil, fmt.Errorf("identity state: operator relationship: %w", err)
	}
	if rel != nil {
		state.Charter = rel.CharterText
		state.TrustLevel = rel.TrustLevel
		state.AutonomyLevel = rel.AutonomyLevel
		if rel.CharterText != "" {
			state.Approval = charterApproval(a.store.CharterApproval(rel))
		}
	}

	others, err := a.store.OtherCurrentRelationships()
	if err != nil {
		return nil, fmt.Errorf("identity state: relationships: %w", err)
	}
	for i := range others {
		r := &others[i]
		item := dashboard.RelationshipItem{ID: r.ID, Name: r.CounterpartName, Role: r.CounterpartRole, Charter: r.CharterText, Record: r.CreatedSeq}
		if r.UpdatedSeq != 0 {
			item.Record = r.UpdatedSeq
		}
		if r.CharterText != "" {
			item.Approval = charterApproval(a.store.CharterApproval(r))
		}
		state.Relationships = append(state.Relationships, item)
	}

	if a.engine != nil {
		ctx, cancel := context.WithTimeout(a.lifetime(), meaningStateTimeout)
		defer cancel()
		if state.Meaning, err = a.meaningState(ctx, a.engine.Instruments(), a.pickMeaningSource()); err != nil {
			return nil, fmt.Errorf("identity state: meaning layer: %w", err)
		}
	}
	return state, nil
}

const meaningStateTimeout = 10 * time.Second

const approvalShown = 1000

func charterApproval(c store.CharterApproval, err error) *dashboard.CharterApproval {
	if err != nil {
		return &dashboard.CharterApproval{State: "unreadable", Error: err.Error()}
	}
	v := &dashboard.CharterApproval{State: string(c.State), Excerpt: c.Excerpt, Turn: c.Turn, SaidAt: c.SaidAt, Record: c.Seq}
	if n := utf8.RuneCountInString(c.Excerpt); n > approvalShown {
		v.Excerpt, v.Chars = string([]rune(c.Excerpt)[:approvalShown]), n
	}
	return v
}

func (a *App) wireContinuityHooks(h *dashboard.WSHandler) {
	h.GetContinuity = a.continuityState
	a.continuity.WireHooks(h)
	if a.identityChoice != nil && h.Continuity != nil {
		h.Continuity.IdentityChoice = a.identityChoiceView
		h.Continuity.ChooseIdentityFiles = a.chooseIdentityFiles
	}
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

	if fac := a.cognition.reviewFacility.Load(); fac != nil {
		if snap := fac.LastReview(); !snap.At.IsZero() {
			c.ReviewAt = snap.At.Format("15:04 Mon Jan 2")
			if snap.Clear {
				c.ReviewStatus = "clear"
			} else {
				c.ReviewStatus = "issues"
				c.ReviewIssues = snap.IssueCount
				c.ReviewLines = append([]string(nil), snap.Issues...)
			}
		}
	}

	switch a.currentMode() {
	case ModeSafe:

		if t, _, _ := a.safeCause(); safePostures[t].degraded {
			c.Mode = "degraded_ring5"
			c.DegradedWhy, _ = a.SafeMode()
		} else {
			c.Mode = "safe"
			c.SafeReason, _ = a.SafeMode()
			c.SafeCleared = a.heldCauseCleared()
		}
		a.dash.mu.Lock()
		c.ServingUnkept = a.dash.servingUnkept
		a.dash.mu.Unlock()
	case ModeDegradedWitness:
		c.Mode = "degraded_witness"
		if since, why, ok := a.DegradedWitnessSince(); ok {
			c.DegradedSince = since.Format("15:04 MST Mon Jan 2")
			c.DegradedWhy = why
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

func (a *App) recordedOf(item dashboard.WorkSessionItem, s store.WorkSession) dashboard.WorkSessionItem {
	item.NextMove = s.NextMove
	item.ExpectedEvidence = s.ExpectedEvidence
	item.Falsifier = s.Falsifier
	item.EvidenceClass = s.Evidence
	item.EvidenceReadback = s.EvidenceReadback
	if c, err := a.store.WorkCheckOf(s.ID); err != nil {
		item.Check = "(could not be read: " + err.Error() + ")"
	} else if c.Command != "" || c.Ran {
		item.Check, item.CheckRun = c.Command, checkRunText(c)
	}
	return item
}

func checkRunText(c store.WorkCheck) string {
	switch {
	case !c.Ran:
		return "not run yet"
	case c.ExitStatus != nil && c.DurationMS != nil:
		return fmt.Sprintf("exited %d in %.1f s", *c.ExitStatus, float64(*c.DurationMS)/1000)
	case c.ExitStatus != nil:
		return fmt.Sprintf("exited %d", *c.ExitStatus)
	}
	return c.Outcome + " " + c.ReasonCode
}

func (a *App) workQueueState() (*dashboard.WorkState, error) {
	w := &dashboard.WorkState{Alarms: []dashboard.AlarmView{}}
	if err := a.fillAlarms(w); err != nil {
		return nil, err
	}
	live, err := a.store.LiveSubagentSessions()
	if err != nil {
		return nil, fmt.Errorf("load live work: %w", err)
	}
	for _, s := range live {
		w.Live = append(w.Live, a.recordedOf(dashboard.WorkSessionItem{
			ID: s.ID, Description: store.SubagentGoal(s.Description), Status: s.Status, Project: s.Project,
		}, s))
	}
	w.Queued, err = a.store.CountLiveWork(identity.SubagentWorkKind)
	if err != nil {
		return nil, fmt.Errorf("count queued work: %w", err)
	}
	done, err := a.store.RecentDeliveredSubagents(3)
	if err != nil {
		return nil, fmt.Errorf("load delivered work: %w", err)
	}

	ids := make([]string, 0, len(done))
	for _, s := range done {
		ids = append(ids, s.ID)
	}
	grades := a.gradesOf(ids)
	for _, s := range done {
		item := a.recordedOf(dashboard.WorkSessionItem{
			ID: s.ID, Description: store.SubagentGoal(s.Description), Status: s.Status, Project: s.Project, Result: compactText(s.Result, 240),
		}, s)
		if g, ok := grades[s.ID]; ok {
			gv := g
			item.Grade = &gv
		}
		w.Delivered = append(w.Delivered, item)
	}
	w.NothingScheduled = a.nothingScheduled(w)
	w.Stage = a.operatorStage()
	w.Resumes = a.resumeView()
	w.Widening = a.wideningOffer()
	return w, nil
}

func (a *App) nothingScheduled(w *dashboard.WorkState) bool {
	if len(w.Alarms) > 0 || len(w.Live) > 0 || w.Queued > 0 {
		return false
	}
	a.turn.mu.Lock()
	queued := a.turn.legsQueued > 0
	a.turn.mu.Unlock()
	if queued {
		return false
	}
	a.asks.mu.Lock()
	asked := len(a.asks.asks) > 0
	a.asks.mu.Unlock()
	if asked {
		return false
	}
	ws, err := a.store.ActiveWorkSession()
	if err != nil || ws == nil {
		return false
	}
	if ws.ResumesOn != "" {
		return false
	}
	unharvested, err := a.store.UnharvestedDeliveries(1)
	return err == nil && len(unharvested) == 0
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
				AgreedBy:    p.Contract.AgreedBy,
				AgreedAt:    p.Contract.AgreedAt,
			}
		}
		ps.Progress = projectDashboardProgress(p)
		out = append(out, ps)
	}
	return out, nil
}

func standingOrUnavailable(src interface {
	StandingFor(id string) (string, error)
}, id string) string {
	standing, err := src.StandingFor(id)
	if err != nil {
		return "unavailable"
	}
	return standing
}

func (a *App) recallForDashboard(query string) (string, error) {
	if a.engine == nil {
		return "", fmt.Errorf("no identity engine is running")
	}
	ctx, cancel := context.WithTimeout(a.lifetime(), 15*time.Second)
	defer cancel()
	return a.engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"query": query})
}

func (a *App) wireIdentityViewHooks(h *dashboard.WSHandler) {

	h.GetIdentity = a.identityState

	h.Recall = a.recallForDashboard

	h.BeliefTrail = a.beliefTrailForDashboard
}

func (a *App) wireWorkHooks(h *dashboard.WSHandler) {
	h.GetWork = a.workQueueState
	h.GradeResult = a.gradeResult
}

const alarmFiringsWindowMs = int64(30 * 24 * time.Hour / time.Millisecond)

func (a *App) fillAlarms(w *dashboard.WorkState) error {
	rows, err := a.store.TimerAlarms()
	if err != nil {
		return fmt.Errorf("load the identity's timers: %w", err)
	}
	fired := map[string]int64{}
	if len(rows) > 0 {
		since := time.Now().UnixMilli() - alarmFiringsWindowMs
		firings, err := a.store.TimerFiringsSince(since)
		if err != nil {
			return fmt.Errorf("load timer firings: %w", err)
		}
		for _, f := range firings {
			id := identity.TimerFiringAlarmID(f.ID)
			if f.CreatedMs > fired[id] {
				fired[id] = f.CreatedMs
			}
		}
	}
	for _, r := range rows {
		tag, msg := identity.DecodeTimerPayload(r.Payload)
		v := dashboard.AlarmView{ID: r.AlarmID, Tag: tag, Message: msg, DueAt: r.Deadline, Every: r.RepeatEvery}
		if at, ok := fired[r.AlarmID]; ok {
			at := at
			v.FiredAt = &at
		}
		w.Alarms = append(w.Alarms, v)
	}
	if rhythm, ok, err := a.store.AlarmByID("rhythm"); err != nil {
		return fmt.Errorf("read the rhythm's alarm: %w", err)
	} else if ok {
		d := rhythm.Deadline
		w.NextPassAt = &d
	}
	return nil
}
