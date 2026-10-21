package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

type pluginRuntime struct {
	*pluginhost.Runtime
	a *App

	handOver time.Duration
}

func newPluginRuntime(a *App) pluginRuntime {
	return pluginRuntime{Runtime: pluginhost.NewRuntime(pluginhost.RuntimeWires{
		Options:  a.pluginOptions,
		Policy:   func() pluginfacility.AdmissionPolicy { return pluginAdmissionPolicy(a.configSnapshot()) },
		Serving:  a.serving,
		Acquirer: a.acquirer,
		Tools:    func() *tools.Registry { return a.pluginToolReg },
		Sections: func() *sections.Registry { return a.sections },
		Pinned:   a.watchPinnedPredecessor,

		EmbedDeadline: func(kind string) time.Duration {
			if kind == pluginhost.EmbedKindCue {
				return a.memory.limits.cueCall()
			}
			return a.memory.limits.memoryCall()
		},
	}), a: a}
}

type pluginVerdicts struct{ a *App }

func (v pluginVerdicts) LoadVerdicts() ([]byte, error) {
	if v.a.store == nil {
		return nil, nil
	}
	return v.a.store.PluginCheckVerdicts()
}

func (v pluginVerdicts) SaveVerdicts(raw []byte) error {
	if v.a.store == nil {
		return nil
	}
	return v.a.store.SetPluginCheckVerdicts(raw)
}

func (h pluginRuntime) Redirect(from, to pluginfacility.Running) error {
	prev, _ := from.(*pluginhost.Activation)
	if prev != nil && prev.Plugin != nil {
		release, err := h.a.handOverSessions(prev.Plugin, h.handOver)
		if err != nil {
			return err
		}
		defer release()
	}
	if err := h.Runtime.Redirect(from, to); err != nil {
		return err
	}
	next := to.(*pluginhost.Activation)
	switch {
	case next.Plugin != nil:
		var prevAp *pluginhost.ActivePlugin
		if prev != nil {
			prevAp = prev.Plugin
		}
		h.a.adoptPlugin(next, prevAp)
	case next.Section != nil:
		h.a.adoptSection(next, prev)
	default:
		h.a.adoptAsset(next)
	}
	h.a.forgetAcquisition(next.ID)
	h.a.afterPluginChange()
	return nil
}

func (h pluginRuntime) Stop(ctx context.Context, r pluginfacility.Running) (pluginfacility.Retirement, error) {
	if act, ok := r.(*pluginhost.Activation); ok {

		h.a.releaseActivation(act)
		if act.Section == nil && act.Plugin != nil {
			h.a.stopSubscriberOf(act.Plugin)
		}
	}
	ret, err := h.Runtime.Stop(ctx, r)
	if err == nil && ret.Established {

		h.a.wakeSweep()
	}
	return ret, err
}

func (a *App) pluginOptions() *pluginhost.Options {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if a.pluginOpts == nil {
		return nil
	}
	snapshot := *a.pluginOpts
	return &snapshot
}

type pluginState struct {
	pluginMu sync.RWMutex

	storedRemoved atomic.Pointer[string]

	plugins      []*pluginhost.ActivePlugin
	retiring     []*pluginhost.ActivePlugin
	sectionActs  []*sections.Section
	activeMeta   map[string]activePkgMeta
	pluginLife   map[string]pluginLifecycle
	policyRev    uint64
	policyFinger string
	trustGen     uint64
	subscribers  map[string]*pluginSubscriber
	facility     *pluginfacility.Facility
	facilityOnce sync.Once

	policyMu sync.Mutex

	engines engineWork
}

func (a *App) serving(id string) *pluginhost.ServingSet {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	meta, ok := a.activeMeta[id]
	if !ok || meta.owner == nil || meta.owner.Plugin == nil {
		return nil
	}
	ap := meta.owner.Plugin
	out := &pluginhost.ServingSet{PackageHash: meta.owner.Hash, ManifestHash: meta.owner.ManifestHash, Variant: ap.VariantID}
	if ap.Accelerator != nil {
		p := *ap.Accelerator
		out.Profile = &p
	}
	return out
}

