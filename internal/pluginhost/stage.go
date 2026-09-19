package pluginhost

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
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
// .
// .

// .
// .
// .
// .
// .
func Verify(pkgPath string, opts *Options) (*packagefmt.Result, error) {
	if opts == nil {
		opts = &Options{}
	}
	res, err := packagefmt.VerifyFile(pkgPath, opts.Roots)
	if err != nil {
		return nil, err
	}
	// .
	// .
	if verr := checkHostWindow(res.Manifest, hostVersionFor(opts)); verr != nil {
		return nil, verr
	}
	return res, nil
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
type Selection struct {
	VariantID   string
	Runtime     string
	Backend     string
	HostBytes   int64
	DeviceBytes *int64
	// .
	// .
	// .
	Startup StartupAllowance

	// .
	// .
	variant       *packagefmt.Variant
	profile       *AcceleratorProfile
	models        []ModelDecl
	artifactBytes []byte
}

// .
// .
func (s Selection) Models() []ModelDecl { return append([]ModelDecl(nil), s.models...) }

// .
// .
// .
// .
// .
// .
func Select(pkgPath string, res *packagefmt.Result, opts *Options) (Selection, error) {
	if opts == nil {
		opts = &Options{}
	}
	m := res.Manifest
	// .
	// .
	// .
	// .
	variant, serr := selectVariant(res, currentHost(opts))
	if serr != nil {
		return Selection{}, serr
	}
	sel := Selection{VariantID: variant.VariantID, Runtime: variant.ExecutionRuntime, variant: variant}
	if variant.ExecutionRuntime == "native_t3_component" {
		profile, perr := loadAccelerator(pkgPath, res, m, variant.VariantID)
		if perr != nil {
			return Selection{}, perr
		}
		models, merr := loadModels(pkgPath, res, m, profile)
		if merr != nil {
			return Selection{}, merr
		}
		sel.profile, sel.models = profile, models
		if profile != nil {
			sel.Backend, sel.HostBytes, sel.DeviceBytes = profile.Backend, profile.MemoryBytes, profile.DeviceMemoryBytes
		}
	}
	sel.Startup = startupAllowance(opts, m.ID, sel.profile)
	artifactBytes, aerr := loadVerifiedMember(pkgPath, res, variant.Entrypoint)
	if aerr != nil {
		return Selection{}, aerr
	}
	sel.artifactBytes = artifactBytes
	return sel, nil
}

// .
// .
// .
// .
// .
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
		material.ModelsDir = filepath.Join(opts.PluginModelsDir, sanitizeToken(m.ID))
	}
	decl, rerr := loadRuntime(pkgPath, res, m, sel.variant.VariantID)
	if rerr != nil {
		return Material{}, rerr
	}
	if decl != nil {
		if opts.PluginRuntimeDir == "" {
			return Material{}, &RuntimeMissingError{PluginID: m.ID, Cause: fmt.Errorf("this host keeps no runtime directory")}
		}
		material.Runtime = decl
		material.RuntimeDir = filepath.Join(opts.PluginRuntimeDir, sanitizeToken(m.ID))
		material.entry = entrypointSpec{Name: filepath.Base(sel.variant.Entrypoint), Bytes: sel.artifactBytes, Digest: res.FileDigests[sel.variant.Entrypoint]}
	}
	return material, nil
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
func Acquire(ctx context.Context, mat Material, opts *Options) (string, error) {
	if opts == nil {
		opts = &Options{}
	}
	if mat.PluginID == "" {
		return "", nil
	}
	if opts.Acquirer != nil && opts.Acquirer.pending(mat) {
		st, _ := opts.Acquirer.Status(mat.PluginID)
		return "", &AcquiringError{PluginID: mat.PluginID, Version: mat.Version, Cause: fmt.Errorf("material acquisition has not settled"), Status: st}
	}
	modelFetch, runtimeFetch, matCtx := opts.ModelFetcher, opts.RuntimeFetcher, ctx
	if opts.Acquirer != nil {
		modelFetch, runtimeFetch, matCtx = nil, nil, opts.Acquirer.lifetime()
	}
	root, eerr := mat.ensure(matCtx, modelFetch, runtimeFetch, opts.logf)
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
type Staged struct {
	pkgPath     string
	opts        *Options
	res         *packagefmt.Result
	sel         Selection
	material    Material
	runtimeRoot string
	// .
	// .
	acquired bool
}

// .
// .
// .
// .
// .
// .
// .
// .
type NotAcquiredError struct {
	PluginID string
	Version  string
}

func (e *NotAcquiredError) Error() string {
	return fmt.Sprintf("pluginhost: %s %s declares material that has not been acquired; call Acquire before Start (nothing was bound)", e.PluginID, e.Version)
}

// .
// .
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

// .
// .
// .
// .
func StageVerified(ctx context.Context, pkgPath string, res *packagefmt.Result, opts *Options) (*Staged, error) {
	s, err := StagePrepared(pkgPath, res, opts)
	if err != nil {
		return nil, err
	}
	if err := s.Acquire(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// .
// .
// .
// .
// .
func StagePrepared(pkgPath string, res *packagefmt.Result, opts *Options) (*Staged, error) {
	if opts == nil {
		opts = &Options{}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if verr := checkHostWindow(res.Manifest, hostVersionFor(opts)); verr != nil {
		return nil, verr
	}
	sel, serr := Select(pkgPath, res, opts)
	if serr != nil {
		return nil, serr
	}
	material, merr := Prepare(pkgPath, res, sel, opts)
	if merr != nil {
		return nil, merr
	}
	return &Staged{pkgPath: pkgPath, opts: opts, res: res, sel: sel, material: material}, nil
}

// .
// .
// .
func (s *Staged) Acquire(ctx context.Context) error {
	s.acquired, s.runtimeRoot = false, ""
	root, err := Acquire(ctx, s.material, s.opts)
	if err != nil {
		return err
	}
	s.runtimeRoot, s.acquired = root, true
	return nil
}

// .
func (s *Staged) Material() Material { return s.material }

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
func (s *Staged) Present() bool { return s.material.PluginID == "" }

// .
// .
func (s *Staged) ID() string                 { return s.res.Manifest.ID }
func (s *Staged) Version() string            { return s.res.Manifest.Version }
func (s *Staged) Tier() packagefmt.Tier      { return s.res.Tier }
func (s *Staged) PackageHash() string        { return s.res.PackageHash }
func (s *Staged) Package() string            { return s.pkgPath }
func (s *Staged) Selection() Selection       { return s.sel }
func (s *Staged) Result() *packagefmt.Result { return s.res }
