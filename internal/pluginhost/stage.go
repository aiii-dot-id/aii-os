package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
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

	Sets []pluginfacility.SetVerdict

	Kept bool

	Better string

	Needs string

	Startup StartupAllowance

	variant *packagefmt.Variant
	profile *AcceleratorProfile
	models  []ModelDecl

	declared      []ModelDecl
	artifactBytes []byte

	held map[string][]byte

	archives []string
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
	rels := []string{AcceleratorFile, ModelsFile, RuntimesFile, SettingsFile}
	for _, v := range m.Variants {
		if v.Platform == host.platform && v.Arch == host.arch && v.Topology == host.topology {
			rels = append(rels, v.Entrypoint)
		}
	}
	held := walkMembers(pkgPath, res, nil, rels...)
	var profiles map[string]AcceleratorProfile
	if m.VariantPreference != nil {
		if err := checkHostWindow(m, hostVersionFor(opts)); err != nil {
			return Selection{}, err
		}
		var err error
		profiles, err = loadAccelerators(pkgPath, res, held, m)
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
		keep := ""
		if opts.Acquirer != nil {
			keep = opts.Acquirer.chosenFor(m.ID, res.PackageHash, res.ManifestHash)
		}
		host.resources = &selectionResources{profiles: profiles, available: credited(available, opts.Serving.profile()), policy: opts.SelectionPolicy, keep: keep}
	}

	order := nativeOrder(m, host)
	var archives []string
	needs := ""
	if len(order) > 0 {
		var err error
		if profiles == nil {
			if profiles, err = loadAccelerators(pkgPath, res, held, m); err != nil {
				return Selection{}, err
			}
		}

		needs = needsWords(order, profiles)
		decls, err := loadRuntimes(pkgPath, res, held, m)
		if err != nil {
			return Selection{}, err
		}
		for _, d := range decls {
			if slices.Contains(order, d.VariantID) {
				archives = append(archives, d.SHA256)
			}
		}
		host.sets = make(map[string]*setMaterial, len(order))
		for _, id := range order {
			var profile *AcceleratorProfile
			if p, ok := profiles[id]; ok {
				profile = &p
			}
			var rt *RuntimeDecl
			for i := range decls {
				if decls[i].VariantID == id {
					rt = &decls[i]
				}
			}
			mat, err := materialOf(pkgPath, res, held, variantByID(m, id), profile, rt, opts)
			if err != nil {
				return Selection{}, err
			}
			host.sets[id] = mat
		}
	}
	rate := func(string) (float64, bool) { return 0, false }
	if opts.Acquirer != nil {
		rate = opts.Acquirer.linkRate
	}
	variant, serr := selectVariant(res, host)
	if serr != nil {

		if len(host.sets) > 0 && len(serr.Ambiguous) == 0 {
			serr.Sets, serr.Needs = verdicts(res, host, order, "", "", "", rate), needs
		}
		return Selection{}, serr
	}
	better := ""
	if len(host.sets) > 0 {

		if opts.Serving.same(res) && opts.Serving.Variant == variant.VariantID {
			return Selection{}, &NoBetterSetError{PluginID: m.ID, Version: m.Version, Serving: variant.VariantID, Needs: needs,
				Sets: verdicts(res, host, order, variant.VariantID, "serving now, and the set this machine would run of these bytes", "", rate)}
		}

		if opts.Serving == nil {
			if id := installedInPlaceOf(res, host, order, variant.VariantID); id != "" {
				better, variant = variant.VariantID, variantByID(m, id)
			}
		}
	}
	sel := Selection{VariantID: variant.VariantID, Runtime: variant.ExecutionRuntime, variant: variant, Better: better, archives: archives, Needs: needs}
	if len(host.sets) > 0 {
		keep := ""
		if host.resources != nil {
			keep = host.resources.keep
		}
		sel.Kept = keep != "" && keep == sel.VariantID
		for _, id := range order {
			if missing := host.refusals(id); len(missing) > 0 {
				sel.ResourceRefusals = append(sel.ResourceRefusals, VariantRefusal{VariantID: id, Missing: logs(missing)})
			}
		}

		var why string
		switch {
		case sel.Better != "":
			why = servesWhileWords(sel.Better)
		case sel.Kept:
			why = "kept: chosen earlier for these same bytes, and its material is in flight or on disk"
		case keep != "" && len(host.refusals(keep)) == 0:
			why = fmt.Sprintf("chosen: the first in the package's order that this machine can run; %s, chosen earlier for these bytes, is later in that order", keep)
		case keep != "":
			why = fmt.Sprintf("chosen: the first in the package's order that this machine can run; %s, chosen earlier for these bytes, is no longer possible here", keep)
		case m.VariantPreference != nil:
			why = "chosen: the first in the package's order that this machine can run"
		case len(order) == 1:
			why = "chosen: the one component set the package has for this machine"
		default:
			why = "chosen: the package's default among the sets this machine can run"
		}
		sel.Sets = verdicts(res, host, order, sel.VariantID, why, sel.Better, rate)

		var b strings.Builder
		fmt.Fprintf(&b, "component set %s %s", sel.VariantID, why)
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
		var stored map[string]any
		if opts.Settings != nil {
			stored = opts.Settings(m.ID)
		}
		models, declared, merr := loadModels(pkgPath, res, held, m, profile, stored)
		if merr != nil {
			return Selection{}, merr
		}
		sel.profile, sel.models, sel.declared = profile, models, declared
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
	store := plugstore.Store{Dir: opts.StoreDir}

	carrierPkg, err := filepath.Abs(pkgPath)
	if err != nil {
		return Material{}, fmt.Errorf("plugin %s: its package's path: %w", m.ID, err)
	}
	material := Material{PluginID: m.ID, Version: m.Version, limits: opts.RuntimeLimits, store: opts.StoreDir,
		pkg: carrierPkg, archives: sel.archives, adopt: opts.adoptionFor(m.ID)}
	if len(sel.models) > 0 || hasConditional(sel.declared) {
		if opts.StoreDir == "" {
			if len(sel.models) > 0 {
				return Material{}, &ModelsMissingError{PluginID: m.ID, Missing: modelNames(sel.models), Cause: fmt.Errorf("this host has no plugin store")}
			}
		} else {

			var names []string
			for _, d := range sel.models {
				if d.When == nil {
					names = append(names, d.Name)
				}
			}
			material.view = &ViewWork{ModelsSHA256: res.FileDigests[ModelsFile], Names: names}
			dir, err := store.View(material.viewKey())
			if err != nil {
				return Material{}, err
			}
			material.Models, material.ModelsDir = sel.models, dir
		}
	}
	decl, rerr := loadRuntime(pkgPath, res, sel.held, m, sel.variant.VariantID)
	if rerr != nil {
		return Material{}, rerr
	}
	if decl != nil {
		if opts.StoreDir == "" {
			return Material{}, &RuntimeMissingError{PluginID: m.ID, Cause: fmt.Errorf("this host has no plugin store")}
		}
		material.Runtime = decl
		material.RuntimeDir = filepath.Join(opts.StoreDir, plugstore.DirRoots)
		material.entry = entrypointSpec{Name: filepath.Base(sel.variant.Entrypoint), Bytes: sel.artifactBytes, Digest: res.FileDigests[sel.variant.Entrypoint]}
		material.member = sel.variant.Entrypoint
	}

	if err := pathLengthRefusal(material, opts); err != nil {
		return Material{}, err
	}
	return material, nil
}

