package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

const ArtifactDirPrefix = "aii-plugin-"

func extractArtifact(res *packagefmt.Result, variant *packagefmt.Variant, artifactBytes []byte, filename string, mode os.FileMode) (dir, path string, verify func() error, err error) {
	dir, err = os.MkdirTemp("", ArtifactDirPrefix+sanitizeToken(res.Manifest.ID)+"-")
	if err != nil {
		return "", "", nil, fmt.Errorf("pluginhost: artifact dir: %w", err)
	}
	path = filepath.Join(dir, filename)
	if err := os.WriteFile(path, artifactBytes, mode); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, fmt.Errorf("pluginhost: write artifact: %w", err)
	}
	verify = func() error {
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return &EntrypointDigestError{Member: variant.Entrypoint, Want: res.FileDigests[variant.Entrypoint]}
		}
		if want, got, ok := res.MemberDigest(variant.Entrypoint, raw); !ok {
			return &EntrypointDigestError{Member: variant.Entrypoint, Want: want, Got: got}
		}
		return nil
	}
	return dir, path, verify, nil
}

func supervisorDispatcher(binding *broker.Binding) supervisor.Dispatcher {
	if binding == nil {
		return nil
	}
	return binding
}

func startSupervisedWASM(ctx context.Context, res *packagefmt.Result, variant *packagefmt.Variant, artifactBytes []byte, binding *broker.Binding, opts *Options, startup StartupAllowance, proof string) (*supervisor.Supervisor, string, error) {
	dir, path, verify, err := extractArtifact(res, variant, artifactBytes, "artifact.wasm", 0o600)
	if err != nil {
		return nil, "", err
	}
	memoryMax := opts.MemoryMax[res.Manifest.ID]
	if memoryMax == 0 {
		memoryMax = pluginworker.DefaultMemoryMaxBytes
	}
	argv := append(append([]string{opts.WorkerBinary}, opts.WorkerArgs...), "-forward",
		fmt.Sprintf("-memory-max=%d", memoryMax),
		"-invoke-timeout="+bbb.DefaultInvokeTimeout.String(),

		"-module-sha256="+res.FileDigests[variant.Entrypoint])
	ready := "event=ready"
	if proof != "" {
		argv = append(argv, "-descriptor-proof="+proof, "-startup-timeout="+startup.Effective.String())

		ready = "descriptor_proof=" + proof
	}
	argv = append(argv, path)
	sup, err := supervisor.StartContext(ctx, supervisor.Spec{
		PluginID:       res.Manifest.ID,
		Argv:           argv,
		ReadyMark:      ready,
		ReadyTimeout:   startup.Effective,
		ReadyAllowance: startup.Sentence(),
		ReadyRemedy:    startup.Remedy(res.Manifest.ID),
		VerifyArtifact: verify,
		ExitMeaning:    supervisor.WorkerExitMeaning,
	}, supervisorDispatcher(binding))
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, "", err
	}
	return sup, dir, nil
}

func startSupervisedNativeWith(ctx context.Context, res *packagefmt.Result, variant *packagefmt.Variant, artifactBytes []byte, binding *broker.Binding, opts *Options, profile *AcceleratorProfile, startup StartupAllowance, modelsDir string, rt *runtimeBinding) (*supervisor.Supervisor, string, bool, error) {
	var dir, path string
	var verify func() error
	if rt != nil {

		path = filepath.Join(rt.root, rt.entry.Name)
		verify = rt.check
	} else {
		var err error
		name := "artifact"
		if packagefmt.HostPlatform() == "windows" {
			name += ".exe"
		}
		dir, path, verify, err = extractArtifact(res, variant, artifactBytes, name, 0o700)
		if err != nil {
			return nil, "", false, err
		}
	}

	if nc := hostcap.Can(hostcap.NativeChild); !nc.Available {
		removeArtifactDir(dir)
		return nil, "", false, fmt.Errorf("plugin %s: native lane unavailable: %s", res.Manifest.ID, nc.Reason)
	}

	image, ierr := OpenVerified(path, res.FileDigests[variant.Entrypoint])
	if ierr != nil {
		removeArtifactDir(dir)
		return nil, "", false, fmt.Errorf("plugin %s: %w", res.Manifest.ID, ierr)
	}

	files := nativeFiles{inherited: len(image.ExtraFiles())}
	if rt != nil {
		files.own = append(files.own, rt.root)
	} else {
		files.own = append(files.own, dir)
	}
	if opts.NativePlaces != nil {
		places, perr := opts.NativePlaces()
		if perr != nil {
			image.Close()
			removeArtifactDir(dir)
			return nil, "", false, fmt.Errorf("plugin %s: its native child is not started: %w", res.Manifest.ID, perr)
		}
		files.places = places
		files.read = append(files.read, dir, modelsDir)
		if rt != nil {
			files.read = append(files.read, rt.root)
		}
		if opts.PluginDataDir != "" {
			files.private = pluginStorageDir(opts.PluginDataDir, res.Manifest.ID)
			files.read = append(files.read, pluginLegacyReadPath(opts.PluginDataDir, res.Manifest.ID, files.private))
		}
	}
	argv, containment, cerr := containArgv(ctx, image.Argv(), profile, files)
	if cerr != nil {
		image.Close()
		removeArtifactDir(dir)
		return nil, "", false, fmt.Errorf("plugin %s: cannot contain the native child: %w", res.Manifest.ID, cerr)
	}
	if rt != nil {
		containment.Description += "; runtime root " + rt.root
	}
	logsink.Info("plugins.start", "plugin %s: native child %s; image %s", res.Manifest.ID, containment, image.Binding())
	spec := supervisor.Spec{
		PluginID:        res.Manifest.ID,
		ArgvContainment: containment,
		Artifact:        image,
		ReadyTimeout:    startup.Effective,
		ReadyAllowance:  startup.Sentence(),
		ReadyRemedy:     startup.Remedy(res.Manifest.ID),
		Argv:            argv,
		Env:             []string{"SEV_PLUGIN_SOCKET=stdio:", "SEV_PLUGIN_ID=" + res.Manifest.ID},
		RLimitASBytes:   opts.MemoryMax[res.Manifest.ID],
		ExtraFiles:      image.ExtraFiles(),
		VerifyArtifact:  verify,
	}

	sessionMode := servesSessionLane(res.Manifest)
	spec.SessionMode = sessionMode
	spec.AudioPair = sessionMode
	if profile != nil {

		spec.ReadyMark = ReadyMark
	}
	if modelsDir != "" {

		spec.Env = append(spec.Env, "AII_MODELS_DIR="+modelsDir)
	}
	if rt != nil {

		spec.Env = append(spec.Env, "AII_RUNTIME_ROOT="+rt.root)
	}

	var grants []string
	var material []plugstore.Entry
	place := func(path string) {
		if e, ok := storeEntryAt(opts.StoreDir, path); ok {
			material = append(material, e)
		} else {
			grants = append(grants, path)
		}
	}
	if rt != nil {
		place(rt.root)
	} else if dir != "" {
		place(dir)
	}
	if modelsDir != "" {
		place(modelsDir)
	}
	spec.AppContainer = wallFor(res.Manifest.ID, grants, material)
	sup, err := supervisor.StartContext(ctx, spec, supervisorDispatcher(binding))
	if err != nil {
		var cleanup *supervisor.ContainmentCleanupError
		if !errors.As(err, &cleanup) {
			image.Close()
			removeArtifactDir(dir)
		}
		return nil, "", false, err
	}
	return sup, dir, sup.Containment().Isolated(), nil
}
