package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/sections"
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
type pluginRuntime struct{ a *App }

// .
// .
type running struct {
	id, version, dir, pkg, hash, kind string
	ap                                *pluginhost.ActivePlugin
	sec                               *sections.Section
	// .
	// .
	// .
	watching atomic.Bool
	// .
	// .
	// .
	// .
	admitted atomic.Bool
}

func (r *running) PluginID() string { return r.id }

// .
// .
// .
type assetLane struct{ res *packagefmt.Result }

func (h pluginRuntime) opts() *pluginhost.Options { return h.a.pluginOpts }

func (h pluginRuntime) roots() packagefmt.TrustRoots {
	if h.a.pluginOpts == nil {
		return packagefmt.TrustRoots{}
	}
	return h.a.pluginOpts.Roots
}

// .
// .
func (h pluginRuntime) Verify(ctx context.Context, pkg string) (pluginfacility.Evidence, error) {
	res, err := pluginhost.Verify(pkg, h.opts())
	if err != nil {
		return pluginfacility.Evidence{}, err
	}
	m := res.Manifest
	return pluginfacility.Evidence{ID: m.ID, Version: m.Version, Package: pkg,
		PackageHash: res.PackageHash, Tier: res.Tier.String(), Family: m.PluginFamily, Handle: res}, nil
}

// .
// .
// .
func (h pluginRuntime) Prepare(ctx context.Context, ev pluginfacility.Evidence) (pluginfacility.Prepared, error) {
	res, ok := ev.Handle.(*packagefmt.Result)
	if !ok {
		return pluginfacility.Prepared{}, fmt.Errorf("pluginhost: verification evidence for %s did not survive the stage", ev.ID)
	}
	if res.Manifest.Kind != "plugin" {
		return pluginfacility.Prepared{Evidence: ev, Present: true, Handle: assetLane{res: res}}, nil
	}
	staged, err := pluginhost.StagePrepared(ev.Package, res, h.opts())
	if err != nil {
		return pluginfacility.Prepared{}, err
	}
	sel := staged.Selection()
	// .
	// .
	// .
	// .
	// .
	host := sel.HostBytes
	if host <= 0 {
		if sel.Runtime == "wasm_component" {
			host = int64(h.opts().MemoryMax[ev.ID])
			if host <= 0 {
				host = pluginworker.DefaultMemoryMaxBytes
			}
		} else {
			host = pluginfacility.DefaultNativeEnvelopeBytes
		}
	}
	out := pluginfacility.Prepared{Evidence: ev, Present: staged.Present(), Handle: staged,
		HostBytes: host, DeviceBytes: sel.DeviceBytes, Backend: sel.Backend}
	if st, ok := h.acquireStatus(ev.ID); ok {
		out.Material = *st
	}
	return out, nil
}

// .
// .
// .
func (h pluginRuntime) Acquire(ctx context.Context, p pluginfacility.Prepared, progress func(pluginfacility.MaterialStatus)) error {
	staged, ok := p.Handle.(*pluginhost.Staged)
	if !ok {
		return nil
	}
	report := func() {
		if st, ok := h.acquireStatus(p.Evidence.ID); ok && progress != nil {
			progress(*st)
		}
	}
	for {
		report()
		err := staged.Acquire(ctx)
		// .
		// .
		// .
		// .
		// .
		// .
		var acquiring *pluginhost.AcquiringError
		if !errors.As(err, &acquiring) {
			report()
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(acquirePoll):
		}
	}
}

// .
// .
const acquirePoll = time.Second

