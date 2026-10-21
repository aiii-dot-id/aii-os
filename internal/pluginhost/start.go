package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

type pendingTool struct {
	tool   *operationTool
	hostOp bool
}

func Activate(ctx context.Context, pkgPath string, reg *tools.Registry, opts *Options) (*ActivePlugin, error) {
	return activatePackage(ctx, pkgPath, reg, opts, true)
}

func ActivateShadow(ctx context.Context, pkgPath string, reg *tools.Registry, opts *Options) (*ActivePlugin, error) {
	return activatePackage(ctx, pkgPath, reg, opts, false)
}

func activatePackage(ctx context.Context, pkgPath string, reg *tools.Registry, opts *Options, registerNow bool) (*ActivePlugin, error) {
	st, err := Stage(ctx, pkgPath, opts)
	if err != nil {
		return nil, err
	}
	return st.Start(ctx, reg, registerNow)
}

func (s *Staged) Start(ctx context.Context, reg *tools.Registry, registerNow bool) (*ActivePlugin, error) {
	return s.start(ctx, reg, registerNow)
}

func (s *Staged) start(ctx context.Context, reg *tools.Registry, registerNow bool) (*ActivePlugin, error) {
	pkgPath, opts, res, sel := s.pkgPath, s.opts, s.res, s.sel
	m := res.Manifest

	if s.material.PluginID != "" && !s.acquired {
		return nil, &NotAcquiredError{PluginID: m.ID, Version: m.Version}
	}

	if m.VariantPreference != nil && sel.profile != nil {
		facts, err := selectionAvailability(ctx, opts)
		if err != nil {
			return nil, err
		}
		if missing := resourceRefusals(*sel.profile, credited(facts, opts.Serving.profile()), opts.SelectionPolicy); len(missing) > 0 {
			return nil, &VariantSelectionError{PluginID: m.ID, Host: currentHost(opts).String(),
				Refusals: []VariantRefusal{{VariantID: sel.VariantID, Missing: missing}}}
		}
	}

	declared := []string{SettingsFile, WebhooksFile, ScheduleFile, SubscriptionsFile, EmbeddingsFile, ModelsFile, packagefmt.ValidationFile}
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		declared = append(declared, descriptorFile(decl))
	}
	held := walkMembers(pkgPath, res, nil, declared...)
	schemaBound := 0
	if opts != nil {
		schemaBound = opts.MaxInputSchemaBytes
	}
	descs, derr := loadDescriptors(pkgPath, res, held, m, schemaBound)
	if derr != nil {
		return nil, derr
	}
	if err := validateContextRuntime(descs, m, sel.variant.ExecutionRuntime, currentHost(opts).supervised); err != nil {
		return nil, err
	}

	embeddings, eerr := admitEmbeddings(pkgPath, res, held, m)
	if eerr != nil {
		return nil, eerr
	}

	validation, verr := loadValidation(pkgPath, res, held, m, descs, embeddings)
	if verr != nil {
		return nil, verr
	}
	host := currentHost(opts)
	variant, artifactBytes := sel.variant, sel.artifactBytes

	var proof string
	if variant.ExecutionRuntime == "wasm_component" || variant.ExecutionRuntime == "wasm_aot_component" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, sel.Startup.Effective)
		defer cancel()
		proof, derr = descriptorExpectation(m, descs)
		if derr != nil {
			return nil, derr
		}
		if !host.supervised {
			if err := proveDescriptor(ctx, m, artifactBytes, proof, opts); err != nil {
				return nil, err
			}
		}
	}

	envelope := signedCapabilitySurface(m, variant, res)
	privateDir := ""
	if opts.PluginDataDir != "" {
		privateDir = filepath.Join(opts.PluginDataDir, sanitizeToken(m.ID))
		if res.Tier.PublisherProven() {
			privateDir = pluginStorageDir(opts.PluginDataDir, m.ID)
			if variant.ExecutionRuntime == "native_t3_component" || slices.Contains(envelope, broker.PrivateFilesCapability) {
				var err error
				privateDir, err = opts.upgradeStorageDir(ctx, opts.PluginDataDir, m.ID)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	binding := opts.Broker.BindRelease(m.ID, res.Tier, envelope, broker.Release{Version: m.Version, PackageHash: res.PackageHash})

	ap := &ActivePlugin{
		ID: m.ID, Version: m.Version, Tier: res.Tier, VariantID: variant.VariantID,
		Backends:  m.BackendDeclarations(variant),
		Publisher: m.Publisher, PublisherID: res.PublisherID, Family: m.PluginFamily,
		Title: m.Title, Description: m.Description,
		Interfaces: interfaceNames(m.Interfaces), Capabilities: append([]string(nil), envelope...),
		Runtime: variant.ExecutionRuntime, PackageHash: m.PackageHash,
		reg: reg, binding: binding, envelope: envelope, validation: validation,
		schemaBound: schemaBound,
	}
	if binding != nil {
		binding.SetPublisher(ap)
	}

	fail := func(err error) (*ActivePlugin, error) {
		return nil, errors.Join(err, binding.CloseContext(ctx))
	}
	if err := s.bind(ap, binding, descs, held, privateDir); err != nil {
		return fail(err)
	}

	var inv invoker
	var werr error
	switch {
	case host.supervised && variant.ExecutionRuntime == "wasm_component":
		inv, werr = s.startWASMSupervised(ctx, ap, binding, proof)
	case variant.ExecutionRuntime == "native_t3_component":

		inv, werr = s.startNativeT3(ctx, ap, binding)
	default:
		inv, werr = s.startInProcess(ctx, ap, binding)
	}
	if werr != nil {
		return fail(werr)
	}

	abort := func(err error) (*ActivePlugin, error) {

		for _, name := range ap.ToolNames {
			reg.Deregister(name)
		}
		return fail(errors.Join(err, ap.closeChannel(ctx)))
	}

	ap.inv = inv
	ap.acts = opts.Acts
	if err := ap.validateCalibration(descs); err != nil {
		return abort(err)
	}

	seen := make(map[string]bool)
	hostDriven := ap.hostDriven()
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		if !ap.offersOperationsFor(decl.ID) {
			continue
		}
		for _, method := range decl.Methods {
			name, terr := toolName(m.ID, method)
			if terr != nil {
				return abort(terr)
			}
			if seen[name] {
				return abort(&ToolNameError{PluginID: m.ID, Name: name,
					Reason: "two declared methods sanitize to the same tool name; refusing to guess which operation the identity would call"})
			}
			seen[name] = true
			desc := descs.ops[method]

			description := fmt.Sprintf("Operation %q of interface %s@%d, plugin %s (brokered: every external effect is a per-invocation capability decision; ungranted plugins are pure compute).",
				method, decl.ID, decl.Version, m.ID)
			if desc != nil && desc.summary != "" {
				description = tools.AuthorLine(m.ID, desc.summary, name) + " — " + description
			}
			t := ap.newOperationTool(name, method, description, desc,
				desc.discovery(m.ID, m.Version, ap.Tier.String(), method))

			hostOp := decl.ID == ChannelInterfaceID || decl.ID == EmbeddingsInterfaceID || hostDriven[method] || t.ContextProvider()
			ap.pending = append(ap.pending, pendingTool{tool: t, hostOp: hostOp})
		}
	}

	ch, cherr := channelOf(m)
	if cherr != nil {
		return abort(cherr)
	}
	ap.Channel = ch

	ap.Embeddings = embeddings

	if registerNow {
		if rerr := ap.registerTools(); rerr != nil {
			return abort(rerr)
		}
	}

	return ap, nil
}

func (s *Staged) bind(ap *ActivePlugin, binding *broker.Binding, descs *descriptorSet, held map[string][]byte, privateDir string) error {
	pkgPath, opts, res := s.pkgPath, s.opts, s.res
	m := res.Manifest

	decls, settingsErr := loadSettings(pkgPath, res, held, m)
	if settingsErr != nil {
		return settingsErr
	}
	ap.Settings = decls
	hooks, herr := loadWebhooks(pkgPath, res, held, m, decls)
	if herr != nil {
		return herr
	}
	ap.Webhooks = hooks
	if opts != nil {
		ap.webhookURL = opts.WebhookURL
	}
	sched, serr := loadSchedule(pkgPath, res, held, m)
	if serr == nil {
		serr = holdScheduleToDescriptors(m.ID, sched, descs)
	}
	if serr != nil {
		return serr
	}
	ap.Schedule = sched
	subs, suberr := loadSubscriptions(pkgPath, res, held, m)
	if suberr != nil {
		return suberr
	}
	ap.Subscriptions = subs
	if binding != nil && opts.StreamMax != nil {
		binding.SetStreamCap(opts.StreamMax[m.ID])
	}
	if binding != nil && opts.PluginDataDir != "" {
		binding.SetFiles(privateDir, opts.FilesMax[m.ID])
	}
	if binding != nil {
		source := opts.Settings
		binding.SetSettings(func() map[string]any {
			var values map[string]any
			if source != nil {
				values = source(m.ID)
			}
			return EffectiveSettings(decls, values)
		})

		binding.SetDeclaredSettings(func() map[string]any { return EffectiveSettings(decls, nil) })
	}
	return nil
}

func (s *Staged) startWASMSupervised(ctx context.Context, ap *ActivePlugin, binding *broker.Binding, proof string) (invoker, error) {
	opts, res, sel := s.opts, s.res, s.sel
	m := res.Manifest
	variant, artifactBytes := sel.variant, sel.artifactBytes
	ap.Mode = ModeSupervised
	startup := sel.Startup
	ap.Startup = &startup
	sup, dir, serr := startSupervisedWASM(ctx, res, variant, artifactBytes, binding, opts, startup, proof)
	if serr != nil {
		var child *supervisor.ChildExitError
		if errors.As(serr, &child) && (child.Code == bbb.ExitDescriptorMismatch || child.Code == bbb.ExitDescriptorUnasked) {
			serr = descriptorError(m, serr, child.Code == bbb.ExitDescriptorUnasked)
		}
		return nil, serr
	}
	ap.sup, ap.artifactDir = sup, dir
	return sup, nil
}

func (s *Staged) startNativeT3(ctx context.Context, ap *ActivePlugin, binding *broker.Binding) (invoker, error) {
	opts, res, sel := s.opts, s.res, s.sel
	m := res.Manifest
	variant, artifactBytes := sel.variant, sel.artifactBytes
	material, runtimeRoot := s.material, s.runtimeRoot
	ap.Mode = ModeSupervised
	profile, models := sel.profile, sel.models
	ap.Accelerator = profile
	startup := sel.Startup
	ap.Startup = &startup
	ap.Models = models
	ap.ModelDecls = sel.declared
	ap.ModelsDir = material.ModelsDir
	ap.material = material
	decl, root := material.Runtime, runtimeRoot

	pins, perr := s.verified.pin(material)
	if perr != nil {
		return nil, perr
	}
	ap.pins = pins
	var rt *runtimeBinding
	if decl != nil {
		entry := material.entry
		rt = &runtimeBinding{root: root, entry: entry, check: runtimeSpawnCheck(ap.pins.root, root, decl, entry)}
		ap.RuntimeRoot = root
	}
	sup, dir, contained, serr := startSupervisedNativeWith(ctx, res, variant, artifactBytes, binding, opts, profile, startup, ap.ModelsDir, rt)
	if serr != nil {
		var cleanup *supervisor.ContainmentCleanupError
		if !errors.As(serr, &cleanup) {
			ap.releasePins()
		}
		return nil, serr
	}
	ap.sup, ap.artifactDir, ap.Contained = sup, dir, contained
	if profile != nil {

		ready := ParseReadiness(sup.ReadyLine())
		ap.Readiness = &ready
		if !ready.Real() {
			cerr := ap.closeChannel(ctx)
			return nil, errors.Join(&ReadinessError{PluginID: m.ID, Line: ready.Line}, cerr)
		}

		kit := ""
		if ready.SDK != "" {
			kit = " sdk=" + ready.SDK
		}
		logsink.Info("plugins.start", "plugin %s: native child ready: models_loaded=%d accelerator=%s probe_ms=%d%s", m.ID, ready.ModelsLoaded, ready.Accelerator, ready.ProbeMS, kit)

		if ready.Differs = ready.Against(profile.Backend); ready.Differs != "" {
			logsink.Info("plugins.start", "plugin %s: %s", m.ID, ready.Differs)
		}
	}
	if servesSessionLane(m) {

		if berr := ap.bindVoiceSession(sup); berr != nil {
			cerr := ap.closeChannel(ctx)
			return nil, errors.Join(berr, cerr)
		}
	}
	return sup, nil
}

func (s *Staged) startInProcess(ctx context.Context, ap *ActivePlugin, binding *broker.Binding) (invoker, error) {
	opts, m, artifactBytes := s.opts, s.res.Manifest, s.sel.artifactBytes
	ap.Mode = ModeInProcess
	workerCfg := pluginworker.Config{MemoryMaxBytes: opts.MemoryMax[m.ID]}
	if binding != nil {
		workerCfg.Dispatcher = binding
	}
	mod, lerr := pluginworker.Load(ctx, artifactBytes, workerCfg)
	if lerr != nil {
		return nil, lerr
	}
	ap.module = mod
	return mod, nil
}
