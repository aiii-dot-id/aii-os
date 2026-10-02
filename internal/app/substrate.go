package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func substratePlaces(cfg Config, home string) []*firewall.Rule {
	var places []*firewall.Rule
	for _, p := range substrateObjects(cfg, home) {
		covered := false
		for _, parent := range places {
			if inside(parent.Path, p.Path) {
				covered = true
				break
			}
		}
		if !covered {
			places = append(places, p)
		}
	}
	return places
}

func substrateObjects(cfg Config, home string) []*firewall.Rule {
	data := identityDataDir(cfg.Identity.LedgerPath)
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	var places []*firewall.Rule
	place := func(id, path, reason string, except ...string) {
		if path == "" {
			return
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		path = realPath(path)
		places = append(places, &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: path,
			Except: except, Reason: reason, Enforced: true})
	}
	const kept = "Kept by the host: your record's archives, your keys and credentials, your backups and your plugins' state."
	if !inside(realPath(data), realPath(home)) {
		place("sub.data", data, fmt.Sprintf("Your data directory is the host's — your record, keys, credentials, backups and plugins' state. What you edit in it: %s/ (your name and the dashboard's frame), %s and %s.",
			uiOverlayDirName, uiThemeFile, uiLayoutFile), uiSurfacePaths(cfg.Identity.LedgerPath)...)
	}

	for _, name := range []string{"credentials", "trust", "tls", "witness-keys", "witness-tail.json", "backups",
		"plugins-data", "plugins-models", "plugins-runtime", "plugins-catalog", "snapshot.key"} {
		place("sub.data."+name, filepath.Join(data, name), kept)
	}

	names := firewall.DefaultPolicy()
	place("sub.ledger", cfg.Identity.LedgerPath, names.Rule("sub.ledger").Reason)
	place("sub.key", cfg.Identity.KeyPath, names.Rule("sub.key").Reason)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		place("sub.db", cfg.Identity.DBPath+suffix, names.Rule("sub.db").Reason)
	}
	place("sub.config", cfg.SourcePath, names.Rule("sub.config").Reason)
	place("sub.providers", providerFilePath(cfg.SourcePath), names.Rule("sub.providers").Reason)
	if cfg.Identity.KeyPath != "" {
		place("sub.snapshot-key", escrow.SnapshotKeyPath(cfg.Identity.KeyPath), kept)
	}
	place("sub.plugins", "plugins", "Your installed plugins are the host's: they change by install, never by a file write.")

	if program, err := updates.Program(); err == nil {
		place("sub.binary", program, "Your running program is your body — self-modification is not available to you.")
	}
	for _, prof := range cfg.Plugins.AuthProfiles {
		for _, f := range []string{prof.SecretFile, prof.ClientSecretFile, prof.TokenFile} {
			place("sub.profile", f, "A credential your operator keeps for a plugin — it is not yours to read.")
		}
	}
	return places
}

func (a *App) applySubstrate(cfg Config) bool {
	policy := a.ensureRing5Policy()
	before := policy.Places()

	policy.SetPlaces(substratePlaces(a.activeConfig(cfg), cfg.Tools.CWD))
	return !slices.Equal(before, policy.Places())
}

func identityUISurface(dir string) []string {
	return []string{
		filepath.Join(dir, uiOverlayDirName),
		filepath.Join(dir, uiThemeFile),
		filepath.Join(dir, uiLayoutFile),
	}
}

func uiSurfacePaths(ledgerPath string) []string {
	open := identityUISurface(realPath(identityDataDir(ledgerPath)))
	for i := range open {
		open[i] = realPath(open[i])
	}
	return open
}

type pluginSandbox struct {
	reg     *tools.Registry
	surface []*firewall.Rule
}

func newPluginSandbox(reg *tools.Registry, ledgerPath string) pluginSandbox {
	var surface []*firewall.Rule
	for _, p := range uiSurfacePaths(ledgerPath) {
		surface = append(surface, &firewall.Rule{ID: "sub.ui", Kind: firewall.KindSubstrate, Path: p, Enforced: true,
			Reason: "The dashboard's frame, colours and layout are your operator's and the identity's to re-form, not a plugin's."})
	}
	return pluginSandbox{reg: reg, surface: surface}
}

func (s pluginSandbox) Admit(op, path string) (dir, rel string, err error) {
	dir, rel, err = s.reg.Admit(op, path)
	if err != nil {
		return "", "", err
	}
	admitted := filepath.Join(dir, rel)
	for _, r := range s.surface {
		if r.Covers(admitted) {
			return "", "", &tools.SubstrateError{Rule: r}
		}
	}
	return dir, rel, nil
}

func identityDataDir(ledgerPath string) string {
	dir := filepath.Dir(ledgerPath)
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func realPath(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
