package app

import (
	"context"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (a *App) continuityWires() continuity.Wires {
	return continuity.Wires{
		Settings: func() continuity.Settings { return continuitySettingsOf(a.configSnapshot()) },
		Active: func(id continuity.Identity) continuity.Identity {
			got := a.activeIdentity(IdentityConfig{LedgerPath: id.LedgerPath, DBPath: id.DBPath, KeyPath: id.KeyPath})
			return continuity.Identity{LedgerPath: got.LedgerPath, DBPath: got.DBPath, KeyPath: got.KeyPath}
		},
		Safe:  func() bool { _, safe := a.SafeMode(); return safe },
		Store: func() *store.Store { return a.store },

		Capture: func(ctx context.Context, tailDst string) (ledger.Capture, *store.Pin, error) {
			return a.door.capture(ctx, tailDst)
		},
		DoorKey: func() *crypto.KeyPair {
			if a.door == nil {
				return nil
			}
			return a.door.kp
		},
		Key:    func() *crypto.KeyPair { return a.keyPair },
		SetKey: func(kp *crypto.KeyPair) { a.keyPair = kp },
		LastSeq: func() (uint64, bool) {
			if a.ledger == nil {
				return 0, false
			}
			return a.ledger.LastSeq(), true
		},
		BootChain:    func() string { return a.bootChain },
		BootAttested: func() (int64, string) { return a.bootAttested, a.bootAttestedHash },
		DisplayName:  a.resolveDisplayName,
		Go:           a.runBackground,
		UntilStopped: a.untilStopped,
		BirthProvider: func(req *dashboard.GenesisRequest) error {
			_, err := a.upsertBirthProvider(req)
			return err
		},
		StartLive:  a.startLive,
		SwapToLive: func() { a.dashboard.SwapHandler(a.buildLiveHandler()) },
		DatabasePath: func() string {
			if v := a.databaseView.Load(); v != nil && v.store != nil {
				return v.path
			}
			return ""
		},
	}
}

func continuitySettingsOf(cfg Config) continuity.Settings {
	return continuity.Settings{
		Maintenance: cfg.Maintenance,
		Identity:    continuity.Identity{LedgerPath: cfg.Identity.LedgerPath, DBPath: cfg.Identity.DBPath, KeyPath: cfg.Identity.KeyPath},
		Compression: store.CompressionOptimization{Levels: cfg.Identity.DBOptimizeLevels, Dictionary: cfg.Identity.DBLearnDictionary},
		WitnessURL:  cfg.Witness.URL, WitnessPlatform: cfg.Witness.PlatformPubkeyPath,

		ReplacedPluginMemories: cfg.Plugins.Runtime.replacedMemoryWindow(),
	}
}

func (a *App) publicNameWires() publicname.Wires {
	return publicname.Wires{
		Lifetime: a.lifetime,
		Ctx:      func() context.Context { return a.bgCtx },
		Go:       a.runBackground,
		Door: func() publicname.AppendFunc {
			if a.door == nil {
				return nil
			}
			return a.door.Append
		},
		Store:     func() *store.Store { return a.store },
		Dashboard: func() *dashboard.Server { return a.dashboard },
		TIME:      func() *cognitive.TIME { return a.timeFac },
		Key:       func() *crypto.KeyPair { return a.keyPair },
		IdentityDir: func() string {
			return filepath.Dir(a.activeIdentity(a.configSnapshot().Identity).LedgerPath)
		},
		Settings: func() publicname.Settings { return publicNameSettingsOf(a.configSnapshot()) },

		Alert:    func(kind, detail string) { a.continuity.Alert(kind, detail) },
		Stopping: errStopping,
	}
}

func publicNameSettingsOf(cfg Config) publicname.Settings {
	return publicname.Settings{
		TLS: cfg.Dashboard.TLS, Host: cfg.Dashboard.Host, Origin: cfg.Dashboard.Origin,
		Certificate: cfg.Certificate,
		IdentityDir: filepath.Dir(cfg.Identity.LedgerPath),
	}
}
