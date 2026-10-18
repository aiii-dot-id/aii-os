package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

var processTable = "/proc"

const (
	sandboxHeldSiblings  = "SIBLINGS_UNLISTED"
	sandboxHeldHostFiles = "HOST_FILES_UNNAMED"
	sandboxHeldSafe      = "SAFE"
	sandboxHeldToken     = "ACCESS_TOKEN_OFF"

	sandboxHeldEarlier = "HELD_EARLIER"
)

var errSandboxHeldInSafe = &SandboxHeldError{Why: sandboxHeldSafe,
	Require: "SAFE holds, and while it does the sandbox is on whatever the saved setting says; the saved setting applies again at a start that passes"}

const sandboxNeedsToken = "turning it off requires this dashboard to ask for its access token, so that only a browser signed in with it can, and it does not now. Turn that on in Settings, under Dashboard, with Ask for an access token"

var (
	errSandboxHeldNoToken = &SandboxHeldError{Why: sandboxHeldToken, Require: sandboxNeedsToken}

	errSandboxHeldUnsigned = &SandboxHeldError{Why: sandboxHeldToken,
		Require: "turning it off requires the page that asks to be signed in with this dashboard's access token, and this one is not. Sign in, then tick the box again"}
)

func (a *App) accessTokenInForce() bool {
	return a.dashboard != nil && a.dashboard.AccessTokenRequired()
}

func (a *App) holdSandboxInSafe() {
	if a.ring5Policy == nil || a.ring5Policy.Boundary() {
		return
	}
	a.ring5Policy.SetBoundary(true)
	a.loadRing5()
	logsink.Warn("ring.decision", "sandbox held ON while SAFE holds, though the saved setting is off; the saved setting applies again at a start that passes")
}

type SandboxHeldError struct {
	Why     string
	Require string
	Cause   error
}

func (e *SandboxHeldError) Error() string {
	if e.Cause != nil {
		return "the sandbox stays on: " + e.Require + ": " + e.Cause.Error()
	}
	return "the sandbox stays on: " + e.Require
}

func (e *SandboxHeldError) Unwrap() error { return e.Cause }

func sandboxOffPlaces(cfg Config, roots []string) (kept []*firewall.Rule, open []string, err error) {

	kept, err = otherIdentitiesPlaces(cfg, ".")
	if err != nil {
		return nil, nil, &SandboxHeldError{Why: sandboxHeldSiblings, Cause: err,
			Require: "turning it off requires every other identity's slot on this account to be listed, so that their tools refuse each, and they cannot be listed"}
	}
	if _, err := os.Lstat(processTable); err == nil {
		kept = append(kept, &firewall.Rule{ID: "sub.processes", Kind: firewall.KindSubstrate, Path: realPath(processTable), Shown: processTable, Enforced: true, Linkless: true,
			Reason: "The process table shows the environment and the open files of your own host and of every program on this account. None of it is yours to read."})
	}
	machinery, err := hostMachinery(cfg)
	if err != nil {
		return nil, nil, err
	}
	kept, open = keptByTheBoundaryAlone(append(kept, machinery...), toolsHome(cfg), roots)
	return kept, open, nil
}

func toolsHome(cfg Config) string {
	home := cfg.Tools.CWD
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	return realPath(home)
}

func keptByTheBoundaryAlone(rules []*firewall.Rule, home string, roots []string) (kept []*firewall.Rule, open []string) {
	reach := append([]string{home}, roots...)
	for _, r := range rules {
		within := false
		for i, dir := range reach {
			granted := i > 0 && !r.ReadOpen
			if inside(dir, r.Path) {
				within = true
				if granted && r.Stem {
					open = append(open, r.Path+"*")
				} else if granted {
					open = append(open, r.Path)
				}
				break
			}
			if !r.Stem && inside(r.Path, dir) {
				r.Except = append(r.Except, dir)
				if granted {
					open = append(open, dir)
				}
			}
		}
		if !within {
			r.Outer = true
			kept = append(kept, r)
		}
	}
	slices.Sort(open)
	return kept, slices.Compact(open)
}