func acquire(ctx context.Context, mat Material, opts *Options) (string, heldEntries, error) {
	if opts == nil {
		opts = &Options{}
	}
	if mat.PluginID == "" {
		return "", heldEntries{}, nil
	}
	if opts.Acquirer != nil {
		pending, err := opts.Acquirer.pending(mat)
		if err != nil {
			return "", heldEntries{}, err
		}
		if pending {
			st, _ := opts.Acquirer.Status(mat.PluginID)
			return "", heldEntries{}, &AcquiringError{PluginID: mat.PluginID, Version: mat.Version, Cause: fmt.Errorf("material acquisition has not settled"), Status: st}
		}
	}
	verifyCtx := ctx
	if opts.Acquirer != nil {
		verifyCtx = opts.Acquirer.lifetime()
	}
	root, held, err := mat.verify(verifyCtx)
	if err == nil || !acquirable(err) {
		return root, held, err
	}
	mat.damaged = damagedEntries(err)
	if opts.Acquirer != nil {
		opts.Acquirer.Want(mat)
		st, _ := opts.Acquirer.Status(mat.PluginID)
		return "", heldEntries{}, &AcquiringError{PluginID: mat.PluginID, Version: mat.Version, Cause: err, Status: st}
	}
	if opts.Materialize == nil {
		return "", heldEntries{}, err
	}
	if merr := opts.Materialize(ctx, mat); merr != nil {
		return "", heldEntries{}, merr
	}
	return mat.verify(ctx)
}

func damagedEntries(err error) []string {
	var d *MaterialDamageError
	if errors.As(err, &d) {
		return []string{d.Entry}
	}
	return nil
}

type Staged struct {
	pkgPath     string
	opts        *Options
	res         *packagefmt.Result
	sel         Selection
	material    Material
	runtimeRoot string

	verified verifiedEntries

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
	s.acquired, s.runtimeRoot, s.verified = false, "", verifiedEntries{}
	root, held, err := acquire(ctx, s.material, s.opts)

	verified, verr := held.identities(s.material)
	held.release()
	var acquiring *AcquiringError
	if err == nil || errors.As(err, &acquiring) {
		s.remember()
	}
	if err != nil {
		return err
	}
	if verr != nil {
		return verr
	}
	s.runtimeRoot, s.verified, s.acquired = root, verified, true
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