func (a *App) adoptPlugin(next *pluginhost.Activation, prev *pluginhost.ActivePlugin) {
	a.pluginMu.Lock()
	if a.activeMeta == nil {
		a.activeMeta = make(map[string]activePkgMeta)
	}
	replaced := false
	for i, p := range a.plugins {
		if p.ID == next.ID {
			a.plugins[i], replaced = next.Plugin, true
			break
		}
	}
	if !replaced {
		a.plugins = append(a.plugins, next.Plugin)
	}
	a.activeMeta[next.ID] = activePkgMeta{pkg: next.Package, owner: next}

	a.startSubscriberLocked(next.Plugin)
	a.pluginMu.Unlock()
	a.markPlugin(next.ID, next.Version, "", "")

	a.noteActivated(next.ID, next.Plugin.Material())
	switch {
	case prev != nil && prev.Version == next.Version && prev.VariantID != next.Plugin.VariantID:

		logsink.Info("plugins.decision", "plugin %s %s: component set %s -> %s side by side; draining the predecessor", next.ID, next.Version, prev.VariantID, next.Plugin.VariantID)
	case prev != nil:
		logsink.Info("plugins.decision", "plugin %s: updated %s -> %s side by side; draining the predecessor", next.ID, prev.Version, next.Version)
	default:
		logsink.Info("plugins.start", "plugin %s activated (%s, %s, variant %s, %s): tools %v", next.Plugin.ID, next.Plugin.Tier, next.Plugin.Mode, next.Plugin.VariantID,
			activationPosture(next.Plugin.Capabilities, a.pluginGranted(next.Plugin.ID)), next.Plugin.Tools())
	}

	a.checkSourceAtAdoption(next.Plugin)
}

func (a *App) adoptSection(next *pluginhost.Activation, prev *pluginhost.Activation) {
	a.pluginMu.Lock()
	if a.activeMeta == nil {
		a.activeMeta = make(map[string]activePkgMeta)
	}
	if prev != nil && prev.Section != nil {
		for i, s := range a.sectionActs {
			if s == prev.Section {
				a.sectionActs = append(a.sectionActs[:i], a.sectionActs[i+1:]...)
				break
			}
		}
	}
	a.sectionActs = append(a.sectionActs, next.Section)
	a.activeMeta[next.ID] = activePkgMeta{pkg: next.Package, owner: next}
	a.pluginMu.Unlock()
	a.markPlugin(next.ID, next.Version, "", "")
	logsink.Info("plugins.start", "section %s activated (slot %s): commands %v topics %v", next.Section.PackageID, next.Section.Decl.Slot, next.Section.Decl.Commands, next.Section.Decl.Topics)
}

func (a *App) adoptAsset(next *pluginhost.Activation) {
	a.pluginMu.Lock()
	if a.activeMeta == nil {
		a.activeMeta = make(map[string]activePkgMeta)
	}
	a.activeMeta[next.ID] = activePkgMeta{pkg: next.Package, owner: next}
	a.pluginMu.Unlock()
	a.markPlugin(next.ID, next.Version, "", "")
	logsink.Info("plugins.refusal", "plugin %s: kind=asset without section.json — nothing activates for it yet", next.Package)
}

func (a *App) releaseActivation(act *pluginhost.Activation) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()

	if meta, ok := a.activeMeta[act.ID]; ok && meta.owner == act {
		delete(a.activeMeta, act.ID)
	}
	switch {
	case act.Plugin != nil:
		for i, p := range a.plugins {
			if p == act.Plugin {
				a.plugins = append(a.plugins[:i], a.plugins[i+1:]...)
				return
			}
		}
	case act.Section != nil:
		for i, s := range a.sectionActs {
			if s == act.Section {
				a.sectionActs = append(a.sectionActs[:i], a.sectionActs[i+1:]...)
				return
			}
		}
	}
}

func (a *App) watchPinnedPredecessor(act *pluginhost.Activation, v *pluginhost.VoiceSession) {
	ap := act.Plugin
	logsink.Info("plugins.decision", "plugin %s: %s holds an open session — pinned until it closes or its process exits", act.ID, ap.Version)
	a.retire(ap)

	a.engines.start("the pinned predecessor "+act.ID+" "+ap.Version, func() {
		released := v.PinReleased()
		untrusted := v.Untrusted()
		for {
			select {
			case <-released:
				if !a.claimRetiring(ap) {
					return
				}
				cctx, cancel := context.WithTimeout(context.Background(), pluginhost.StopBudget)
				if cerr := ap.CloseQuiet(cctx); cerr != nil {
					logsink.Warn("plugins.error", "plugin %s: pinned predecessor %s stop: %v", act.ID, ap.Version, cerr)
				}
				cancel()
				logsink.Info("plugins.end", "plugin %s: pinned predecessor %s released and stopped", act.ID, ap.Version)

				a.pluginFacility().Settle(act.ID)
				a.pokePluginSweep()
				return
			case <-untrusted:

				untrusted = nil
				logsink.Warn("plugins.refusal", "plugin %s: pinned predecessor %s: session untrusted (%s) — asking the engine to abort", act.ID, ap.Version, v.FaultReason())
				actx, acancel := context.WithTimeout(context.Background(), pluginhost.StopBudget)
				if cerr := v.Close(actx, "abort", "host fault: "+v.FaultReason()); cerr != nil {
					logsink.Warn("plugins.error", "plugin %s: pinned predecessor %s: abort not admitted (%v); waiting for the reap", act.ID, ap.Version, cerr)
				}
				acancel()
			}
		}
	})
}