// .
// .
func (h pluginRuntime) acquireStatus(id string) (*pluginfacility.MaterialStatus, bool) {
	acq := h.a.acquirer()
	if acq == nil {
		return nil, false
	}
	st, ok := acq.Status(id)
	if !ok || st.FilesTotal == 0 {
		return nil, false
	}
	return &pluginfacility.MaterialStatus{BytesPresent: st.BytesPresent, BytesTotal: st.BytesTotal,
		FilesPresent: st.FilesPresent, FilesTotal: st.FilesTotal}, true
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
func (h pluginRuntime) Start(ctx context.Context, p pluginfacility.Prepared, lease *pluginfacility.Lease) (pluginfacility.Running, error) {
	ev := p.Evidence
	out := &running{id: ev.ID, version: ev.Version, pkg: ev.Package, hash: ev.PackageHash}
	switch handle := p.Handle.(type) {
	case assetLane:
		sec, serr := sections.ActivateFromPackage(ev.Package, h.roots())
		switch {
		case serr == nil:
			sec.Allowed = lease.Authorized
			out.kind, out.sec = "section", sec
			return out, nil
		case errors.Is(serr, sections.ErrAssetNotSection):
			// .
			// .
			out.kind = "asset"
			return out, nil
		default:
			return nil, serr
		}
	case *pluginhost.Staged:
		ap, err := handle.Start(ctx, h.a.pluginToolReg, false)
		if err != nil {
			return nil, err
		}
		ap.Authorize(lease.Authorized)
		out.kind, out.ap = "plugin", ap
		return out, nil
	}
	return nil, fmt.Errorf("pluginhost: %s was not prepared", ev.ID)
}

// .
// .
func (h pluginRuntime) Health(ctx context.Context, r pluginfacility.Running) error {
	act, ok := r.(*running)
	if !ok || act.ap == nil {
		return nil
	}
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return act.ap.Health(hctx)
}

// .
// .
// .
func (h pluginRuntime) Redirect(from, to pluginfacility.Running) error {
	next, ok := to.(*running)
	if !ok {
		return fmt.Errorf("pluginhost: nothing to admit")
	}
	prev, _ := from.(*running)
	switch next.kind {
	case "plugin":
		var prevAp *pluginhost.ActivePlugin
		if prev != nil {
			prevAp = prev.ap
		}
		if err := next.ap.Redirect(prevAp); err != nil {
			return err
		}
		next.admitted.Store(true)
		h.a.adoptPlugin(next, prevAp)
	case "section":
		// .
		// .
		if h.a.sections != nil {
			var prevSec *sections.Section
			if prev != nil {
				prevSec = prev.sec
			}
			if err := h.a.sections.Replace(prevSec, next.sec); err != nil {
				return err
			}
		}
		h.a.adoptSection(next, prev)
	default:
		h.a.adoptAsset(next)
	}
	h.a.forgetAcquisition(next.id)
	h.a.afterPluginChange()
	return nil
}

// .
// .
// .
// .
// .
// .
// .
func (h pluginRuntime) Stop(ctx context.Context, r pluginfacility.Running) (pluginfacility.Retirement, error) {
	act, ok := r.(*running)
	if !ok {
		return pluginfacility.Retirement{Established: true}, nil
	}
	// .
	// .
	// .
	// .
	// .
	h.a.releaseActivation(act)
	switch {
	case act.sec != nil:
		if h.a.sections != nil {
			h.a.sections.RemoveOwned(act.sec)
		}
		if err := act.sec.Close(); err != nil {
			return pluginfacility.Retirement{}, err
		}
		return pluginfacility.Retirement{Established: true}, nil
	case act.ap == nil:
		return pluginfacility.Retirement{Established: true}, nil
	}

	ap := act.ap
	h.a.stopSubscriberOf(ap)
	if ap.Pinned() {
		if act.watching.CompareAndSwap(false, true) {
			h.a.watchPinnedPredecessor(act)
		}
		return pluginfacility.Retirement{Established: false,
			Residue: []string{"a resident session is open; the engine runs until it closes or its child exits"}}, nil
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
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stop := ap.CloseQuiet
	if act.admitted.Load() {
		stop = ap.Deactivate
	}
	if err := stop(dctx); err != nil {
		return pluginfacility.Retirement{Established: false, Residue: []string{err.Error()}}, nil
	}
	logsink.Info("plugins.end", "plugin %s stopped", act.id)
	return pluginfacility.Retirement{Established: true}, nil
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
func (a *App) adoptPlugin(next *running, prev *pluginhost.ActivePlugin) {
	a.pluginMu.Lock()
	if a.activeMeta == nil {
		a.activeMeta = make(map[string]activePkgMeta)
	}
	replaced := false
	for i, p := range a.plugins {
		if p.ID == next.id {
			a.plugins[i], replaced = next.ap, true
			break
		}
	}
	if !replaced {
		a.plugins = append(a.plugins, next.ap)
	}
	a.activeMeta[next.id] = activePkgMeta{dir: next.dir, pkg: next.pkg, hash: next.hash, kind: "plugin", owner: next}
	// .
	// .
	// .
	// .
	// .
	// .
	a.startSubscriber(next.ap)
	a.pluginMu.Unlock()
	a.markPlugin(next.id, next.version, "", "")
	if prev != nil {
		logsink.Info("plugins.decision", "plugin %s: updated %s -> %s side by side; draining the predecessor", next.id, prev.Version, next.version)
	} else {
		logsink.Info("plugins.start", "plugin %s activated (%s, %s, variant %s): tools %v", next.ap.ID, next.ap.Tier, next.ap.Mode, next.ap.VariantID, next.ap.Tools())
	}
}

// .
func (a *App) adoptSection(next *running, prev *running) {
	a.pluginMu.Lock()
	if a.activeMeta == nil {
		a.activeMeta = make(map[string]activePkgMeta)
	}
	if prev != nil && prev.sec != nil {
		for i, s := range a.sectionActs {
			if s == prev.sec {
				a.sectionActs = append(a.sectionActs[:i], a.sectionActs[i+1:]...)
				break
			}
		}
	}
	a.sectionActs = append(a.sectionActs, next.sec)
	a.activeMeta[next.id] = activePkgMeta{dir: next.dir, pkg: next.pkg, hash: next.hash, kind: "section", owner: next}
	a.pluginMu.Unlock()
	a.markPlugin(next.id, next.version, "", "")
	logsink.Info("plugins.start", "section %s activated (slot %s): commands %v topics %v", next.sec.PackageID, next.sec.Decl.Slot, next.sec.Decl.Commands, next.sec.Decl.Topics)
}

// .
func (a *App) adoptAsset(next *running) {
	a.pluginMu.Lock()
	if a.activeMeta == nil {
		a.activeMeta = make(map[string]activePkgMeta)
	}
	a.activeMeta[next.id] = activePkgMeta{dir: next.dir, pkg: next.pkg, hash: next.hash, kind: "asset", owner: next}
	a.pluginMu.Unlock()
	a.markPlugin(next.id, next.version, "", "")
	logsink.Info("plugins.refusal", "plugin %s: kind=asset without section.json — nothing activates for it yet", next.pkg)
}

// .
// .
// .
func (a *App) releaseActivation(act *running) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	// .
	// .
	// .
	if meta, ok := a.activeMeta[act.id]; ok && meta.owner == act {
		delete(a.activeMeta, act.id)
	}
	switch {
	case act.ap != nil:
		for i, p := range a.plugins {
			if p == act.ap {
				a.plugins = append(a.plugins[:i], a.plugins[i+1:]...)
				return
			}
		}
	case act.sec != nil:
		for i, s := range a.sectionActs {
			if s == act.sec {
				a.sectionActs = append(a.sectionActs[:i], a.sectionActs[i+1:]...)
				return
			}
		}
	}
}

// .
// .
// .
// .
func (a *App) watchPinnedPredecessor(act *running) {
	ap := act.ap
	logsink.Info("plugins.decision", "plugin %s: %s holds an open session — pinned until it closes or its process exits", act.id, ap.Version)
	a.retire(ap)
	go func() {
		released := ap.PinReleased()
		untrusted := ap.Voice.Untrusted()
		for {
			select {
			case <-released:
				if !a.claimRetiring(ap) {
					return
				}
				cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if cerr := ap.CloseQuiet(cctx); cerr != nil {
					logsink.Warn("plugins.error", "plugin %s: pinned predecessor %s stop: %v", act.id, ap.Version, cerr)
				}
				cancel()
				logsink.Info("plugins.end", "plugin %s: pinned predecessor %s released and stopped", act.id, ap.Version)
				// .
				// .
				a.pluginFacility().Settle(act.id)
				a.pokePluginSweep()
				return
			case <-untrusted:
				// .
				// .
				// .
				// .
				// .
				untrusted = nil
				logsink.Warn("plugins.refusal", "plugin %s: pinned predecessor %s: session untrusted (%s) — asking the engine to abort", act.id, ap.Version, ap.Voice.FaultReason())
				actx, acancel := context.WithTimeout(context.Background(), 5*time.Second)
				if cerr := ap.Voice.Close(actx, "abort", "host fault: "+ap.Voice.FaultReason()); cerr != nil {
					logsink.Warn("plugins.error", "plugin %s: pinned predecessor %s: abort not admitted (%v); waiting for the reap", act.id, ap.Version, cerr)
				}
				acancel()
			}
		}
	}()
}

// .

// .
// .
// .
// .
// .
// .
func (a *App) pluginFacility() *pluginfacility.Facility {
	a.facilityOnce.Do(func() {
		if a.facility != nil {
			return
		}
		a.facility = pluginfacility.New(pluginfacility.Config{
			Runtime:  pluginRuntime{a: a},
			Capacity: hostCapacity{},
			Discover: a.discoverPlugins,
			Spawn:    a.runBackground,
			Log: func(format string, args ...any) {
				logsink.Info("plugins.decision", format, args...)
			},
		})
	})
	return a.facility
}

// .
// .
// .
// .
func (a *App) pluginPolicy(cfg Config, minTier packagefmt.Tier, trustMoved bool) pluginfacility.Policy {
	a.pluginMu.Lock()
	if trustMoved {
		a.trustGen++
	}
	// .
	// .
	// .
	// .
	// .
	// .
	admission := pluginfacility.AdmissionPolicy{ReserveBytes: cfg.Plugins.Runtime.AdmissionMemoryReserveBytes,
		BudgetBytes: cfg.Plugins.Runtime.AdmissionMemoryBudgetBytes, MaxConcurrentStarts: cfg.Plugins.Runtime.MaxConcurrentStarts}
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
		// .
		// .
		// .
		// .
		Allows: func(ev pluginfacility.Evidence) (bool, string) {
			// .
			// .
			// .
			// .
			// .
			// .
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

// .
// .
func safeHoldSentence(reason string) string {
	s := "the identity is in SAFE — nothing new is activated while its integrity is unverified"
	if reason != "" {
		s += " (" + reason + ")"
	}
	return s
}

// .
// .
// .
// .
func (a *App) afterPluginChange() {
	if ctx := a.bgCtx; ctx != nil {
		a.convergeChannels(ctx)
	}
	a.pluginsChanged()
}

// .

// .
// .
// .
// .
// .
// .
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

// .
// .
func (a *App) applyFacilitySnapshot() {
	snap := a.pluginFacility().Snapshot()
	a.pluginMu.Lock()
	if a.pluginLife == nil {
		a.pluginLife = map[string]pluginLifecycle{}
	}
	// .
	// .
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
		// .
		// .
		// .
		// .
		// .
		if v.Held != "" && v.State != pluginfacility.StateActive && v.State != pluginfacility.StateUpdating && v.State != pluginfacility.StateDraining {
			a.pluginLife[v.ID] = pluginLifecycle{version: v.Version, phase: lifeHeld, since: v.Since, note: v.Held, view: v}
			continue
		}
		switch v.State {
		case pluginfacility.StateAcquiring:
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			delete(a.pluginLife, v.ID)
		case pluginfacility.StateAdmitting:
			// .
			// .
			a.pluginLife[v.ID] = pluginLifecycle{version: v.Version, phase: lifeStarting, since: v.Since, note: v.Admission, view: v}
		case pluginfacility.StateStarting, pluginfacility.StateUpdating:
			// .
			// .
			// .
			// .
			// .
			// .
			a.pluginLife[v.ID] = pluginLifecycle{version: v.Version, phase: lifeStarting, since: v.Since, view: v}
		case pluginfacility.StateDraining:
			// .
			// .
			// .
			// .
			// .
			// .
			// .
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

// .
// .
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

// .
// .
// .
func lifecycleView(v pluginfacility.InstanceView) *dashboard.PluginLifecycleView {
	out := &dashboard.PluginLifecycleView{State: string(v.State), Admission: v.Admission, Residue: append([]string(nil), v.Residue...), Held: v.Held}
	if !v.Since.IsZero() {
		out.Since = v.Since.UTC().Format(time.RFC3339)
	}
	if !v.RetryAt.IsZero() {
		out.RetryAt = v.RetryAt.UTC().Format(time.RFC3339)
	}
	for _, a := range v.Activations {
		av := dashboard.PluginActivationView{Gen: uint64(a.Gen), Role: string(a.Role), Version: a.Version}
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

// .
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