const (
	machineryReason = "Your host's own machinery, outside your home: what updates and starts your program, the programs and trust roots it runs by, the credentials it reads, its service's link and stores, and its temporary files. None of it is yours to read or change."
	logsReason      = "Your host's logs, outside your home: yours to read with read, grep and ls, and not to write."
)

func hostMachinery(cfg Config) ([]*firewall.Rule, error) {
	var out []*firewall.Rule
	place := func(id, shown, reason, path string) *firewall.Rule {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		r := &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: realPath(path), Shown: shown, Reason: reason, Enforced: true}
		out = append(out, r)
		return r
	}
	unnamed := func(what string, err error) error {
		return &SandboxHeldError{Why: sandboxHeldHostFiles, Cause: err,
			Require: "turning it off requires " + what + " to be named, so that their tools refuse them, and they cannot be named"}
	}
	both := func(id, shown string, files, stems []string) {
		for _, f := range files {
			place(id, shown, machineryReason, f)
		}
		for _, s := range stems {
			place(id, shown, machineryReason, s).Stem = true
		}
	}

	files, stems, err := updates.Beside()
	if err != nil {
		return nil, unnamed("the files the updater keeps beside your program", err)
	}
	both("sub.update", "your program's update files", files, stems)

	if files, stems, err = install.StartFiles(); err != nil {
		return nil, unnamed("the files that start your program", err)
	}
	both("sub.start", "the files that start your program", files, stems)

	trust := []string{cfg.Plugins.WorkerBinary, cfg.Plugins.CertifierRoot, cfg.Plugins.ReviewerRoot, cfg.Plugins.PlatformRoot, cfg.Witness.PlatformPubkeyPath}
	if cfg.Plugins.DevSection != nil {
		trust = append(trust, cfg.Plugins.DevSection.Path)
	}
	for _, path := range trust {
		if path != "" {
			place("sub.trust", "your operator's trust roots and worker program", machineryReason, path)
		}
	}

	if cfg.SourcePath != "" {

		reg, err := loadProvidersFile(providerFilePath(cfg.SourcePath))
		if err != nil {
			return nil, unnamed("the credentials the provider table has the host borrow", err)
		}
		for _, e := range reg.Providers {
			if e.Credential == "" {
				continue
			}
			if path, err := oauth.BorrowedFile(e.Credential, e.CredentialOptions); err == nil && path != "" {
				place("sub.borrowed", "credentials your host borrows", machineryReason, path)
			}
		}

		for _, suffix := range []string{configBeforeUpdate, configRolledBack} {
			place("sub.config", ConfigFileName, firewall.DefaultPolicy().Rule("sub.config").Reason, cfg.SourcePath+suffix)
		}
	}

	if machine.Supported {
		if dir, err := machineDirOf(cfg, "."); err == nil {
			if link, err := machine.LinkPath(dir); err == nil && !inside(dir, link) {
				place("sub.machine", "the machine service's link and stores", machineryReason, link)
			}
			draining, err := machine.DrainingStores(dir)
			if err != nil {
				return nil, unnamed("the plugin stores the machine service is moving away from", err)
			}
			for _, store := range draining {
				place("sub.store", "the machine service's link and stores", machineryReason, store)
			}
		}
	}

	for _, prefix := range []string{pluginhost.ArtifactDirPrefix, sections.ExtractDirPrefix} {
		place("sub.temp", "your host's temporary files", machineryReason, filepath.Join(os.TempDir(), prefix)).Stem = true
	}

	for _, dir := range []string{cfg.Logs.Dir, filepath.Dir(LogControlPathIn(identityHomeFromConfig(cfg.SourcePath)))} {
		if dir != "" {
			place("sub.logs", "your host's logs", logsReason, dir).ReadOpen = true
		}
	}
	return out, nil
}

func (a *App) grantedRoots() []string {
	if a.toolReg == nil {
		return nil
	}
	_, roots := a.toolReg.Roots()
	return roots
}

