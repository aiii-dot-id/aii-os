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
package mobile

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
)

// .
// .
// .
// .
// .
// .
var BuildCommit = "devel"

// .
// .
func BuildID() string { return BuildCommit }

// .
type Runtime struct {
	a *app.App
}

// .
// .
func Start(configPath, dataDir string) (*Runtime, error) {
	// .
	// .
	// .
	// .
	// .
	// .
	if dataDir == "" {
		// .
		// .
		// .
		// .
		return nil, fmt.Errorf("mobile: empty container dir — the shell must pass its app-private directory")
	}
	if err := os.Chdir(dataDir); err != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		return nil, fmt.Errorf("mobile: cannot enter the app container %q: %w", dataDir, err)
	}
	cfg, err := app.LoadConfig(configPath)
	if err != nil {
		cfg, err = quarantineUnreadableConfig(configPath, err)
		if err != nil {
			return nil, err
		}
	}
	if err := rerootContainerPaths(cfg, dataDir); err != nil {
		return nil, err
	}
	a := app.New(cfg)
	if err := a.StartEmbedded(); err != nil {
		// .
		// .
		// .
		// .
		a.Stop()
		return nil, err
	}
	return &Runtime{a: a}, nil
}

// .
func (r *Runtime) DashboardURL() string { return r.a.DashboardURL() }

// .
func (r *Runtime) SetForeground(live bool) { r.a.SetForeground(live) }

// .
// .
func (r *Runtime) TimeWake() { r.a.TimeWake() }

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
type WakeScheduler interface {
	Schedule(atUnixMs int64)
	Cancel()
}

// .
// .
// .
type wakeAdapter struct {
	s WakeScheduler
}

func (w wakeAdapter) WakeAt(at time.Time) error {
	w.s.Schedule(at.UnixMilli())
	return nil
}

func (w wakeAdapter) WakeClear() { w.s.Cancel() }

// .
// .
// .
// .
// .
func (r *Runtime) SetWakeScheduler(s WakeScheduler) {
	if s == nil {
		r.a.SetPlatformWake(nil)
		return
	}
	r.a.SetPlatformWake(wakeAdapter{s: s})
}

// .
func (r *Runtime) Stop() { r.a.Stop() }

// .
// .
// .
func Version() string { return app.Version }

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
func rerootContainerPaths(cfg *app.Config, dataDir string) error {
	if cfg == nil || dataDir == "" {
		return nil
	}
	type resolution struct{ name, dir string }
	var moved []resolution
	fix := func(name string, p *string) error {
		if *p == "" || !filepath.IsAbs(*p) {
			return nil
		}
		if rel, err := filepath.Rel(dataDir, *p); err == nil && !strings.HasPrefix(rel, "..") {
			return nil
		}
		was := *p
		// .
		// .
		// .
		// .
		parts := strings.Split(filepath.ToSlash(was), "/")
		cands := make([]string, 0, 5)
		for k := min(4, len(parts)-1); k >= 1; k-- {
			cands = append(cands, filepath.Join(append([]string{dataDir}, parts[len(parts)-k:]...)...))
		}
		cands = append(cands, filepath.Join(dataDir, "data", filepath.Base(was)))
		seen := map[string]bool{}
		var found []string
		for _, cand := range cands {
			if seen[cand] {
				continue
			}
			seen[cand] = true
			if st, err := os.Stat(cand); err == nil && st.Mode().IsRegular() {
				found = append(found, cand)
			}
		}
		switch len(found) {
		case 1:
			*p = found[0]
		case 0:
			// .
			// .
			// .
			*p = filepath.Join(dataDir, "data", filepath.Base(was))
		default:
			return fmt.Errorf("mobile: %s is ambiguous — %s all exist in this container; refusing to choose between identity records (recovery required)", name, strings.Join(found, " and "))
		}
		log.Printf("mobile: %s pointed outside this container (%s) — re-rooted to %s", name, was, *p)
		moved = append(moved, resolution{name: name, dir: filepath.Dir(*p)})
		return nil
	}
	if err := fix("ledger_path", &cfg.Identity.LedgerPath); err != nil {
		return err
	}
	if err := fix("db_path", &cfg.Identity.DBPath); err != nil {
		return err
	}
	if err := fix("key_path", &cfg.Identity.KeyPath); err != nil {
		return err
	}
	// .
	// .
	// .
	// .
	for i := 1; i < len(moved); i++ {
		if moved[i].dir != moved[0].dir {
			return fmt.Errorf("mobile: re-rooted identity files resolved into different directories (%s in %s, %s in %s) — one identity lives in one place; refusing a mixed set (recovery required)", moved[0].name, moved[0].dir, moved[i].name, moved[i].dir)
		}
	}
	return nil
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
func quarantineUnreadableConfig(configPath string, cause error) (*app.Config, error) {
	raw, _ := os.ReadFile(configPath)
	aside := configPath + ".unreadable-" + time.Now().UTC().Format("20060102T150405Z")
	if rerr := os.Rename(configPath, aside); rerr != nil {
		// .
		log.Printf("mobile: config is unreadable (%v) and could not be set aside (%v)", cause, rerr)
		return nil, cause
	}
	log.Printf("mobile: config was unreadable (%v) — set aside as %s and started from a default; "+
		"the identity's record and key are untouched", cause, filepath.Base(aside))
	cfg, err := app.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	// .
	// .
	// .
	// .
	if adopted, perr := app.SalvageIdentityInto(raw, cfg); len(adopted) > 0 {
		if perr != nil {
			// .
			// .
			// .
			// .
			return nil, fmt.Errorf("mobile: identity locations were salvaged from the unreadable config but could not be persisted: %w", perr)
		}
		log.Printf("mobile: salvaged from the unreadable config: %s", strings.Join(adopted, ", "))
	}
	return cfg, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
type ForegroundNeedListener interface {
	Need(active bool, reason string)
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
func (r *Runtime) SetForegroundNeedListener(l ForegroundNeedListener) {
	if l == nil {
		r.a.SubscribeForegroundNeed(nil)
		return
	}
	r.a.SubscribeForegroundNeed(func(need bool, reason string) { l.Need(need, reason) })
}

// .
// .
// .
// .
// .
// .
func (r *Runtime) DashboardMintedToken() string { return r.a.DashboardMintedToken() }
