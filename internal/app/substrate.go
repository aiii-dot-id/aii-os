package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func substratePlaces(cfg Config, home string) []*firewall.Rule {
	var places []*firewall.Rule
	for _, p := range substrateObjects(cfg, home) {
		if p.ID == configDirectoryLabel {
			continue
		}
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

const configDirectoryLabel = "sub.config-directory"

func shellSubstrateLabels(cfg Config, home string) []*firewall.Rule {
	if runtime.GOOS != "windows" {
		return nil
	}
	var labels []*firewall.Rule
	for _, p := range substrateObjects(cfg, home) {
		if p.ID == configDirectoryLabel {
			labels = append(labels, p)
		}
	}
	return labels
}

func substrateObjects(cfg Config, home string) []*firewall.Rule {
	data := identityDataDir(cfg.Identity.LedgerPath)
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	var places []*firewall.Rule
	place := func(id, path, reason string, except ...string) *firewall.Rule {
		if path == "" {
			return nil
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		path = realPath(path)
		r := &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: path,
			Except: except, Reason: reason, Enforced: true}
		places = append(places, r)
		return r
	}
	const kept = "Kept by the host: your record's archives, your keys and credentials, your backups and your plugins' state."
	if !inside(realPath(data), realPath(home)) {
		place("sub.data", data, fmt.Sprintf("Your data directory is the host's — your record, keys, credentials, backups and plugins' state. What you edit in it: %s/ (your name and the dashboard's frame), %s and %s.",
			uiOverlayDirName, uiThemeFile, uiLayoutFile), uiSurfacePaths(cfg.Identity.LedgerPath)...)
	}

	for _, name := range []string{"credentials", "trust", "tls", "witness-keys", "witness-tail.json", "backups",
		"plugins-data", "plugins-models", "plugins-runtime", "plugins-catalog", "snapshot.key"} {
		r := place("sub.data."+name, filepath.Join(data, name), kept)
		if r != nil && (name == "plugins-models" || name == "plugins-runtime") {
			r.NativeReads = true
		}
	}

	place("sub.data."+seedRecordDirName, filepath.Join(data, seedRecordDirName),
		"Kept by the host: which version of SKILLS.md, METHOD.md and ui/README.md it last wrote, so that it upgrades only what it wrote and never your edits.")

	names := firewall.DefaultPolicy()
	place("sub.ledger", cfg.Identity.LedgerPath, names.Rule("sub.ledger").Reason)
	place("sub.key", cfg.Identity.KeyPath, names.Rule("sub.key").Reason)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		place("sub.db", cfg.Identity.DBPath+suffix, names.Rule("sub.db").Reason)
	}

	if runtime.GOOS == "windows" && cfg.SourcePath != "" {
		if dir, err := filepath.Abs(filepath.Dir(cfg.SourcePath)); err == nil {
			dir = realPath(dir)
			if realPath(home) != dir && inside(realPath(home), dir) {
				place(configDirectoryLabel, dir, names.Rule("sub.config").Reason)
			}
		}
	}
	place("sub.config", cfg.SourcePath, names.Rule("sub.config").Reason)
	place("sub.providers", providerFilePath(cfg.SourcePath), names.Rule("sub.providers").Reason)

	if cfg.SourcePath != "" {
		for _, f := range []struct{ id, path string }{{"sub.config", cfg.SourcePath}, {"sub.providers", providerFilePath(cfg.SourcePath)}} {
			places = append(places, &firewall.Rule{ID: f.id, Kind: firewall.KindSubstrate, Path: firewall.StemPath(f.path + "."),
				Stem: true, Shown: filepath.Base(f.path), Reason: names.Rule(f.id).Reason, Enforced: true})
		}
	}
	if cfg.Identity.KeyPath != "" {
		place("sub.snapshot-key", continuity.SnapshotKeyPath(cfg.Identity.KeyPath), kept)
	}
	place("sub.plugins", "plugins", "Your installed plugins are the host's: they change by install, never by a file write.").NativeReads = true

	for _, dir := range logDirs(cfg, home) {
		var except []string
		if inside(dir, logControlFile(cfg)) {
			except = []string{logControlFile(cfg)}
		}
		place("sub.logs", dir, logsReason, except...).ReadOpen = true
	}

	if machine.Supported {
		if dir, err := machineDirOf(cfg, "."); err == nil {
			place("sub.machine", dir, "The machine service's directory belongs to the account, shared by every identity on it — not yours to read or change.")

			if store, err := machine.StoreDir(dir); err == nil {
				place("sub.store", store, "The account's plugin store holds every identity's plugins' models and runtimes, written by the machine service alone — not yours to read or change.").NativeReads = true
			}
		}
	}

	if program, err := updates.Program(); err == nil {
		place("sub.binary", program, "Your running program is your body — self-modification is not available to you.")
	}
	for _, prof := range cfg.Plugins.AuthProfiles {

		token, _ := prof.TokenPath(profilesDir(cfg.Identity.LedgerPath))
		for _, f := range []string{prof.SecretFile, prof.ClientSecretFile, token} {
			place("sub.profile", f, "A credential your operator keeps for a plugin — it is not yours to read.")
		}
	}
	return places
}

const logsReason = "Your host's log and its captures of what your models were sent: yours to read with read, grep and ls, and not to write, but for control.json in it, your switch for what is recorded."

func logDirs(cfg Config, home string) []string {
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	home = realPath(home)
	var dirs []string
	for _, dir := range []string{filepath.Dir(logControlFile(cfg)), cfg.Logs.Dir} {
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if dir = realPath(dir); !inside(dir, home) && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func logControlFile(cfg Config) string {
	path := LogControlPathIn(identityHomeFromConfig(cfg.SourcePath))
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return firewall.ResolveSlot(path)
}

func otherIdentitiesPlaces(cfg Config, slot string) ([]*firewall.Rule, error) {
	if !machine.Supported {
		return nil, nil
	}
	dir, err := machineDirOf(cfg, ".")
	if err != nil {
		return nil, err
	}
	slots, err := machine.RecordedSlots(dir)
	if err != nil {
		return nil, fmt.Errorf("the account's other identities cannot be named from the machine service's state, so none could be hidden from a native plugin: %w", err)
	}
	siblings, err := install.SiblingSlots(slot)
	if err != nil {
		return nil, fmt.Errorf("the identities beside this one cannot be listed, so none could be hidden from a native plugin: %w", err)
	}
	own := realPath(slot)
	if abs, err := filepath.Abs(slot); err == nil {
		own = realPath(abs)
	}
	var out []*firewall.Rule
	seen := map[string]bool{own: true}
	for _, s := range append(slots, siblings...) {
		if p := realPath(s); s != "" && !seen[p] {
			seen[p] = true

			out = append(out, &firewall.Rule{ID: firewall.RuleOtherIdentity, Kind: firewall.KindSubstrate, Path: p, Shown: "other identities' slots", Enforced: true,
				Reason: "Another identity's slot on this account: its keys, its record and its credentials are its own."})
		}
	}
	return out, nil
}

func (a *App) applySubstrate(cfg Config) bool {
	off := !a.ensureRing5Policy().Boundary()
	changed, err := a.nameSubstrate(cfg, off)
	if err != nil {
		a.holdSandboxOn(cfg, err)
		return true
	}
	return changed
}

func identityUISurface(dir string) []string {
	return []string{
		filepath.Join(dir, uiOverlayDirName),
		filepath.Join(dir, uiThemeFile),
		filepath.Join(dir, uiLayoutFile),
	}
}

func uiSurfacePaths(ledgerPath string) []string {
	data := realPath(identityDataDir(ledgerPath))
	var open []string
	for _, p := range identityUISurface(data) {
		slot := firewall.ResolveSlot(p)
		if !inside(data, slot) || slot == data {
			logsink.Warn("ring.refusal", "the dashboard surface %s does not lie in the data directory %s, so it is not open", slot, data)
			continue
		}
		open = append(open, slot)
	}
	return open
}

func linkLeads(p string) (target string, linked bool, err error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
		return p, false, nil
	}
	t, err := os.Readlink(p)
	if err != nil {
		return "", true, err
	}
	t = strings.TrimPrefix(t, `\??\`)
	if !filepath.IsAbs(t) {
		t = filepath.Join(filepath.Dir(p), t)
	}
	return realPath(t), true, nil
}

type pluginSandbox struct {
	reg     *tools.Registry
	surface []*firewall.Rule

	gate tools.Gate
}

func newPluginSandbox(reg *tools.Registry, cfg Config) pluginSandbox {
	const (
		uiWhy  = "The dashboard's frame, colours and layout are your operator's and the identity's to re-form, not a plugin's."
		logWhy = "The identity's log, and the switch for what it records, are not a plugin's to read or change."
	)
	var surface []*firewall.Rule
	for _, p := range uiSurfacePaths(cfg.Identity.LedgerPath) {
		surface = append(surface, keptFromPlugins("sub.ui", p, uiWhy)...)
	}
	surface = append(surface, keptFromPlugins("sub.logs", logControlFile(cfg), logWhy)...)
	return pluginSandbox{reg: reg, surface: surface}
}

func keptFromPlugins(id, p, why string) []*firewall.Rule {

	rules := []*firewall.Rule{{ID: id, Kind: firewall.KindSubstrate, Path: p, Enforced: true, Reason: why}}

	target, linked, err := linkLeads(p)
	switch {
	case !linked || realPath(p) != firewall.ResolveSlot(p):
	case err != nil:
		logsink.Warn("ring.refusal", "where %s leads could not be read, so a plugin is refused every file: %v", p, err)
		rules = append(rules, &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: filepath.VolumeName(p) + string(filepath.Separator), Enforced: true, Reason: why})
	case target != p:
		rules = append(rules, &firewall.Rule{ID: id, Kind: firewall.KindSubstrate, Path: target, Enforced: true, Reason: why})
	}
	return rules
}

func (s pluginSandbox) Look() tools.Gate {
	s.gate = s.reg.Look()
	return s
}

func (s pluginSandbox) sandbox() tools.Gate {
	if s.gate != nil {
		return s.gate
	}
	return s.reg
}

func (s pluginSandbox) Admit(op, path string) (dir, rel string, err error) {
	dir, rel, err = s.sandbox().Admit(op, path)
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

func (s pluginSandbox) Held(file fs.FileInfo, by *os.File) error {
	if err := s.sandbox().Held(file, by); err != nil {
		return err
	}
	for _, r := range s.surface {
		if info, err := os.Stat(r.Path); err == nil && os.SameFile(info, file) {
			return &tools.SubstrateError{Rule: r}
		}
	}
	return nil
}

func identityDataDir(ledgerPath string) string {
	dir := filepath.Dir(ledgerPath)
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func realPath(path string) string {
	return firewall.ResolvePath(path)
}

func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