func (a *App) pluginFacility() *pluginfacility.Facility {
	a.facilityOnce.Do(func() {
		if a.facility != nil {
			return
		}
		f := pluginfacility.New(pluginfacility.Config{
			Runtime:  newPluginRuntime(a),
			Capacity: hostCapacity{},
			Discover: a.discoverPlugins,
			Spawn:    a.runBackground,
			Log: func(format string, args ...any) {
				logsink.Info("plugins.decision", format, args...)
			},

			Verdicts:  pluginVerdicts{a},
			HostBuild: Current() + " " + BuildIdentity(),
		})
		a.pluginMu.Lock()
		a.facility = f
		a.pluginMu.Unlock()
	})
	return a.facility
}

func (a *App) pluginPolicy(cfg Config, minTier packagefmt.Tier, trustMoved bool) pluginfacility.Policy {
	a.pluginMu.Lock()
	if trustMoved {
		a.trustGen++
	}

	admission := pluginAdmissionPolicy(cfg)

	safeReason, safe := a.SafeMode()
	if fp := fmt.Sprintf("%s|%d|%d|%d|safe=%t", cfg.Plugins.Autoload, admission.ReserveBytes, admission.BudgetBytes, admission.MaxConcurrentStarts, safe); fp != a.policyFinger {
		a.policyFinger, a.policyRev = fp, a.policyRev+1
	}
	rev, gen := a.policyRev, a.trustGen
	a.pluginMu.Unlock()
	return pluginfacility.Policy{
		Revision:  rev,
		TrustGen:  gen,
		Admission: admission,
		Hold:      safe,
		HoldWhy:   safeHoldSentence(safeReason),

		Allows: func(ev pluginfacility.Evidence) (bool, string) {

			if cfg.Plugins.Autoload == "none" {
				return false, "plugins.autoload is none — automatic loading is disabled"
			}
			tier, none, ok := autoloadTier(ev.Tier)
			if !ok || none {
				return true, ""
			}
			if tier < minTier {
				return false, fmt.Sprintf("verified %s is below plugins.autoload %s", ev.Tier, cfg.Plugins.Autoload)
			}
			return true, ""
		},
	}
}

func safeHoldSentence(reason string) string {
	s := "the identity is in SAFE — nothing new is activated while its integrity is unverified"
	if reason != "" {
		s += " (" + reason + ")"
	}
	return s
}

func (a *App) afterPluginChange() {
	a.wakeSweep()
	a.pluginsChanged()
}

func (a *App) wakeSweep() {
	if a.bgCtx != nil {
		select {
		case a.sweepPoke <- struct{}{}:
		default:
		}
	}
}

func (a *App) watchFacility(ctx context.Context) {
	ch, stop := a.pluginFacility().Subscribe()
	a.runBackground(func() {
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
			}
			a.applyFacilitySnapshot()
		}
	})
}

func (a *App) applyFacilitySnapshot() {
	snap := a.pluginFacility().Snapshot()
	a.pluginMu.Lock()
	if a.pluginLife == nil {
		a.pluginLife = map[string]pluginLifecycle{}
	}

	present := map[string]bool{}
	for _, v := range snap.Instances {
		present[v.ID] = true
	}
	for id := range a.pluginLife {
		if !present[id] {
			delete(a.pluginLife, id)
		}
	}
	for _, v := range snap.Instances {

		if v.Held != "" && v.State != pluginfacility.StateActive && v.State != pluginfacility.StateUpdating && v.State != pluginfacility.StateDraining {
			a.pluginLife[v.ID] = pluginLifecycle{version: v.Version, phase: lifeHeld, since: v.Since, note: v.Held, view: v}
			continue
		}
		switch v.State {
		case pluginfacility.StateAcquiring:

			delete(a.pluginLife, v.ID)
		case pluginfacility.StateAdmitting:

			a.pluginLife[v.ID] = pluginLifecycle{version: v.Version, phase: lifeStarting, since: v.Since, note: v.Admission, view: v}
		case pluginfacility.StateUpdating:

			if m := v.Material; m != nil && m.FilesTotal > 0 && m.FilesPresent < m.FilesTotal {
				delete(a.pluginLife, v.ID)
				break
			}
			a.pluginLife[v.ID] = pluginLifecycle{version: v.Version, phase: lifeStarting, since: v.Since, view: v}
		case pluginfacility.StateStarting:

			a.pluginLife[v.ID] = pluginLifecycle{version: v.Version, phase: lifeStarting, since: v.Since, view: v}
		case pluginfacility.StateDraining:

			a.pluginLife[v.ID] = pluginLifecycle{version: v.Version, phase: lifeRetiring, since: v.Since, view: v}
		case pluginfacility.StateRefused:
			a.pluginLife[v.ID] = pluginLifecycle{version: v.Version, phase: lifeRefused,
				reason: refusalReason(v.Refusal), since: v.Since, view: v}
		default:
			delete(a.pluginLife, v.ID)
		}
	}
	a.pluginMu.Unlock()
	a.pluginsChanged()
}