func (a *App) nameSubstrate(cfg Config, off bool) (changed bool, err error) {
	policy := a.ensureRing5Policy()
	before := policy.Kept()

	active := a.activeConfig(cfg)
	places := substratePlaces(active, cfg.Tools.CWD)
	if off {
		kept, _, err := sandboxOffPlaces(active, a.grantedRoots())
		if err != nil {
			return false, err
		}
		places = append(places, kept...)
	}
	policy.SetPlaces(places)
	return !slices.Equal(before, policy.Kept()), nil
}

func (a *App) moveSandbox(cfg Config, off bool) error {
	policy := a.ensureRing5Policy()
	if off {

		if _, safe := a.SafeMode(); safe {
			policy.SetBoundary(true)
			return errSandboxHeldInSafe
		}

		if !a.accessTokenInForce() {
			policy.SetBoundary(true)
			return errSandboxHeldNoToken
		}
		if _, err := a.nameSubstrate(cfg, true); err != nil {
			return err
		}
		policy.SetBoundary(false)
		return nil
	}
	policy.SetBoundary(true)
	_, err := a.nameSubstrate(cfg, false)
	return err
}

func (a *App) holdSandboxOn(cfg Config, why error) {
	if err := a.moveSandbox(cfg, false); err != nil {
		logsink.Error("ring.refusal", "the floor's places were not named again after the sandbox was held on: %v", err)
	}
	logsink.Warn("ring.refusal", "sandbox held ON though the saved setting is off: %v", why)
}

func (a *App) keepSandboxOffPlaces() {
	if a.toolReg == nil || !a.toolReg.SandboxOff() {
		return
	}
	cfg := a.configSnapshot()
	if !a.accessTokenInForce() {
		a.holdSandboxOn(cfg, errSandboxHeldNoToken)
		a.loadRing5()
		return
	}
	if others, err := otherIdentitiesPlaces(a.activeConfig(cfg), "."); err == nil {
		kept, fresh := a.ensureRing5Policy().Kept(), true
		others, _ = keptByTheBoundaryAlone(others, toolsHome(cfg), a.grantedRoots())
		for _, o := range others {
			fresh = fresh && slices.Contains(kept, o.Path)
		}
		if fresh {
			return
		}
	}
	if a.applySubstrate(cfg) {
		a.loadRing5()
	}
}

func (a *App) sandboxHeld(cfg Config) (why, sentence string) {
	if _, safe := a.SafeMode(); safe {

		if cfg.Tools.SandboxOff {
			return sandboxHeldSafe, "SAFE holds, and while it does the sandbox is on. The saved setting is off and applies again at a start that passes."
		}
		return sandboxHeldSafe, "SAFE holds, and while it does the sandbox is on and cannot be turned off."
	}
	if !a.accessTokenInForce() {

		if cfg.Tools.SandboxOff {
			return sandboxHeldToken, "The saved setting is off, but the sandbox stays on: " + sandboxNeedsToken + ", then tick the box."
		}
		return sandboxHeldToken, "The sandbox stays on: " + sandboxNeedsToken + "."
	}
	if !cfg.Tools.SandboxOff || a.toolReg == nil || a.toolReg.SandboxOff() {
		return "", ""
	}
	if _, _, err := sandboxOffPlaces(a.activeConfig(cfg), a.grantedRoots()); err != nil {
		return heldWhy(err), "The saved setting is off, but " + err.Error()
	}
	return sandboxHeldEarlier, "The saved setting is off, but the sandbox stays on: what turning it off requires was not there earlier in this run, and is now. Tick the box to turn it off again."
}

func heldWhy(err error) string {
	var held *SandboxHeldError
	if errors.As(err, &held) {
		return held.Why
	}
	return ""
}

func (a *App) applySavedSandbox(cfg Config) {
	if !cfg.Tools.SandboxOff {
		return
	}
	if err := a.moveSandbox(cfg, true); err != nil {
		logsink.Warn("ring.refusal", "sandbox held ON though the saved setting is off: %v", err)
		return
	}
	logsink.Info("ring.decision", "%s", sandboxSwitchLine(true, "saved setting, read at start"))
}
