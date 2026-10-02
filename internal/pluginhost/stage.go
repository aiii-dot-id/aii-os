package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func Verify(pkgPath string, opts *Options) (*packagefmt.Result, error) {
	if opts == nil {
		opts = &Options{}
	}
	res, err := packagefmt.VerifyFile(pkgPath, opts.Roots)
	if err != nil {
		return nil, err
	}

	if verr := checkHostWindow(res.Manifest, hostVersionFor(opts)); verr != nil {
		return nil, verr
	}
	return res, nil
}

type Selection struct {
	VariantID   string
	Runtime     string
	Backend     string
	HostBytes   int64
	DeviceBytes *int64

	ResourceRefusals []VariantRefusal

	Kept bool

	Startup StartupAllowance

	variant       *packagefmt.Variant
	profile       *AcceleratorProfile
	models        []ModelDecl
	artifactBytes []byte

	held map[string][]byte
}

func (s Selection) Models() []ModelDecl { return append([]ModelDecl(nil), s.models...) }

func Select(pkgPath string, res *packagefmt.Result, opts *Options) (Selection, error) {
	return SelectContext(context.Background(), pkgPath, res, opts)
}

func SelectContext(ctx context.Context, pkgPath string, res *packagefmt.Result, opts *Options) (Selection, error) {
	if opts == nil {
		opts = &Options{}
	}
	m := res.Manifest

	host := currentHost(opts)
	rels := []string{AcceleratorFile, ModelsFile, RuntimesFile}
	for _, v := range m.Variants {
		if v.Platform == host.platform && v.Arch == host.arch && v.Topology == host.topology {
			rels = append(rels, v.Entrypoint)
		}
	}
	held := walkMembers(pkgPath, res, nil, rels...)
	if m.VariantPreference != nil {
		if err := checkHostWindow(m, hostVersionFor(opts)); err != nil {
			return Selection{}, err
		}
		profiles, err := loadAccelerators(pkgPath, res, held, m)
		if err != nil {
			return Selection{}, err
		}
		if err := validateSelectionProfiles(m, profiles); err != nil {
			return Selection{}, err
		}
		available, err := selectionAvailability(ctx, opts)
		if err != nil {
			return Selection{}, err
		}
		decls, err := loadRuntimes(pkgPath, res, held, m)
		if err != nil {
			return Selection{}, err
		}
		runtimes := make(map[string]RuntimeDecl, len(decls))
		for _, d := range decls {
			runtimes[d.VariantID] = d
		}
		keep := ""
		if opts.Acquirer != nil {
			keep = opts.Acquirer.chosenFor(m.ID, res.PackageHash, res.ManifestHash)
		}
		host.resources = &selectionResources{profiles: profiles, available: credited(available, opts.Replacing), policy: opts.SelectionPolicy,
			runtimes: runtimes, limits: opts.RuntimeLimits, keep: keep}
	}
	variant, serr := selectVariant(res, host)
	if serr != nil {
		return Selection{}, serr
	}
	sel := Selection{VariantID: variant.VariantID, Runtime: variant.ExecutionRuntime, variant: variant}
	if host.resources != nil {
		keep := host.resources.keep
		sel.Kept = keep != "" && keep == sel.VariantID
		for _, v := range m.Variants {
			if v.Platform != host.platform || v.Arch != host.arch || v.Topology != host.topology || v.ExecutionRuntime != "native_t3_component" {
				continue
			}
			if missing := host.resources.refusals(v.VariantID); len(missing) > 0 {
				sel.ResourceRefusals = append(sel.ResourceRefusals, VariantRefusal{VariantID: v.VariantID, Missing: missing})
			}
		}

		var b strings.Builder
		switch {
		case sel.Kept:
			fmt.Fprintf(&b, "component set %s kept: chosen earlier for these bytes, its material in flight or on disk", sel.VariantID)
		case keep != "":
			fmt.Fprintf(&b, "component set %s selected; %s, chosen earlier for these bytes, is no longer possible here", sel.VariantID, keep)
		default:
			fmt.Fprintf(&b, "component set %s selected by the signed preference", sel.VariantID)
		}
		for _, r := range sel.ResourceRefusals {
			fmt.Fprintf(&b, "; passed over %s: %s", r.VariantID, strings.Join(r.Missing, ", "))
		}
		logDecision("plugin %s %s: %s", m.ID, m.Version, b.String())
	}
	if variant.ExecutionRuntime == "native_t3_component" {
		var profile *AcceleratorProfile
		var perr error
		if host.resources != nil {
			p := host.resources.profiles[variant.VariantID]
			profile = &p
		} else {
			profile, perr = loadAccelerator(pkgPath, res, held, m, variant.VariantID)
		}
		if perr != nil {
			return Selection{}, perr
		}
		models, merr := loadModels(pkgPath, res, held, m, profile)
		if merr != nil {
			return Selection{}, merr
		}
		sel.profile, sel.models = profile, models
		if profile != nil {
			sel.Backend, sel.HostBytes, sel.DeviceBytes = profile.Backend, profile.MemoryBytes, profile.DeviceMemoryBytes
		}
	}
	sel.Startup = startupAllowance(opts, m.ID, sel.profile)
	artifactBytes, aerr := loadVerifiedMember(pkgPath, res, held, variant.Entrypoint)
	if aerr != nil {
		return Selection{}, aerr
	}

	for _, v := range m.Variants {
		delete(held, v.Entrypoint)
	}
	sel.artifactBytes, sel.held = artifactBytes, held
	return sel, nil
}

