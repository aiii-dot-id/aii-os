package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

type Runtime struct{ w RuntimeWires }

func NewRuntime(w RuntimeWires) *Runtime { return &Runtime{w: w} }

type RuntimeWires struct {
	Options func() *Options

	Policy  func() pluginfacility.AdmissionPolicy
	Serving func(id string) *ServingSet

	Acquirer func() *Acquirer

	Tools    func() *tools.Registry
	Sections func() *sections.Registry

	Pinned func(act *Activation, v *VoiceSession)

	HandOver time.Duration

	EmbedDeadline func(kind string) time.Duration
}

type Activation struct {
	ID, Version, Package, Hash string
	Plugin                     *ActivePlugin
	Section                    *sections.Section

	ManifestHash string

	watching atomic.Bool

	admitted atomic.Bool
}

func (r *Activation) PluginID() string { return r.ID }

func (r *Activation) HostBytesHeld() (int64, bool) {
	if r.Plugin == nil {
		return 0, false
	}
	return r.Plugin.hostBytesHeld()
}

func (r *Activation) Terminated() <-chan struct{} {
	if r.Plugin == nil {
		return nil
	}
	return r.Plugin.Terminated()
}

func (r *Activation) Failure() ([]time.Time, error) {
	if r.Plugin == nil {
		return nil, nil
	}
	return r.Plugin.Failure()
}

type assetLane struct{ res *packagefmt.Result }

func (h *Runtime) roots() packagefmt.TrustRoots {
	opts := h.w.Options()
	if opts == nil {
		return packagefmt.TrustRoots{}
	}
	return opts.Roots
}

func (h *Runtime) Verify(ctx context.Context, pkg string) (pluginfacility.Evidence, error) {
	res, err := Verify(pkg, h.w.Options())
	if err != nil {
		return pluginfacility.Evidence{}, err
	}
	m := res.Manifest
	return pluginfacility.Evidence{ID: m.ID, Version: m.Version, Package: pkg,
		PackageHash: res.PackageHash, ManifestHash: res.ManifestHash, Tier: res.Tier.String(), Family: m.PluginFamily, Handle: res}, nil
}

func (h *Runtime) Prepare(ctx context.Context, ev pluginfacility.Evidence) (pluginfacility.Prepared, error) {
	res, ok := ev.Handle.(*packagefmt.Result)
	if !ok {
		return pluginfacility.Prepared{}, fmt.Errorf("pluginhost: verification evidence for %s did not survive the stage", ev.ID)
	}
	if res.Manifest.Kind != "plugin" {
		return pluginfacility.Prepared{Evidence: ev, Present: true, Handle: assetLane{res: res}}, nil
	}
	opts := h.w.Options()
	if opts == nil {
		opts = &Options{}
	}
	if res.Manifest.VariantPreference != nil {
		opts.SelectionPolicy = h.w.Policy()
	}

	opts.ChecksFailed = ev.ChecksFailed

	if slices.ContainsFunc(res.Manifest.Variants, func(v packagefmt.Variant) bool { return v.ExecutionRuntime == "native_t3_component" }) {
		opts.Serving = h.w.Serving(ev.ID)
	}
	staged, err := StagePreparedContext(ctx, ev.Package, res, opts)
	if err != nil {
		return pluginfacility.Prepared{}, err
	}
	sel := staged.Selection()

	host := sel.HostBytes
	if host <= 0 {
		if sel.Runtime == "wasm_component" {
			host = int64(opts.MemoryMax[ev.ID])
			if host <= 0 {
				host = pluginworker.DefaultMemoryMaxBytes
			}
		} else {
			host = pluginfacility.DefaultNativeEnvelopeBytes
		}
	}
	out := pluginfacility.Prepared{Evidence: ev, Present: staged.Present(), Handle: staged,
		HostBytes: host, DeviceBytes: sel.DeviceBytes, Backend: sel.Backend}
	if len(sel.Sets) > 0 {

		out.Selection = &pluginfacility.Selection{Variant: sel.VariantID, Better: sel.Better, Kept: sel.Kept, Sets: sel.Sets, Needs: sel.Needs}
		out.Interim = sel.Better != ""
		for _, r := range sel.ResourceRefusals {
			out.Selection.Excluded = append(out.Selection.Excluded, pluginfacility.Exclusion{Variant: r.VariantID, Reasons: append([]string(nil), r.Missing...)})
		}
	}
	if st, ok := h.acquireStatus(ev.ID); ok {
		out.Material = *st
	}
	return out, nil
}