func refusalReason(r *pluginfacility.Refusal) string {
	if r == nil {
		return ""
	}
	out := r.Error()
	if r.Remedy != "" {
		out += " — " + r.Remedy
	}
	return out
}

func lifecycleView(v pluginfacility.InstanceView) *dashboard.PluginLifecycleView {
	out := &dashboard.PluginLifecycleView{State: string(v.State), Admission: v.Admission, Residue: append([]string(nil), v.Residue...), Held: v.Held}
	if !v.Since.IsZero() {
		out.Since = v.Since.UTC().Format(time.RFC3339)
	}
	if !v.RetryAt.IsZero() {
		out.RetryAt = v.RetryAt.UTC().Format(time.RFC3339)
	}
	for _, a := range v.Activations {
		av := dashboard.PluginActivationView{Gen: uint64(a.Gen), Role: string(a.Role), Version: a.Version, Selection: selectionView(a.Selection), Checks: checksView(a.Checks)}
		if !a.Since.IsZero() {
			av.Since = a.Since.UTC().Format(time.RFC3339)
		}
		for st, d := range a.Timings {
			if av.Timings == nil {
				av.Timings = map[string]int64{}
			}
			av.Timings[string(st)] = int64(d / time.Millisecond)
		}
		out.Activations = append(out.Activations, av)
	}
	out.Refusal = refusalView(v.Refusal)
	return out
}

func checksView(c *pluginfacility.ChecksVerdict) *dashboard.PluginChecksView {
	if c == nil {
		return nil
	}
	out := &dashboard.PluginChecksView{State: c.State, Variant: c.Variant, Timeouts: c.Timeouts}
	if !c.At.IsZero() {
		out.At = c.At.UTC().Format(time.RFC3339)
	}
	if f := c.Failed; f != nil {
		out.Check, out.Outcome, out.Words = f.Check, string(f.Outcome), f.Words
	}
	return out
}

func selectionView(s *pluginfacility.Selection) *dashboard.PluginSelectionView {
	if s == nil {
		return nil
	}
	out := &dashboard.PluginSelectionView{Variant: s.Variant, Better: s.Better, Kept: s.Kept, Needs: s.Needs}
	for _, e := range s.Excluded {
		out.Excluded = append(out.Excluded, dashboard.PluginExclusionView{Variant: e.Variant, Reasons: append([]string(nil), e.Reasons...)})
	}
	for _, v := range s.Sets {
		sv := dashboard.PluginSetView{Variant: v.Variant, Chosen: v.Chosen, Arriving: v.Arriving, Why: v.Why, Storage: v.Storage, Bandwidth: v.Bandwidth, Resources: v.Resources}
		for _, r := range v.Reasons {
			sv.Reasons = append(sv.Reasons, dashboard.PluginReasonView{Words: r.Words, Remedy: r.Remedy})
		}
		out.Sets = append(out.Sets, sv)
	}
	return out
}

func latestSelection(v pluginfacility.InstanceView) *dashboard.PluginSelectionView {
	for i := len(v.Activations) - 1; i >= 0; i-- {
		if s := v.Activations[i].Selection; s != nil {
			return selectionView(s)
		}
	}
	return nil
}

func refusalView(r *pluginfacility.Refusal) *dashboard.PluginRefusalView {
	if r == nil {
		return nil
	}
	cause := ""
	if r.Cause != nil {
		cause = r.Cause.Error()
	}
	return &dashboard.PluginRefusalView{Stage: string(r.Stage), Class: string(r.Class), Cause: cause, Remedy: r.Remedy, Evidence: r.Evidence}
}

type activePkgMeta struct {
	pkg string

	owner *pluginhost.Activation
}