func Prepare(pkgPath string, res *packagefmt.Result, sel Selection, opts *Options) (Material, error) {
	if opts == nil {
		opts = &Options{}
	}
	if sel.Runtime != "native_t3_component" {
		return Material{}, nil
	}
	m := res.Manifest
	material := Material{PluginID: m.ID, Version: m.Version, limits: opts.RuntimeLimits}
	if len(sel.models) > 0 {
		if opts.PluginModelsDir == "" {
			return Material{}, &ModelsMissingError{PluginID: m.ID, Missing: modelNames(sel.models), Cause: fmt.Errorf("this host keeps no models directory")}
		}
		material.Models = sel.models
		material.ModelsDir = pluginStorageDir(opts.PluginModelsDir, m.ID)
	}
	decl, rerr := loadRuntime(pkgPath, res, sel.held, m, sel.variant.VariantID)
	if rerr != nil {
		return Material{}, rerr
	}
	if decl != nil {
		if opts.PluginRuntimeDir == "" {
			return Material{}, &RuntimeMissingError{PluginID: m.ID, Cause: fmt.Errorf("this host keeps no runtime directory")}
		}
		material.Runtime = decl
		material.RuntimeDir = pluginStorageDir(opts.PluginRuntimeDir, m.ID)
		material.entry = entrypointSpec{Name: filepath.Base(sel.variant.Entrypoint), Bytes: sel.artifactBytes, Digest: res.FileDigests[sel.variant.Entrypoint]}
	}
	return material, nil
}

func Acquire(ctx context.Context, mat Material, opts *Options) (string, error) {
	if opts == nil {
		opts = &Options{}
	}
	if mat.PluginID == "" {
		return "", nil
	}
	for _, location := range []struct{ base, selected string }{
		{opts.PluginModelsDir, mat.ModelsDir}, {opts.PluginRuntimeDir, mat.RuntimeDir},
	} {
		if location.base != "" && location.selected != "" {
			if _, err := opts.upgradeStorageDir(ctx, location.base, mat.PluginID); err != nil {
				return "", err
			}
		}
	}
	if opts.Acquirer != nil {
		pending, err := opts.Acquirer.pending(mat)
		if err != nil {
			return "", err
		}
		if pending {
			st, _ := opts.Acquirer.Status(mat.PluginID)
			return "", &AcquiringError{PluginID: mat.PluginID, Version: mat.Version, Cause: fmt.Errorf("material acquisition has not settled"), Status: st}
		}
	}
	modelFetch, runtimeFetch, matCtx := opts.ModelFetcher, opts.RuntimeFetcher, ctx
	if opts.Acquirer != nil {
		modelFetch, runtimeFetch, matCtx = nil, nil, opts.Acquirer.lifetime()
	}
	root, eerr := mat.ensure(matCtx, modelFetch, runtimeFetch, logDecision)
	if eerr != nil {
		if opts.Acquirer != nil && acquirable(eerr) {
			opts.Acquirer.Want(mat)
			st, _ := opts.Acquirer.Status(mat.PluginID)
			return "", &AcquiringError{PluginID: mat.PluginID, Version: mat.Version, Cause: eerr, Status: st}
		}
		return "", eerr
	}
	return root, nil
}