func (h *Runtime) Acquire(ctx context.Context, p pluginfacility.Prepared, progress func(pluginfacility.MaterialStatus)) error {
	staged, ok := p.Handle.(*Staged)
	if !ok {
		return nil
	}
	report := func() {
		if st, ok := h.acquireStatus(p.Evidence.ID); ok && progress != nil {
			progress(*st)
		}
	}
	for {
		err := staged.Acquire(ctx)

		report()

		var acquiring *AcquiringError
		if !errors.As(err, &acquiring) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(acquirePoll):
		}
	}
}

const acquirePoll = time.Second

func (h *Runtime) acquireStatus(id string) (*pluginfacility.MaterialStatus, bool) {
	acq := h.w.Acquirer()
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

func (h *Runtime) Start(ctx context.Context, p pluginfacility.Prepared, lease *pluginfacility.Lease) (pluginfacility.Running, error) {
	ev := p.Evidence
	out := &Activation{ID: ev.ID, Version: ev.Version, Package: ev.Package, Hash: ev.PackageHash, ManifestHash: ev.ManifestHash}
	switch handle := p.Handle.(type) {
	case assetLane:
		sec, serr := sections.ActivateFromPackage(ev.Package, h.roots())
		switch {
		case serr == nil:
			sec.Allowed = lease.Authorized
			out.Section = sec
			return out, nil
		case errors.Is(serr, sections.ErrAssetNotSection):

			return out, nil
		default:
			return nil, serr
		}
	case *Staged:
		ap, err := handle.Start(ctx, h.w.Tools(), false)
		if err != nil {
			return nil, err
		}
		ap.Authorize(lease.Authorized)
		out.Plugin = ap
		return out, nil
	}
	return nil, fmt.Errorf("pluginhost: %s was not prepared", ev.ID)
}

func (h *Runtime) Health(ctx context.Context, r pluginfacility.Running) error {
	act, ok := r.(*Activation)
	if !ok || act.Plugin == nil {
		return nil
	}
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return act.Plugin.Health(hctx)
}

func (h *Runtime) Redirect(from, to pluginfacility.Running) error {
	next, ok := to.(*Activation)
	if !ok {
		return fmt.Errorf("pluginhost: nothing to admit")
	}
	prev, _ := from.(*Activation)

	switch {
	case next.Plugin != nil:
		var prevAp *ActivePlugin
		if prev != nil {
			prevAp = prev.Plugin
		}
		bound := h.w.HandOver
		if bound <= 0 {
			bound = HandOverBudget
		}
		if err := next.Plugin.HandOver(prevAp, bound); err != nil {
			return err
		}
		next.admitted.Store(true)
	case next.Section != nil:

		if reg := h.w.Sections(); reg != nil {
			var prevSec *sections.Section
			if prev != nil {
				prevSec = prev.Section
			}
			if err := reg.Replace(prevSec, next.Section); err != nil {
				return err
			}
		}
	}

	return nil
}

const StopBudget = 5 * time.Second

func (h *Runtime) Stop(ctx context.Context, r pluginfacility.Running) (pluginfacility.Retirement, error) {
	act, ok := r.(*Activation)
	if !ok {
		return pluginfacility.Retirement{Established: true}, nil
	}

	switch {
	case act.Section != nil:
		if reg := h.w.Sections(); reg != nil {
			reg.RemoveOwned(act.Section)
		}
		if err := act.Section.Close(); err != nil {
			return pluginfacility.Retirement{}, err
		}
		return pluginfacility.Retirement{Established: true}, nil
	case act.Plugin == nil:
		return pluginfacility.Retirement{Established: true}, nil
	}

	ap := act.Plugin
	if v := ap.Voice.Load(); v != nil && v.Pins() {
		if act.watching.CompareAndSwap(false, true) {
			h.w.Pinned(act, v)
		}
		return pluginfacility.Retirement{Established: false,
			Residue: []string{"a resident session is open; the engine runs until it closes or its child exits"}}, nil
	}

	dctx, cancel := context.WithTimeout(ctx, StopBudget)
	defer cancel()
	stop := ap.CloseQuiet
	if act.admitted.Load() {
		stop = ap.Deactivate
	}
	if err := stop(dctx); err != nil {

		return pluginfacility.Retirement{Established: false, Residue: []string{err.Error()}, Busy: errors.Is(err, ErrRetirementBusy)}, nil
	}
	logsink.Info("plugins.end", "plugin %s stopped", act.ID)
	return pluginfacility.Retirement{Established: true}, nil
}