type Staged struct {
	pkgPath     string
	opts        *Options
	res         *packagefmt.Result
	sel         Selection
	material    Material
	runtimeRoot string

	acquired bool
}

type NotAcquiredError struct {
	PluginID string
	Version  string
}

func (e *NotAcquiredError) Error() string {
	return fmt.Sprintf("pluginhost: %s %s declares material that has not been acquired; call Acquire before Start (nothing was bound)", e.PluginID, e.Version)
}

func Stage(ctx context.Context, pkgPath string, opts *Options) (*Staged, error) {
	if opts == nil {
		opts = &Options{}
	}
	res, err := Verify(pkgPath, opts)
	if err != nil {
		return nil, err
	}
	return StageVerified(ctx, pkgPath, res, opts)
}

func StageVerified(ctx context.Context, pkgPath string, res *packagefmt.Result, opts *Options) (*Staged, error) {
	s, err := StagePreparedContext(ctx, pkgPath, res, opts)
	if err != nil {
		return nil, err
	}
	if err := s.Acquire(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func StagePrepared(pkgPath string, res *packagefmt.Result, opts *Options) (*Staged, error) {
	return StagePreparedContext(context.Background(), pkgPath, res, opts)
}

func StagePreparedContext(ctx context.Context, pkgPath string, res *packagefmt.Result, opts *Options) (*Staged, error) {
	if opts == nil {
		opts = &Options{}
	}

	if verr := checkHostWindow(res.Manifest, hostVersionFor(opts)); verr != nil {
		return nil, verr
	}
	sel, serr := SelectContext(ctx, pkgPath, res, opts)
	if serr != nil {
		return nil, serr
	}
	material, merr := Prepare(pkgPath, res, sel, opts)
	if merr != nil {
		return nil, merr
	}
	return &Staged{pkgPath: pkgPath, opts: opts, res: res, sel: sel, material: material}, nil
}

func (s *Staged) Acquire(ctx context.Context) error {
	s.acquired, s.runtimeRoot = false, ""
	root, err := Acquire(ctx, s.material, s.opts)
	var acquiring *AcquiringError
	if err == nil || errors.As(err, &acquiring) {
		s.remember()
	}
	if err != nil {
		return err
	}
	s.runtimeRoot, s.acquired = root, true
	return nil
}

func (s *Staged) remember() {
	m := s.res.Manifest
	if s.opts.Acquirer == nil || m.VariantPreference == nil || s.sel.profile == nil || s.material.PluginID == "" {
		return
	}
	s.opts.Acquirer.choose(m.ID, s.res.PackageHash, s.res.ManifestHash, s.sel.VariantID)
}

func (s *Staged) Material() Material { return s.material }

func (s *Staged) Present() bool { return s.material.PluginID == "" }

func (s *Staged) ID() string                 { return s.res.Manifest.ID }
func (s *Staged) Version() string            { return s.res.Manifest.Version }
func (s *Staged) Tier() packagefmt.Tier      { return s.res.Tier }
func (s *Staged) PackageHash() string        { return s.res.PackageHash }
func (s *Staged) Package() string            { return s.pkgPath }
func (s *Staged) Selection() Selection       { return s.sel }
func (s *Staged) Result() *packagefmt.Result { return s.res }
